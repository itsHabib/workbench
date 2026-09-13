package reviewpanel

import (
	"regexp"
	"strings"
)

// Comment is the GitHub metadata needed to decode a harness-authored review
// sentinel. Author and IsBot must come from the authenticated API response,
// never from the body. CommitID and Path distinguish inline/formal comments
// from issue comments. This is decoder input, not a new artifact schema.
type Comment struct {
	ID       int64
	Author   string
	IsBot    bool
	Body     string
	CommitID string
	Path     string
}

var fullCommit = regexp.MustCompile(`\A[0-9a-f]{40}\z`)

var codexReviewedCommit = regexp.MustCompile(
	"(?m)^\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{10}|[0-9a-f]{40})`\\r?$",
)

// DecodeCodexComment decodes the connector's reviewed-commit footer against a
// known full head. The live connector emits ten hex characters; the full SHA
// shape previously supported by review is also exact. Other abbreviations and
// ambiguous multiple footers are invalid. This does not resolve short SHAs or
// establish diff equivalence: callers must supply the observed PR head.
//
// CLEAN records the connector's no-findings framing. A review with findings is
// still a completed submission (COMMENTED); decoding never accepts its findings
// or authorizes a merge.
func DecodeCodexComment(comment Comment, headSHA string) (Reviewer, bool) {
	if !issueCommentFrom(comment, "chatgpt-codex-connector[bot]") ||
		!fullCommit.MatchString(headSHA) || !strings.HasPrefix(comment.Body, "Codex Review:") {
		return Reviewer{}, false
	}
	matches := codexReviewedCommit.FindAllStringSubmatch(comment.Body, -1)
	if len(matches) != 1 || !strings.HasPrefix(headSHA, matches[0][1]) {
		return Reviewer{}, false
	}
	state := "COMMENTED"
	if strings.HasPrefix(comment.Body, "Codex Review: Didn't find any major issues.") {
		state = "CLEAN"
	}
	return Reviewer{
		Name: "codex", Actor: comment.Author, State: state,
		HeadSHA: headSHA, ReviewID: comment.ID,
	}, true
}

var attestationBody = regexp.MustCompile(
	"\\A<!-- gate:review-attestation -->" +
		"\\r?\\n\\*\\*Reviewer:\\*\\* ([a-z0-9-]{1,40})" +
		"\\r?\\n\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{40})`\\s*\\z",
)

// DecodeWorkflowAttestation decodes the repository Actions token's whole-body
// attestation. Its authority comes from the workflow that ran the reviewer at
// the recorded commit, not provider prose. A quoted marker, surrounding prose,
// another actor, or an inline comment is invalid. Callers must match the
// decoded reviewer and head to their subject before crediting completion.
func DecodeWorkflowAttestation(comment Comment) (Reviewer, bool) {
	if !issueCommentFrom(comment, "github-actions[bot]") {
		return Reviewer{}, false
	}
	match := attestationBody.FindStringSubmatch(comment.Body)
	if len(match) != 3 {
		return Reviewer{}, false
	}
	return Reviewer{
		Name: match[1], Actor: comment.Author, State: "COMMENTED",
		HeadSHA: match[2], ReviewID: comment.ID,
	}, true
}

func issueCommentFrom(comment Comment, actor string) bool {
	return comment.ID > 0 && comment.Author == actor && comment.IsBot &&
		comment.CommitID == "" && comment.Path == ""
}
