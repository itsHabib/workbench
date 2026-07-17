package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// testEnv builds an env over throwaway state and key dirs (the key dir a
// sibling, honouring the key-outside-state invariant).
func testEnv(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	e, err := newEnv(filepath.Join(root, "state"), "triage-floor", filepath.Join(root, "keys"))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// recordReduced appends a reduced verdict artifact so act has the parent its
// outcome will name — the same shape runGate produces.
func recordReduced(t *testing.T, e env, run string, v verify.Verdict) string {
	t.Helper()
	a, err := verify.Record(e.st, run, nil, v)
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func reducedVerdict(subject verify.Subject, decision, tier string) verify.Verdict {
	return verify.Verdict{Subject: subject, Source: "reducer",
		Producer: verify.Producer{Class: verify.ClassCode},
		Decision: decision, Tier: tier, Confidence: 1.0, Why: "test"}
}

// TestExitCodesAreStable pins the driver contract (spec §6): the five code
// values, each decision→code mapping, and code↔JSON-outcome agreement.
// Renumbering or re-pairing any of these is a breaking change the driver
// cannot see.
func TestExitCodesAreStable(t *testing.T) {
	if codeMerge != 0 || codeBlocked != 1 || codeParked != 2 || codeRefused != 3 || codeError != 4 {
		t.Fatal("exit-code contract renumbered")
	}

	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		decision    string
		tier        string
		live        bool
		wantCode    int
		wantOutcome string
	}{
		{"pass within ceilings", verify.DecisionPass, "T0", false, codeMerge, "would_merge"},
		{"block", verify.DecisionBlock, "T0", false, codeBlocked, "blocked"},
		{"escalate", verify.DecisionEscalate, "T0", false, codeParked, "parked_for_judgment"},
		{"tier over ceiling", verify.DecisionPass, "T2", false, codeParked, "parked_for_judgment"},
		{"live merge unbuilt", verify.DecisionPass, "T0", true, codeMerge, "merge_not_implemented"},
	}
	for i, c := range cases {
		run := state.NewRunID()
		v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 100 + i, HeadSHA: "abc"}, c.decision, c.tier)
		id := recordReduced(t, e, run, v)
		res, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, c.live)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if code != c.wantCode || res.Outcome != c.wantOutcome {
			t.Errorf("%s: got code %d outcome %q, want %d %q", c.name, code, res.Outcome, c.wantCode, c.wantOutcome)
		}
		// Every evidence-backed outcome must carry the judged head so a caller
		// can bind an out-of-band status to the exact commit gate verified —
		// the SHA-decoupling fix. "abc" is this subject's head.
		if res.HeadSHA != "abc" {
			t.Errorf("%s: head_sha = %q, want the judged head %q", c.name, res.HeadSHA, "abc")
		}
	}

	// No valid grant: coded refusal, and the code↔outcome pairing holds there too.
	expired, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", -time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 200, HeadSHA: "abc"}, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	res, code, err := act(e, run, expired.ID, v, id, gateResult{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || res.Outcome != "capability_refused" {
		t.Errorf("expired grant: got code %d outcome %q, want %d %q", code, res.Outcome, codeRefused, "capability_refused")
	}
}

// TestResultEmitsJudgedHeadSHA pins the SHA-binding fix: an evidence-backed
// result carries head_sha under that exact JSON key, set to the head gate
// judged, so a caller (the enforcement workflow) can bind an out-of-band commit
// status to the commit gate actually verified rather than a decoupled one. A
// result with no verdict — the zero value — emits an empty head_sha.
func TestResultEmitsJudgedHeadSHA(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "deadbeef"}, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	res, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false)
	if err != nil || code != codeMerge {
		t.Fatalf("clean pass: code %d err %v", code, err)
	}
	if res.HeadSHA != "deadbeef" {
		t.Fatalf("result head_sha = %q, want the judged head %q", res.HeadSHA, "deadbeef")
	}

	// The field reaches stdout under the exact key the workflow reads.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		HeadSHA string `json:"head_sha"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.HeadSHA != "deadbeef" {
		t.Fatalf("marshaled JSON head_sha = %q, want %q; raw: %s", back.HeadSHA, "deadbeef", raw)
	}

	// A result finalized with no verdict (the pre-evidence refusal / hard-error
	// shape runGate returns) emits an empty head_sha — never a stale bind.
	var zero struct {
		HeadSHA string `json:"head_sha"`
	}
	zraw, err := json.Marshal(gateResult{PR: "o/r#7", Outcome: "capability_refused"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(zraw, &zero); err != nil {
		t.Fatal(err)
	}
	if zero.HeadSHA != "" {
		t.Fatalf("a verdict-less result must emit empty head_sha, got %q", zero.HeadSHA)
	}
}

// TestGateTypoExitsError pins the code-space fix (spec §6 mf2): a malformed
// invocation must exit codeError. Under flag.ExitOnError it exited 2 —
// which the driver reads as parked_for_judgment, a fail-open. The test
// re-execs itself so the os.Exit path is observed for real.
func TestGateTypoExitsError(t *testing.T) {
	if os.Getenv("GATE_ARGV_HELPER") == "1" {
		os.Args = []string{"gate", "gate", "-typo"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestGateTypoExitsError")
	cmd.Env = append(os.Environ(), "GATE_ARGV_HELPER=1")
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("want an exit error from the helper, got %v", err)
	}
	if ee.ExitCode() != codeError {
		t.Fatalf("gate gate -typo exited %d, want %d", ee.ExitCode(), codeError)
	}
}

// TestHelpRequestExitsZero pins that -h is a clean success, not codeError:
// ContinueOnError makes flag return ErrHelp rather than exiting, and the
// wrapper must treat that as a normal exit-0 help path, not a hard error.
func TestHelpRequestExitsZero(t *testing.T) {
	if os.Getenv("GATE_ARGV_HELPER") == "1" {
		os.Args = []string{"gate", "grant", "-h"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestHelpRequestExitsZero")
	cmd.Env = append(os.Environ(), "GATE_ARGV_HELPER=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("gate grant -h must exit 0, got %v", err)
	}
}

// TestCycleCountRefusesTamperedLog pins the mf1 tamper-resistance claim at the
// enforcement point: if the log is rewritten to under-count cycles, deriving
// the count must fail closed (codeError), never emit a would_merge from a
// doctored log. Corrupting a recorded outcome body breaks the hash chain,
// which Audit catches before the count is trusted.
func TestCycleCountRefusesTamperedLog(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 3, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}

	// One clean pass records a would_merge outcome to later tamper with.
	run := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false); err != nil || code != codeMerge {
		t.Fatalf("clean first pass: code %d err %v", code, err)
	}

	logPath := filepath.Join(e.stateDir, "log.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// Same-length rewrite of a recorded outcome: keeps every line parseable,
	// but the body no longer matches its recorded hash.
	tampered := strings.Replace(string(raw), "would_merge", "would_XXXXX", 1)
	if tampered == string(raw) {
		t.Fatal("expected a would_merge outcome in the log to tamper")
	}
	if err := os.WriteFile(logPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	run2 := state.NewRunID()
	v2 := reducedVerdict(subject, verify.DecisionPass, "T0")
	id2 := recordReduced(t, e, run2, v2)
	_, code, err := act(e, run2, grantArt.ID, v2, id2, gateResult{}, false)
	if code != codeError || !errors.Is(err, errLogTampered) {
		t.Fatalf("a tampered log must fail closed to codeError: got code %d err %v", code, err)
	}
}

// TestCycleCapParksOverCap is the scripted replay from spec §12/P2: a
// -max-cycles 2 grant lets two passes through and parks the third with the
// coded reason; another PR's cycles never count against it; and the operator
// resolution — re-minting a wider grant — unparks it without a judge.
func TestCycleCapParksOverCap(t *testing.T) {
	e := testEnv(t)
	capped, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 2, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}

	for i := 1; i <= 2; i++ {
		run := state.NewRunID()
		v := reducedVerdict(subject, verify.DecisionPass, "T0")
		id := recordReduced(t, e, run, v)
		res, code, err := act(e, run, capped.ID, v, id, gateResult{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if code != codeMerge {
			t.Fatalf("cycle %d under the cap: got code %d outcome %q", i, code, res.Outcome)
		}
	}

	run := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	res, code, err := act(e, run, capped.ID, v, id, gateResult{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeParked || res.Outcome != "parked_for_judgment" {
		t.Fatalf("cycle 3 over cap 2: got code %d outcome %q, want park", code, res.Outcome)
	}
	if !strings.Contains(res.Why, "grant_cycle_exceeded") {
		t.Fatalf("over-cap park must carry the coded reason, got %q", res.Why)
	}

	// A different PR on the same repo starts at zero — the count joins on subject.
	otherRun := state.NewRunID()
	other := reducedVerdict(verify.Subject{Repo: "o/r", Number: 8, HeadSHA: "def"}, verify.DecisionPass, "T0")
	otherID := recordReduced(t, e, otherRun, other)
	if _, code, err := act(e, otherRun, capped.ID, other, otherID, gateResult{}, false); err != nil || code != codeMerge {
		t.Fatalf("fresh PR under the same grant: got code %d err %v", code, err)
	}

	// The park resolves by re-minting a grant exactly one cycle wider (D2),
	// not a judge. This only works because the ceiling park itself did not
	// consume a cycle — authorization parks are exhaustion, not consumption;
	// if they counted, every failed retry would burn the cycle the wider
	// grant was minted to free and a +1 re-mint would never unpark.
	wider, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 3, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	retryRun := state.NewRunID()
	retryID := recordReduced(t, e, retryRun, v)
	if _, code, err := act(e, retryRun, wider.ID, v, retryID, gateResult{}, false); err != nil || code != codeMerge {
		t.Fatalf("re-minted one-wider grant must unpark: got code %d err %v", code, err)
	}
}

// TestCycleCapJudgeCannotLaunderCeiling pins D2's ladder law for cycles: a
// judgment pass on an over-cap run, checked against the same capped grant,
// must still park. A judge decides content; widening a ceiling is an
// authorization decision that only a re-mint makes.
func TestCycleCapJudgeCannotLaunderCeiling(t *testing.T) {
	e := testEnv(t)
	capped, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 2, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}
	for i := 1; i <= 2; i++ {
		run := state.NewRunID()
		v := reducedVerdict(subject, verify.DecisionPass, "T0")
		id := recordReduced(t, e, run, v)
		if _, code, err := act(e, run, capped.ID, v, id, gateResult{}, false); err != nil || code != codeMerge {
			t.Fatalf("cycle %d under the cap: code %d err %v", i, code, err)
		}
	}
	run := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, capped.ID, v, id, gateResult{}, false); err != nil || code != codeParked {
		t.Fatalf("cycle 3 over cap 2: code %d err %v", code, err)
	}

	// The judgment path re-enters act on the same run with the same grant —
	// exactly what cmdJudge does after appending a judgment verdict.
	judged := reducedVerdict(subject, verify.DecisionPass, "T0")
	jid := recordReduced(t, e, run, judged)
	res, code, err := act(e, run, capped.ID, judged, jid, gateResult{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeParked || !strings.Contains(res.Why, "grant_cycle_exceeded") {
		t.Fatalf("a judgment must not launder the cycle ceiling: got code %d why %q", code, res.Why)
	}
}

// TestCycleCountSkipsCapabilityRefusals pins the consumption rule for the
// refusal path: a run refused by an expired grant produced no ladder decision
// and must not burn a cycle against the re-minted retry.
func TestCycleCountSkipsCapabilityRefusals(t *testing.T) {
	e := testEnv(t)
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}

	expired, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 1, "test", -time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, expired.ID, v, id, gateResult{}, false); err != nil || code != codeRefused {
		t.Fatalf("expired grant: code %d err %v", code, err)
	}

	fresh, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 1, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	retryRun := state.NewRunID()
	retryID := recordReduced(t, e, retryRun, v)
	if _, code, err := act(e, retryRun, fresh.ID, v, retryID, gateResult{}, false); err != nil || code != codeMerge {
		t.Fatalf("retry after refusal must have a full cycle budget: code %d err %v", code, err)
	}
}

// TestCycleCountUnreadableParks pins fail-closed counting: an outcome artifact
// whose parent verdict cannot be resolved parks the run — it must never read
// as fewer cycles consumed.
func TestCycleCountUnreadableParks(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 3, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Append(state.KindAction, "run_stray", nil, map[string]any{"outcome": "would_merge"}); err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	res, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeParked || !strings.Contains(res.Why, "cycle count unreadable") {
		t.Fatalf("unreadable count must park: got code %d why %q", code, res.Why)
	}
}

// TestGrantFlagDefaultsThreeCycles pins mf5: a plain `gate grant` mints the
// canonical 3-cycle cap. The field's zero value stays unbounded; only the CLI
// default carries the opinion.
func TestGrantFlagDefaultsThreeCycles(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	if err := cmdGrant([]string{"-state", stateDir, "-key", filepath.Join(root, "keys"), "-repo", "o/r"}); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(stateDir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := st.List(func(a state.Artifact) bool { return a.Kind == state.KindGrant })
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatalf("want one grant, got %d", len(grants))
	}
	var g capability.Grant
	if err := json.Unmarshal(grants[0].Body, &g); err != nil {
		t.Fatal(err)
	}
	if g.MaxCycles != 3 {
		t.Fatalf("default -max-cycles minted %d, want 3", g.MaxCycles)
	}
}

// TestStateDirTagDistinguishesDirs guards the anchor-collision fix: two
// different state dirs sharing one key dir must map to different anchor records,
// or appending to one would falsely fail the other's audit.
func TestStateDirTagDistinguishesDirs(t *testing.T) {
	a := stateDirTag("stateA")
	b := stateDirTag("stateB")
	if a == b {
		t.Fatalf("distinct state dirs produced the same anchor tag: %q", a)
	}
	if a == "" || b == "" {
		t.Fatal("empty anchor tag")
	}
}

// TestStateDirTagStableAcrossSpellings pins that two spellings of the same dir
// resolve to one anchor, so a relative and absolute path to the same log don't
// fork the anchor.
func TestStateDirTagStableAcrossSpellings(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join(dir, "sub", "..", "state")
	abs := filepath.Join(dir, "state")
	if stateDirTag(rel) != stateDirTag(abs) {
		t.Fatalf("same dir via different spellings forked the anchor: %q vs %q", rel, abs)
	}
}

// TestBacktestLeavesNoSpendableGrantInDurableState is the acceptance test for
// the capability-backstop fix: a read-only backtest must not write a spendable
// grant into the operator's durable state log. backtest runs its dry-run passes
// against a throwaway ephemeral store, so nothing durable is touched — in
// particular the default relative `state` dir is never created. The gate passes
// themselves fail fast (gh cannot reach a real PR under test), which is fine:
// the mint happens before the passes, so if backtest wrote its grant durably it
// would show up regardless of the passes erroring.
func TestBacktestLeavesNoSpendableGrantInDurableState(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Order matters: t.TempDir registers its RemoveAll cleanup first, so by
	// LIFO the chdir-back below runs BEFORE it — the CWD is restored out of the
	// temp tree before RemoveAll fires. Registering the chdir cleanup before
	// t.TempDir would invert that, leaving RemoveAll to delete a directory that
	// is still the process CWD. Windows refuses that ("the directory is in
	// use"); Linux tolerates it, which is why CI did not catch the ordering.
	work := t.TempDir()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}

	// PATH stripped so the floor and gh binaries are absent: the passes error
	// out quickly instead of hanging on real network reads.
	t.Setenv("PATH", "")
	if err := runBacktest("owner/repo", "1,2", filepath.Join(work, "no-such-floor")); err != nil {
		t.Fatalf("runBacktest returned a hard error: %v", err)
	}

	// The default durable state dir is relative to the working dir. It must not
	// exist: a backtest that minted into durable state (the old behaviour, or a
	// regression back to it) would have created it.
	if _, err := os.Stat(filepath.Join(work, "state")); !os.IsNotExist(err) {
		t.Fatalf("backtest created a durable state dir %q (stat err: %v)", filepath.Join(work, "state"), err)
	}
	if entries, err := os.ReadDir(work); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("backtest wrote durable files into the working dir: %v", entries)
	}
}

// TestExplainJSONFlag pins that explain -json emits one JSON document from the
// fixture store without changing the text path.
func TestExplainJSONFlag(t *testing.T) {
	root := t.TempDir()
	fixtureSrc := observeFixtureDir(t)
	fixtureDst := filepath.Join(root, "state")
	if err := copyDir(fixtureSrc, fixtureDst); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = cmdExplain([]string{"-state", fixtureDst, "-run", "run_explain_fixture", "-json"})
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	var doc struct {
		Run       string `json:"run"`
		Artifacts []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, buf.Bytes())
	}
	if doc.Run != "run_explain_fixture" || len(doc.Artifacts) == 0 {
		t.Fatalf("unexpected projection: %+v", doc)
	}
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func observeFixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		cand := filepath.Join(dir, "internal", "observe", "testdata", "fixture")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("observe fixture dir not found")
		}
		dir = parent
	}
}

// TestNewEphemeralEnvIsThrowaway pins the ephemeral capability path: the store
// backtest mints into is a real, spendable store while it lives (the grant it
// mints validates), but it is a throwaway — separate from any durable state and
// fully removed by cleanup, so no spendable grant survives the run.
func TestNewEphemeralEnvIsThrowaway(t *testing.T) {
	e, cleanup, err := newEphemeralEnv("triage-floor")
	if err != nil {
		t.Fatal(err)
	}

	now := func() time.Time { return time.Unix(1000, 0) }
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "backtest-ephemeral", time.Hour, now)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	// While the ephemeral store lives the grant is a real, spendable T2 grant —
	// backtest needs it to pass the capability check on its dry-run passes.
	grants, err := e.st.List(func(a state.Artifact) bool { return a.Kind == state.KindGrant })
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].ID != grantArt.ID {
		cleanup()
		t.Fatalf("expected the ephemeral grant in the throwaway store, got %d artifacts", len(grants))
	}

	cleanup()

	// After cleanup the entire ephemeral tree is gone: no state log, no grant,
	// nothing spendable persisted anywhere.
	if _, err := os.Stat(e.stateDir); !os.IsNotExist(err) {
		t.Fatalf("cleanup left the ephemeral state dir behind: %q (stat err: %v)", e.stateDir, err)
	}
	if _, err := os.Stat(e.keyPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup left the ephemeral key behind: %q (stat err: %v)", e.keyPath, err)
	}
}
