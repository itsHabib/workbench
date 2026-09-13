package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
	"github.com/itsHabib/workbench/contracts/reviewroute"
)

// These are original GitHub comments, including authenticated actor metadata,
// from workbench#334. The fixture retains their source URLs and timestamps.
func recordedCompletionFixture(t *testing.T) (reviewroute.Plan, []issueComment) {
	t.Helper()
	data, err := os.ReadFile("../../contracts/reviewpanel/testdata/workbench-334-comments.json")
	if err != nil {
		t.Fatal(err)
	}
	var comments []issueComment
	if err := json.Unmarshal(data, &comments); err != nil {
		t.Fatal(err)
	}
	plan := reviewroute.Plan{
		Subject: reviewroute.Subject{
			Repo: "itsHabib/workbench", Number: 334,
			HeadSHA: "d9ca988176bd47beb6afc33a70b31092b4302e0e",
		},
		Required: []string{"claude", "codex"},
	}
	return plan, comments
}

func TestRecordedReviewCompletion(t *testing.T) {
	plan, comments := recordedCompletionFixture(t)
	panel := buildPanel(plan, nil, comments, nil)
	t.Logf("review.observe: completed=%+v missing=%v", panel.Completed, panel.Missing)
	if err := reviewpanel.Validate(panel); err != nil {
		t.Fatal(err)
	}
	if len(panel.Completed) != 2 || len(panel.Missing) != 0 {
		t.Fatalf("recorded exact-head reviews must complete both slots: %+v", panel)
	}
	if panel.Completed[0].ReviewID != 5653962971 || panel.Completed[0].State != "COMMENTED" ||
		panel.Completed[1].ReviewID != 5653964389 || panel.Completed[1].State != "CLEAN" {
		t.Fatalf("completion provenance or state changed: %+v", panel.Completed)
	}
	stale := plan
	stale.Subject.HeadSHA = testHeadB
	if got := buildPanel(stale, nil, comments, nil); len(got.Completed) != 0 {
		t.Fatalf("stale evidence counted at a new head: %+v", got)
	}
	other := plan
	other.Required = []string{"cursor"}
	if got := buildPanel(other, nil, comments, nil); len(got.Completed) != 0 {
		t.Fatalf("evidence completed the wrong reviewer: %+v", got)
	}
	formal := []rawReview{review("claude[bot]", "CHANGES_REQUESTED", plan.Subject.HeadSHA, 99)}
	if got := buildPanel(plan, formal, comments, nil); got.Completed[0].State != "CHANGES_REQUESTED" ||
		got.Completed[0].ReviewID != 99 {
		t.Fatalf("attestation hid a formal change request: %+v", got)
	}
	for i := range comments {
		comments[i].User.Type = "User"
	}
	if got := buildPanel(plan, nil, comments, nil); len(got.Completed) != 0 {
		t.Fatalf("bot login without bot metadata completed: %+v", got)
	}
	for i := range comments {
		comments[i].User.Login = "itsHabib"
		comments[i].User.Type = "User"
	}
	if got := buildPanel(plan, nil, comments, nil); len(got.Completed) != 0 {
		t.Fatalf("human copies counted as provider evidence: %+v", got)
	}
}

func TestRecordedCommentTrust(t *testing.T) {
	plan, comments := recordedCompletionFixture(t)
	for name, mutate := range map[string]func(*issueComment){
		"missing source ID": func(c *issueComment) { c.ID = 0 },
		"wrong issuer":      func(c *issueComment) { c.User.Login = "codex[bot]" },
	} {
		t.Run(name, func(t *testing.T) {
			copies := slices.Clone(comments)
			for i := range copies {
				mutate(&copies[i])
			}
			if got := buildPanel(plan, nil, copies, nil); len(got.Completed) != 0 {
				t.Fatalf("untrusted comments completed: %+v", got)
			}
		})
	}
	withFindings := slices.Clone(comments)
	withFindings[1].Body = strings.Replace(withFindings[1].Body, "Didn't find any major issues.", "Found a P1 issue.", 1)
	if got := buildPanel(plan, nil, withFindings, nil); len(got.Completed) != 2 || got.Completed[1].State != "COMMENTED" {
		t.Fatalf("findings must complete without being marked clean: %+v", got)
	}
}
