package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/readiness"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// fakeBaseSHA is the base branch head testEnv's reader reports.
const fakeBaseSHA = "b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"

// forkSHA is where a behind head forked from the base.
const forkSHA = "f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0"

// baseReader stands in for evidence.BaseHead: it records one read of the PR's
// base branch at baseSHA, with the judged head behindBy commits short of it —
// zero is a head that contains the base's head. It writes evidence.BaseHeadBody,
// the type the live read writes, so every act test crosses the same
// evidence→verify JSON seam production does.
func baseReader(baseSHA string, behindBy int) func(*state.Store, string, evidence.PRRef, string) (string, error) {
	return func(st *state.Store, run string, pr evidence.PRRef, head string) (string, error) {
		body := evidence.BaseHeadBody{
			PR: pr, State: "OPEN", BaseRef: "main", BaseSHA: baseSHA, HeadSHA: head,
			MergeBaseSHA: baseSHA, Status: "ahead",
		}
		if behindBy > 0 {
			body.Status, body.BehindBy, body.MergeBaseSHA = "diverged", behindBy, forkSHA
		}
		a, err := st.Append(state.KindEvidence, run, nil, body)
		return a.ID, err
	}
}

// TestJudgedMergeRefusesAMovedBase reproduces ivy#115 (2026-09-23). Gate
// gathered the PR's evidence at 05:31Z and parked it for judgment. By 06:01Z
// three merges had moved main to a commit the head did not contain, one of them
// adding review receipts that hash a file the PR changed. At 16:30Z a judgment
// passed the park, gate emitted the merge command on the 05:31Z evidence, and
// main's next build failed on the stale receipt.
//
// Evidence at base A, the base advances to B, the judgment passes: gate must
// not emit. It blocks with base_moved_since_evidence, decided on a base read
// taken after the judgment rather than the one the park was recorded against.
func TestJudgedMergeRefusesAMovedBase(t *testing.T) {
	const baseA = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	const baseB = "c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8"
	e := testEnv(t)
	grant := mintMerge(t, e, 3)
	subject := verify.Subject{Repo: "o/r", Number: 115, HeadSHA: "3d1152e0a609c18b1d99874bead070022388c04d"}

	e.baseHead = baseReader(baseA, 0)
	run, esc := parkForJudgment(t, e, grant, subject)

	e.baseHead = baseReader(baseB, 3)
	res, code, _, err := applyJudgment(e, run, esc.ID, grant, judgmentOptions{
		Decision: verify.DecisionPass, Why: "findings addressed", Who: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != verify.DecisionPass {
		t.Fatalf("setup: the judgment must reduce the run to a pass, got %s: %s", res.Decision, res.Why)
	}
	if code != codeBlocked || res.Outcome != "blocked" || res.Code != verify.CodeBaseMoved {
		t.Fatalf("a judged pass onto a moved base must block with %s, got code %d outcome %q code %q: %s",
			verify.CodeBaseMoved, code, res.Outcome, res.Code, res.Why)
	}
	if res.Action != "" {
		t.Fatalf("no merge command may be emitted onto a moved base, got %q", res.Action)
	}
	assertNoMergeAction(t, e, run)
	read := blockingBaseRead(t, e, run)
	if read.BaseSHA != baseB {
		t.Fatalf("the block must rest on the base read after the judgment (%s), got %s", baseB, read.BaseSHA)
	}
	if n := mustCycleCount(t, e, subject); n != 1 {
		t.Fatalf("the judged run counts once, through its park: cycles = %d, want 1", n)
	}
}

// TestActEmitsOnlyOntoABaseTheHeadContains pins both directions at emission,
// where the two outcomes that can page or merge are decided. A head that
// contains the base's head merges or parks exactly as before, and names the
// base read it was decided against. A head that does not is blocked before
// either — no merge command, and no content park paging a human about a PR that
// cannot merge on this evidence.
func TestActEmitsOnlyOntoABaseTheHeadContains(t *testing.T) {
	cases := []struct {
		name        string
		decision    string
		behindBy    int
		wantCode    int
		wantOutcome string
	}{
		{"green head containing the base merges", verify.DecisionPass, 0, codeMerge, "would_merge"},
		{"green head behind the base is blocked", verify.DecisionPass, 1, codeBlocked, "blocked"},
		{"content park on a head containing the base parks", verify.DecisionEscalate, 0, codeParked, "parked_for_judgment"},
		{"content park on a head behind the base is blocked first", verify.DecisionEscalate, 4, codeBlocked, "blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := testEnv(t)
			e.baseHead = baseReader(fakeBaseSHA, tc.behindBy)
			grant := mintMerge(t, e, 3)
			run := state.NewRunID()
			v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}, tc.decision, "T0")
			id := recordReduced(t, e, run, v)
			res, code, err := act(e, run, grant, v, id, gateResult{Run: run}, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if code != tc.wantCode || res.Outcome != tc.wantOutcome {
				t.Fatalf("got code %d outcome %q, want %d %q: %s", code, res.Outcome, tc.wantCode, tc.wantOutcome, res.Why)
			}
			terminal := lastTerminal(t, e, run)
			if len(terminal.Parents) != 3 || terminal.Parents[0] != id || terminal.Parents[1] != grant {
				t.Fatalf("outcome parents %v must be the verdict, the grant, then the base read", terminal.Parents)
			}
			if tc.behindBy > 0 {
				assertBaseMovedBlock(t, e, run, res)
			}
		})
	}
}

// TestLadderBlockOutranksTheBaseRead keeps a red head red. A ladder block is
// final whatever the base did, so it is reported as the block it is and costs
// no base read: the refresh advice would be wrong for a failing check.
func TestLadderBlockOutranksTheBaseRead(t *testing.T) {
	e := testEnv(t)
	reads := 0
	e.baseHead = func(*state.Store, string, evidence.PRRef, string) (string, error) {
		reads++
		return "", errors.New("unreachable")
	}
	grant := mintMerge(t, e, 3)
	run := state.NewRunID()
	v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}, verify.DecisionBlock, "T0")
	res, code, err := act(e, run, grant, v, recordReduced(t, e, run, v), gateResult{Run: run}, false, nil)
	if err != nil || code != codeBlocked {
		t.Fatalf("a ladder block must stay a block: code %d err %v", code, err)
	}
	if res.Code == verify.CodeBaseMoved || reads != 0 {
		t.Fatalf("a ladder block must not be decided on the base (code %q, %d reads)", res.Code, reads)
	}
}

// TestUnreadBaseNeverEmits is the fail-closed direction: a base read that
// fails decides nothing. act returns the error and records no outcome, so it
// emits no merge command and pages no one — on a gate run that is an ordinary
// aborted run, which spends no cycle.
func TestUnreadBaseNeverEmits(t *testing.T) {
	e := testEnv(t)
	e.baseHead = func(*state.Store, string, evidence.PRRef, string) (string, error) {
		return "", errors.New("evidence: gh [api graphql]: HTTP 502")
	}
	grant := mintMerge(t, e, 3)
	for _, decision := range []string{verify.DecisionPass, verify.DecisionEscalate} {
		run := state.NewRunID()
		v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}, decision, "T0")
		res, code, err := act(e, run, grant, v, recordReduced(t, e, run, v), gateResult{Run: run}, false, nil)
		if err == nil || code != codeError {
			t.Fatalf("%s: an unread base must be a hard error, got code %d outcome %q", decision, code, res.Outcome)
		}
		for _, a := range mustRunArtifacts(t, e, run) {
			if isOutcome(a) {
				t.Fatalf("%s: an unread base must record no outcome, got %s %s", decision, a.Kind, a.Body)
			}
		}
	}
}

// TestJudgmentStrandedOnTheBaseReadResumes pins the recovery the read costs.
// The base read is the one network call between a durable judgment and its
// action, so a fault there strands the judgment — and the strand must be the
// ordinary resumable one: the retry resumes the recorded judgment (it is not
// spent), reads the base again, and decides on that fresh read.
func TestJudgmentStrandedOnTheBaseReadResumes(t *testing.T) {
	e := testEnv(t)
	grant := mintMerge(t, e, 3)
	run, esc := parkForJudgment(t, e, grant, verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"})
	opts := judgmentOptions{Decision: verify.DecisionPass, Why: "safe", Who: "operator"}

	e.baseHead = func(*state.Store, string, evidence.PRRef, string) (string, error) {
		return "", errors.New("evidence: gh [api graphql]: i/o timeout")
	}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grant, opts); err == nil {
		t.Fatal("a failed base read must fail the judgment's outcome, not pass it")
	}
	if last := lastTerminal(t, e, run); last.ID != esc.ID {
		t.Fatalf("the strand must record no outcome past the park, got %s %s", last.Kind, last.Body)
	}

	e.baseHead = baseReader(fakeBaseSHA, 0)
	res, code, _, err := applyJudgment(e, run, esc.ID, grant, opts)
	if err != nil || code != codeMerge || res.Outcome != "would_merge" {
		t.Fatalf("the retry must resume the recorded judgment on a fresh read: code %d outcome %q err %v", code, res.Outcome, err)
	}
	if read := blockingBaseRead(t, e, run); read.BaseSHA != fakeBaseSHA {
		t.Fatalf("the resumed outcome must name the fresh read, got %+v", read)
	}
}

// TestBaseMovedBlockSpendsNoCycle pins the budget rule and the two counts that
// enforce it. Other PRs' merges moving the base is not a review round: the
// block is followed by a refresh and a fresh run, and that run is the cycle.
// `gate next` counts through its own projection (observe.countsAsCycleRow), so
// the inbox must agree with the ceiling gate enforces.
func TestBaseMovedBlockSpendsNoCycle(t *testing.T) {
	e := testEnv(t)
	grant := mintMerge(t, e, 3)
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}

	e.baseHead = baseReader(fakeBaseSHA, 2)
	blocked := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionPass, "T0")
	if res, code, err := act(e, blocked, grant, v, recordReduced(t, e, blocked, v), gateResult{Run: blocked}, false, nil); err != nil || res.Code != verify.CodeBaseMoved {
		t.Fatalf("setup: want a base-moved block, got code %d outcome %q err %v", code, res.Outcome, err)
	}
	if n := mustCycleCount(t, e, subject); n != 0 {
		t.Fatalf("a base-moved block burned a cycle: count = %d, want 0", n)
	}

	e.baseHead = baseReader(fakeBaseSHA, 0)
	parked := recordPark(t, e, grant, subject)
	if n := mustCycleCount(t, e, subject); n != 1 {
		t.Fatalf("the refreshed run's park must be the first cycle: count = %d, want 1", n)
	}
	in := nextInbox(t, e)
	if len(in.Parked) != 1 || in.Parked[0].Run != parked {
		t.Fatalf("the inbox must show the refreshed run's park, got %+v", in.Parked)
	}
	if used := in.Parked[0].CyclesUsed; used != 1 {
		t.Fatalf("next reports %d cycles, gate enforces 1 — the two counts must agree", used)
	}
}

// TestNewEnvWiresTheLiveBaseRead guards the one line that makes the check real:
// every test env swaps the reader, so without this a production env could ship
// with none and act would fail every merge closed.
func TestNewEnvWiresTheLiveBaseRead(t *testing.T) {
	root := t.TempDir()
	e, err := newEnv(filepath.Join(root, "state"), "triage-floor", filepath.Join(root, "keys"))
	if err != nil {
		t.Fatal(err)
	}
	if e.baseHead == nil {
		t.Fatal("newEnv must wire evidence.BaseHead")
	}
}

// parkForJudgment records the ladder a real content park stands on — a code
// floor that passes and a review that escalates — and parks it through act, so
// a judged pass reduces to a real pass and reaches emission for the reason
// production does. (A run with no code floor still escalates after judgment.)
func parkForJudgment(t *testing.T, e env, grant string, subject verify.Subject) (string, state.Artifact) {
	t.Helper()
	run := state.NewRunID()
	for _, v := range []verify.Verdict{
		{Subject: subject, Source: "triage-floor", Producer: verify.Producer{Class: verify.ClassCode},
			Decision: verify.DecisionPass, Tier: "T0", Confidence: 1, Why: "floor passes"},
		{Subject: subject, Source: "review-consolidation", Producer: verify.Producer{Class: verify.ClassLocal},
			Decision: verify.DecisionEscalate, Tier: "T0", Confidence: 1, Why: "a reviewer finding needs a judge"},
	} {
		if _, err := verify.Record(e.st, run, nil, v); err != nil {
			t.Fatal(err)
		}
	}
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	if _, code, err := act(e, run, grant, rv, recordReduced(t, e, run, rv), gateResult{Run: run}, false, nil); err != nil || code != codeParked {
		t.Fatalf("setup park: code %d err %v", code, err)
	}
	return run, firstOfKind(t, e, run, state.KindEscalation)
}

// assertBaseMovedBlock checks what a base-moved block leaves: a coded result
// with no merge command, no park, and an action that carries the code, the
// reason, the refresh route, and the base read it was decided on.
func assertBaseMovedBlock(t *testing.T, e env, run string, res gateResult) {
	t.Helper()
	if res.Code != verify.CodeBaseMoved || res.Action != "" {
		t.Fatalf("result = code %q action %q, want %s and no command", res.Code, res.Action, verify.CodeBaseMoved)
	}
	for _, want := range []string{verify.CodeBaseMoved, "Merge main into the branch", fakeBaseSHA[:12]} {
		if !strings.Contains(res.Why, want) {
			t.Fatalf("why must name %q: %s", want, res.Why)
		}
	}
	for _, a := range mustRunArtifacts(t, e, run) {
		if a.Kind == state.KindEscalation {
			t.Fatalf("a base-moved head must not park for judgment: %s", a.Body)
		}
	}
	assertNoMergeAction(t, e, run)
	var body struct {
		Code   string           `json:"code"`
		Why    string           `json:"why"`
		Escape *readiness.Route `json:"escape"`
	}
	if err := json.Unmarshal(lastTerminal(t, e, run).Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != verify.CodeBaseMoved || body.Why != res.Why {
		t.Fatalf("the action must record the code and reason: %+v", body)
	}
	if body.Escape == nil || !strings.Contains(body.Escape.Why, "merge it into the branch") {
		t.Fatalf("the block must route to a refresh, not a code change: %+v", body.Escape)
	}
	blockingBaseRead(t, e, run)
}

// blockingBaseRead returns the base read the run's final outcome names.
func blockingBaseRead(t *testing.T, e env, run string) evidence.BaseHeadBody {
	t.Helper()
	terminal := lastTerminal(t, e, run)
	if len(terminal.Parents) < 3 {
		t.Fatalf("outcome %s names no base read: %v", terminal.ID, terminal.Parents)
	}
	a, err := e.st.Get(terminal.Parents[2])
	if err != nil {
		t.Fatal(err)
	}
	var read evidence.BaseHeadBody
	if err := json.Unmarshal(a.Body, &read); err != nil || a.Kind != state.KindEvidence {
		t.Fatalf("outcome parent %s is not a base read: %s %v", a.ID, a.Kind, err)
	}
	return read
}

func lastTerminal(t *testing.T, e env, run string) state.Artifact {
	t.Helper()
	var last state.Artifact
	for _, a := range mustRunArtifacts(t, e, run) {
		if isOutcome(a) {
			last = a
		}
	}
	if last.ID == "" {
		t.Fatalf("run %s recorded no outcome", run)
	}
	return last
}

func assertNoMergeAction(t *testing.T, e env, run string) {
	t.Helper()
	for _, a := range mustRunArtifacts(t, e, run) {
		var body struct {
			Outcome string `json:"outcome"`
			Command string `json:"command"`
		}
		if a.Kind != state.KindAction || json.Unmarshal(a.Body, &body) != nil {
			continue
		}
		if body.Outcome == "would_merge" || body.Outcome == "merge_not_implemented" || body.Command != "" {
			t.Fatalf("run %s recorded a merge action: %s", run, a.Body)
		}
	}
}

func mintMerge(t *testing.T, e env, maxCycles int) string {
	t.Helper()
	g, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", maxCycles, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return g.ID
}
