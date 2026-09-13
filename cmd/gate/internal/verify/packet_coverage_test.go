package verify

import (
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func TestPacketCountsEachRenderedReviewOnce(t *testing.T) {
	old := "UNRESOLVED OLD REVIEW " + strings.Repeat("a", 40000)
	current := "CURRENT REVIEW " + strings.Repeat("b", 40000)
	comments := []map[string]any{
		{"is_bot": true, "body": old, "created_at": "2026-09-12T01:00:00Z"},
		{"is_bot": true, "body": current, "created_at": "2026-09-13T01:00:00Z"},
	}
	p, err := JudgmentPacket([]state.Artifact{packetArtifact(t, map[string]any{"comments": comments})}, Subject{})
	if err != nil || !p.Complete {
		t.Fatalf("already rendered review became missing: %v %v", p.Missing, err)
	}
	if strings.Count(p.Context, old) != 1 || strings.Count(p.Context, current) != 1 {
		t.Fatal("required review was duplicated or omitted")
	}
}

func TestPacketDoesNotHideUnrepresentedReview(t *testing.T) {
	comments := []map[string]any{{"is_bot": true, "body": strings.Repeat("x", reviewContextCap+1)}}
	p, err := JudgmentPacket([]state.Artifact{packetArtifact(t, map[string]any{"comments": comments})}, Subject{})
	if err != nil || p.Complete || !strings.Contains(strings.Join(p.Missing, " "), "required review budget") {
		t.Fatalf("unrepresented review was counted complete: %v %v", p.Missing, err)
	}
}

func TestPacketKeepsUnknownPreciseFindingDespiteNewCleanReview(t *testing.T) {
	subject := Subject{HeadSHA: "current"}
	comments := []map[string]any{
		{"is_bot": true, "body": "P1: `auth/policy.go:42` still accepts arbitrary users. Previously reviewed at oldsha.", "created_at": "2026-09-12T01:00:00Z"},
		{"is_bot": true, "body": "No new issues.", "commit_id": "current", "created_at": "2026-09-13T01:00:00Z"},
	}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"comments": comments}), packetArtifact(t, SourceEvidence{Subject: subject, IndexComplete: true, FileIndex: []string{"auth/policy.go"}})}
	p, err := JudgmentPacket(arts, subject)
	if err != nil || p.Complete || len(p.RequiredSources) != 1 || p.RequiredSources[0] != "auth/policy.go" {
		t.Fatalf("unknown precise finding lost its source requirement: %v %v", p.Missing, err)
	}
	if !strings.Contains(p.Context, "still accepts arbitrary users") {
		t.Fatal("clean summary hid an unresolved finding")
	}
}

func TestPacketDoesNotPromoteBareCommandOrAmbiguousIndexHint(t *testing.T) {
	comments := []map[string]any{{"is_bot": true, "body": "Run `scrape-audit`; see `spec.md:296-303`."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"comments": comments}), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"scrape-audit", "a/spec.md", "b/spec.md", "cmd/scrape-audit/main.go"}})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete || len(p.RequiredSources) != 0 {
		t.Fatalf("unanchored hints invented source requirements: %v %v", p.Missing, err)
	}
	for _, want := range []string{"scrape-audit", "not represented as required source", "a/spec.md", "b/spec.md", "no source selected or finding resolved"} {
		if !strings.Contains(strings.Join(p.SourceHints, " "), want) {
			t.Fatalf("missing truthful hint diagnostic %q: %v", want, p.SourceHints)
		}
	}
}

func TestPacketExplicitBinaryAnchorStillRequiresSource(t *testing.T) {
	comments := []map[string]any{{"is_bot": true, "path": "scrape-audit", "line": 1, "body": "The required executable changed."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"comments": comments}), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"scrape-audit"}})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || p.Complete || len(p.RequiredSources) != 1 || p.RequiredSources[0] != "scrape-audit" {
		t.Fatalf("explicit binary requirement disappeared: %v %v", p.Missing, err)
	}
}

func TestPacketExactPathDoesNotSelectSameBasenameElsewhere(t *testing.T) {
	comments := []map[string]any{{"is_bot": true, "body": "Check `spec.md:1`."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"comments": comments}), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"spec.md", "a/spec.md", "b/spec.md"}})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || p.Complete || strings.Join(p.RequiredSources, ",") != "spec.md" {
		t.Fatalf("exact path expanded to unrelated basenames: %v %v", p.RequiredSources, err)
	}
}
