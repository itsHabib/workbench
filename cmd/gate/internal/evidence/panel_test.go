package evidence

import (
	"testing"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

func TestClassifyPanelPermutations(t *testing.T) {
	const head = "head"
	reviewers := []string{"codex", "cursor", "claude"}
	for mask := 0; mask < 1<<len(reviewers); mask++ {
		panel := reviewpanel.Evidence{
			SchemaVersion: 1,
			Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
			Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: reviewers},
		}
		var reviews []rawComment
		for i, reviewer := range reviewers {
			if mask&(1<<i) == 0 {
				continue
			}
			reviews = append(reviews, rawReview(reviewer+"[bot]", "Bot", "", head, "COMMENTED"))
			reviews[len(reviews)-1].ID = int64(i + 1)
		}
		got := classifyPanel(panel, reviews, nil)
		if len(got.Completed) != bits(mask) || len(got.Missing) != len(reviewers)-bits(mask) {
			t.Fatalf("mask %03b: completed=%v missing=%v", mask, got.Completed, got.Missing)
		}
		if err := reviewpanel.Validate(got); err != nil {
			t.Fatalf("mask %03b: invalid evidence: %v", mask, err)
		}
	}
}

func TestClassifyPanelPending(t *testing.T) {
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
	}
	got := classifyPanel(panel, nil, []string{"chatgpt-codex-connector[bot]"})
	if len(got.Pending) != 1 || got.Pending[0] != "codex" {
		t.Fatalf("pending = %v", got.Pending)
	}
}

func TestClassifyPanelHeadAdvance(t *testing.T) {
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "new"},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
	}
	stale := rawReview("chatgpt-codex-connector[bot]", "Bot", "", "old", "APPROVED")
	stale.ID = 1
	got := classifyPanel(panel, []rawComment{stale}, nil)
	if len(got.Completed) != 0 || len(got.Missing) != 1 {
		t.Fatalf("stale review satisfied new head: %+v", got)
	}
}

func TestClassifyPanelRejectsHumanAndUnknownState(t *testing.T) {
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
	}
	human := rawReview("chatgpt-codex-connector[bot]", "User", "", "head", "APPROVED")
	human.ID = 1
	got := classifyPanel(panel, []rawComment{human}, nil)
	if len(got.Completed) != 0 || len(got.Missing) != 1 {
		t.Fatalf("human actor satisfied bot panel: %+v", got)
	}
	unknown := rawReview("chatgpt-codex-connector[bot]", "Bot", "", "head", "PENDING")
	unknown.ID = 2
	got = classifyPanel(panel, []rawComment{unknown}, nil)
	if len(got.Completed) != 0 || len(got.Missing) != 1 {
		t.Fatalf("unknown review state satisfied panel: %+v", got)
	}
}

func TestClassifyPanelCodeRabbitActor(t *testing.T) {
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"coderabbit"}},
	}
	current := rawReview("coderabbitai[bot]", "Bot", "", "head", "COMMENTED")
	current.ID = 1
	got := classifyPanel(panel, []rawComment{current}, nil)
	if len(got.Completed) != 1 || got.Completed[0].Name != "coderabbit" || len(got.Missing) != 0 {
		t.Fatalf("current-head CodeRabbit review not completed: %+v", got)
	}

	stale := rawReview("coderabbitai[bot]", "Bot", "", "old", "COMMENTED")
	stale.ID = 2
	got = classifyPanel(panel, []rawComment{stale}, nil)
	if len(got.Completed) != 0 || len(got.Missing) != 1 || got.Missing[0] != "coderabbit" {
		t.Fatalf("stale CodeRabbit review did not remain missing: %+v", got)
	}
}

func bits(value int) int {
	count := 0
	for value > 0 {
		count += value & 1
		value >>= 1
	}
	return count
}
