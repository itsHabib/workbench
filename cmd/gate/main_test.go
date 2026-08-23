package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/readiness"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
	"github.com/itsHabib/workbench/contracts/escalation"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
	"github.com/itsHabib/workbench/contracts/reviewpanel"
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

func TestReviewsOptionalStillRunsPanelCompleteness(t *testing.T) {
	e := testEnv(t)
	subject := verify.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"}
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
		Missing:       []string{"codex"},
	}
	artifact, err := e.st.Append(state.KindEvidence, "run_t", nil, panel)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := reviewVerdictIDs(e, "run_t", evidence.Bundle{Panel: artifact.ID}, subject, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("reviews-optional verdict count = %d, want deterministic panel only", len(ids))
	}
	verdictArtifact, err := e.st.Get(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := verify.Load(verdictArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Source != "review-panel-completeness" || verdict.Decision != verify.DecisionEscalate {
		t.Fatalf("reviews-optional skipped incomplete panel: %+v", verdict)
	}
}

// The ladder wiring, pinned where the verify-level tests cannot see it: those
// hand Readiness a panel id directly, so they stay green even if the composition
// stops passing one. Without bundle.Panel reaching readiness, a complete
// exact-head panel cannot answer an absent GitHub review decision and every PR
// reviewed by comment-posting bots parks for a judge again.
func TestReadinessJudgesTheGatheredPanel(t *testing.T) {
	e := testEnv(t)
	view, err := e.st.Append(state.KindEvidence, "run_t", nil, map[string]any{
		"data": map[string]any{
			"state": "OPEN", "mergeable": "MERGEABLE", "headRefOid": "head",
			"statusCheckRollup": []map[string]any{{"name": "ci", "conclusion": "SUCCESS"}},
			"author":            map[string]any{"login": "agent"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	panel, err := e.st.Append(state.KindEvidence, "run_t", nil, reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
		Completed: []reviewpanel.Reviewer{{
			Name: "codex", Actor: "chatgpt-codex-connector[bot]", State: "COMMENTED",
			HeadSHA: "head", ReviewID: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := evidence.Bundle{View: view.ID, Panel: panel.ID}
	artifact, _, err := readinessVerdict(e, "run_t", bundle, verify.Subject{Repo: "o/r", Number: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := verify.Load(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != verify.DecisionPass {
		t.Fatalf("gathered panel did not reach readiness: %s (%s)", verdict.Decision, verdict.Why)
	}
	if !slices.Contains(artifact.Parents, panel.ID) {
		t.Fatalf("readiness parents %v must name the panel evidence it judged", artifact.Parents)
	}
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
		res, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, c.live, nil)
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
		if c.wantCode != codeMerge {
			assertTerminalArtifactHasEscape(t, e, run)
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
	res, code, err := act(e, run, expired.ID, v, id, gateResult{}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || res.Outcome != "capability_refused" {
		t.Errorf("expired grant: got code %d outcome %q, want %d %q", code, res.Outcome, codeRefused, "capability_refused")
	}
}

func assertTerminalArtifactHasEscape(t *testing.T, e env, run string) {
	t.Helper()
	artifacts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	last := artifacts[len(artifacts)-1]
	var body struct {
		Escape     *readiness.Route `json:"escape"`
		RetryHelps *bool            `json:"retry_helps"`
	}
	if err := json.Unmarshal(last.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Escape == nil || body.Escape.Why == "" || body.Escape.Next == "" {
		t.Fatalf("terminal artifact %s has no usable escape: %s", last.ID, last.Body)
	}
	if body.RetryHelps == nil {
		t.Fatalf("terminal artifact %s omitted retry_helps: %s", last.ID, last.Body)
	}
}

func TestTerminalErrorBeforeResultStillPrintsARoute(t *testing.T) {
	got := terminalErrorFor(errors.New("gate: -repo, -pr, -grant required"), []string{
		"gate", "-repo", "o/r", "-state", "/tmp/gate-state",
	})
	if got.Escape.Next != `gate next -state '/tmp/gate-state'` {
		t.Fatalf("escape = %+v", got.Escape)
	}
	if got.Error == "" {
		t.Fatal("terminal error lost its cause")
	}
}

func TestTerminalErrorUsesExternalRouteForBrokenSubstrate(t *testing.T) {
	err := fmt.Errorf("open state: %w", state.ErrAnchorMissing)
	got := terminalErrorFor(err, []string{"audit", "-state", "/tmp/gate-state", "-key", "/tmp/gate-key"})
	if got.Escape.Next != `ls -ld /tmp/gate-state /tmp /tmp/gate-key` {
		t.Fatalf("escape = %+v", got.Escape)
	}
}

func TestTerminalErrorIncludesHostedAnchorOnParseFailure(t *testing.T) {
	t.Setenv("GATE_ANCHOR_RECORD", "/srv/gate/anchor.json")
	got := terminalErrorFor(
		errors.New("state: parse anchor: invalid character"),
		[]string{"audit", "-state", "/tmp/gate-state", "-key", "/tmp/gate-key"},
	)
	for _, want := range []string{"/srv/gate/anchor.json", "/srv/gate"} {
		if !strings.Contains(got.Escape.Next, want) {
			t.Fatalf("escape = %q, want hosted anchor target %q", got.Escape.Next, want)
		}
	}
}

func TestTerminalErrorIncludesAnchorCustodyOnTamperedChain(t *testing.T) {
	t.Setenv("GATE_ANCHOR_RECORD", "/srv/gate/anchor.json")
	got := terminalErrorFor(
		fmt.Errorf("%w: rewrite: anchor MAC mismatch", errLogTampered),
		[]string{"audit", "-state", "/tmp/gate-state", "-key", "/tmp/gate-key"},
	)
	for _, want := range []string{"/tmp/gate-key", "/srv/gate/anchor.json", "/srv/gate"} {
		if !strings.Contains(got.Escape.Next, want) {
			t.Fatalf("escape = %q, want anchor custody target %q", got.Escape.Next, want)
		}
	}
}

func TestCapabilityRefusalUsesGrantKeyCustodyRoute(t *testing.T) {
	err := fmt.Errorf("%w: /tmp/gate-key/grant.key", capability.ErrKeyMissing)
	res := gateResult{Outcome: "capability_refused", Why: err.Error()}
	decorateTerminalContext(
		&res, resultSubstrateOK(res), "/tmp/gate-state",
		[]string{"gate", "-key", "/tmp/gate-key"}, err,
	)
	if res.Escape == nil || res.Escape.Next != `ls -ld /tmp/gate-state /tmp /tmp/gate-key` {
		t.Fatalf("escape = %+v", res.Escape)
	}
}

func TestStateSubstrateClassification(t *testing.T) {
	args := []string{"resolve", "-state", "/tmp/gate-state", "-key", "/tmp/gate-key"}
	if !stateSubstrateOK(fmt.Errorf("state: artifact esc_typo: %w", state.ErrNotFound), args) {
		t.Fatal("an ordinary missing artifact was classified as a broken substrate")
	}
	if stateSubstrateOK(fmt.Errorf("audit: %w", errLogTampered), args) {
		t.Fatal("a tampered log was classified as a usable substrate")
	}
	if stateSubstrateOK(&os.PathError{Op: "open", Path: "/tmp/gate-state/log.jsonl", Err: os.ErrNotExist}, args) {
		t.Fatal("state log I/O was classified as a usable substrate")
	}
	if !stateSubstrateOK(&os.PathError{Op: "open", Path: "/tmp/input-judgment.json", Err: os.ErrNotExist}, args) {
		t.Fatal("an unrelated input file was classified as a broken substrate")
	}
}

func TestHostedAnchorDirErrorsAreSubstrateFaults(t *testing.T) {
	t.Setenv("GATE_ANCHOR_RECORD", "/srv/gate/anchor.json")
	args := []string{"gate", "-state", "/tmp/gate-state", "-key", "/tmp/gate-key"}
	if stateSubstrateOK(&os.PathError{Op: "open", Path: "/srv/gate/.anchor-123.tmp", Err: os.ErrPermission}, args) {
		t.Fatal("hosted anchor temp write was classified as a usable substrate")
	}
	if stateSubstrateOK(&os.PathError{Op: "mkdir", Path: "/srv/gate", Err: os.ErrPermission}, args) {
		t.Fatal("hosted anchor dir creation was classified as a usable substrate")
	}
	if !stateSubstrateOK(&os.PathError{Op: "open", Path: "/srv/other/file", Err: os.ErrNotExist}, args) {
		t.Fatal("a path outside the hosted anchor dir was classified as a broken substrate")
	}
	// Custody is the record, its temp siblings, and the exact parent — never
	// the parent's other children.
	if !stateSubstrateOK(&os.PathError{Op: "open", Path: "/srv/gate/unrelated.json", Err: os.ErrNotExist}, args) {
		t.Fatal("an unrelated sibling of the hosted anchor was classified as a broken substrate")
	}
}

func TestRelativeHostedAnchorDoesNotSwallowInputErrors(t *testing.T) {
	// A relative record has "." for a parent; the working directory must not
	// become substrate, or `gate judge -judgment missing.json` gets the
	// custody route instead of an input-file diagnosis.
	t.Setenv("GATE_ANCHOR_RECORD", "anchor.json")
	args := []string{"judge", "-state", "/tmp/gate-state", "-key", "/tmp/gate-key"}
	if !stateSubstrateOK(&os.PathError{Op: "open", Path: "missing.json", Err: os.ErrNotExist}, args) {
		t.Fatal("an input file in the working directory was classified as a broken substrate")
	}
	if stateSubstrateOK(&os.PathError{Op: "open", Path: "anchor.json", Err: os.ErrPermission}, args) {
		t.Fatal("the relative hosted anchor itself was classified as a usable substrate")
	}
}

func TestMalformedJudgmentRoutesToAlternateProvider(t *testing.T) {
	got := terminalErrorFor(
		errors.New(`judgment_malformed: json: unknown field "findings"`),
		[]string{"judge", "-run", "run_1", "-grant", "grt_1", "-auto", "-provider", "claude", "-state", "/tmp/gate-state"},
	)
	for _, want := range []string{"gate", "judge", "run_1", "grt_1", "-provider", "codex", "/tmp/gate-state"} {
		if !strings.Contains(got.Escape.Next, want) {
			t.Fatalf("escape = %q, want %q", got.Escape.Next, want)
		}
	}
	if !got.SelfGated || got.RetryHelps {
		t.Fatalf("terminal metadata = %+v", got)
	}
}

func TestMalformedJudgmentRoutesDoubleDashProvider(t *testing.T) {
	got := terminalErrorFor(
		errors.New(`judgment_malformed: json: unknown field "findings"`),
		[]string{"judge", "-run", "run_1", "-grant", "grt_1", "-auto", "--provider=claude"},
	)
	if !strings.Contains(got.Escape.Next, "--provider=codex") {
		t.Fatalf("escape = %q", got.Escape.Next)
	}
}

func TestMalformedEscalationDoesNotSwitchProviders(t *testing.T) {
	got := terminalErrorFor(
		errors.New("judgment_malformed_escalation: question is empty"),
		[]string{"judge", "-run", "run_1", "-grant", "grt_1", "-auto", "-provider", "claude"},
	)
	if strings.Contains(got.Escape.Next, "codex") {
		t.Fatalf("malformed escalation incorrectly switched providers: %q", got.Escape.Next)
	}
}

func TestBlockedResultRoutesToItsRecordedDecision(t *testing.T) {
	res := gateResult{Run: "run_1", Outcome: "blocked", Why: "code finding remains"}
	decorateTerminal(&res, true, "/tmp/gate-state")
	if res.Escape == nil || !strings.Contains(res.Escape.Next, `gate explain -run 'run_1'`) {
		t.Fatalf("escape = %+v", res.Escape)
	}
	if res.RetryHelps == nil || *res.RetryHelps {
		t.Fatalf("retry_helps = %v", res.RetryHelps)
	}
}

func TestNonBlockedResultsCarryRunnableRoutes(t *testing.T) {
	for _, outcome := range []string{"parked_for_judgment", "capability_refused"} {
		res := gateResult{Run: "run_1", Outcome: outcome, Why: "grant_expired"}
		decorateTerminal(&res, true, "/tmp/gate-state")
		if res.Escape == nil || res.Escape.Next == "" {
			t.Fatalf("%s escape = %+v", outcome, res.Escape)
		}
	}
}

func TestGrantCeilingRoutesToRecordedRunNotJudge(t *testing.T) {
	for _, tc := range []struct {
		code   string
		reason string
	}{
		{escalation.CodeCycleExceeded, "grant_cycle_exceeded: cycle 3"},
		{escalation.CodeTierExceeded, "grant_tier_exceeded: verdict tier T2 exceeds grant ceiling T1"},
	} {
		res := gateResult{Run: "run_1", Outcome: "parked_for_judgment", Code: tc.code, Why: tc.reason}
		decorateTerminal(&res, true, "/tmp/gate-state")
		if res.Escape == nil || !strings.Contains(res.Escape.Next, "gate explain") || strings.Contains(res.Escape.Next, "gate judge") {
			t.Fatalf("ceiling escape for %q = %+v", tc.reason, res.Escape)
		}
	}
}

func TestProceduralParkClassifiesStructuredCode(t *testing.T) {
	res := gateResult{Run: "run_1", Outcome: "parked_for_judgment", Why: "cycle_count_unreadable: input/output error"}
	decorateTerminalCode(&res, escalation.CodeCountUnreadable, true, "/tmp/gate-state")
	if res.Escape == nil || res.Escape.Next != "gate next -state '/tmp/gate-state'" {
		t.Fatalf("procedural escape = %+v", res.Escape)
	}
}

func TestContentParkDoesNotInferProceduralCodeFromProse(t *testing.T) {
	res := gateResult{Run: "run_1", Outcome: "parked_for_judgment", Why: "discussion mentions grant_tier_exceeded as an example"}
	decorateTerminal(&res, true, "/tmp/gate-state")
	if res.Escape == nil || strings.Contains(res.Escape.Next, "gate explain") {
		t.Fatalf("content park inferred a procedural route: %+v", res.Escape)
	}
}

func TestStateAndKeyFlagsAcceptDoubleDash(t *testing.T) {
	args := []string{"audit", "--state=/tmp/gate-state", "--key", "/tmp/gate-key"}
	if got := stateFlag(args); got != "/tmp/gate-state" {
		t.Fatalf("stateFlag() = %q", got)
	}
	if got := keyFlag(args); got != "/tmp/gate-key" {
		t.Fatalf("keyFlag() = %q", got)
	}
}

func TestShellQuoteExactPreservesApostrophesForRunnableRoutes(t *testing.T) {
	if got := shellQuoteExact("operator's state"); got != `'operator'"'"'s state'` {
		t.Fatalf("shellQuoteExact() = %q", got)
	}
}

func TestShellJoinLeavesFlagsLegibleAndQuotesUnsafeValues(t *testing.T) {
	got := shellJoin([]string{"gate", "judge", "-run", "run_1", "-state", "operator's state"})
	want := `gate judge -run run_1 -state 'operator'"'"'s state'`
	if got != want {
		t.Fatalf("shellJoin() = %q, want %q", got, want)
	}
}

func TestActPersistsExactMergeArgv(t *testing.T) {
	e := testEnv(t)
	grant, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{
		Repo: "o/r", Number: 7,
		HeadSHA: strings.Repeat("a", 40),
	}
	verdict := reducedVerdict(subject, verify.DecisionPass, "T0")
	verdictID := recordReduced(t, e, run, verdict)
	if _, code, err := act(e, run, grant.ID, verdict, verdictID, gateResult{}, false, nil); err != nil || code != codeMerge {
		t.Fatalf("act code=%d err=%v", code, err)
	}
	artifacts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Command string   `json:"command"`
		Argv    []string `json:"argv"`
	}
	if err := json.Unmarshal(artifacts[len(artifacts)-1].Body, &body); err != nil {
		t.Fatal(err)
	}
	want := gateauthorization.ExpectedMergeArgv(gateauthorization.Subject{
		Repo: subject.Repo, Number: subject.Number, HeadSHA: subject.HeadSHA,
	})
	if strings.Join(body.Argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv=%q want=%q", body.Argv, want)
	}
	if body.Command != strings.Join(want, " ") {
		t.Fatalf("command=%q want=%q", body.Command, strings.Join(want, " "))
	}
}

// TestEscalationCarriesPRSubject pins the escalation click-target contract: a
// parked_for_judgment escalation names the PR it parked (repo + number) so a
// notification sink can link straight to it, while action outcomes stay
// subject-free (their join runs outcome → parent verdict).
func TestEscalationCarriesPRSubject(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	run := state.NewRunID()
	v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 41, HeadSHA: "abc"}, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("escalate: code %d err %v", code, err)
	}
	var subject struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}
	unmarshalKindBody(t, e, run, state.KindEscalation, &subject)
	if subject.Repo != "o/r" || subject.Number != 41 {
		t.Fatalf("escalation must carry its PR subject, got %+v", subject)
	}

	run2 := state.NewRunID()
	v2 := reducedVerdict(verify.Subject{Repo: "o/r", Number: 42, HeadSHA: "abc"}, verify.DecisionBlock, "T0")
	id2 := recordReduced(t, e, run2, v2)
	if _, code, err := act(e, run2, grantArt.ID, v2, id2, gateResult{}, false, nil); err != nil || code != codeBlocked {
		t.Fatalf("block: code %d err %v", code, err)
	}
	var actionSubject struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}
	unmarshalKindBody(t, e, run2, state.KindAction, &actionSubject)
	if actionSubject.Repo != "" || actionSubject.Number != 0 {
		t.Fatalf("action body must stay subject-free, got %+v", actionSubject)
	}
}

// unmarshalKindBody decodes the body of run's sole artifact of the given kind.
// TestEscalationCarriesSynthesizedBrief pins the happy path: a content park
// under a valid grant records the synthesized brief on its escalation body,
// and the added post-synthesis grant re-check does not disturb it.
func TestEscalationCarriesSynthesizedBrief(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 84, HeadSHA: "abc"}, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	synth := func(q string) (verify.Brief, error) {
		if q != "test" {
			t.Fatalf("synth must receive the park question, got %q", q)
		}
		return verify.Brief{WhatItIs: "a spec", Concern: "broken check", Risk: "Medium", Recommendation: "author fixes"}, nil
	}
	_, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, synth)
	if err != nil || code != codeParked {
		t.Fatalf("content park: code %d err %v", code, err)
	}
	var body struct {
		Brief verify.Brief `json:"brief"`
	}
	unmarshalKindBody(t, e, run, state.KindEscalation, &body)
	if body.Brief.Concern != "broken check" {
		t.Fatalf("escalation must carry the synthesized brief, got %+v", body.Brief)
	}
}

// TestEscalationGrantExpiresDuringSynthesis pins the TOCTOU fix: a grant live
// at the pre-synthesis check but expired by the time a slow synthesis returns
// must refuse — the post-synthesis re-check catches it, and no escalation is
// ever recorded under the expired grant. The grant TTL is short and the synth
// call sleeps past it, so the second check sees an expiry the first could not.
func TestEscalationGrantExpiresDuringSynthesis(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", 80*time.Millisecond, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	v := reducedVerdict(verify.Subject{Repo: "o/r", Number: 84, HeadSHA: "abc"}, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	slowSynth := func(string) (verify.Brief, error) {
		time.Sleep(300 * time.Millisecond) // outlast the grant TTL, as a stalled model call would
		return verify.Brief{WhatItIs: "a spec", Concern: "broken check", Risk: "Medium", Recommendation: "author fixes"}, nil
	}
	res, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, slowSynth)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || res.Outcome != "capability_refused" {
		t.Fatalf("expiry during synthesis must refuse: got code %d outcome %q", code, res.Outcome)
	}
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Kind == state.KindEscalation {
			t.Fatal("an escalation must never record under a grant that expired during synthesis")
		}
	}
}

func unmarshalKindBody(t *testing.T, e env, run, kind string, into any) {
	t.Helper()
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Kind != kind {
			continue
		}
		if err := json.Unmarshal(a.Body, into); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("run %s has no %s artifact", run, kind)
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
	res, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, nil)
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

func TestValidateJudgeFlagsUsesClosedProviderSet(t *testing.T) {
	for _, provider := range []string{verify.JudgeProviderClaude, verify.JudgeProviderCodex} {
		t.Run(provider, func(t *testing.T) {
			err := validateJudgeFlags("run_123", "grt_123", judgmentOptions{
				Auto:     true,
				Provider: provider,
			})
			if err != nil {
				t.Fatalf("built-in provider refused: %v", err)
			}
		})
	}

	cases := []struct {
		name string
		opts judgmentOptions
		code string
	}{
		{
			name: "missing provider",
			opts: judgmentOptions{Auto: true},
			code: "judge_provider_unconfigured",
		},
		{
			name: "caller selected executable",
			opts: judgmentOptions{Auto: true, Provider: `C:\tmp\judge.exe`},
			code: "judge_provider_unsupported",
		},
		{
			name: "provider without auto",
			opts: judgmentOptions{
				Decision: verify.DecisionPass,
				Why:      "safe",
				Provider: verify.JudgeProviderCodex,
			},
			code: "-provider requires -auto",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateJudgeFlags("run_123", "grt_123", tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
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
	if _, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, nil); err != nil || code != codeMerge {
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
	_, code, err := act(e, run2, grantArt.ID, v2, id2, gateResult{}, false, nil)
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
		res, code, err := act(e, run, capped.ID, v, id, gateResult{}, false, nil)
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
	res, code, err := act(e, run, capped.ID, v, id, gateResult{}, false, nil)
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
	if _, code, err := act(e, otherRun, capped.ID, other, otherID, gateResult{}, false, nil); err != nil || code != codeMerge {
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
	if _, code, err := act(e, retryRun, wider.ID, v, retryID, gateResult{}, false, nil); err != nil || code != codeMerge {
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
		if _, code, err := act(e, run, capped.ID, v, id, gateResult{}, false, nil); err != nil || code != codeMerge {
			t.Fatalf("cycle %d under the cap: code %d err %v", i, code, err)
		}
	}
	run := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionPass, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, capped.ID, v, id, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("cycle 3 over cap 2: code %d err %v", code, err)
	}

	// The judgment path re-enters act on the same run with the same grant —
	// exactly what cmdJudge does after appending a judgment verdict.
	judged := reducedVerdict(subject, verify.DecisionPass, "T0")
	jid := recordReduced(t, e, run, judged)
	res, code, err := act(e, run, capped.ID, judged, jid, gateResult{}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeParked || !strings.Contains(res.Why, "grant_cycle_exceeded") {
		t.Fatalf("a judgment must not launder the cycle ceiling: got code %d why %q", code, res.Why)
	}
}

// TestRefuseMergedRunRefusesTerminally pins the already-merged refusal: a
// subject GitHub reports as MERGED has no merge left to authorize, so the run
// terminates with a coded refusal (exit 3) naming the merge commit — recorded
// as a terminal action for audit, never a park. A park here would be
// unresolvable: judging it pass would stamp irreversible merge authorization
// for a merge that already happened.
func TestRefuseMergedRunRefusesTerminally(t *testing.T) {
	e := testEnv(t)
	run := state.NewRunID()
	view, err := e.st.Append(state.KindEvidence, run, nil, map[string]any{
		"data": map[string]any{
			"state":       "MERGED",
			"headRefOid":  "headsha",
			"mergeCommit": map[string]any{"oid": "mergesha"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mv, err := verify.MergedPR(e.st, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Merged || mv.MergeSHA != "mergesha" || mv.HeadSHA != "headsha" {
		t.Fatalf("MergedPR = %+v, want merged with both SHAs", mv)
	}
	subject := verify.Subject{Repo: "o/r", Number: 214}
	res, code, err := refuseMergedRun(e, run, view.ID, "grt_x", subject, mv, gateResult{})
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || res.Outcome != outcomeAlreadyMerged {
		t.Fatalf("code=%d outcome=%q, want refusal with %q", code, res.Outcome, outcomeAlreadyMerged)
	}
	if !strings.Contains(res.Why, "mergesha") {
		t.Fatalf("the refusal must name the merge commit, got why=%q", res.Why)
	}
	if res.HeadSHA != "headsha" {
		t.Fatalf("res.HeadSHA=%q, want the judged head from evidence", res.HeadSHA)
	}
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Kind == state.KindEscalation {
			t.Fatal("an already-merged subject must never park")
		}
	}
	var body struct {
		Outcome     string `json:"outcome"`
		MergeCommit string `json:"merge_commit"`
		Repo        string `json:"repo"`
		Number      int    `json:"number"`
	}
	unmarshalKindBody(t, e, run, state.KindAction, &body)
	if body.Outcome != outcomeAlreadyMerged || body.MergeCommit != "mergesha" {
		t.Fatalf("action body = %+v, want terminal %s naming the merge commit", body, outcomeAlreadyMerged)
	}
	if body.Repo != "o/r" || body.Number != 214 {
		t.Fatalf("action body must carry the subject (no verdict parent to join through), got %+v", body)
	}
}

// TestMergedPROpenPRIsNotMerged pins the live path: an OPEN subject does not
// trip the already-merged refusal.
func TestMergedPROpenPRIsNotMerged(t *testing.T) {
	e := testEnv(t)
	view, err := e.st.Append(state.KindEvidence, state.NewRunID(), nil, map[string]any{
		"data": map[string]any{"state": "OPEN", "headRefOid": "headsha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mv, err := verify.MergedPR(e.st, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Merged {
		t.Fatal("an OPEN PR must not read as merged")
	}
}

// TestCycleCountSkipsAlreadyMerged pins two facts about the refusal artifact:
// it consumes no review cycle, and — because its parent is view evidence, not
// a reduced verdict — the cycle counter must skip it entirely rather than fail
// the count unreadable (which would park every later run on the subject).
func TestCycleCountSkipsAlreadyMerged(t *testing.T) {
	e := testEnv(t)
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}
	run := state.NewRunID()
	view, err := e.st.Append(state.KindEvidence, run, nil, map[string]any{
		"data": map[string]any{"state": "MERGED", "headRefOid": "abc", "mergeCommit": map[string]any{"oid": "m"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mv, err := verify.MergedPR(e.st, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := refuseMergedRun(e, run, view.ID, "grt_x", subject, mv, gateResult{}); err != nil || code != codeRefused {
		t.Fatalf("refusal: code %d err %v", code, err)
	}

	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 1, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	retryRun := state.NewRunID()
	v := reducedVerdict(subject, verify.DecisionPass, "T0")
	id := recordReduced(t, e, retryRun, v)
	if _, code, err := act(e, retryRun, grantArt.ID, v, id, gateResult{}, false, nil); err != nil || code != codeMerge {
		t.Fatalf("a prior already_merged refusal must not burn a cycle or break the count: code %d err %v", code, err)
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
	if _, code, err := act(e, run, expired.ID, v, id, gateResult{}, false, nil); err != nil || code != codeRefused {
		t.Fatalf("expired grant: code %d err %v", code, err)
	}

	fresh, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 1, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	retryRun := state.NewRunID()
	retryID := recordReduced(t, e, retryRun, v)
	if _, code, err := act(e, retryRun, fresh.ID, v, retryID, gateResult{}, false, nil); err != nil || code != codeMerge {
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
	res, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, nil)
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
	if err := cmdGrant([]string{"-state", stateDir, "-key", filepath.Join(root, "keys"), "-repo", "o/r", "-init"}); err != nil {
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

// TestGrantRefusesFreshStateDir pins the misdirected-mint guard: minting into
// a state dir with no existing log (e.g. the relative default from the wrong
// cwd) must refuse and leave nothing behind, instead of silently creating a
// fresh state tree whose grants the canonical one can't see.
func TestGrantRefusesFreshStateDir(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	err := cmdGrant([]string{"-state", stateDir, "-key", filepath.Join(root, "keys"), "-repo", "o/r"})
	if err == nil {
		t.Fatal("mint into a fresh state dir must refuse without -init")
	}
	if !strings.Contains(err.Error(), "-init") {
		t.Fatalf("refusal must point at -init: %v", err)
	}
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("refused mint must not create the state dir: %v", statErr)
	}
}

// TestGrantAcceptsExistingStateDir pins that the guard keys on the log's
// existence, not on -init: a state dir that already holds log.jsonl mints
// without any extra flag.
func TestGrantAcceptsExistingStateDir(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	if err := cmdGrant([]string{"-state", stateDir, "-key", filepath.Join(root, "keys"), "-repo", "o/r", "-init"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdGrant([]string{"-state", stateDir, "-key", filepath.Join(root, "keys"), "-repo", "o/r"}); err != nil {
		t.Fatalf("mint into an existing state dir must not need -init: %v", err)
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

func TestHostedAnchorRecordIsPortableAndOutsideState(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "gate-state", "state")
	keyDir := filepath.Join(root, "keys")
	anchorPath := filepath.Join(root, "gate-state", "anchor.json")
	t.Setenv("GATE_ANCHOR_RECORD", anchorPath)
	e, err := newEnv(stateDir, "triage-floor", keyDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Append(state.KindEvidence, "run_test", nil, map[string]string{"evidence": "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(anchorPath); err != nil {
		t.Fatalf("hosted anchor was not written at stable path: %v", err)
	}

	t.Setenv("GATE_ANCHOR_RECORD", filepath.Join(stateDir, "anchor.json"))
	if _, err := newEnv(stateDir, "triage-floor", keyDir); err == nil {
		t.Fatal("anchor record inside state dir must refuse")
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

// grantNeededArts returns every grant_needed artifact in the store.
func grantNeededArts(t *testing.T, e env) []state.Artifact {
	t.Helper()
	arts, err := e.st.List(func(a state.Artifact) bool { return a.Kind == state.KindGrantNeeded })
	if err != nil {
		t.Fatal(err)
	}
	return arts
}

// TestRunGateExpiredGrantPersistsGrantNeeded pins Half 1: the top-level
// capability refusal on an expired grant records exactly one grant_needed
// artifact (reason grant_expired, the repo), while still returning codeRefused —
// and the record is NOT an outcome, so it never counts as a review cycle.
func TestRunGateExpiredGrantPersistsGrantNeeded(t *testing.T) {
	e := testEnv(t)
	expired, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 3, "test", -time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	res, code, err := runGate(e, "o/r", 7, expired.ID, false, "local", false)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || res.Outcome != "capability_refused" {
		t.Fatalf("expired grant: got code %d outcome %q, want refusal", code, res.Outcome)
	}
	arts := grantNeededArts(t, e)
	if len(arts) != 1 {
		t.Fatalf("want exactly one grant_needed artifact, got %d", len(arts))
	}
	var b struct {
		Repo   string `json:"repo"`
		Reason string `json:"reason"`
		At     string `json:"at"`
	}
	if err := json.Unmarshal(arts[0].Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.Repo != "o/r" || b.Reason != "grant_expired" || b.At == "" {
		t.Fatalf("grant_needed body = %+v, want repo o/r reason grant_expired with a timestamp", b)
	}
	// The record must never read as a consumed cycle: isOutcome ignores it, so a
	// later run's cycle count is unaffected by the persisted refusal.
	if isOutcome(arts[0]) {
		t.Fatal("grant_needed must not be an outcome — it would burn a review cycle")
	}
	n, err := cycleCount(e.st, verify.Subject{Repo: "o/r", Number: 7}, state.NewRunID())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a persisted refusal must not count as a cycle, got %d", n)
	}
}

// TestRunGateAbsentGrantPersistsGrantNeeded pins that a grant id naming nothing
// records reason grant_absent.
func TestRunGateAbsentGrantPersistsGrantNeeded(t *testing.T) {
	e := testEnv(t)
	// A store with at least one artifact so newEnv's anchor is initialized.
	if _, err := capability.Mint(e.st, e.keyPath, "o/other", "merge", "T1", 3, "test", time.Hour, time.Now); err != nil {
		t.Fatal(err)
	}
	res, code, err := runGate(e, "o/r", 7, "grt_does_not_exist", false, "local", false)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || res.Outcome != "capability_refused" {
		t.Fatalf("absent grant: got code %d outcome %q, want refusal", code, res.Outcome)
	}
	arts := grantNeededArts(t, e)
	if len(arts) != 1 {
		t.Fatalf("want one grant_needed artifact, got %d", len(arts))
	}
	var b struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(arts[0].Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.Reason != "grant_absent" {
		t.Fatalf("reason = %q, want grant_absent", b.Reason)
	}
}

// TestRecordGrantNeededSkipsEmptyTree pins the fresh-tree guard: recording a
// refusal into an empty/fresh -state dir must write NOTHING and must not create
// log.jsonl or the anchor — otherwise a misdirected cwd would materialize a
// state tree that bypasses checkGrantStateDir's fresh-tree refusal.
func TestRecordGrantNeededSkipsEmptyTree(t *testing.T) {
	e := testEnv(t) // brand-new state + key dirs, no log yet
	recordGrantNeeded(e, "o/r", fmt.Errorf("%w: no such grant", state.ErrNotFound))
	if arts := grantNeededArts(t, e); len(arts) != 0 {
		t.Fatalf("recording into an empty tree must write nothing, got %d", len(arts))
	}
	if _, err := os.Stat(filepath.Join(e.stateDir, "log.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("recording into an empty tree must not create the log (stat err: %v)", err)
	}
}

// TestRunGateScopeMismatchDoesNotPersist pins that a non-materialization refusal
// class — here a scope mismatch — records NO grant_needed artifact: it is a
// misconfiguration, not a "mint a grant for this repo" fact.
func TestRunGateScopeMismatchDoesNotPersist(t *testing.T) {
	e := testEnv(t)
	// A live grant for a DIFFERENT repo: the check fails on scope, not expiry/absence.
	other, err := capability.Mint(e.st, e.keyPath, "o/other", "merge", "T1", 3, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	res, code, err := runGate(e, "o/r", 7, other.ID, false, "local", false)
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || res.Outcome != "capability_refused" {
		t.Fatalf("scope mismatch: got code %d outcome %q, want refusal", code, res.Outcome)
	}
	if arts := grantNeededArts(t, e); len(arts) != 0 {
		t.Fatalf("a scope-mismatch refusal must not persist grant_needed, got %d", len(arts))
	}
}

// TestGrantNeededReasonClassification pins the reason mapping directly.
func TestGrantNeededReasonClassification(t *testing.T) {
	if got := grantNeededReason(capability.ErrExpired); got != "grant_expired" {
		t.Fatalf("expired = %q, want grant_expired", got)
	}
	if got := grantNeededReason(state.ErrNotFound); got != "grant_absent" {
		t.Fatalf("not-found = %q, want grant_absent", got)
	}
	for _, e := range []error{capability.ErrSignature, capability.ErrScope, capability.ErrKeyMissing} {
		if got := grantNeededReason(e); got != "" {
			t.Fatalf("non-materialization error %v classified as %q, want none", e, got)
		}
	}
}

// TestExplainJSONFlag pins that explain -json emits one JSON document from the
// fixture store without changing the text path.
func TestExplainJSONFlag(t *testing.T) {
	root := t.TempDir()
	fixtureSrc := observeFixtureDir(t)
	fixtureDst := filepath.Join(root, "state")
	if err := copyObserveFixture(fixtureSrc, fixtureDst); err != nil {
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

func copyObserveFixture(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(src, "log.jsonl"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "log.jsonl"), data, 0o644)
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

func TestBacktestRejectsBadPRBeforeWritingStdout(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := runBacktest("owner/repo", "1,bad", "triage-floor")
	_ = w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if runErr == nil || !strings.Contains(runErr.Error(), `backtest: bad pr "bad"`) {
		t.Fatalf("error = %v", runErr)
	}
	if len(out) != 0 {
		t.Fatalf("backtest wrote partial stdout before rejecting input: %q", out)
	}
}

func TestExplainHTMLFlag(t *testing.T) {
	root := t.TempDir()
	fixtureSrc := observeFixtureDir(t)
	fixtureDst := filepath.Join(root, "state")
	if err := copyObserveFixture(fixtureSrc, fixtureDst); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "trace.html")

	var buf bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = cmdExplain([]string{"-state", fixtureDst, "-run", "run_explain_fixture", "-html", "-out", out})
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()

	printed := strings.TrimSpace(buf.String())
	if printed == "" || !strings.Contains(printed, "trace.html") {
		t.Fatalf("did not print the output path, got %q", printed)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if !strings.Contains(html, `const EMBEDDED_FIXTURE = {"run":"run_explain_fixture"`) {
		t.Fatal("run projection not embedded as the fixture")
	}
	if strings.Contains(html, "run_ceiling_park_demo") {
		t.Fatal("demo fixture survived the swap")
	}
	if !strings.Contains(html, "renderGraph(EMBEDDED_FIXTURE)") {
		t.Fatal("render logic missing from the emitted page")
	}
}

func TestExplainRejectsJSONWithHTML(t *testing.T) {
	err := cmdExplain([]string{"-run", "run_x", "-json", "-html"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("want mutual-exclusion error, got %v", err)
	}
}

func TestExplainRejectsOutWithoutHTML(t *testing.T) {
	err := cmdExplain([]string{"-run", "run_x", "-out", "t.html"})
	if err == nil || !strings.Contains(err.Error(), "requires -html") {
		t.Fatalf("want -out-requires-html error, got %v", err)
	}
}

// TestSynthBriefFailOpen pins the escalation-brief contract: a successful
// synthesis returns the typed contract Brief (field-for-field from
// verify.Brief), any failure (or no synthesizer at all) returns nil — the park
// falls back to the raw question and never fails on a missing brief.
func TestSynthBriefFailOpen(t *testing.T) {
	got := synthBrief("needs judgment", func(q string) (verify.Brief, error) {
		if q != "needs judgment" {
			t.Fatalf("synth must receive the park question, got %q", q)
		}
		return verify.Brief{WhatItIs: "a spec", Concern: "check is broken", Risk: "Medium", Recommendation: "author fixes"}, nil
	})
	if got == nil || got.Concern != "check is broken" || got.WhatItIs != "a spec" {
		t.Fatalf("brief must map field-for-field onto the contract Brief, got %+v", got)
	}

	failed := synthBrief("needs judgment", func(string) (verify.Brief, error) {
		return verify.Brief{}, errors.New("model down")
	})
	if failed != nil {
		t.Fatalf("a failed synthesis must yield no brief, got %+v", failed)
	}

	if synthBrief("needs judgment", nil) != nil { // nil synth: no brief, never a panic
		t.Fatal("a nil synthesizer must yield no brief")
	}
}

// TestParkCodesMatchCapability pins the no-drift law between the shared park-code
// vocabulary and gate's own capability errors: the contract enum values MUST
// equal the strings gate persists, or a reader keying on the contract enum would
// silently miss a real park. Adopting the typed body changed no wire value, and
// this test keeps it that way.
func TestParkCodesMatchCapability(t *testing.T) {
	if escalation.CodeTierExceeded != capability.ErrTierCeiling.Error() {
		t.Errorf("CodeTierExceeded %q != capability.ErrTierCeiling %q", escalation.CodeTierExceeded, capability.ErrTierCeiling.Error())
	}
	if escalation.CodeCycleExceeded != capability.ErrCycleExceeded.Error() {
		t.Errorf("CodeCycleExceeded %q != capability.ErrCycleExceeded %q", escalation.CodeCycleExceeded, capability.ErrCycleExceeded.Error())
	}
	if escalation.CodeCountUnreadable != "cycle_count_unreadable" {
		t.Errorf("CodeCountUnreadable %q != gate's persisted cycle_count_unreadable", escalation.CodeCountUnreadable)
	}
}

// TestDecisionVocabMatchesVerify pins that the contract's resolution decision
// vocabulary equals gate's verdict decisions, so the back-channel's pass/block
// and gate's ladder can never drift — the symmetry TestParkCodesMatchCapability
// gives the park codes. resolve validates against verify's constants and the
// contract validates against its own; this keeps the two spellings identical.
func TestDecisionVocabMatchesVerify(t *testing.T) {
	if escalation.DecisionPass != verify.DecisionPass {
		t.Errorf("DecisionPass %q != verify.DecisionPass %q", escalation.DecisionPass, verify.DecisionPass)
	}
	if escalation.DecisionBlock != verify.DecisionBlock {
		t.Errorf("DecisionBlock %q != verify.DecisionBlock %q", escalation.DecisionBlock, verify.DecisionBlock)
	}
}

// recordVerifier appends a non-reducer verifier verdict so runVerdicts finds the
// subject to judge — the shape a real gate run records before it reduces. The
// reduced verdict act consumes is passed separately (Source "reducer"), which
// runVerdicts skips.
func recordVerifier(t *testing.T, e env, run string, subject verify.Subject, decision string) {
	t.Helper()
	v := verify.Verdict{
		Subject:    subject,
		Source:     "readiness",
		Producer:   verify.Producer{Class: verify.ClassLocal},
		Decision:   decision,
		Tier:       "T0",
		Confidence: 1.0,
		Why:        "the reviewer panel disagrees",
	}
	if _, err := verify.Record(e.st, run, nil, v); err != nil {
		t.Fatal(err)
	}
}

func firstOfKind(t *testing.T, e env, run, kind string) state.Artifact {
	t.Helper()
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Kind == kind {
			return a
		}
	}
	t.Fatalf("run %s has no %s artifact", run, kind)
	return state.Artifact{}
}

// TestParkWritesTypedV1 pins D1's write side: a content park writes a
// body that is a valid V1 — it carries the schema version, decodes
// through the shared contract, and passes the strict Validate. This is the proof
// the untyped map became the typed contract on the wire.
func TestParkWritesTypedV1(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 41, HeadSHA: "abc"}
	v := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("escalate: code %d err %v", code, err)
	}

	esc := firstOfKind(t, e, run, state.KindEscalation)
	body, err := escalation.DecodeBody(esc.Body)
	if err != nil {
		t.Fatalf("the park body must decode through the shared contract: %v", err)
	}
	if body.SchemaVersion != escalation.SchemaVersion {
		t.Fatalf("park body must carry schema_version %q, got %q", escalation.SchemaVersion, body.SchemaVersion)
	}
	if err := escalation.Validate(body); err != nil {
		t.Fatalf("a real park body must pass the strict contract law: %v", err)
	}
	if body.Repo != "o/r" || body.Number != 41 || body.RunID != run || body.Grant != grantArt.ID {
		t.Fatalf("park body lost provenance: %+v", body)
	}
	if body.Resolution != nil {
		t.Fatalf("a fresh park must carry no resolution, got %+v", body.Resolution)
	}
}

// TestResolveClosesLoop is the end-to-end thread D2 exists to prove: a parked
// escalation, resolved by its id, records a judgment parented to that escalation
// AND stamps a resolution artifact with full provenance — and the hash chain
// still verifies. This exercises the exact helpers cmdResolve composes
// (runOfEscalation → applyJudgment → stampResolution), minus the os.Exit wrapper.
func TestResolveClosesLoop(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 126, HeadSHA: "abc"}
	// A real run records its verifier verdicts before it reduces; the park then
	// stands on the reduced escalate.
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	rvID := recordReduced(t, e, run, rv)
	if _, code, err := act(e, run, grantArt.ID, rv, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)

	// The back-channel holds only the escalation id a notification carried;
	// runOfEscalation recovers the run from it.
	gotRun, err := runOfEscalation(e, esc.ID)
	if err != nil {
		t.Fatalf("runOfEscalation: %v", err)
	}
	if gotRun != run {
		t.Fatalf("runOfEscalation resolved the wrong run: got %s want %s", gotRun, run)
	}

	res, code, judgmentID, err := applyJudgment(e, run, esc.ID, grantArt.ID, judgmentOptions{Decision: verify.DecisionPass, Why: "the retry has a ceiling; safe to land", Who: "operator"})
	if err != nil {
		t.Fatalf("applyJudgment: %v", err)
	}
	if judgmentID == "" {
		t.Fatal("a recorded decision must return its judgment id")
	}
	if err := stampResolution(e, run, esc.ID, judgmentID, res.Decision, "operator", verify.MethodCLIOperator); err != nil {
		t.Fatalf("stampResolution: %v", err)
	}

	assertJudgmentParented(t, e, run, esc.ID, judgmentID)
	assertResolutionStamp(t, e, run, esc.ID, judgmentID, res.Decision)
	assertChainIntact(t, e)
	_ = code // the terminal code depends on the reduce ladder; the loop's provenance is the invariant under test.
}

func TestDuplicateJudgmentRefusesWithoutStateMutation(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 127, HeadSHA: "abc"}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	rvID := recordReduced(t, e, run, rv)
	if _, code, err := act(e, run, grantArt.ID, rv, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)
	opts := judgmentOptions{Decision: verify.DecisionPass, Why: "safe", Who: "operator"}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantArt.ID, opts); err != nil {
		t.Fatalf("first judgment: %v", err)
	}
	before, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantArt.ID, opts); err == nil || !strings.Contains(err.Error(), "judgment_duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}
	after, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("duplicate refusal mutated state: before %d artifacts, after %d", len(before), len(after))
	}
}

// `judge` is one-shot and irreversible, so the operator's first question on any
// failure is whether the shot was spent — and answering it used to mean
// grepping log.jsonl for a judgment artifact on the run. Both sides of the
// append are pinned: a refusal that recorded nothing says a retry is legal, and
// one that already recorded a judgment says a retry resumes it rather than
// replacing it. The cause stays wrapped either way, so a caller matching on the
// failure code still matches.
func TestJudgeSlotStateReportsWhetherTheOneShotWasSpent(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 129, HeadSHA: "abc"}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	rvID := recordReduced(t, e, run, rv)
	if _, code, err := act(e, run, grantArt.ID, rv, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)
	cause := errors.New(`judgment_malformed: json: unknown field "findings"`)

	unspent := judgeSlotState(e, run, esc.ID, cause)
	if !errors.Is(unspent, cause) {
		t.Fatalf("annotation dropped the cause it wraps: %v", unspent)
	}
	for _, want := range []string{"judgment_malformed", "no judgment is recorded", "retry is legal"} {
		if !strings.Contains(unspent.Error(), want) {
			t.Fatalf("pre-append error = %v\nwant it to name %q", unspent, want)
		}
	}

	opts := judgmentOptions{Decision: verify.DecisionPass, Why: "safe", Who: "operator"}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantArt.ID, opts); err != nil {
		t.Fatalf("first judgment: %v", err)
	}
	jdg := firstOfKind(t, e, run, state.KindJudgment)
	settled := judgeSlotState(e, run, esc.ID, cause)
	for _, want := range []string{jdg.ID, "already produced an outcome", "judgment_duplicate"} {
		if !strings.Contains(settled.Error(), want) {
			t.Fatalf("settled-judgment error = %v\nwant it to name %q", settled, want)
		}
	}
	if strings.Contains(settled.Error(), "a retry resumes") {
		t.Fatalf("a settled park was advertised as resumable: %v", settled)
	}
}

// A spent slot means two different things, and only one of them is retryable:
// finishJudgment RESUMES a judgment whose reduction never reached an outcome,
// and refuses one that did. Pinned against finishJudgment's own precondition so
// the advice cannot drift from the behaviour it describes.
func TestJudgeSlotStateSeparatesResumableFromSettled(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 130, HeadSHA: "abc"}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	rvID := recordReduced(t, e, run, rv)
	if _, code, err := act(e, run, grantArt.ID, rv, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)

	// A judgment with no reduction yet is the resumable state — the same one
	// finishJudgment drives to an outcome rather than refusing.
	judgment := verify.Verdict{
		Subject:    subject,
		Source:     "operator-judgment",
		Producer:   verify.Producer{Class: verify.ClassJudgment, Impl: "operator"},
		Decision:   verify.DecisionPass,
		Tier:       "T0",
		Confidence: 1,
		Why:        "safe",
	}
	jArt, err := e.st.AppendIfAbsentParent(state.KindJudgment, run, esc.ID, []string{esc.ID, grantArt.ID}, judgment)
	if err != nil {
		t.Fatal(err)
	}
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if judgmentSettled(arts, jArt.ID) {
		t.Fatal("a judgment with no reduced verdict reported as settled")
	}
	resumable := judgeSlotState(e, run, esc.ID, errors.New("anchor_failed: boom"))
	if !strings.Contains(resumable.Error(), "a retry resumes") {
		t.Fatalf("resumable error = %v\nwant it to say a retry resumes", resumable)
	}

	// The same unsettled slot behind an incomplete-append refusal is NOT
	// retry-resumable: every audit-gated append re-refuses the one-ahead
	// ledger until the anchor reseals. The annotation must not promise a
	// resume it cannot deliver.
	// The wedge is classified from the audited state, never from error text:
	// a mistyped grant id that state.Get embeds in its error can carry the
	// wedge's spelling while the ledger is intact. On this healthy store the
	// spoofed causes must still read as resumable.
	spoofed := errors.New("state: artifact grt_incomplete-append:-oops: not found")
	if got := judgeSlotState(e, run, esc.ID, spoofed); !strings.Contains(got.Error(), "a retry resumes that judgment") {
		t.Fatalf("healthy store: spoofed cause = %v\nwant the resumable message", got)
	}

	// A REAL wedge: any cause must now carry the reseal guidance.
	wedgeAnchor(t, e, run)
	wedged := judgeSlotState(e, run, esc.ID, errors.New("anchor_failed: boom"))
	if strings.Contains(wedged.Error(), "a retry resumes that judgment") {
		t.Fatalf("wedged error = %v\nmust not claim a bare retry resumes", wedged)
	}
	for _, want := range []string{"anchor was not updated", "reseals"} {
		if !strings.Contains(wedged.Error(), want) {
			t.Fatalf("wedged error = %v\nwant it to mention %q", wedged, want)
		}
	}

	// One plain append reseals the anchor under rebind's bounded recovery —
	// the same route the guidance names — so the judgment can be driven on.
	if _, err := e.st.Append(state.KindEvidence, run, nil, map[string]string{"note": "reseal"}); err != nil {
		t.Fatal(err)
	}

	// Driving it to an outcome flips the same slot to settled.
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantArt.ID, judgmentOptions{Decision: verify.DecisionPass, Who: "operator"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	arts, err = e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if !judgmentSettled(arts, jArt.ID) {
		t.Fatal("a judgment that produced an outcome reported as resumable")
	}

	// A wedge on the OUTCOME append lands in the settled branch: the park is
	// resolved, but the reseal guidance must still reach the operator —
	// audit-gated writes stay refused until the anchor reseals.
	wedgeAnchor(t, e, run)
	settled := judgeSlotState(e, run, esc.ID, errors.New("anchor_failed: boom"))
	for _, want := range []string{"judgment_duplicate", "anchor was not updated", "reseals"} {
		if !strings.Contains(settled.Error(), want) {
			t.Fatalf("settled wedge error = %v\nwant it to mention %q", settled, want)
		}
	}
}

// wedgeAnchor recreates the interrupted-append state: save the anchor, land
// one more append, restore the stale anchor — log one ahead, anchor pinning a
// matching prefix head. The next successful plain append reseals it.
func wedgeAnchor(t *testing.T, e env, run string) {
	t.Helper()
	saved, err := os.ReadFile(e.anchor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Append(state.KindEvidence, run, nil, map[string]string{"note": "wedge"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.anchor, saved, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAtMostOneJudgmentProperty(t *testing.T) {
	property := func(attempts []bool) bool {
		var arts []state.Artifact
		accepted := 0
		const escalationID = "esc_once"
		for _, attempt := range attempts {
			if _, exists := artifactForParent(arts, state.KindJudgment, escalationID); !attempt || exists {
				continue
			}
			arts = append(arts, state.Artifact{Kind: state.KindJudgment, Parents: []string{escalationID}})
			accepted++
		}
		return accepted <= 1
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatalf("at-most-one judgment property: %v", err)
	}
}

func TestStaleSubmittedJudgmentRefusesWithoutStateMutation(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 128, HeadSHA: "current"}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	rvID := recordReduced(t, e, run, rv)
	if _, code, err := act(e, run, grantArt.ID, rv, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)
	escBody, err := escalation.DecodeBody(esc.Body)
	if err != nil {
		t.Fatal(err)
	}
	artifact := verify.JudgmentArtifactV1{
		Version:      verify.JudgmentV1,
		Run:          run,
		EscalationID: esc.ID,
		Subject:      verify.Subject{Repo: "o/r", Number: 128, HeadSHA: "stale"},
		Grant:        verify.JudgmentGrantV1{ID: grantArt.ID, MaxTier: "T2"},
		Question:     escBody.Question,
		Producer:     verify.Producer{Class: verify.ClassJudgment, Impl: "codex:test"},
		Decision:     verify.DecisionPass,
		Tier:         "T0",
		Confidence:   1,
		Why:          "test",
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "judgment.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	opts := judgmentOptions{ArtifactPath: path, Who: "operator"}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantArt.ID, opts); err == nil || !strings.Contains(err.Error(), "judgment_stale_head") {
		t.Fatalf("stale-head error = %v", err)
	}
	after, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("stale refusal mutated state: before %d artifacts, after %d", len(before), len(after))
	}
}

func TestNewCeilingParkCanBeJudgedWithWiderGrant(t *testing.T) {
	e := testEnv(t)
	narrow, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 129, HeadSHA: "abc"}
	codePass := verify.Verdict{
		Subject: subject, Source: "triage-floor",
		Producer: verify.Producer{Class: verify.ClassCode},
		Decision: verify.DecisionPass, Tier: "T2", Confidence: 1, Why: "sensitive path",
	}
	if _, err := verify.Record(e.st, run, nil, codePass); err != nil {
		t.Fatal(err)
	}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	reduced, err := verify.Reduce(subject, []verify.Verdict{
		codePass,
		{
			Subject: subject, Source: "readiness",
			Producer: verify.Producer{Class: verify.ClassLocal},
			Decision: verify.DecisionEscalate, Tier: "T0", Confidence: 1, Why: "panel disagreement",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rvID := recordReduced(t, e, run, reduced)
	if _, code, err := act(e, run, narrow.ID, reduced, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("content park: code %d err %v", code, err)
	}
	contentPark := firstOfKind(t, e, run, state.KindEscalation)
	if _, code, _, err := applyJudgment(e, run, contentPark.ID, narrow.ID, judgmentOptions{Decision: verify.DecisionPass, Why: "content is safe", Who: "operator"}); err != nil || code != codeParked {
		t.Fatalf("narrow-grant judgment should re-park at ceiling: code %d err %v", code, err)
	}
	ceilingPark := lastOfKind(t, e, run, state.KindEscalation)
	if ceilingPark.ID == contentPark.ID {
		t.Fatal("judgment did not create a distinct ceiling park")
	}
	wider, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, code, _, err := applyJudgment(e, run, ceilingPark.ID, wider.ID, judgmentOptions{Decision: verify.DecisionPass, Why: "wider grant authorizes the tier", Who: "operator"}); err != nil || code != codeMerge {
		t.Fatalf("new ceiling park must accept its own judgment: code %d err %v", code, err)
	}
}

func TestRetryResumesPersistedJudgmentBeforeReduction(t *testing.T) {
	e, run, grantID, _, esc, judgment, _ := resumableJudgmentFixture(t)
	jArt, err := e.st.AppendIfAbsentParent(state.KindJudgment, run, esc.ID, []string{esc.ID, grantID}, judgment)
	if err != nil {
		t.Fatal(err)
	}
	res, code, gotID, err := applyJudgment(e, run, esc.ID, grantID, judgmentOptions{})
	if err != nil {
		t.Fatalf("resume after persisted judgment: %v", err)
	}
	if code != codeMerge || res.Outcome != "would_merge" || gotID != jArt.ID {
		t.Fatalf("resume result = code %d outcome %q judgment %s", code, res.Outcome, gotID)
	}
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if got := countKindParent(arts, state.KindJudgment, esc.ID); got != 1 {
		t.Fatalf("resume appended %d judgments for one escalation", got)
	}
}

func TestRetryResumesAfterReducedVerdictPersisted(t *testing.T) {
	e, run, grantID, subject, esc, judgment, verdicts := resumableJudgmentFixture(t)
	jArt, err := e.st.AppendIfAbsentParent(state.KindJudgment, run, esc.ID, []string{esc.ID, grantID}, judgment)
	if err != nil {
		t.Fatal(err)
	}
	reduced, err := verify.Reduce(subject, append(verdicts, judgment))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AppendIfAbsentParent(state.KindVerdict, run, jArt.ID, []string{jArt.ID}, reduced); err != nil {
		t.Fatal(err)
	}
	res, code, gotID, err := applyJudgment(e, run, esc.ID, grantID, judgmentOptions{})
	if err != nil {
		t.Fatalf("resume after anchor-style post-fsync interruption: %v", err)
	}
	if code != codeMerge || res.Outcome != "would_merge" || gotID != jArt.ID {
		t.Fatalf("resume result = code %d outcome %q judgment %s", code, res.Outcome, gotID)
	}
}

func TestRetryReauthorizesPersistedJudgmentWithReplacementGrant(t *testing.T) {
	e, run, _, _, esc, judgment, _ := resumableJudgmentFixture(t)
	expired, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Second, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	jArt, err := e.st.AppendIfAbsentParent(state.KindJudgment, run, esc.ID, []string{esc.ID, expired.ID}, judgment)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	replacement, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	res, code, gotID, err := applyJudgment(e, run, esc.ID, replacement.ID, judgmentOptions{Decision: verify.DecisionPass, Who: "operator"})
	if err != nil {
		t.Fatalf("replacement grant must resume immutable content judgment: %v", err)
	}
	if code != codeMerge || res.Outcome != "would_merge" || gotID != jArt.ID {
		t.Fatalf("reauthorized resume = code %d outcome %q judgment %s", code, res.Outcome, gotID)
	}
	outcome := lastOfKind(t, e, run, state.KindAction)
	if !stateArtifactHasParent(outcome, replacement.ID) {
		t.Fatalf("outcome does not name replacement grant: %v", outcome.Parents)
	}
	if stateArtifactHasParent(outcome, expired.ID) {
		t.Fatalf("outcome incorrectly authorized by expired grant: %v", outcome.Parents)
	}
}

func TestProviderDelayExpiryRefusesBeforeJudgmentAppend(t *testing.T) {
	e, run, grantID, _, esc, _, _ := resumableJudgmentFixture(t)
	now := time.Now()
	e.now = func() time.Time { return now }
	opts := judgmentOptions{
		Decision: verify.DecisionPass,
		Why:      "provider completed after the grant expired",
		Who:      "operator",
		beforeAppend: func() {
			now = now.Add(2 * time.Hour)
		},
	}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantID, opts); err == nil || !strings.Contains(err.Error(), capability.ErrExpired.Error()) {
		t.Fatalf("post-provider expiry error = %v", err)
	}
	arts := mustRunArtifacts(t, e, run)
	if got := countKindParent(arts, state.KindJudgment, esc.ID); got != 0 {
		t.Fatalf("expired provider result mutated judgment state: %d artifacts", got)
	}
}

func TestCapabilityRefusalDoesNotCompletePersistedJudgment(t *testing.T) {
	e, run, originalGrant, subject, esc, judgment, verdicts := resumableJudgmentFixture(t)
	jArt, err := e.st.AppendIfAbsentParent(state.KindJudgment, run, esc.ID, []string{esc.ID, originalGrant}, judgment)
	if err != nil {
		t.Fatal(err)
	}
	reduced, err := verify.Reduce(subject, append(verdicts, judgment))
	if err != nil {
		t.Fatal(err)
	}
	reducedArt, err := e.st.AppendIfAbsentParent(state.KindVerdict, run, jArt.ID, []string{jArt.ID}, reduced)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", -time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if res, code, err := act(e, run, expired.ID, reduced, reducedArt.ID, gateResult{}, false, nil); err != nil || code != codeRefused || res.Outcome != "capability_refused" {
		t.Fatalf("refusal setup = code %d outcome %q err %v", code, res.Outcome, err)
	}
	replacement, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	res, code, gotID, err := applyJudgment(e, run, esc.ID, replacement.ID, judgmentOptions{Decision: verify.DecisionPass, Who: "operator"})
	if err != nil {
		t.Fatalf("replacement grant must resume after capability refusal: %v", err)
	}
	if code != codeMerge || res.Outcome != "would_merge" || gotID != jArt.ID {
		t.Fatalf("replacement result = code %d outcome %q judgment %s", code, res.Outcome, gotID)
	}
	arts := mustRunArtifacts(t, e, run)
	completed := 0
	for _, artifact := range arts {
		if stateArtifactHasParent(artifact, reducedArt.ID) && completedJudgmentOutcome(artifact) {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("completed outcomes = %d, want exactly one", completed)
	}
	if _, _, _, err := applyJudgment(e, run, esc.ID, replacement.ID, judgmentOptions{Decision: verify.DecisionPass, Who: "operator"}); err == nil || !strings.Contains(err.Error(), "judgment_duplicate") {
		t.Fatalf("post-authorization duplicate error = %v", err)
	}
}

func TestConflictingResolveRetryCannotChangePersistedDecision(t *testing.T) {
	e, run, grantID, _, esc, judgment, _ := resumableJudgmentFixture(t)
	jArt, err := e.st.AppendIfAbsentParent(state.KindJudgment, run, esc.ID, []string{esc.ID, grantID}, judgment)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantID, judgmentOptions{Decision: verify.DecisionBlock, Why: "conflicting retry", Who: "operator"}); err == nil || !strings.Contains(err.Error(), "judgment_retry_conflict") {
		t.Fatalf("conflicting retry error = %v", err)
	}
	if _, ok := artifactForParent(mustRunArtifacts(t, e, run), state.KindVerdict, jArt.ID); ok {
		t.Fatal("conflicting retry advanced the judgment chain")
	}
	res, _, gotID, err := applyJudgment(e, run, esc.ID, grantID, judgmentOptions{Decision: verify.DecisionPass, Why: "same decision retry", Who: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if err := stampResolution(e, run, esc.ID, gotID, res.Decision, "operator", verify.MethodCLIOperator); err != nil {
		t.Fatal(err)
	}
	assertResolutionStamp(t, e, run, esc.ID, gotID, verify.DecisionPass)
}

func mustRunArtifacts(t *testing.T, e env, run string) []state.Artifact {
	t.Helper()
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	return arts
}

func resumableJudgmentFixture(t *testing.T) (env, string, string, verify.Subject, state.Artifact, verify.Verdict, []verify.Verdict) {
	t.Helper()
	e := testEnv(t)
	grant, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 130, HeadSHA: "abc"}
	codePass := verify.Verdict{
		Subject: subject, Source: "triage-floor",
		Producer: verify.Producer{Class: verify.ClassCode},
		Decision: verify.DecisionPass, Tier: "T0", Confidence: 1, Why: "floor passes",
	}
	localEscalate := verify.Verdict{
		Subject: subject, Source: "review-consolidation",
		Producer: verify.Producer{Class: verify.ClassLocal},
		Decision: verify.DecisionEscalate, Tier: "T0", Confidence: 1, Why: "panel disagreement",
	}
	for _, verdict := range []verify.Verdict{codePass, localEscalate} {
		if _, err := verify.Record(e.st, run, nil, verdict); err != nil {
			t.Fatal(err)
		}
	}
	verdicts := []verify.Verdict{codePass, localEscalate}
	reduced, err := verify.Reduce(subject, verdicts)
	if err != nil {
		t.Fatal(err)
	}
	reducedID := recordReduced(t, e, run, reduced)
	if _, code, err := act(e, run, grant.ID, reduced, reducedID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)
	judgment := verify.Verdict{
		Subject: subject, Source: "operator-judgment",
		Producer: verify.Producer{Class: verify.ClassJudgment, Impl: "operator"},
		Decision: verify.DecisionPass, Tier: "T0", Confidence: 1, Why: "safe",
	}
	return e, run, grant.ID, subject, esc, judgment, verdicts
}

func countKindParent(arts []state.Artifact, kind, parentID string) int {
	count := 0
	for _, artifact := range arts {
		if artifact.Kind == kind && stateArtifactHasParent(artifact, parentID) {
			count++
		}
	}
	return count
}

func lastOfKind(t *testing.T, e env, run, kind string) state.Artifact {
	t.Helper()
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(arts) - 1; i >= 0; i-- {
		if arts[i].Kind == kind {
			return arts[i]
		}
	}
	t.Fatalf("run %s has no %s artifact", run, kind)
	return state.Artifact{}
}

// assertJudgmentParented checks the run's judgment is the one resolve recorded
// and is parented to the escalation it resolves.
func assertJudgmentParented(t *testing.T, e env, run, escID, wantJID string) {
	t.Helper()
	jArt := firstOfKind(t, e, run, state.KindJudgment)
	if jArt.ID != wantJID {
		t.Fatalf("judgment id mismatch: stamped %s, recorded %s", wantJID, jArt.ID)
	}
	if len(jArt.Parents) == 0 || jArt.Parents[0] != escID {
		t.Fatalf("judgment must be parented to the escalation, got parents %v", jArt.Parents)
	}
}

// assertResolutionStamp checks the resolution artifact carries full provenance
// and links both the escalation and the judgment.
func assertResolutionStamp(t *testing.T, e env, run, escID, jID, wantDecision string) {
	t.Helper()
	resArt := firstOfKind(t, e, run, state.KindResolution)
	stamp, err := decodeResolution(resArt.Body)
	if err != nil {
		t.Fatalf("resolution body must decode: %v", err)
	}
	if stamp.Decision != wantDecision || stamp.Who != "operator" || stamp.JudgmentID != jID || stamp.At == "" {
		t.Fatalf("resolution lost provenance: %+v", stamp)
	}
	if len(resArt.Parents) != 2 || resArt.Parents[0] != escID || resArt.Parents[1] != jID {
		t.Fatalf("resolution must be parented to [escalation, judgment], got %v", resArt.Parents)
	}
}

// assertChainIntact checks the append-only log still audits clean — closing the
// loop must leave the hash chain verifiable.
func assertChainIntact(t *testing.T, e env) {
	t.Helper()
	audit, err := e.st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if !audit.OK {
		t.Fatalf("the chain must still verify after the loop closes: %s", audit.Reason)
	}
}

// TestEscalationIsOpenGuard pins the replay/stale-id guard: an escalation is
// resolvable only while it is the run's newest terminal. Once a resolve records
// a terminal action (the merge), the same escalation id is no longer open — a
// retried callback or a double-tapped button must not append a second
// authoritative outcome for one park.
func TestEscalationIsOpenGuard(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 126, HeadSHA: "abc"}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	rvID := recordReduced(t, e, run, rv)
	if _, code, err := act(e, run, grantArt.ID, rv, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)

	// Fresh park: the escalation is the newest terminal → open.
	open, err := escalationIsOpen(e, run, esc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !open {
		t.Fatal("a fresh park's escalation must be open")
	}

	// Resolve it (pass → would_merge writes a terminal action).
	res, _, judgmentID, err := applyJudgment(e, run, esc.ID, grantArt.ID, judgmentOptions{Decision: verify.DecisionPass, Why: "safe to land", Who: "operator"})
	if err != nil {
		t.Fatalf("applyJudgment: %v", err)
	}
	if err := stampResolution(e, run, esc.ID, judgmentID, res.Decision, "operator", verify.MethodCLIOperator); err != nil {
		t.Fatal(err)
	}

	// A later terminal action now supersedes the escalation → no longer open, so
	// a replayed resolve of the same id is refused.
	open, err = escalationIsOpen(e, run, esc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if open {
		t.Fatal("a resolved escalation must not read as open — the replay guard would let a second outcome land")
	}
}

// TestResolveRefusesEscalationSupersededBeforeAppend pins the compare-and-resolve:
// the open-terminal test belongs to the judgment write, not to a read that
// happened before it. cmdResolve's escalationIsOpen check is unlocked, so a
// re-park landing between that check and the append would otherwise let a
// judgment decide a park that is no longer the run's open one. The uniqueness
// guard cannot catch this case — it is keyed on this escalation, and there is
// only ever one judgment for it. beforeAppend stands in for the concurrent
// re-park an out-of-process resolve would race.
func TestResolveRefusesEscalationSupersededBeforeAppend(t *testing.T) {
	e, run, grantID, _, esc, _, _ := resumableJudgmentFixture(t)
	opts := judgmentOptions{
		Decision:              verify.DecisionPass,
		Why:                   "approved from the phone",
		Who:                   "@mhdevstuff (U123)",
		Method:                verify.MethodSlackInteractive,
		requireOpenEscalation: true,
		beforeAppend: func() {
			if _, err := e.st.Append(state.KindEscalation, run, []string{esc.ID}, map[string]any{"outcome": "parked_for_judgment"}); err != nil {
				t.Fatal(err)
			}
		},
	}
	_, _, _, err := applyJudgment(e, run, esc.ID, grantID, opts)
	if !errors.Is(err, errStaleEscalation) {
		t.Fatalf("superseded resolve error = %v, want errStaleEscalation", err)
	}
	arts := mustRunArtifacts(t, e, run)
	if got := countKindParent(arts, state.KindJudgment, esc.ID); got != 0 {
		t.Fatalf("a superseded resolve recorded a judgment anyway: %d artifacts", got)
	}
}

// TestJudgeDoesNotTakeTheResolveOnlyOpenGuard pins that the guard above is
// opt-in — and pins the accepted cost, because the setup below IS the race:
// it supersedes the escalation before the append and asserts the judgment
// lands anyway.
//
// Judge is exempt because it derives the escalation itself, so its exposure is
// the program-scale window between that read and the append rather than the
// human-scale stale id resolve is handed. That is a probability judgement, not
// immunity: the window is real, and failing judge on it would cost operators a
// retry for a race that is practically improbable. FOLLOWUPS "Still open (3)"
// carries it. Recorded as a test so extending the guard later reads as a
// deliberate change of that trade-off, not as fixing an oversight.
func TestJudgeDoesNotTakeTheResolveOnlyOpenGuard(t *testing.T) {
	e, run, grantID, _, esc, _, _ := resumableJudgmentFixture(t)
	opts := judgmentOptions{
		Decision: verify.DecisionPass,
		Why:      "operator judged from the run",
		Who:      "operator",
		beforeAppend: func() {
			if _, err := e.st.Append(state.KindEscalation, run, []string{esc.ID}, map[string]any{"outcome": "parked_for_judgment"}); err != nil {
				t.Fatal(err)
			}
		},
	}
	if _, _, _, err := applyJudgment(e, run, esc.ID, grantID, opts); err != nil {
		t.Fatalf("judge must not inherit the resolve-only open-terminal guard: %v", err)
	}
}

// TestRunOfEscalationRejectsWrongKind pins that resolve fails loudly on an id
// that is not an escalation, so a mistyped or wrong-kind id never resolves the
// wrong run.
func TestRunOfEscalationRejectsWrongKind(t *testing.T) {
	e := testEnv(t)
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	// A grant artifact is a real id of the wrong kind.
	if _, err := runOfEscalation(e, grantArt.ID); err == nil {
		t.Fatal("a non-escalation id must be rejected")
	}
	if _, err := runOfEscalation(e, "esc_deadbeef"); err == nil {
		t.Fatal("an absent escalation id must be rejected")
	}
}

func decodeResolution(body []byte) (escalation.Resolution, error) {
	var r escalation.Resolution
	if err := json.Unmarshal(body, &r); err != nil {
		return escalation.Resolution{}, err
	}
	return r, nil
}

// parkedRunForDecider builds a run parked on a content escalation, returning the
// run id, the grant, and the escalation — the exact state a judgment resolves.
func parkedRunForDecider(t *testing.T, e env, number int) (string, state.Artifact, state.Artifact) {
	t.Helper()
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: number, HeadSHA: "abc"}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	rv := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	rvID := recordReduced(t, e, run, rv)
	if _, code, err := act(e, run, grantArt.ID, rv, rvID, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park: code %d err %v", code, err)
	}
	return run, grantArt, firstOfKind(t, e, run, state.KindEscalation)
}

// TestJudgmentRecordsItsDecider is the separation-of-duties invariant this
// binding exists for: the judgment ARTIFACT — not a sidecar, not the free-text
// why — names who decided and through which channel, so a human approval and an
// agent-composed one stop being the same record.
func TestJudgmentRecordsItsDecider(t *testing.T) {
	e := testEnv(t)
	run, grantArt, esc := parkedRunForDecider(t, e, 620)
	at := time.Date(2026, 8, 21, 22, 15, 0, 0, time.UTC)
	_, _, judgmentID, err := applyJudgment(e, run, esc.ID, grantArt.ID, judgmentOptions{
		Decision: verify.DecisionPass,
		Why:      "reviewed the diff; the residual is a nit",
		Who:      "@mhdevstuff (U123)",
		Method:   verify.MethodSlackInteractive,
		now:      func() time.Time { return at },
	})
	if err != nil {
		t.Fatalf("applyJudgment: %v", err)
	}
	artifact, err := e.st.Get(judgmentID)
	if err != nil {
		t.Fatal(err)
	}
	judgment, err := verify.Load(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if judgment.Decider == nil {
		t.Fatal("the judgment artifact must carry its decider — a sidecar resolution is not the decision")
	}
	if judgment.Decider.Who != "@mhdevstuff (U123)" {
		t.Fatalf("decider.who = %q, want the identity the transport established", judgment.Decider.Who)
	}
	if judgment.Decider.Method != verify.MethodSlackInteractive {
		t.Fatalf("decider.method = %q, want %q", judgment.Decider.Method, verify.MethodSlackInteractive)
	}
	if judgment.Decider.At != "2026-08-21T22:15:00Z" {
		t.Fatalf("decider.at = %q, want the decider's own clock", judgment.Decider.At)
	}
	// The binding must survive the wire, not just the Go struct: an auditor reads
	// log.jsonl, and a field that only exists in memory closes nothing.
	if !strings.Contains(string(artifact.Body), `"decider"`) {
		t.Fatalf("the decider must be persisted in the artifact body, got %s", artifact.Body)
	}
}

// TestUnattributedJudgmentIsRefusedBeforeTheOneShotIsSpent pins the refusal at
// the point that matters. A judgment is irreversible once appended, so refusing
// after the write would burn the escalation's single judgment on a record that
// names nobody — worse than the gap it was meant to close.
func TestUnattributedJudgmentIsRefusedBeforeTheOneShotIsSpent(t *testing.T) {
	e := testEnv(t)
	run, grantArt, esc := parkedRunForDecider(t, e, 621)
	before, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	_, _, judgmentID, err := applyJudgment(e, run, esc.ID, grantArt.ID, judgmentOptions{
		Decision: verify.DecisionPass, Why: "safe",
	})
	if err == nil {
		t.Fatal("a judgment naming nobody must be refused")
	}
	if !errors.Is(err, verify.ErrDeciderMissing) {
		t.Fatalf("refusal must be classifiable as ErrDeciderMissing, got %v", err)
	}
	if judgmentID != "" {
		t.Fatalf("a refused judgment must return no id, got %s", judgmentID)
	}
	after, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the refusal appended %d artifact(s); the one-shot must be unspent", len(after)-len(before))
	}
	// Unspent means retryable: the same escalation still takes a judgment once it
	// names its decider.
	if _, _, id, err := applyJudgment(e, run, esc.ID, grantArt.ID, judgmentOptions{
		Decision: verify.DecisionPass, Why: "safe", Who: "operator",
	}); err != nil || id == "" {
		t.Fatalf("the escalation must still be judgeable after the refusal: id %q err %v", id, err)
	}
}

// TestJudgeFlagsRequireADecider pins the CLI surface: every path a person
// authors demands -who, and -auto refuses one because the provider is the
// decider and gate derives it.
func TestJudgeFlagsRequireADecider(t *testing.T) {
	cases := []struct {
		name    string
		opts    judgmentOptions
		wantErr string
	}{
		{"manual without who", judgmentOptions{Decision: verify.DecisionPass, Why: "safe"}, "-who required"},
		{"manual with who", judgmentOptions{Decision: verify.DecisionPass, Why: "safe", Who: "mh"}, ""},
		{"manual with slack channel", judgmentOptions{Decision: verify.DecisionPass, Why: "safe", Who: "mh", Method: verify.MethodSlackInteractive}, ""},
		{"manual with an unknown channel", judgmentOptions{Decision: verify.DecisionPass, Why: "safe", Who: "mh", Method: "carrier-pigeon"}, "judgment_unattributed"},
		{"submitted artifact without who", judgmentOptions{ArtifactPath: "j.json"}, "-who required"},
		{"submitted artifact with who", judgmentOptions{ArtifactPath: "j.json", Who: "mh"}, ""},
		{"auto derives its own decider", judgmentOptions{Auto: true, Provider: "claude"}, ""},
		{"auto refuses a claimed decider", judgmentOptions{Auto: true, Provider: "claude", Who: "mh"}, "not accepted with -auto"},
		{"auto refuses a claimed channel", judgmentOptions{Auto: true, Provider: "claude", Method: verify.MethodCLIOperator}, "not accepted with -auto"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateJudgeFlags("run_1", "grt_1", c.opts)
			if c.wantErr == "" && err != nil {
				t.Fatalf("validateJudgeFlags = %v, want nil", err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("validateJudgeFlags = %v, want an error containing %q", err, c.wantErr)
			}
		})
	}
}

// TestResolveFlagsCarryTheChannel pins that the back-channel's -method reaches
// validation: the same operator string typed at a shell and produced by a
// verified Slack callback are different facts, and only the channel separates
// them.
func TestResolveFlagsCarryTheChannel(t *testing.T) {
	if err := validateResolveFlags("esc_1", "grt_1", verify.DecisionPass, "approved", "@mhdevstuff (U123)", verify.MethodSlackInteractive); err != nil {
		t.Fatalf("a slack-interactive resolution must validate: %v", err)
	}
	if err := validateResolveFlags("esc_1", "grt_1", verify.DecisionPass, "approved", "@mhdevstuff (U123)", "carrier-pigeon"); err == nil {
		t.Fatal("an unknown channel must be refused")
	}
	if err := validateResolveFlags("esc_1", "grt_1", verify.DecisionPass, "approved", "", verify.MethodSlackInteractive); err == nil {
		t.Fatal("a resolution naming nobody must be refused")
	}
}
