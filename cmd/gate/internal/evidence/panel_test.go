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

func TestActorMatchesReviewerAliases(t *testing.T) {
	tests := []struct {
		expected string
		actor    string
	}{
		{"codex", "chatgpt-codex-connector[bot]"},
		{"codex", "codex[bot]"},
		{"cursor", "cursor[bot]"},
		{"claude", "claude[bot]"},
		{"copilot", "copilot-pull-request-reviewer[bot]"},
		{"copilot", "github-copilot[bot]"},
		{"coderabbit", "coderabbitai[bot]"},
		{"custom", "custom[bot]"},
	}
	for _, test := range tests {
		t.Run(test.expected+"/"+test.actor, func(t *testing.T) {
			if !actorMatches(test.expected, test.actor) {
				t.Fatalf("%q did not match %q", test.actor, test.expected)
			}
		})
	}
	if actorMatches("claude", "chatgpt-codex-connector[bot]") {
		t.Fatal("cross-provider actor alias matched")
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

const attestHead = "2b754f7a73c1d2e3f405162738495a6b7c8d9e0f"

func attestation(reviewer, sha string) Comment {
	return Comment{
		ID: 1, Author: "github-actions[bot]", IsBot: true,
		Body: "<!-- gate:review-attestation -->\n**Reviewer:** " + reviewer +
			"\n**Reviewed commit:** `" + sha + "`\n",
	}
}

func attestPanel() reviewpanel.Evidence {
	return reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: attestHead},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"claude"}},
	}
}

func TestClassifyPanelWorkflowAttestation(t *testing.T) {
	got := classifyPanel(attestPanel(), nil, nil, []Comment{attestation("claude", attestHead)})
	if len(got.Completed) != 1 || len(got.Missing) != 0 {
		t.Fatalf("exact-head attestation not completed: %+v", got)
	}
	reviewer := got.Completed[0]
	if reviewer.Name != "claude" || reviewer.Actor != "github-actions[bot]" ||
		reviewer.State != "COMMENTED" || reviewer.HeadSHA != attestHead {
		t.Fatalf("attested reviewer recorded wrong: %+v", reviewer)
	}
	if err := reviewpanel.Validate(got); err != nil {
		t.Fatalf("attested evidence invalid: %v", err)
	}
}

// Every re-review appends another attestation, so the head that counts is the
// one attested last — in either arrival order.
func TestClassifyPanelWorkflowAttestationLatestWins(t *testing.T) {
	const stale = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fresh := attestation("claude", attestHead)
	fresh.ID = 2
	old := attestation("claude", stale)

	got := classifyPanel(attestPanel(), nil, nil, []Comment{old, fresh})
	if len(got.Completed) != 1 || got.Completed[0].ReviewID != 2 {
		t.Fatalf("fresh attestation did not win over an earlier stale one: %+v", got)
	}
	// Reversed: the stale attestation arrives last and must not un-complete the
	// panel — the reviewer did attest this head, just not most recently.
	got = classifyPanel(attestPanel(), nil, nil, []Comment{fresh, old})
	if len(got.Completed) != 1 || got.Completed[0].ReviewID != 2 {
		t.Fatalf("a later stale attestation masked the exact-head one: %+v", got)
	}
}

// The workflow posts LF, but GitHub hands some comment bodies back CRLF, and a
// reviewer that only matched one line ending would fail on live evidence.
func TestClassifyPanelWorkflowAttestationLineEndings(t *testing.T) {
	comment := attestation("claude", attestHead)
	comment.Body = strings.ReplaceAll(comment.Body, "\n", "\r\n")
	got := classifyPanel(attestPanel(), nil, nil, []Comment{comment})
	if len(got.Completed) != 1 || len(got.Missing) != 0 {
		t.Fatalf("CRLF attestation not completed: %+v", got)
	}
}

func TestClassifyPanelWorkflowAttestationRefusals(t *testing.T) {
	const other = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := map[string]func(*Comment){
		// The head moved after the review ran; the attestation names the old one.
		"stale head": func(c *Comment) { c.Body = strings.Replace(c.Body, attestHead, other, 1) },
		// Only the repository's own Actions token may attest.
		"wrong author": func(c *Comment) { c.Author = "claude[bot]" },
		"human author": func(c *Comment) { c.IsBot = false },
		// An attestation for one reviewer must not clear another's slot.
		"other reviewer": func(c *Comment) {
			c.Body = strings.Replace(c.Body, "**Reviewer:** claude", "**Reviewer:** cursor", 1)
		},
		// Prose that merely quotes the format — a review of this very feature
		// must never clear a panel, wherever the block sits in the body.
		"no marker":        func(c *Comment) { c.Body = strings.TrimPrefix(c.Body, attestationMarker+"\n") },
		"marker not first": func(c *Comment) { c.Body = "Nice work!\n\n" + c.Body },
		"prose after":      func(c *Comment) { c.Body += "\nand some commentary\n" },
		"quoted in prose": func(c *Comment) {
			c.Body = "A review of the attestation format:\n\n" + c.Body + "\n\nLooks right to me.\n"
		},
		// The two fields must stay adjacent, so no text can drift between the
		// reviewer named and the commit claimed.
		"split fields": func(c *Comment) {
			c.Body = strings.Replace(c.Body, "\n**Reviewed commit:**", "\n\n**Reviewed commit:**", 1)
		},
		// An abbreviated SHA is not an exact head match.
		"abbreviated sha": func(c *Comment) { c.Body = strings.Replace(c.Body, attestHead, attestHead[:10], 1) },
		"malformed":       func(c *Comment) { c.Body = strings.Replace(c.Body, "`"+attestHead+"`", attestHead, 1) },
		// An inline review comment is anchored to a diff line, not a head.
		"inline comment": func(c *Comment) { c.Path = "cmd/gate/main.go"; c.CommitID = attestHead },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			comment := attestation("claude", attestHead)
			mutate(&comment)
			got := classifyPanel(attestPanel(), nil, nil, []Comment{comment})
			if len(got.Completed) != 0 || len(got.Missing) != 1 {
				t.Fatalf("invalid attestation satisfied panel: %+v", got)
			}
		})
	}
}

// A formal exact-head review is the direct evidence and outranks an attestation,
// which stands in only when the provider never posts one.
func TestClassifyPanelPrefersFormalReviewOverAttestation(t *testing.T) {
	review := rawReview("claude[bot]", "Bot", "", attestHead, "CHANGES_REQUESTED")
	review.ID = 7
	got := classifyPanel(attestPanel(), []rawComment{review}, nil,
		[]Comment{attestation("claude", attestHead)})
	if len(got.Completed) != 1 || got.Completed[0].State != "CHANGES_REQUESTED" ||
		got.Completed[0].ReviewID != 7 {
		t.Fatalf("attestation shadowed the formal review: %+v", got)
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
