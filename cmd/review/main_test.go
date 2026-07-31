package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/review/internal/policy"
	"github.com/itsHabib/workbench/contracts/reviewroute"
)

const (
	testHeadA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testHeadB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var mainTestNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

type fakeRunner struct {
	run func(input []byte, name string, args ...string) ([]byte, error)
}

func (runner fakeRunner) Run(
	_ context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	return runner.run(nil, name, args...)
}

func (runner fakeRunner) RunInput(
	_ context.Context,
	input []byte,
	name string,
	args ...string,
) ([]byte, error) {
	return runner.run(input, name, args...)
}

func TestPlanRoutesCurrentExactHead(t *testing.T) {
	temp := t.TempDir()
	policyPath := writeTestPolicy(t, temp)
	out := filepath.Join(temp, "plan.json")
	runner := planningRunner(testHeadA, testHeadA, `{
		"floor":"T1","signals":[{"signal":"size","tier":"T1","why":"medium diff"}],
		"files":2,"added":20,"removed":1
	}`)
	code := run(context.Background(), []string{
		"plan", "-repo", "itsHabib/ship", "-pr", "7", "-head", testHeadA,
		"-policy", policyPath, "-out", out,
	}, runner, io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var plan reviewroute.Plan
	readJSON(t, out, &plan)
	if plan.Disposition != "tier_routed" || plan.Classification.Tier != "T1" {
		t.Fatalf("plan = %s %#v", plan.Disposition, plan.Classification)
	}
	if got := namesOf(plan.Reviewers); !slices.Equal(got, []string{"codex"}) {
		t.Fatalf("reviewers = %v", got)
	}
}

func TestPlanUsesCheckedInPolicyWithoutCallerVersionSelection(t *testing.T) {
	temp := t.TempDir()
	out := filepath.Join(temp, "plan.json")
	code := run(context.Background(), []string{
		"plan", "-repo", "itsHabib/ship", "-pr", "7", "-head", testHeadA, "-out", out,
	}, planningRunner(testHeadA, testHeadA, `{
		"floor":"T1","signals":[],"files":1,"added":1,"removed":0
	}`), io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var plan reviewroute.Plan
	readJSON(t, out, &plan)
	if plan.Policy == nil || plan.Policy.ID != "tier-aware-canary" ||
		!strings.HasPrefix(plan.Policy.Digest, "sha256:") {
		t.Fatalf("policy = %#v", plan.Policy)
	}
}

func TestMalformedClassifierFallsBackToFullPanel(t *testing.T) {
	temp := t.TempDir()
	out := filepath.Join(temp, "plan.json")
	code := run(context.Background(), []string{
		"plan", "-repo", "itsHabib/ship", "-pr", "7", "-head", testHeadA,
		"-policy", writeTestPolicy(t, temp), "-out", out,
	}, planningRunner(testHeadA, testHeadA, `{"floor":"T9"}`),
		io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var plan reviewroute.Plan
	readJSON(t, out, &plan)
	if plan.Disposition != "full_panel_fallback" || len(plan.Reviewers) != 4 {
		t.Fatalf("plan = %s reviewers %d", plan.Disposition, len(plan.Reviewers))
	}
	if !strings.Contains(plan.Reason, "triage-floor") {
		t.Fatalf("reason = %q", plan.Reason)
	}
}

func TestMissingClassifierFallsBackToFullPanel(t *testing.T) {
	temp := t.TempDir()
	out := filepath.Join(temp, "plan.json")
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			return []byte(`{"headRefOid":"` + testHeadA + `","state":"OPEN"}`), nil
		}
		if name == "gh" && slices.Contains(args, "diff") {
			return []byte("diff --git a/a b/a"), nil
		}
		if name == "missing-triage-floor" {
			return nil, errors.New("executable file not found")
		}
		return nil, errors.New("unexpected command")
	}}
	code := run(context.Background(), []string{
		"plan", "-repo", "itsHabib/ship", "-pr", "7", "-head", testHeadA,
		"-triage-bin", "missing-triage-floor", "-out", out,
	}, runner, io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var plan reviewroute.Plan
	readJSON(t, out, &plan)
	if plan.Disposition != "full_panel_fallback" || len(plan.Reviewers) != 4 {
		t.Fatalf("plan = %s reviewers %d", plan.Disposition, len(plan.Reviewers))
	}
	if !strings.Contains(plan.Reason, "triage-floor failed") {
		t.Fatalf("reason = %q", plan.Reason)
	}
}

func TestHeadChangeDuringClassificationFallsBackOnNewHead(t *testing.T) {
	temp := t.TempDir()
	out := filepath.Join(temp, "plan.json")
	code := run(context.Background(), []string{
		"plan", "-repo", "itsHabib/ship", "-pr", "7", "-head", testHeadA,
		"-policy", writeTestPolicy(t, temp), "-out", out,
	}, planningRunner(testHeadA, testHeadB, `{
		"floor":"T1","signals":[],"files":1,"added":1,"removed":0
	}`), io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var plan reviewroute.Plan
	readJSON(t, out, &plan)
	if plan.Disposition != "full_panel_fallback" || plan.Subject.HeadSHA != testHeadB {
		t.Fatalf("plan = %s head %s", plan.Disposition, plan.Subject.HeadSHA)
	}
}

func TestInvalidPolicyCannotReduceReview(t *testing.T) {
	temp := t.TempDir()
	policyPath := filepath.Join(temp, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(temp, "plan.json")
	code := run(context.Background(), []string{
		"plan", "-repo", "itsHabib/ship", "-pr", "7", "-head", testHeadA,
		"-policy", policyPath, "-out", out,
	}, planningRunner(testHeadA, testHeadA, `{
		"floor":"T0","signals":[],"files":1,"added":1,"removed":0
	}`), io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var plan reviewroute.Plan
	readJSON(t, out, &plan)
	if plan.Disposition != "full_panel_fallback" || len(plan.Reviewers) != 4 {
		t.Fatalf("plan = %s reviewers %d", plan.Disposition, len(plan.Reviewers))
	}
}

func TestRequestTargetsOnlyNamedReviewer(t *testing.T) {
	temp := t.TempDir()
	plan := testPlan(t, "T2")
	planPath := filepath.Join(temp, "plan.json")
	writeJSON(t, planPath, plan)
	out := filepath.Join(temp, "request.json")
	var mentions []string
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			return []byte(`{"headRefOid":"` + testHeadA + `","state":"OPEN"}`), nil
		}
		if name == "gh" && slices.Contains(args, "comment") {
			mentions = append(mentions, args[len(args)-1])
			return []byte("https://example/comment/1"), nil
		}
		return nil, errors.New("unexpected command")
	}}
	code := run(context.Background(), []string{
		"request", "-plan", planPath, "-reviewers", "codex", "-out", out,
	}, runner, io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !slices.Equal(mentions, []string{"@codex review"}) {
		t.Fatalf("mentions = %v", mentions)
	}
	var receipt reviewroute.RequestReceipt
	readJSON(t, out, &receipt)
	if receipt.Status != "requested" || len(receipt.Requests) != 1 {
		t.Fatalf("receipt = %s requests %d", receipt.Status, len(receipt.Requests))
	}
}

func TestRequestFailsWhenGitHubDoesNotRecordReviewer(t *testing.T) {
	temp := t.TempDir()
	plan := testPlan(t, "T2")
	planPath := filepath.Join(temp, "plan.json")
	writeJSON(t, planPath, plan)
	out := filepath.Join(temp, "request.json")
	var attempted []string
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			return []byte(`{"headRefOid":"` + testHeadA + `","state":"OPEN"}`), nil
		}
		if name == "gh" && slices.Contains(args, "api") &&
			slices.Contains(args, "POST") {
			attempted = append(attempted, args[len(args)-1])
			return []byte(`{"requested_reviewers":[]}`), nil
		}
		if name == "gh" && slices.Contains(args, "api") {
			return []byte(`[]`), nil
		}
		return nil, errors.New("unexpected command")
	}}
	code := run(context.Background(), []string{
		"request", "-plan", planPath, "-reviewers", "copilot", "-out", out,
	}, runner, io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	wantAttempts := []string{
		"reviewers[]=Copilot",
		"reviewers[]=copilot-pull-request-reviewer",
		"reviewers[]=copilot-pull-request-reviewer[bot]",
	}
	if !slices.Equal(attempted, wantAttempts) {
		t.Fatalf("attempted = %v", attempted)
	}
	var receipt reviewroute.RequestReceipt
	readJSON(t, out, &receipt)
	if receipt.Status != "failed" || len(receipt.Requests) != 1 {
		t.Fatalf("receipt = %s requests %d", receipt.Status, len(receipt.Requests))
	}
	if receipt.Requests[0].Status != "failed" ||
		!strings.Contains(receipt.Requests[0].Ref, "did not record") {
		t.Fatalf("request = %#v", receipt.Requests[0])
	}
}

func TestRequestAcceptsFreshCopilotTimelineEvidence(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name != "gh" || !slices.Contains(args, "api") {
			return nil, errors.New("unexpected command")
		}
		if slices.Contains(args, "POST") {
			return []byte(`{"requested_reviewers":[]}`), nil
		}
		if strings.Contains(args[len(args)-1], "/timeline?") {
			return []byte(`[{
				"event":"copilot_work_started",
				"created_at":"` + now + `",
				"actor":{"login":"Copilot"}
			}]`), nil
		}
		return []byte(`[]`), nil
	}}
	ref, err := requestGitHubReviewer(
		context.Background(),
		runner,
		reviewroute.Subject{
			Repo: "itsHabib/ship", Number: 7, HeadSHA: testHeadA,
		},
		"copilot",
	)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "timeline:copilot_work_started" {
		t.Fatalf("ref = %q", ref)
	}
}

func TestRequestAcceptsRecordedCopilotReviewer(t *testing.T) {
	recorded, err := responseRecordsReviewer(
		[]byte(`{"requested_reviewers":[{"login":"Copilot"}]}`),
		"copilot-pull-request-reviewer[bot]",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !recorded {
		t.Fatal("Copilot reviewer was not recognized")
	}
}

func TestStalePlanRefusesBeforeRequestWrites(t *testing.T) {
	temp := t.TempDir()
	planPath := filepath.Join(temp, "plan.json")
	writeJSON(t, planPath, testPlan(t, "T1"))
	writes := 0
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			return []byte(`{"headRefOid":"` + testHeadB + `","state":"OPEN"}`), nil
		}
		writes++
		return nil, nil
	}}
	code := run(context.Background(), []string{
		"request", "-plan", planPath, "-out", filepath.Join(temp, "request.json"),
	}, runner, io.Discard, io.Discard)
	if code != exitRefused || writes != 0 {
		t.Fatalf("exit = %d writes = %d", code, writes)
	}
}

func TestRequestStopsBeforeNextWriteWhenHeadChanges(t *testing.T) {
	temp := t.TempDir()
	planPath := filepath.Join(temp, "plan.json")
	writeJSON(t, planPath, testPlan(t, "T2"))
	out := filepath.Join(temp, "request.json")
	headCalls := 0
	var writes []string
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			head := testHeadA
			if headCalls >= 2 {
				head = testHeadB
			}
			headCalls++
			return []byte(`{"headRefOid":"` + head + `","state":"OPEN"}`), nil
		}
		if name == "gh" && slices.Contains(args, "comment") {
			writes = append(writes, args[len(args)-1])
			return []byte("https://example/comment/1"), nil
		}
		return nil, errors.New("unexpected command")
	}}
	code := run(context.Background(), []string{
		"request", "-plan", planPath, "-out", out,
	}, runner, io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !slices.Equal(writes, []string{"@codex review"}) {
		t.Fatalf("writes = %v", writes)
	}
	var receipt reviewroute.RequestReceipt
	readJSON(t, out, &receipt)
	if receipt.Status != "stale" || receipt.HeadAfter != testHeadB {
		t.Fatalf("receipt = %s head %s", receipt.Status, receipt.HeadAfter)
	}
}

func TestDecideRefusesWhenLiveHeadChanges(t *testing.T) {
	for _, tt := range []struct {
		name   string
		before string
		after  string
	}{
		{name: "stale before decision", before: testHeadB, after: testHeadB},
		{name: "changed during decision", before: testHeadA, after: testHeadB},
	} {
		t.Run(tt.name, func(t *testing.T) {
			temp := t.TempDir()
			plan := testPlan(t, "T1")
			planPath := filepath.Join(temp, "plan.json")
			inputPath := filepath.Join(temp, "input.json")
			out := filepath.Join(temp, "decision.json")
			writeJSON(t, planPath, plan)
			writeJSON(t, inputPath, reviewroute.CycleInput{
				SchemaVersion:       reviewroute.SchemaVersion,
				Subject:             plan.Subject,
				PlanID:              plan.PlanID,
				Cycle:               1,
				CurrentTier:         "T1",
				ChecksPassed:        true,
				PanelComplete:       true,
				CoordinatorComplete: true,
				AdversarialComplete: true,
				Findings:            []reviewroute.FindingState{},
			})
			runner := planningRunner(tt.before, tt.after, "")
			code := run(context.Background(), []string{
				"decide", "-plan", planPath, "-input", inputPath, "-out", out,
			}, runner, io.Discard, io.Discard)
			if code != exitRefused {
				t.Fatalf("exit = %d", code)
			}
			if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("decision output exists or stat failed: %v", err)
			}
		})
	}
}

func TestDecideRefusesInvalidPlanBeforeNetwork(t *testing.T) {
	temp := t.TempDir()
	plan := testPlan(t, "T1")
	plan.MaxCycles++
	planPath := filepath.Join(temp, "plan.json")
	inputPath := filepath.Join(temp, "input.json")
	writeJSON(t, planPath, plan)
	writeJSON(t, inputPath, reviewroute.CycleInput{})
	calls := 0
	runner := fakeRunner{run: func(_ []byte, _ string, _ ...string) ([]byte, error) {
		calls++
		return nil, errors.New("network should not be called")
	}}
	code := run(context.Background(), []string{
		"decide", "-plan", planPath, "-input", inputPath,
		"-out", filepath.Join(temp, "decision.json"),
	}, runner, io.Discard, io.Discard)
	if code != exitRefused || calls != 0 {
		t.Fatalf("exit = %d calls = %d", code, calls)
	}
}

func TestAdvisoryVerifierRejectsUnknownRecommendation(t *testing.T) {
	if validAdvisory(json.RawMessage(`{
		"recommendation":"ignore","rationale":"no","confidence":1
	}`)) {
		t.Fatal("unknown recommendation accepted")
	}
	if !validAdvisory(json.RawMessage(`{
		"recommendation":"defer","rationale":"noncritical and tracked","confidence":0.8
	}`)) {
		t.Fatal("valid recommendation rejected")
	}
}

func planningRunner(before, after, floor string) fakeRunner {
	headCalls := 0
	return fakeRunner{run: func(input []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			head := before
			if headCalls > 0 {
				head = after
			}
			headCalls++
			return []byte(`{"headRefOid":"` + head + `","state":"OPEN"}`), nil
		}
		if name == "gh" && slices.Contains(args, "diff") {
			return []byte("diff --git a/a b/a"), nil
		}
		if name == "triage-floor" {
			if string(input) != "diff --git a/a b/a" ||
				!slices.Equal(args, []string{"-repo", "itsHabib/ship"}) {
				return nil, errors.New("triage input mismatch")
			}
			return []byte(floor), nil
		}
		return nil, errors.New("unexpected command")
	}}
}

func writeTestPolicy(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "policy.json")
	writeJSON(t, path, testPolicyValue())
	return path
}

func testPlan(t *testing.T, tier string) reviewroute.Plan {
	t.Helper()
	config := testPolicyValue()
	subject := reviewroute.Subject{Repo: "itshabib/ship", Number: 7, HeadSHA: testHeadA}
	plan, err := policy.Route(config, subject,
		reviewroute.Classification{Tier: tier}, mainTestNow)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func testPolicyValue() reviewroute.Policy {
	panel := []reviewroute.Reviewer{
		{Name: "codex", Trigger: "mention"},
		{Name: "claude", Trigger: "mention"},
		{Name: "cursor", Trigger: "mention"},
		{Name: "copilot", Trigger: "reviewer-request"},
	}
	tier := func(reviewers, required []string, cycles int, coordinator string) reviewroute.TierPolicy {
		return reviewroute.TierPolicy{
			Reviewers: reviewers, Required: required, MaxCycles: cycles,
			Requirements: reviewroute.Requirements{
				Coordinator: coordinator, AllowProofSubstitution: true,
			},
		}
	}
	return reviewroute.Policy{
		SchemaVersion:       reviewroute.SchemaVersion,
		ID:                  "tier-aware-canary",
		EnabledRepositories: []string{"itsHabib/ship"}, FullPanel: panel,
		Tiers: map[string]reviewroute.TierPolicy{
			"T0": tier(nil, nil, 1, "none"),
			"T1": tier([]string{"codex"}, []string{"codex"}, 3, "on-findings"),
			"T2": tier([]string{"codex", "cursor", "copilot"}, []string{"codex", "cursor"}, 3, "required"),
			"T3": tier([]string{"codex", "claude", "cursor", "copilot"}, []string{"codex", "claude", "cursor"}, 8, "required"),
		},
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func namesOf(reviewers []reviewroute.Reviewer) []string {
	names := make([]string, 0, len(reviewers))
	for _, reviewer := range reviewers {
		names = append(names, reviewer.Name)
	}
	slices.Sort(names)
	return names
}
