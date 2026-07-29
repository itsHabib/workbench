package evidence

import (
	"fmt"
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

func TestClassifyPanelClaudeCleanIssueComment(t *testing.T) {
	const head = "e96af9fbfc123456789012345678901234567890"
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"claude"}},
	}
	comment := Comment{
		ID: 1, Author: "claude[bot]", IsBot: true, CommitID: head,
		Body: claudeCleanBody("o/r", 42),
	}
	got := classifyPanel(panel, nil, nil, []Comment{comment})
	if len(got.Completed) != 1 || got.Completed[0].State != "CLEAN" ||
		got.Completed[0].HeadSHA != head || len(got.Missing) != 0 {
		t.Fatalf("clean exact-head Claude comment not completed: %+v", got)
	}
}

func TestClassifyPanelClaudeCleanCommentRefusals(t *testing.T) {
	const head = "e96af9fbfc123456789012345678901234567890"
	base := Comment{
		ID: 1, Author: "claude[bot]", IsBot: true, CommitID: head,
		Body: claudeCleanBody("o/r", 42),
	}
	tests := map[string]func(*Comment){
		"actionable": func(c *Comment) {
			c.Body = strings.Replace(c.Body, "\n\nLGTM.", "\n\n### Finding 1\nBug found.\n\nLGTM.", 1)
		},
		"wrong actor": func(c *Comment) { c.Author = "some-bot[bot]" },
		"stale":       func(c *Comment) { c.CommitID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"malformed":   func(c *Comment) { c.Body = strings.Replace(c.Body, "LGTM. Ready to merge.", "Looks fine.", 1) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			comment := base
			mutate(&comment)
			panel := reviewpanel.Evidence{
				SchemaVersion: 1,
				Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
				Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"claude"}},
			}
			got := classifyPanel(panel, nil, nil, []Comment{comment})
			if len(got.Completed) != 0 || len(got.Missing) != 1 {
				t.Fatalf("invalid Claude comment satisfied panel: %+v", got)
			}
		})
	}
}

func TestBindClaudeIssueHeads(t *testing.T) {
	const head = "e96af9fbfc123456789012345678901234567890"
	comment := rawReview("claude[bot]", "Bot", claudeCleanBody("o/r", 42), "", "")
	comments := []rawComment{comment}
	fetches := 0
	err := bindClaudeIssueHeads("o/r", comments, func(repo string, runID int64) (workflowRunResponse, error) {
		fetches++
		if repo != "o/r" || runID != 42 {
			t.Fatalf("fetch = %q run %d", repo, runID)
		}
		return validClaudeRun(42, head), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if comments[0].CommitID != head || fetches != 1 {
		t.Fatalf("bound comment = %+v, fetches = %d", comments[0], fetches)
	}
}

func TestBindClaudeIssueHeadsRefusals(t *testing.T) {
	const head = "e96af9fbfc123456789012345678901234567890"
	tests := map[string]struct {
		mutate  func(*rawComment)
		run     workflowRunResponse
		fetches int
	}{
		"wrong actor": {
			mutate:  func(c *rawComment) { c.User.Login = "some-bot[bot]" },
			run:     validClaudeRun(42, head),
			fetches: 0,
		},
		"ambiguous links": {
			mutate: func(c *rawComment) {
				c.Body += "\nhttps://github.com/o/r/actions/runs/43"
			},
			run:     validClaudeRun(42, head),
			fetches: 0,
		},
		"actionable": {
			mutate: func(c *rawComment) {
				c.Body = strings.Replace(c.Body, "\n\nLGTM.", "\n\n### Finding 1\nBug found.\n\nLGTM.", 1)
			},
			run:     validClaudeRun(42, head),
			fetches: 0,
		},
		"wrong repository": {
			mutate: func(*rawComment) {},
			run: func() workflowRunResponse {
				run := validClaudeRun(42, head)
				run.HeadRepository.FullName = "other/r"
				return run
			}(),
			fetches: 1,
		},
		"failed run": {
			mutate: func(*rawComment) {},
			run: func() workflowRunResponse {
				run := validClaudeRun(42, head)
				run.Conclusion = "failure"
				return run
			}(),
			fetches: 1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			comment := rawReview("claude[bot]", "Bot", claudeCleanBody("o/r", 42), "", "")
			test.mutate(&comment)
			comments := []rawComment{comment}
			fetches := 0
			err := bindClaudeIssueHeads("o/r", comments, func(string, int64) (workflowRunResponse, error) {
				fetches++
				return test.run, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if comments[0].CommitID != "" || fetches != test.fetches {
				t.Fatalf("invalid comment bound: %+v, fetches = %d", comments[0], fetches)
			}
		})
	}
}

func TestBindClaudeIssueHeadsDoesNotFallBackPastLatestStance(t *testing.T) {
	const head = "e96af9fbfc123456789012345678901234567890"
	clean := rawReview("claude[bot]", "Bot", claudeCleanBody("o/r", 41), "", "")
	actionable := rawReview(
		"claude[bot]", "Bot",
		strings.Replace(claudeCleanBody("o/r", 42), "\n\nLGTM.", "\n\n### Finding 1\nBug found.\n\nLGTM.", 1),
		"", "",
	)
	comments := []rawComment{clean, actionable}
	fetches := 0
	err := bindClaudeIssueHeads("o/r", comments, func(string, int64) (workflowRunResponse, error) {
		fetches++
		return validClaudeRun(41, head), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if comments[0].CommitID != "" || comments[1].CommitID != "" || fetches != 0 {
		t.Fatalf("older clean stance was reused: %+v, fetches = %d", comments, fetches)
	}
}

func claudeCleanBody(repo string, runID int64) string {
	return "**Claude finished @operator's task in 1m** —— [View job](https://github.com/" +
		repo + "/actions/runs/" + fmt.Sprint(runID) +
		")\n\n---\n\n### Code Review\n\nLGTM. Ready to merge.\n"
}

func validClaudeRun(runID int64, head string) workflowRunResponse {
	run := workflowRunResponse{
		ID: runID, HeadSHA: head, Event: "issue_comment", Conclusion: "success",
	}
	run.HeadRepository.FullName = "o/r"
	return run
}

func bits(value int) int {
	count := 0
	for value > 0 {
		count += value & 1
		value >>= 1
	}
	return count
}
