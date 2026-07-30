package evidence

import (
	"testing"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

func TestReviewAppearingBetweenReadsCannotCompleteSnapshot(t *testing.T) {
	pr := PRRef{Repo: "o/r", Number: 1}
	remote := []rawComment{}
	fetchers := reviewFetchers{
		reviews: func(PRRef) ([]rawComment, error) {
			return append([]rawComment(nil), remote...), nil
		},
		comments: func(_ PRRef, snapshot []rawComment) ([]Comment, error) {
			appeared := rawReview("chatgpt-codex-connector[bot]", "Bot", "P1: bug", "head", "COMMENTED")
			appeared.ID = 1
			remote = append(remote, appeared)
			return reviewBodies(snapshot), nil
		},
		panel: func(_ PRRef, head string, snapshot []rawComment, comments []Comment) reviewpanel.Evidence {
			panel := reviewpanel.Evidence{
				SchemaVersion: 1,
				Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
				Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
			}
			return classifyPanel(panel, snapshot, nil, comments)
		},
	}
	comments, panel, err := fetchReviewEvidence(pr, "head", fetchers)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Fatalf("findings snapshot unexpectedly contains later review: %+v", comments)
	}
	if len(panel.Completed) != 0 || len(panel.Missing) != 1 {
		t.Fatalf("later review credited against earlier findings snapshot: %+v", panel)
	}
}
