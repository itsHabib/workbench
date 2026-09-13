package reviewpanel

import (
	"regexp"
	"strings"
)

// CodexComment is the connector's body framing, not authenticated completion
// evidence. ReviewedCommit is a ten-character token or a full SHA. Callers
// authenticate the issuer and bind the token to an independently observed head.
type CodexComment struct {
	ReviewedCommit    string
	NoFindingsFraming bool
}

var codexReviewedCommit = regexp.MustCompile(
	"(?m)^\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{10}|[0-9a-f]{40})`\\r?$",
)

// DecodeCodexComment parses the connector's fixed framing and exactly one
// reviewed-commit footer. The live connector emits ten hex characters; the
// full-SHA shape previously supported by review is also recognized. Other
// abbreviations and ambiguous multiple footers are invalid. A human copy can
// parse identically: this function establishes no trust, head match, review
// completion, or truth of the body's no-findings claim.
func DecodeCodexComment(body string) (CodexComment, bool) {
	if !strings.HasPrefix(body, "Codex Review:") {
		return CodexComment{}, false
	}
	matches := codexReviewedCommit.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return CodexComment{}, false
	}
	return CodexComment{
		ReviewedCommit:    matches[0][1],
		NoFindingsFraming: strings.HasPrefix(body, "Codex Review: Didn't find any major issues."),
	}, true
}

// WorkflowAttestation is the reviewer and full head named by an attestation
// body. Callers must independently authenticate its source and match its subject.
type WorkflowAttestation struct {
	Reviewer string
	HeadSHA  string
}

var attestationBody = regexp.MustCompile(
	"\\A<!-- gate:review-attestation -->" +
		"\\r?\\n\\*\\*Reviewer:\\*\\* ([a-z0-9-]{1,40})" +
		"\\r?\\n\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{40})`\\s*\\z",
)

// DecodeWorkflowAttestation parses a whole-body attestation. A quoted marker,
// surrounding prose, or abbreviated SHA is invalid. This recognizes the format
// only; a body's assertion never authenticates the actor that posted it.
func DecodeWorkflowAttestation(body string) (WorkflowAttestation, bool) {
	match := attestationBody.FindStringSubmatch(body)
	if len(match) != 3 {
		return WorkflowAttestation{}, false
	}
	return WorkflowAttestation{Reviewer: match[1], HeadSHA: match[2]}, true
}
