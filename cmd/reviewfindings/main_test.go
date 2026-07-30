package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/itsHabib/workbench/contracts/reviewfindings"
)

type fakeRunner struct {
	pr       pullRequest
	comments []comment
	err      error
}

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(args) > 0 && args[0] == "pr" {
		return json.Marshal(f.pr)
	}
	return json.Marshal([][]comment{f.comments})
}

func TestProduceExactHeadArtifact(t *testing.T) {
	head := strings.Repeat("a", 40)
	item := reviewComment(head)
	opts := validOptions(t, head)
	opts.catalogRevision = strings.Repeat("c", 40)
	artifact, err := produce(context.Background(), fakeRunner{
		pr: pullRequest{HeadRefOID: head, State: "OPEN"}, comments: []comment{item},
	}, opts, time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Subject.HeadSHA != head || artifact.Producer.Harness != "codex" {
		t.Fatalf("unexpected artifact identity: %+v", artifact)
	}
	if artifact.Producer.CatalogRevision != opts.catalogRevision {
		t.Fatalf("catalog revision = %q, want %q", artifact.Producer.CatalogRevision, opts.catalogRevision)
	}
	if !strings.HasPrefix(artifact.ArtifactID, "rf_") {
		t.Fatalf("artifact id = %q", artifact.ArtifactID)
	}
	if err := reviewfindings.Validate(artifact); err != nil {
		t.Fatalf("artifact rejected by shared contract: %v", err)
	}
}

func TestProduceRefusesMalformedCatalogRevision(t *testing.T) {
	head := strings.Repeat("a", 40)
	opts := validOptions(t, head)
	opts.catalogRevision = "dirty"
	_, err := produce(context.Background(), fakeRunner{
		pr:       pullRequest{HeadRefOID: head, State: "OPEN"},
		comments: []comment{reviewComment(head)},
	}, opts, time.Now())
	if err == nil || !strings.Contains(err.Error(), "catalog_revision") {
		t.Fatalf("produce() error = %v, want catalog revision refusal", err)
	}
}

func TestProduceRefusesStaleHeadBeforeCommentsRead(t *testing.T) {
	requested := strings.Repeat("a", 40)
	live := strings.Repeat("b", 40)
	_, err := produce(context.Background(), fakeRunner{
		pr: pullRequest{HeadRefOID: live, State: "OPEN"},
	}, validOptions(t, requested), time.Now())
	if err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("produce() error = %v, want stale refusal", err)
	}
}

func TestProduceRefusesEmptyOrUnsourcedExactHead(t *testing.T) {
	head := strings.Repeat("a", 40)
	_, err := produce(context.Background(), fakeRunner{
		pr:       pullRequest{HeadRefOID: head, State: "OPEN"},
		comments: []comment{reviewComment(strings.Repeat("b", 40))},
	}, validOptions(t, head), time.Now())
	if err == nil || !strings.Contains(err.Error(), "no sourced") {
		t.Fatalf("produce() error = %v, want unsourced refusal", err)
	}
}

func TestProduceUsesOriginalReviewedHead(t *testing.T) {
	head := strings.Repeat("a", 40)
	item := reviewComment(strings.Repeat("b", 40))
	_, err := produce(context.Background(), fakeRunner{
		pr: pullRequest{HeadRefOID: head, State: "OPEN"}, comments: []comment{item},
	}, validOptions(t, head), time.Now())
	if err == nil || !strings.Contains(err.Error(), "no sourced") {
		t.Fatalf("produce() error = %v, want stale original-head refusal", err)
	}
}

func TestProduceCanonicalizesCompletedReviewer(t *testing.T) {
	head := strings.Repeat("a", 40)
	opts := validOptions(t, head)
	opts.completed = listFlag{"Codex"}
	artifact, err := produce(context.Background(), fakeRunner{
		pr: pullRequest{HeadRefOID: head, State: "OPEN"}, comments: []comment{reviewComment(head)},
	}, opts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Panel.Completed[0] != "codex" {
		t.Fatalf("completed = %v", artifact.Panel.Completed)
	}
}

func TestRunDoesNotReplaceOutputOnRefusal(t *testing.T) {
	head := strings.Repeat("a", 40)
	opts := validOptions(t, head)
	if err := os.WriteFile(opts.out, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"github", "-repo", opts.repo, "-pr", "1", "-head", head,
		"-requested", "codex", "-completed", "codex", "-out", opts.out,
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, fakeRunner{
		pr: pullRequest{HeadRefOID: strings.Repeat("b", 40), State: "OPEN"},
	}, &stdout, &stderr)
	if code != exitRefused {
		t.Fatalf("run() code = %d, stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(opts.out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("output replaced on refusal: %q", data)
	}
}

func TestProduceReturnsErrorOnRunnerFailure(t *testing.T) {
	head := strings.Repeat("a", 40)
	_, err := produce(context.Background(), fakeRunner{err: errors.New("boom")}, validOptions(t, head), time.Now())
	if err == nil {
		t.Fatal("produce() error = nil")
	}
}

func TestRunClassifiesRunnerFailureAsOperational(t *testing.T) {
	head := strings.Repeat("a", 40)
	opts := validOptions(t, head)
	args := []string{
		"github", "-repo", opts.repo, "-pr", "1", "-head", head,
		"-requested", "codex", "-completed", "codex", "-out", opts.out,
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, fakeRunner{err: errors.New("network unavailable")}, &stdout, &stderr)
	if code != exitError {
		t.Fatalf("run() code = %d, want %d", code, exitError)
	}
}

func TestTruncateUTF8KeepsCompleteRunes(t *testing.T) {
	value := strings.Repeat("a", 511) + "é"
	got := truncateUTF8(value, 512)
	if !utf8.ValidString(got) || len([]byte(got)) > 512 {
		t.Fatalf("truncateUTF8() = %q (%d bytes)", got, len([]byte(got)))
	}
}

func validOptions(t *testing.T, head string) options {
	t.Helper()
	return options{
		repo: "itsHabib/ship", pr: 1, head: head,
		requested: listFlag{"codex"}, completed: listFlag{"codex"},
		out: filepath.Join(t.TempDir(), "findings.json"), producer: "codex:test",
	}
}

func reviewComment(head string) comment {
	item := comment{
		ID: 1, Body: "[P1] Refuse the stale head", HTMLURL: "https://github.com/itsHabib/ship/pull/1#discussion_r1",
		Path: "packages/driver/src/engine.ts", Line: 10, OriginalCommitID: head,
	}
	item.User.Login = "chatgpt-codex-connector[bot]"
	return item
}
