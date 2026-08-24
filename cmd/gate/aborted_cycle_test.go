package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/observe"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// TestOnlyRunsThatDecideBurnACycle is the invariant the review-cycle ceiling
// rests on: the budget is spent by review, never by infrastructure. One
// subject walks the three cases in order — a completed run consumes a cycle,
// a run that dies gathering evidence consumes none, a later completed run
// consumes the next one — so a regression in either direction fails here.
//
// The abort is the real 2026-08-23 failure: `gh pr view` lands its evidence,
// then api.github.com resets the connection under `gh pr diff`. That run had
// already appended an evidence artifact, which is exactly why "did this run
// spend a cycle?" must be answered from the outcome it never produced and not
// from the evidence it left behind.
func TestOnlyRunsThatDecideBurnACycle(t *testing.T) {
	e := testEnv(t)
	fakeGH(t)
	g, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 3, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc123"}

	recordPark(t, e, g.ID, subject)
	if n := mustCycleCount(t, e, subject); n != 1 {
		t.Fatalf("a completed run must consume a cycle: count = %d, want 1", n)
	}

	res, code, err := runGate(e, "o/r", 7, g.ID, false, "local", false)
	if err == nil || code != codeError {
		t.Fatalf("a reset connection mid-evidence must hard-error: code %d outcome %q err %v", code, res.Outcome, err)
	}
	if !strings.Contains(err.Error(), "connection reset by peer") {
		t.Fatalf("the abort must surface the transport cause, got %v", err)
	}
	assertRunAborted(t, e, res.Run, subject, "connection reset by peer")

	if n := mustCycleCount(t, e, subject); n != 1 {
		t.Fatalf("the aborted run burned a cycle: count = %d, want 1 — a network blip must not spend the budget", n)
	}

	// And the budget is not merely frozen: the next run that actually decides
	// still advances it, so nothing above bought its safety by breaking the count.
	newest := recordPark(t, e, g.ID, subject)
	if n := mustCycleCount(t, e, subject); n != 2 {
		t.Fatalf("the next completed run must consume the next cycle: count = %d, want 2", n)
	}
	assertNextAgreesOnBudget(t, e, newest, subject)
}

// assertNextAgreesOnBudget pins the two counts an operator compares to one
// number. `gate next` re-derives the budget through its own projection
// (observe.cyclesBySubject), so a change to one counting rule and not the
// other would show up as an inbox that disagrees with the ceiling actually
// being enforced — the operator reading "2/3" while gate refuses at 3. The row
// is the subject's NEWEST park: the inbox reduces one row per PR, so a fresh
// park supersedes the earlier one.
func assertNextAgreesOnBudget(t *testing.T, e env, parkRun string, subject verify.Subject) {
	t.Helper()
	want := mustCycleCount(t, e, subject)
	in := nextInbox(t, e)
	var row *observe.ParkedRun
	for i := range in.Parked {
		if in.Parked[i].Run == parkRun {
			row = &in.Parked[i]
		}
	}
	if row == nil {
		t.Fatalf("inbox lost the park %s: %+v", parkRun, in.Parked)
	}
	if row.CyclesUsed != want {
		t.Fatalf("next reports %d cycles, gate enforces %d — the two counts must agree", row.CyclesUsed, want)
	}
	var text bytes.Buffer
	if err := observe.NextText(&text, e.st, time.Now, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "cycles 2/3") {
		t.Fatalf("next text must print the agreed budget:\n%s", text.String())
	}
}

// assertRunAborted checks what an aborted run leaves behind: the evidence it
// had already recorded (an append-only log never un-writes a fact), exactly
// one run_aborted record naming the subject and the cause, and — the point —
// no action or escalation, since the run decided nothing. `gate explain` must
// surface the cause, or the operator is left reading a run that simply stops.
func assertRunAborted(t *testing.T, e env, run string, subject verify.Subject, wantCause string) {
	t.Helper()
	arts := mustRunArtifacts(t, e, run)
	var aborts, evidence int
	for _, a := range arts {
		if isOutcome(a) {
			t.Fatalf("an aborted run must record no outcome, got %s %s", a.Kind, a.ID)
		}
		switch a.Kind {
		case state.KindRunAborted:
			aborts++
			assertAbortBody(t, a, subject, wantCause)
		case state.KindEvidence:
			evidence++
		}
	}
	if evidence == 0 {
		t.Fatal("the evidence recorded before the abort must survive it")
	}
	if aborts != 1 {
		t.Fatalf("want exactly one run_aborted record, got %d in %+v", aborts, arts)
	}
	var explained bytes.Buffer
	if err := observe.Explain(&explained, e.st, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(explained.String(), wantCause) {
		t.Fatalf("explain must show why the run stopped:\n%s", explained.String())
	}
}

func assertAbortBody(t *testing.T, a state.Artifact, subject verify.Subject, wantCause string) {
	t.Helper()
	var body struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Repo != subject.Repo || body.Number != subject.Number {
		t.Fatalf("abort record must name the subject, got %s#%d", body.Repo, body.Number)
	}
	if !strings.Contains(body.Error, wantCause) {
		t.Fatalf("abort record must carry the cause, got %q", body.Error)
	}
}

// recordPark drives one COMPLETED run for subject — a content park, the
// commonest real terminal — and returns its run id.
func recordPark(t *testing.T, e env, grantID string, subject verify.Subject) string {
	t.Helper()
	run := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	if _, code, err := act(e, run, grantID, v, recordReduced(t, e, run, v), gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park run: code %d err %v", code, err)
	}
	return run
}

func mustCycleCount(t *testing.T, e env, subject verify.Subject) int {
	t.Helper()
	n, err := cycleCount(e.st, subject, state.NewRunID())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestMain lets this package's own binary stand in for the gh CLI. When the
// helper variable is set the process is running AS gh — installed on PATH by
// fakeGH — and must answer like it rather than run the suite. This is the
// portable form of the stub: a shell script would tie the abort test to a
// POSIX shell, and the evidence package already stubs gh this way.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_GH_RESET_HELPER_PROCESS") == "1" {
		os.Exit(runResetAfterView())
	}
	os.Exit(m.Run())
}

// runResetAfterView is the CLI on the day this bug was reported: the view read
// lands, and every read after it dies on the transport. The message is the one
// gh actually printed, so the retry classifier is exercised on real text — the
// run makes all three bounded attempts before it aborts, which is also the
// proof that the bound terminates.
func runResetAfterView() int {
	if len(os.Args) > 2 && os.Args[1] == "pr" && os.Args[2] == "view" {
		fmt.Println(openPRView)
		return 0
	}
	fmt.Fprintln(os.Stderr, resetPeer)
	return 1
}

const resetPeer = `error connecting to api.github.com: Post "https://api.github.com/graphql": ` +
	`read tcp 10.0.0.2:53000->140.82.113.5:443: read: connection reset by peer`

// openPRView is the smallest view the sweep accepts: an open PR with a head to
// judge, so the already-merged refusal declines to fire and the run proceeds to
// the diff read that kills it.
const openPRView = `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN",` +
	`"baseRefName":"main","reviewDecision":"APPROVED","statusCheckRollup":[],"headRefOid":"abc123",` +
	`"title":"t","mergedAt":null,"author":{"login":"someone","is_bot":false},"mergeCommit":null}`

// fakeGH installs this test binary on PATH as gh. PATH is replaced rather than
// prefixed so nothing can fall through to a real, authenticated CLI.
// Deliberately a local copy of the evidence package's installer: a shared
// test-only helper package would couple two suites for twenty lines.
func fakeGH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	install(t, executable, filepath.Join(dir, name))
	t.Setenv("GO_WANT_GH_RESET_HELPER_PROCESS", "1")
	t.Setenv("PATH", dir)
}

// install hard-links the binary when the filesystem allows it and copies
// otherwise — the same fallback the evidence package needs across platforms.
func install(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Link(from, to); err == nil {
		return
	}
	binary, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	if err := os.WriteFile(to, binary, 0o755); err != nil {
		t.Fatalf("install fake gh: %v", err)
	}
}

// TestAbortRecordIsScopedToUndecidedRuns pins which failures get annotated.
// The record exists to explain a run that stopped mid-flight, so a run that
// reached a terminal must not get one — an abort record over a real outcome
// would read as a second, contradictory story about the same run. A tampered
// log is excluded too: writing into a chain that already failed its audit adds
// noise to corruption. In every case the run's own error passes through
// unchanged, because that is the cause the operator has to act on.
func TestAbortRecordIsScopedToUndecidedRuns(t *testing.T) {
	subject := verify.Subject{Repo: "o/r", Number: 7}
	cases := []struct {
		name   string
		code   int
		cause  error
		record bool
	}{
		{"undecided run is annotated", codeError, errors.New("read tcp: connection reset by peer"), true},
		{"a park decided something", codeParked, errors.New("escalation write failed"), false},
		{"a refusal decided something", codeRefused, errors.New("nope"), false},
		{"no error, nothing to explain", codeError, nil, false},
		{"a tampered log is corruption, not an abort", codeError, fmt.Errorf("%w: bad chain", errLogTampered), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := testEnv(t)
			run := state.NewRunID()
			got := recordAbortIfUndecided(e, run, "grt_x", subject, tc.code, tc.cause)
			if !errors.Is(got, tc.cause) {
				t.Fatalf("the run's own cause must survive: got %v, want %v", got, tc.cause)
			}
			records := len(mustRunArtifacts(t, e, run))
			if want := map[bool]int{true: 1, false: 0}[tc.record]; records != want {
				t.Fatalf("recorded %d artifacts, want %d", records, want)
			}
		})
	}
}
