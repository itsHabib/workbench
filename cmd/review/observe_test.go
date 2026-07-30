package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

func TestBuildPanelUsesOnlyExactHeadCompletion(t *testing.T) {
	plan := testPlan(t, "T2")
	reviews := []rawReview{
		review("cursor[bot]", "COMMENTED", testHeadB, 1),
		review("cursor[bot]", "COMMENTED", testHeadA, 2),
	}
	comments := []issueComment{
		comment("chatgpt-codex-connector[bot]",
			"Codex Review: Didn't find any major issues.\n\n**Reviewed commit:** `aaaaaaaaaa`", 3),
	}
	evidence := buildPanel(plan, reviews, comments, []string{"Copilot"})
	if err := reviewpanel.Validate(evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence.Completed) != 2 {
		t.Fatalf("completed = %#v", evidence.Completed)
	}
	if len(evidence.Pending) != 0 || len(evidence.Missing) != 0 {
		t.Fatalf("pending %v missing %v", evidence.Pending, evidence.Missing)
	}
}

func TestBuildPanelPartitionsPendingAndMissing(t *testing.T) {
	plan := testPlan(t, "T3")
	evidence := buildPanel(plan, nil, nil, []string{"chatgpt-codex-connector[bot]"})
	if err := reviewpanel.Validate(evidence); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(evidence.Pending, []string{"codex"}) {
		t.Fatalf("pending = %v", evidence.Pending)
	}
	if !slices.Equal(evidence.Missing, []string{"claude", "cursor"}) {
		t.Fatalf("missing = %v", evidence.Missing)
	}
}

func TestObserveRejectsHeadChangeWithoutPublishing(t *testing.T) {
	temp := t.TempDir()
	planPath := filepath.Join(temp, "plan.json")
	writeJSON(t, planPath, testPlan(t, "T1"))
	out := filepath.Join(temp, "panel.json")
	headCalls := 0
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			head := testHeadA
			if headCalls > 0 {
				head = testHeadB
			}
			headCalls++
			return []byte(`{"headRefOid":"` + head + `","state":"OPEN"}`), nil
		}
		if name != "gh" || !slices.Contains(args, "api") {
			return nil, errors.New("unexpected command")
		}
		endpoint := args[len(args)-1]
		if slices.Contains(args, "--slurp") {
			return []byte(`[[]]`), nil
		}
		if endpoint == "repos/itshabib/ship/pulls/7/requested_reviewers" {
			return []byte(`{"users":[]}`), nil
		}
		return nil, errors.New("unexpected endpoint")
	}}
	code := run(context.Background(), []string{
		"observe", "-plan", planPath, "-out", out,
	}, runner, io.Discard, io.Discard)
	if code != exitRefused {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("stale observation published: %v", err)
	}
}

func review(actor, state, head string, id int64) rawReview {
	value := rawReview{ID: id, State: state, CommitID: head}
	value.User.Login = actor
	value.User.Type = "Bot"
	return value
}

func comment(actor, body string, id int64) issueComment {
	value := issueComment{ID: id, Body: body}
	value.User.Login = actor
	value.User.Type = "Bot"
	return value
}

func TestObservePublishesValidPanel(t *testing.T) {
	temp := t.TempDir()
	planPath := filepath.Join(temp, "plan.json")
	writeJSON(t, planPath, testPlan(t, "T1"))
	out := filepath.Join(temp, "panel.json")
	runner := fakeRunner{run: func(_ []byte, name string, args ...string) ([]byte, error) {
		if name == "gh" && slices.Contains(args, "view") {
			return []byte(`{"headRefOid":"` + testHeadA + `","state":"OPEN"}`), nil
		}
		endpoint := args[len(args)-1]
		switch endpoint {
		case "repos/itshabib/ship/pulls/7/reviews":
			value := review("chatgpt-codex-connector[bot]", "COMMENTED", testHeadA, 9)
			data, _ := json.Marshal([][]rawReview{{value}})
			return data, nil
		case "repos/itshabib/ship/issues/7/comments":
			return []byte(`[[]]`), nil
		case "repos/itshabib/ship/pulls/7/requested_reviewers":
			return []byte(`{"users":[]}`), nil
		}
		return nil, errors.New("unexpected endpoint")
	}}
	code := run(context.Background(), []string{
		"observe", "-plan", planPath, "-out", out,
	}, runner, io.Discard, io.Discard)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var evidence reviewpanel.Evidence
	readJSON(t, out, &evidence)
	if err := reviewpanel.Validate(evidence); err != nil {
		t.Fatal(err)
	}
}
