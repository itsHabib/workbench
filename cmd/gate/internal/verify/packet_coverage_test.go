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
	comments := []map[string]any{{"is_bot": true, "body": strings.Repeat("x", RequiredEvidenceBudget+1)}}
	p, err := JudgmentPacket([]state.Artifact{packetArtifact(t, map[string]any{"comments": comments})}, Subject{})
	if err != nil || p.Complete || !p.EvidenceBudgetExceeded || !strings.Contains(strings.Join(p.Missing, " "), "shared evidence budget") {
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

func TestPacketRootReferenceWinsOverChangedBasename(t *testing.T) {
	diff := "diff --git a/a/spec.md b/a/spec.md\n--- a/a/spec.md\n+++ b/a/spec.md\n@@ -50 +50 @@\n-old\n+new\n"
	for _, reference := range []string{"./spec.md:50", "spec.md:50"} {
		t.Run(reference, func(t *testing.T) {
			comments := []map[string]any{{"is_bot": true, "body": "P1: inspect `" + reference + "`."}}
			arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments})}
			p, err := JudgmentPacket(arts, Subject{})
			if err != nil || p.Complete || !strings.Contains(strings.Join(p.Missing, " "), "file index unavailable") {
				t.Fatalf("changed basename claimed complete coverage without an index: %v %v", p.Missing, err)
			}
			arts = append(arts, packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"spec.md", "a/spec.md"}}))
			p, err = JudgmentPacket(arts, Subject{})
			if err != nil || p.Complete || strings.Join(p.RequiredSources, ",") != "spec.md" {
				t.Fatalf("nested diff substituted for root source: %v %v", p.RequiredSources, err)
			}
		})
	}
}

func TestPacketChecksAmbiguityAcrossDiffAndIndex(t *testing.T) {
	diff := "diff --git a/a/spec.md b/a/spec.md\n--- a/a/spec.md\n+++ b/a/spec.md\n@@ -50 +50 @@\n-old\n+new\n"
	comments := []map[string]any{{"is_bot": true, "body": "Inspect `spec.md:50`."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments}), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"a/spec.md", "b/spec.md"}})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete || len(p.RequiredSources) != 0 || !strings.Contains(strings.Join(p.SourceHints, " "), "ambiguous spec.md") {
		t.Fatalf("diff preference hid known ambiguity: %v %v %v", p.RequiredSources, p.SourceHints, err)
	}
	if !strings.Contains(p.Context, "[review-referenced source hint:") {
		t.Fatal("hint was mislabeled as required context unavailable")
	}
}

func TestPacketBareTokenNeverRequiresUnchangedIndexBlob(t *testing.T) {
	diff := "diff --git a/web/package-lock.json b/web/package-lock.json\n--- a/web/package-lock.json\n+++ b/web/package-lock.json\n@@ -1 +1 @@\n-old\n+new\n"
	comments := []map[string]any{{"is_bot": true, "body": "The `package-lock.json` churn looks unrelated."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete || len(p.RequiredSources) != 0 {
		t.Fatalf("bare token matching a changed file lost its diff coverage: %v %v", p.Missing, err)
	}
	// Recording the index must not re-resolve the mention to an unchanged
	// (possibly oversized) root blob the collector cannot supply.
	arts = append(arts, packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"package-lock.json", "web/package-lock.json"}}))
	p, err = JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete || len(p.RequiredSources) != 0 {
		t.Fatalf("bare token promoted an unchanged index blob: %v %v %v", p.Missing, p.RequiredSources, err)
	}
	if !strings.Contains(p.Context, "+++ b/web/package-lock.json") {
		t.Fatal("changed file named by the bare token is not represented")
	}
}

func TestPacketBareTokenAmbiguousAcrossChangedFiles(t *testing.T) {
	diff := "diff --git a/a/Makefile b/a/Makefile\n--- a/a/Makefile\n+++ b/a/Makefile\n@@ -1 +1 @@\n-old\n+new\n" +
		"diff --git a/b/Makefile b/b/Makefile\n--- a/b/Makefile\n+++ b/b/Makefile\n@@ -1 +1 @@\n-old\n+new\n"
	comments := []map[string]any{{"is_bot": true, "body": "Update `Makefile` too."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments}), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"Makefile", "a/Makefile", "b/Makefile"}})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete || len(p.RequiredSources) != 0 {
		t.Fatalf("ambiguous bare token invented a requirement: %v %v %v", p.Missing, p.RequiredSources, err)
	}
	if !strings.Contains(strings.Join(p.SourceHints, " "), "ambiguous Makefile; candidates [a/Makefile b/Makefile Makefile]") {
		t.Fatalf("ambiguous bare token hint missing: %v", p.SourceHints)
	}
}

func TestPacketPreciseBasenameWithSeveralChangedMatchesNeedsIndex(t *testing.T) {
	diff := "diff --git a/a/spec.md b/a/spec.md\n--- a/a/spec.md\n+++ b/a/spec.md\n@@ -50 +50 @@\n-old\n+new\n" +
		"diff --git a/b/spec.md b/b/spec.md\n--- a/b/spec.md\n+++ b/b/spec.md\n@@ -50 +50 @@\n-old\n+new\n"
	comments := []map[string]any{{"is_bot": true, "body": "P1: `spec.md:50` accepts arbitrary users."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || p.Complete || !strings.Contains(strings.Join(p.Missing, " "), "file index unavailable") {
		t.Fatalf("several changed basenames claimed coverage without the index: %v %v", p.Missing, err)
	}
	withRoot := append(append([]state.Artifact(nil), arts...), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"spec.md", "a/spec.md", "b/spec.md"}}))
	p, err = JudgmentPacket(withRoot, Subject{})
	if err != nil || p.Complete || strings.Join(p.RequiredSources, ",") != "spec.md" {
		t.Fatalf("exact root file not required once indexed: %v %v", p.RequiredSources, err)
	}
	nested := append(append([]state.Artifact(nil), arts...), packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"a/spec.md", "b/spec.md"}}))
	p, err = JudgmentPacket(nested, Subject{})
	if err != nil || !p.Complete || len(p.RequiredSources) != 0 || !strings.Contains(strings.Join(p.SourceHints, " "), "ambiguous spec.md") {
		t.Fatalf("indexed ambiguity not reported as a hint: %v %v %v", p.Missing, p.SourceHints, err)
	}
}

// A lone changed match is not the only file with that name once the index is
// known. An oversized sibling lockfile must not become unsatisfiable source
// because a review mentioned the shared name.
func TestPacketBareTokenWithSameNamedSiblingIsHint(t *testing.T) {
	diff := "diff --git a/web/package-lock.json b/web/package-lock.json\n--- a/web/package-lock.json\n+++ b/web/package-lock.json\n@@ -0,0 +1,20000 @@\n" + strings.Repeat("+  \"resolved\": \"https://registry.example/pkg.tgz\"\n", 20000)
	comments := []map[string]any{{"is_bot": true, "body": "The `package-lock.json` churn looks unrelated."}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": comments})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || p.Complete || strings.Join(p.RequiredSources, ",") != "web/package-lock.json" {
		t.Fatalf("unique changed name should require its diff before the index is known: %v %v %v", p.Missing, p.RequiredSources, err)
	}
	arts = append(arts, packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"api/package-lock.json", "web/package-lock.json"}}))
	p, err = JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete || len(p.RequiredSources) != 0 {
		t.Fatalf("same-named sibling left a bare mention unsatisfiable: %v %v %v", p.Missing, p.RequiredSources, err)
	}
	if !strings.Contains(strings.Join(p.SourceHints, " "), "ambiguous package-lock.json; candidates [web/package-lock.json api/package-lock.json]") {
		t.Fatalf("sibling ambiguity not reported: %v", p.SourceHints)
	}
}

// A bare name that only one repository path carries stays a requirement once
// the index is known; only a same-named sibling demotes it to a hint.
func TestPacketBareTokenUniqueChangedNameStaysRequired(t *testing.T) {
	diff := "diff --git a/web/package-lock.json b/web/package-lock.json\n--- a/web/package-lock.json\n+++ b/web/package-lock.json\n@@ -1 +1 @@\n-old\n+new\n"
	comments := []map[string]any{{"is_bot": true, "body": "The `package-lock.json` churn looks unrelated."}}
	arts := []state.Artifact{
		packetArtifact(t, map[string]any{"diff": diff, "comments": comments}),
		packetArtifact(t, SourceEvidence{IndexComplete: true, FileIndex: []string{"web/package-lock.json", "go.mod"}}),
	}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete || len(p.SourceHints) != 0 {
		t.Fatalf("unique changed name lost its requirement: %v %v %v", p.Missing, p.SourceHints, err)
	}
	if !strings.Contains(p.Context, "## Required recorded diff (complete file section)\n```\ndiff --git a/web/package-lock.json") {
		t.Fatal("unique changed name is not rendered as a required complete diff section")
	}
}
