package evidence

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

func recordedCompletionFixture(t *testing.T) (reviewpanel.Evidence, []Comment) {
	t.Helper()
	data, err := os.ReadFile("../../../../contracts/reviewpanel/testdata/workbench-334-comments.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw []rawComment
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var comments []Comment
	for _, comment := range raw {
		comments = append(comments, Comment{
			ID: comment.ID, Body: comment.Body,
			Author: comment.User.Login, IsBot: comment.User.Type == "Bot",
		})
	}
	panel := reviewpanel.Evidence{
		SchemaVersion: reviewpanel.SchemaVersion,
		Subject: reviewpanel.Subject{
			Repo: "itsHabib/workbench", Number: 334,
			HeadSHA: "d9ca988176bd47beb6afc33a70b31092b4302e0e",
		},
		Declaration: reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"claude", "codex"}},
	}
	return panel, comments
}

func TestRecordedReviewCompletion(t *testing.T) {
	panel, comments := recordedCompletionFixture(t)
	got := classifyPanel(panel, nil, nil, comments)
	t.Logf("gate.evidence: completed=%+v missing=%v", got.Completed, got.Missing)
	if err := reviewpanel.Validate(got); err != nil {
		t.Fatal(err)
	}
	if len(got.Completed) != 2 || len(got.Missing) != 0 {
		t.Fatalf("recorded exact-head reviews must complete both slots: %+v", got)
	}
	if got.Completed[0].ReviewID != 5653962971 || got.Completed[0].State != "COMMENTED" ||
		got.Completed[1].ReviewID != 5653964389 || got.Completed[1].State != "CLEAN" {
		t.Fatalf("completion provenance or state changed: %+v", got.Completed)
	}
	stale := panel
	stale.Subject.HeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got := classifyPanel(stale, nil, nil, comments); len(got.Completed) != 0 {
		t.Fatalf("stale evidence counted at a new head: %+v", got)
	}
	other := panel
	other.Declaration.Expected = []string{"cursor"}
	if got := classifyPanel(other, nil, nil, comments); len(got.Completed) != 0 {
		t.Fatalf("evidence completed the wrong reviewer: %+v", got)
	}
	for i := range comments {
		comments[i].IsBot = false
	}
	if got := classifyPanel(panel, nil, nil, comments); len(got.Completed) != 0 {
		t.Fatalf("bot login without bot metadata completed: %+v", got)
	}
	for i := range comments {
		comments[i].Author = "itsHabib"
		comments[i].IsBot = false
	}
	if got := classifyPanel(panel, nil, nil, comments); len(got.Completed) != 0 {
		t.Fatalf("human copies counted as provider evidence: %+v", got)
	}
}

func TestRecordedCommentTrust(t *testing.T) {
	panel, comments := recordedCompletionFixture(t)
	for name, mutate := range map[string]func(*Comment){
		"missing source ID": func(c *Comment) { c.ID = 0 },
		"wrong issuer":      func(c *Comment) { c.Author = "codex[bot]" },
		"inline comment":    func(c *Comment) { c.Path = "main.go" },
		"formal comment":    func(c *Comment) { c.CommitID = panel.Subject.HeadSHA },
	} {
		t.Run(name, func(t *testing.T) {
			copies := slices.Clone(comments)
			for i := range copies {
				mutate(&copies[i])
			}
			if got := classifyPanel(panel, nil, nil, copies); len(got.Completed) != 0 {
				t.Fatalf("untrusted comments completed: %+v", got)
			}
		})
	}
}
