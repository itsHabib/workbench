package evidence

import (
	"strings"
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
		got := classifyPanel(panel, reviews, nil, nil)
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
	got := classifyPanel(panel, nil, []string{"chatgpt-codex-connector[bot]"}, nil)
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
	got := classifyPanel(panel, []rawComment{stale}, nil, nil)
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
	got := classifyPanel(panel, []rawComment{human}, nil, nil)
	if len(got.Completed) != 0 || len(got.Missing) != 1 {
		t.Fatalf("human actor satisfied bot panel: %+v", got)
	}
	unknown := rawReview("chatgpt-codex-connector[bot]", "Bot", "", "head", "PENDING")
	unknown.ID = 2
	got = classifyPanel(panel, []rawComment{unknown}, nil, nil)
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
	got := classifyPanel(panel, []rawComment{current}, nil, nil)
	if len(got.Completed) != 1 || got.Completed[0].Name != "coderabbit" || len(got.Missing) != 0 {
		t.Fatalf("current-head CodeRabbit review not completed: %+v", got)
	}

	stale := rawReview("coderabbitai[bot]", "Bot", "", "old", "COMMENTED")
	stale.ID = 2
	got = classifyPanel(panel, []rawComment{stale}, nil, nil)
	if len(got.Completed) != 0 || len(got.Missing) != 1 || got.Missing[0] != "coderabbit" {
		t.Fatalf("stale CodeRabbit review did not remain missing: %+v", got)
	}
}

func TestClassifyPanelCodexCleanIssueComment(t *testing.T) {
	const head = "e96af9fbfc123456789012345678901234567890"
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
	}
	comment := Comment{
		ID: 1, Author: "chatgpt-codex-connector[bot]", IsBot: true,
		Body: "Codex Review: Didn't find any major issues. Swish!\n\n**Reviewed commit:** `e96af9fbfc`\n",
	}
	got := classifyPanel(panel, nil, nil, []Comment{comment})
	if len(got.Completed) != 1 || got.Completed[0].State != "CLEAN" || len(got.Missing) != 0 {
		t.Fatalf("clean exact-head Codex comment not completed: %+v", got)
	}
}

func TestClassifyPanelCodexCleanCommentRefusals(t *testing.T) {
	const head = "e96af9fbfc123456789012345678901234567890"
	base := Comment{
		ID: 1, Author: "chatgpt-codex-connector[bot]", IsBot: true,
		Body: "Codex Review: Didn't find any major issues. Swish!\n\n**Reviewed commit:** `e96af9fbfc`\n",
	}
	tests := map[string]func(*Comment){
		"stale":       func(c *Comment) { c.Body = strings.Replace(c.Body, "e96af9fbfc", "aaaaaaaaaa", 1) },
		"malformed":   func(c *Comment) { c.Body = strings.Replace(c.Body, "`e96af9fbfc`", "e96af9fbfc", 1) },
		"wrong actor": func(c *Comment) { c.Author = "some-bot[bot]" },
		"not clean":   func(c *Comment) { c.Body = strings.Replace(c.Body, "Didn't find any major issues.", "Found a P1.", 1) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			comment := base
			mutate(&comment)
			panel := reviewpanel.Evidence{
				SchemaVersion: 1,
				Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
				Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
			}
			got := classifyPanel(panel, nil, nil, []Comment{comment})
			if len(got.Completed) != 0 || len(got.Missing) != 1 {
				t.Fatalf("invalid Codex comment satisfied panel: %+v", got)
			}
		})
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
