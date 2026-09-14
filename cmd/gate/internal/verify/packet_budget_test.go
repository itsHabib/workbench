package verify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func TestPacketSharesBudgetWithoutOmittingRequiredEvidence(t *testing.T) {
	// Each source remains below the collector's 256 KiB per-file bound. Their
	// combined 570 KiB and 180 KiB of active reviews exceed the old sub-budgets.
	subject := Subject{Repo: "o/r", Number: 1, HeadSHA: "head"}
	source := SourceEvidence{Subject: subject, IndexComplete: true}
	for i, length := range []int{240, 130, 80, 70, 50} {
		path := fmt.Sprintf("source%d.go", i)
		source.FileIndex = append(source.FileIndex, path)
		source.Sources = append(source.Sources, SourceFile{Path: path, Blob: fmt.Sprint(i), Content: strings.Repeat(fmt.Sprint(i), length*1024)})
	}
	var comments []map[string]any
	for i := 0; i < 30; i++ {
		comments = append(comments, map[string]any{
			"is_bot": true,
			"body":   fmt.Sprintf("Unresolved finding %d at `source%d.go:100`: ", i, i%5) + strings.Repeat("x", 6*1024),
		})
	}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"comments": comments}), packetArtifact(t, source)}
	p, err := JudgmentPacket(arts, subject)
	if err != nil || !p.Complete || p.EvidenceBudgetExceeded {
		t.Fatalf("stranded budget still blocks full evidence: %v %v", p.Missing, err)
	}
	for _, comment := range comments {
		if strings.Count(p.Context, comment["body"].(string)) != 1 {
			t.Fatal("a required review was omitted or duplicated")
		}
	}
	for _, file := range source.Sources {
		if !strings.Contains(p.Context, file.Content) {
			t.Fatalf("source was clipped: %s", file.Path)
		}
	}
}

func TestPacketCountsSourceHeadersAndScrubbing(t *testing.T) {
	content := strings.Repeat("x", RequiredEvidenceBudget)
	for _, tc := range []struct{ name, content string }{
		{"header", content},
		{"quoted-marker", strings.Repeat(artifactsEnd, RequiredEvidenceBudget/len(artifactsEnd))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := SourceEvidence{Sources: []SourceFile{{Path: "source.go", Content: tc.content}}}
			arts := []state.Artifact{packetArtifact(t, map[string]any{"comments": []map[string]string{{"body": "Recorded context"}}}), packetArtifact(t, source)}
			p, err := JudgmentPacket(arts, Subject{})
			if err != nil || p.Complete || !p.EvidenceBudgetExceeded {
				t.Fatalf("rendered overflow accepted: %v %v", p.Missing, err)
			}
			if strings.Contains(p.Context, "## Exact-head source source.go") {
				t.Fatal("over-budget source was partially rendered")
			}
		})
	}
}

func TestPacketRendersRepeatedSourceOnce(t *testing.T) {
	subject := Subject{Repo: "o/r", Number: 1, HeadSHA: "head"}
	comments := []map[string]any{{"is_bot": true, "body": "Check `p.md:1`."}}
	file := SourceFile{Path: "p.md", Blob: "blob-p", Content: strings.Repeat("p", 500*1024)}
	source := SourceEvidence{Subject: subject, Sources: []SourceFile{file}, IndexComplete: true, FileIndex: []string{"p.md"}}
	arts := []state.Artifact{packetArtifact(t, map[string]any{"comments": comments}), packetArtifact(t, source)}
	once, err := JudgmentPacket(arts, subject)
	if err != nil || !once.Complete {
		t.Fatalf("single copy: %v %v", once.Missing, err)
	}
	// A second copy of the same exact-head file would overflow if charged twice.
	twice, err := JudgmentPacket(append(arts, packetArtifact(t, source)), subject)
	if err != nil || !twice.Complete || twice.EvidenceBudgetExceeded {
		t.Fatalf("repeated source charged twice: %v %v", twice.Missing, err)
	}
	if n := strings.Count(twice.Context, "## Exact-head source p.md"); n != 1 {
		t.Fatalf("repeated source rendered %d times", n)
	}
}
