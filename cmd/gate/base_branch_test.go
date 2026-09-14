package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// TestNonDefaultBaseRefusesBeforeTheLadder pins the stacked-PR refusal through
// the live verb. The merge command gate emits lands in whatever branch the PR
// targets, so a PR based on anything but the default branch refuses with
// base_not_default (exit 3) before any verdict exists: no escalation is
// written, so no judgment can authorize the merge, and no review cycle is
// spent. Unknown values fail closed — an unread default branch or a view with
// no base is a hard error, never a pass.
//
// The fake gh answers only the view and the repository read, so a run that
// gets past the check dies on the diff read. That is how the negative cases
// prove the check let them through, rather than refusing more than it should.
func TestNonDefaultBaseRefusesBeforeTheLadder(t *testing.T) {
	cases := []struct {
		name      string
		repo      string
		base      string
		replay    bool
		wantCode  int
		wantInErr string
	}{
		{name: "stacked PR refuses", repo: "o/r", base: "feat/parent", wantCode: codeRefused},
		{name: "default base reaches the ladder", repo: "o/r", base: "main", wantCode: codeError, wantInErr: "evidence: gh"},
		{name: "replay is exempt", repo: "o/r", base: "feat/parent", replay: true, wantCode: codeError, wantInErr: "evidence: gh"},
		{name: "a view with no base fails closed", repo: "o/r", base: "", wantCode: codeError, wantInErr: "no base branch"},
		{name: "an unread default branch fails closed", repo: "o/x", base: "feat/parent", wantCode: codeError, wantInErr: "read repo o/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := testEnv(t)
			res, code, err := gateWithBase(t, e, tc.repo, tc.base, tc.replay)
			if code != tc.wantCode {
				t.Fatalf("code = %d (outcome %q, err %v), want %d", code, res.Outcome, err, tc.wantCode)
			}
			if code == codeRefused {
				assertBaseRefusal(t, e, res, err, tc.base)
				return
			}
			assertUndecided(t, e, res, err, tc.wantInErr)
		})
	}
}

// gateWithBase runs one gate pass for PR 7 against a fake gh whose view reports
// base as the PR's base branch. replay takes the backtest entry point, which
// skips the refusals that only the live verb applies.
func gateWithBase(t *testing.T, e env, repo, base string, replay bool) (gateResult, int, error) {
	t.Helper()
	fakeGH(t)
	t.Setenv(fakeBaseEnv, base)
	g, err := capability.Mint(e.st, e.keyPath, repo, "merge", "T1", 3, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if replay {
		return runGateWithSynthesis(e, repo, 7, g.ID, false, "local", false, true, false, "")
	}
	return runGate(e, repo, 7, g.ID, false, "local", false)
}

// assertBaseRefusal checks what the refusal leaves: a coded exit-3 result with
// no merge command, one terminal action and nothing from the ladder, and a
// cycle count that is still readable and still zero.
func assertBaseRefusal(t *testing.T, e env, res gateResult, err error, base string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != outcomeBaseNotDefault || res.Code != outcomeBaseNotDefault {
		t.Fatalf("outcome %q code %q, want %s for both", res.Outcome, res.Code, outcomeBaseNotDefault)
	}
	if res.Action != "" {
		t.Fatalf("a refused run must emit no merge command, got %q", res.Action)
	}
	if !strings.Contains(res.Why, strconv.Quote(base)) || !strings.Contains(res.Why, strconv.Quote("main")) {
		t.Fatalf("why must name the base and the default branch, got %q", res.Why)
	}
	if res.HeadSHA != "abc123" {
		t.Fatalf("head_sha = %q, want the head the view recorded", res.HeadSHA)
	}
	actions := 0
	for _, a := range mustRunArtifacts(t, e, res.Run) {
		switch a.Kind {
		case state.KindVerdict, state.KindEscalation:
			t.Fatalf("the refusal must precede the ladder, got a %s", a.Kind)
		case state.KindAction:
			actions++
			assertBaseRefusalBody(t, e, a, base)
		}
	}
	if actions != 1 {
		t.Fatalf("want exactly one terminal action, got %d", actions)
	}
	if n := mustCycleCount(t, e, verify.Subject{Repo: "o/r", Number: 7}); n != 0 {
		t.Fatalf("a base refusal decided nothing and must spend nothing: %d cycles", n)
	}
}

// assertBaseRefusalBody checks the artifact contract for a pre-ladder refusal:
// it names its subject and both branches on its own body, and its provenance is
// the view evidence it was decided from.
func assertBaseRefusalBody(t *testing.T, e env, a state.Artifact, base string) {
	t.Helper()
	var body struct {
		Outcome       string `json:"outcome"`
		Repo          string `json:"repo"`
		Number        int    `json:"number"`
		BaseRef       string `json:"base_ref"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Outcome != outcomeBaseNotDefault || body.Repo != "o/r" || body.Number != 7 ||
		body.BaseRef != base || body.DefaultBranch != "main" {
		t.Fatalf("action body = %+v, want %s for o/r#7 naming %q and main", body, outcomeBaseNotDefault, base)
	}
	if len(a.Parents) != 1 {
		t.Fatalf("parents = %v, want exactly the view evidence", a.Parents)
	}
	parent, err := e.st.Get(a.Parents[0])
	if err != nil {
		t.Fatal(err)
	}
	if parent.Kind != state.KindEvidence {
		t.Fatalf("parent is a %s, want the view evidence the refusal was decided from", parent.Kind)
	}
}

// assertUndecided checks a run that ended in a hard error: the cause names
// where it stopped, and no outcome was recorded — above all, no would_merge.
func assertUndecided(t *testing.T, e env, res gateResult, err error, wantInErr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), wantInErr) {
		t.Fatalf("err = %v, want it to name %q", err, wantInErr)
	}
	for _, a := range mustRunArtifacts(t, e, res.Run) {
		if isOutcome(a) {
			t.Fatalf("an undecided run must record no outcome, got %s %s", a.Kind, a.ID)
		}
	}
}

// TestMergedRefusalPrecedesTheDefaultBranchRead pins the order inside
// refuseUnmergeable: an already-merged subject refuses from the view alone,
// before the default-branch read. A merged stacked PR therefore reports
// already_merged, and an unreachable GitHub cannot turn that refusal into an
// error. PATH is emptied so any gh call would fail the run.
func TestMergedRefusalPrecedesTheDefaultBranchRead(t *testing.T) {
	e := testEnv(t)
	t.Setenv("PATH", "")
	run := state.NewRunID()
	view := json.RawMessage(`{"state":"MERGED","baseRefName":"feat/parent","headRefOid":"abc","mergeCommit":{"oid":"m"}}`)
	art, err := e.st.Append(state.KindEvidence, run, nil, map[string]any{"data": view})
	if err != nil {
		t.Fatal(err)
	}
	subject := verify.Subject{Repo: "o/r", Number: 7}
	res, code, done, err := refuseUnmergeable(e, run, art.ID, view, "grt_x", subject, gateResult{})
	if err != nil || !done || code != codeRefused || res.Outcome != outcomeAlreadyMerged {
		t.Fatalf("done=%v code=%d outcome=%q err=%v, want an already_merged refusal", done, code, res.Outcome, err)
	}
}
