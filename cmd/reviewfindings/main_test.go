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

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/contracts/reviewfindings"
	"github.com/itsHabib/workbench/driverstate"
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

func TestAddressAcceptCLIConsumesOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(driverstate.StateDirEnv, dir)
	head := strings.Repeat("a", 40)
	runID := "dsr_impl"
	stream := "dss_impl"
	lease, err := driverstate.Claim(dir, runID, "session:test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	appendAddressFixtureEvent(t, dir, lease, driverstate.Event{
		ID: "evt_import", V: dsc.Version, Kind: dsc.KindRunImported, Time: now, Actor: "session:test",
		Body: mustJSON(t, dsc.RunImportedBody{
			Repo: "itsHabib/workbench", Source: "docs/task.md", GeneratedAt: "g",
			Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: stream, DocPath: "docs/task.md"}},
			Parent: "dsr_parent", ParentStream: "dss_parent",
		}),
	})
	appendAddressFixtureEvent(t, dir, lease, driverstate.Event{
		ID: "evt_dispatch", V: dsc.Version, Kind: dsc.KindStreamDispatched, Stream: stream,
		Time: now.Add(time.Second), Actor: "session:test",
	})
	appendAddressFixtureEvent(t, dir, lease, driverstate.Event{
		ID: "evt_attempt", V: dsc.Version, Kind: dsc.KindStreamAttempt, Stream: stream,
		Time: now.Add(2 * time.Second), Actor: "session:test",
		Body: mustJSON(t, dsc.StreamAttemptBody{Seq: 1, DocPath: "docs/task.md", Terminal: true}),
	})
	appendAddressFixtureEvent(t, dir, lease, driverstate.Event{
		ID: "evt_pr", V: dsc.Version, Kind: dsc.KindStreamPROpened, Stream: stream,
		Time: now.Add(3 * time.Second), Actor: "session:test",
		Body: mustJSON(t, dsc.StreamPROpenedBody{PR: 1, URL: "https://github.com/itsHabib/workbench/pull/1", HeadSHA: head}),
	})
	appendAddressFixtureEvent(t, dir, lease, driverstate.Event{
		ID: "evt_cycle", V: dsc.Version, Kind: dsc.KindReviewCycle, Stream: stream,
		Time: now.Add(4 * time.Second), Actor: "session:test",
		Body: mustJSON(t, dsc.ReviewCycleBody{Cycle: 1, PanelSettled: true, Findings: 1}),
	})
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	artifact := reviewfindings.Artifact{
		SchemaVersion: 1, ArtifactID: "rf_cli", Decision: reviewfindings.DecisionAddress,
		Subject:  reviewfindings.Subject{Type: reviewfindings.SubjectPullRequest, Repo: "itsHabib/workbench", Number: 1, HeadSHA: head},
		Producer: reviewfindings.Producer{ID: "codex:test", Harness: "codex", GeneratedAt: now},
		Panel:    reviewfindings.Panel{Requested: []string{"codex"}, Completed: []string{"codex"}, Missing: []string{}},
		Findings: []reviewfindings.Finding{{
			ID: "f1", Severity: "P1", Summary: "fix", Evidence: "evidence",
			Sources: []reviewfindings.Source{{Reviewer: "codex", CommentID: "1", URL: "https://github.com/itsHabib/workbench/pull/1#discussion_r1"}},
		}},
	}
	artifactPath := filepath.Join(t.TempDir(), "findings.json")
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"address", "accept", "-run", runID, "-stream", stream, "-artifact", artifactPath, "-max-cycles", "3"}
	runner := fakeRunner{pr: pullRequest{HeadRefOID: head, State: "OPEN"}}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, runner, &stdout, &stderr); code != exitOK {
		t.Fatalf("accept code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), args, runner, &stdout, &stderr); code != exitRefused || !strings.Contains(stderr.String(), "[duplicate]") {
		t.Fatalf("duplicate code=%d stderr=%s", code, stderr.String())
	}
}

func TestValidateCompletedHeadDistinguishesClosedPR(t *testing.T) {
	head := strings.Repeat("a", 40)
	err := validateCompletedHead("CLOSED", head, head)
	assertAddressRefusal(t, err, "pr-not-open")
	if !strings.Contains(err.Error(), "pull request is CLOSED") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateResumeHeadRejectsStartedDivergence(t *testing.T) {
	resume := driverstate.AddressResume{
		State: dsc.ReviewAddressRecord{
			Status: "started", WorkRef: "address/work.json",
		},
	}
	err := validateResumeHead(resume, "OPEN", "diverged")
	assertAddressRefusal(t, err, "stale-head")
}

func assertAddressRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var refusal driverstate.AddressRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v, want AddressRefusal", err)
	}
	if refusal.Code != code {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, code)
	}
}

func appendAddressFixtureEvent(t *testing.T, dir string, lease driverstate.Lease, event driverstate.Event) {
	t.Helper()
	if _, err := driverstate.Append(dir, lease, event); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
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
