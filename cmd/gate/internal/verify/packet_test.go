package verify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func packetArtifact(t *testing.T, body any) state.Artifact {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return state.Artifact{Kind: state.KindEvidence, ID: "ev", Body: raw}
}

func TestPacketIncludesEveryAmbiguousREADMEAfterLargeDiff(t *testing.T) {
	diff := "diff --git a/huge b/huge\n--- a/huge\n+++ b/huge\n@@ -0,0 +1,9000 @@\n" + strings.Repeat("+irrelevant text\n", 9000)
	for _, path := range []string{"README.md", "a/README.md", "b/README.md", "c/README.md"} {
		diff += fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1 +1 @@\n-old\n+complete %s\n", path, path, path, path, path)
	}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": []map[string]any{{"is_bot": true, "body": "Check `README.md`"}}})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete {
		t.Fatalf("%+v %v", p, err)
	}
	for _, path := range []string{"README.md", "a/README.md", "b/README.md", "c/README.md"} {
		if !strings.Contains(p.Context, "complete "+path) {
			t.Fatalf("missing %s", path)
		}
	}
}

func TestPacketBudgetAndExactHeadRepair(t *testing.T) {
	subject := Subject{Repo: "o/r", Number: 1, HeadSHA: "head"}
	diff := "diff --git a/large.md b/large.md\n--- a/large.md\n+++ b/large.md\n@@ -1 +1 @@\n-" + strings.Repeat("x", SourceBudget) + "\n+small replacement\n"
	arts := []state.Artifact{packetArtifact(t, SourceEvidence{Subject: subject, IndexComplete: true, FileIndex: []string{"large.md", "other.md"}}), packetArtifact(t, map[string]any{"diff": diff, "comments": []map[string]any{{"is_bot": true, "body": "Check `large.md` and `other.md`"}}})}
	p, err := JudgmentPacket(arts, subject)
	if err != nil || p.Complete || len(p.Missing) != 2 {
		t.Fatalf("missing all: %+v %v", p.Missing, err)
	}
	source := SourceEvidence{Subject: subject, Sources: []SourceFile{{Path: "large.md", Content: "small replacement"}, {Path: "other.md", Content: "companion"}}}
	arts = append(arts, packetArtifact(t, source))
	p, err = JudgmentPacket(arts, subject)
	if err != nil || !p.Complete {
		t.Fatalf("repaired: %+v %v", p.Missing, err)
	}
	subject.HeadSHA = "changed"
	if _, err := JudgmentPacket(arts, subject); err == nil {
		t.Fatal("cross-head repair accepted")
	}
}

func TestPacketPreservesRequiredReviewWhenAuthorCommentsFillOldBudget(t *testing.T) {
	review := "Check `guide.md`"
	comments := []map[string]any{{"is_bot": true, "body": review}, {"author": "author", "body": strings.Repeat("author chatter ", 6000)}}
	diff := "diff --git a/guide.md b/guide.md\n--- a/guide.md\n+++ b/guide.md\n@@ -1 +1 @@\n-old\n+fixed\n"
	p, err := JudgmentPacket([]state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments})}, Subject{})
	if err != nil || !p.Complete || !strings.Contains(p.Context, "Required source review") {
		t.Fatalf("%+v %v", p.Missing, err)
	}
}

func TestPacketRequiresStructuredAnchorOutsideDiff(t *testing.T) {
	diff := "diff --git a/guide.md b/guide.md\n--- a/guide.md\n+++ b/guide.md\n@@ -1 +1 @@\n-old\n+fixed\n"
	comments := []map[string]any{{"is_bot": true, "path": "guide.md", "line": 500, "body": "This code needs review."}}
	p, err := JudgmentPacket([]state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments})}, Subject{})
	if err != nil || p.Complete || !strings.Contains(strings.Join(p.Missing, " "), "guide.md:500") {
		t.Fatalf("%+v %v", p.Missing, err)
	}
}

func TestPacketDoesNotMistakeGoSymbolsForMissingFiles(t *testing.T) {
	comments := []map[string]any{{"is_bot": true, "body": "Check `url.PathEscape`, `strings.Join` and `companion.md`."}}
	p, err := JudgmentPacket([]state.Artifact{packetArtifact(t, map[string]any{"comments": comments}), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"companion.md"}})}, Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Missing) != 1 || !strings.Contains(p.Missing[0], "companion.md:") {
		t.Fatalf("symbols became required source files: %v", p.Missing)
	}
}

func TestPacketGroundsProseInRecordedIndex(t *testing.T) {
	subject := Subject{Repo: "o/r", HeadSHA: "current"}
	index := SourceEvidence{Subject: subject, IndexComplete: true, FileIndex: []string{"Dockerfile", "docs/real.md", "docs/README.md", "other/docs/README.md"}}
	diff := "diff --git a/docs/README.md b/docs/README.md\n--- a/docs/README.md\n+++ b/docs/README.md\n@@ -1 +1 @@\n-old\n+fixed\n"
	comments := []map[string]any{{"is_bot": true, "body": "Check `Dockerfile`, `docs/real.md:200`, and `docs/README.md`; examples include `.md`, `pkg/foo.go`, `url.PathEscape` and `*/contents/*`."}}
	arts := []state.Artifact{packetArtifact(t, index), packetArtifact(t, map[string]any{"diff": diff, "comments": comments})}
	p, err := JudgmentPacket(arts, subject)
	if err != nil || strings.Join(p.RequiredSources, ",") != "Dockerfile,docs/real.md" {
		t.Fatalf("%v %v", p.RequiredSources, err)
	}
	// Full current source is evidence even when an old cited line no longer exists.
	arts = append(arts, packetArtifact(t, SourceEvidence{Subject: subject, Sources: []SourceFile{{Path: "Dockerfile", Content: "FROM scratch\n"}, {Path: "docs/real.md", Content: "short replacement\n"}}}))
	p, err = JudgmentPacket(arts, subject)
	if err != nil || !p.Complete {
		t.Fatalf("%v %v", p.Missing, err)
	}
}

func TestPacketRecordsAbsenceAndIgnoresStaleRequirements(t *testing.T) {
	subject := Subject{HeadSHA: "current"}
	comments := []map[string]any{{"is_bot": true, "commit_id": "old", "path": "stale.md", "line": 1}, {"is_bot": true, "body": "review-coordinator-verdict `other.md`"}, {"is_bot": true, "commit_id": "current", "path": "deleted.md", "line": 100}}
	arts := []state.Artifact{packetArtifact(t, SourceEvidence{Subject: subject, IndexComplete: true, FileIndex: []string{"stale.md", "other.md"}}), packetArtifact(t, map[string]any{"comments": comments})}
	p, err := JudgmentPacket(arts, subject)
	if err != nil || !p.Complete || !strings.Contains(p.Context, "deleted.md has no file blob") {
		t.Fatalf("%v %v", p.Missing, err)
	}
}
