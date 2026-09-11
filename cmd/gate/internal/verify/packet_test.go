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
	for _, path := range []string{"a/README.md", "b/README.md", "c/README.md"} {
		diff += fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1 +1 @@\n-old\n+complete %s\n", path, path, path, path, path)
	}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": []map[string]any{{"is_bot": true, "body": "Check `README.md`"}}})}
	p, err := JudgmentPacket(arts, Subject{})
	if err != nil || !p.Complete {
		t.Fatalf("%+v %v", p, err)
	}
	for _, path := range []string{"a/README.md", "b/README.md", "c/README.md"} {
		if !strings.Contains(p.Context, "complete "+path) {
			t.Fatalf("missing %s", path)
		}
	}
}

func TestPacketBudgetAndExactHeadRepair(t *testing.T) {
	subject := Subject{Repo: "o/r", Number: 1, HeadSHA: "head"}
	diff := "diff --git a/large.md b/large.md\n--- a/large.md\n+++ b/large.md\n@@ -1 +1 @@\n-" + strings.Repeat("x", SourceBudget) + "\n+small replacement\n"
	arts := []state.Artifact{packetArtifact(t, map[string]any{"diff": diff, "comments": []map[string]any{{"is_bot": true, "body": "Check `large.md` and `other.md`"}}})}
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
