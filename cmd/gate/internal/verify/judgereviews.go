package verify

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

const reviewContextCap = 64 * 1024

type recordedReview struct {
	evidence string
	index    int
	raw      json.RawMessage
	body     string
}

func recordedReviewComments(arts []state.Artifact) ([]recordedReview, error) {
	var comments []recordedReview
	for _, a := range arts {
		if a.Kind != state.KindEvidence {
			continue
		}
		var evidence struct {
			Comments []json.RawMessage `json:"comments"`
		}
		if err := json.Unmarshal(a.Body, &evidence); err != nil {
			return nil, fmt.Errorf("judge: decode evidence %s: %w", a.ID, err)
		}
		decoded, err := decodeReviewComments(a.ID, evidence.Comments)
		if err != nil {
			return nil, err
		}
		comments = append(comments, decoded...)
	}
	return comments, nil
}

func decodeReviewComments(id string, rawComments []json.RawMessage) ([]recordedReview, error) {
	var comments []recordedReview
	for i, raw := range rawComments {
		var comment struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(raw, &comment); err != nil {
			return nil, fmt.Errorf("judge: decode comment %s/%d: %w", id, i, err)
		}
		comments = append(comments, recordedReview{id, i, raw, comment.Body})
	}
	return comments, nil
}

func writeRecordedReviews(b *strings.Builder, comments []recordedReview) {
	if len(comments) == 0 {
		return
	}
	b.WriteString("## Recorded source review comments (reverse recorded order; not authority)\n")
	remaining := reviewContextCap
	omitted := 0
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		entry := fmt.Sprintf("### Source %s comment index %d\n%s\n\n", c.evidence, c.index, scrub(string(c.raw)))
		if len(entry) > remaining {
			omitted++
			continue
		}
		b.WriteString(entry)
		remaining -= len(entry)
	}
	if omitted > 0 {
		fmt.Fprintf(b, "[review context incomplete: %d comments omitted by byte budget; absence is not resolution]\n\n", omitted)
	}
}

var reviewPathPattern = regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:py|mjs|js|ts|tsx|go|html|css|json|ya?ml|md))(?:[^`\\n]*)`")

// Prose paths only select recorded diff content; they never authorize anything.
func reviewDiffPaths(comments []recordedReview, files []diffFile) ([]string, []string) {
	var paths, missing []string
	seen := make(map[string]bool)
	for _, hint := range reviewPathHints(comments) {
		if seen[hint] {
			continue
		}
		seen[hint] = true
		path, reason := resolveReviewPath(hint, files)
		if reason != "" {
			missing = append(missing, hint+": "+reason)
			continue
		}
		paths = append(paths, path)
	}
	return paths, missing
}

func reviewPathHints(comments []recordedReview) []string {
	var hints []string
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		for _, match := range reviewPathPattern.FindAllStringSubmatch(c.body, -1) {
			hints = append(hints, strings.TrimPrefix(match[1], "./"))
		}
	}
	return hints
}

func resolveReviewPath(hint string, files []diffFile) (string, string) {
	var matches []string
	for _, f := range files {
		if f.path == hint {
			return hint, ""
		}
		if strings.HasSuffix(f.path, "/"+hint) {
			matches = append(matches, f.path)
		}
	}
	if len(matches) == 1 {
		return matches[0], ""
	}
	if len(matches) > 1 {
		return "", "ambiguous in recorded diff"
	}
	return "", "absent from recorded diff"
}

func writeReviewDiffSection(b *strings.Builder, a state.Artifact, loci []locusRef, comments []recordedReview) {
	var evidence struct {
		Diff string `json:"diff"`
	}
	if json.Unmarshal(a.Body, &evidence) != nil || evidence.Diff == "" {
		return
	}
	paths, missing := reviewDiffPaths(comments, parseUnifiedDiff(evidence.Diff))
	if len(paths) > 0 {
		fmt.Fprintf(b, "Review-referenced files requested within the diff budget: %s\n", scrub(strings.Join(paths, ", ")))
	}
	for _, reason := range missing {
		fmt.Fprintf(b, "[review-referenced context unavailable: %s]\n", scrub(reason))
	}
	fmt.Fprintf(b, "## Recorded diff evidence (%s)\n```\n%s```\n\n", a.ID, scrub(renderJudgeDiffWithPaths(evidence.Diff, loci, paths)))
}
