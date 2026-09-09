package verify

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

const reviewContextCap = 64 * 1024
const reviewPathMetadataCap = 8 * 1024

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
		if json.Valid(a.Body) && !strings.HasPrefix(strings.TrimSpace(string(a.Body)), "{") {
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

var reviewPathPattern = regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:py|mjs|js|ts|tsx|go|html|css|json|ya?ml|md|rs|sh|rb|java|c|cpp|sql))(?::([0-9]+)(?:[-–][0-9]+)?)?`")

// Prose paths only select recorded diff content; they never authorize anything.
func reviewDiffPaths(comments []recordedReview, files []diffFile) ([]string, []string) {
	var paths, missing []string
	seen := make(map[string]bool)
	resolved := make(map[string]bool)
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
		if resolved[path] {
			continue
		}
		resolved[path] = true
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
	if err := json.Unmarshal(a.Body, &evidence); err != nil {
		fmt.Fprintf(b, "[recorded diff unavailable (%s): decode error]\n", scrub(a.ID))
		return
	}
	if evidence.Diff == "" {
		return
	}
	files := parseUnifiedDiff(evidence.Diff)
	paths, missing := reviewDiffPaths(comments, files)
	loci = append(append([]locusRef(nil), loci...), reviewLineHints(comments, files)...)
	writeReviewPathMetadata(b, paths, missing)
	fmt.Fprintf(b, "## Recorded diff evidence (%s)\n```\n%s```\n\n", a.ID, scrub(renderJudgeDiffWithPaths(evidence.Diff, loci, paths)))
}

// Diagnostic text has its own cap; it cannot consume the substantive diff budget.
func writeReviewPathMetadata(b *strings.Builder, paths, missing []string) {
	remaining, omitted := reviewPathMetadataCap, 0
	emit := func(kind, value string) {
		entry := fmt.Sprintf("[review-referenced %s: %s]\n", kind, scrub(value))
		if len(entry) > remaining {
			omitted++
			return
		}
		b.WriteString(entry)
		remaining -= len(entry)
	}
	for _, path := range paths {
		emit("file requested within diff budget", path)
	}
	for _, reason := range missing {
		emit("context unavailable", reason)
	}
	if omitted > 0 {
		fmt.Fprintf(b, "[review-path metadata incomplete: %d entries omitted by byte budget]\n", omitted)
	}
}

// Explicit prose line references select windows, just as structured loci do.
// They remain untrusted hints and can only resolve inside the recorded diff.
func reviewLineHints(comments []recordedReview, files []diffFile) []locusRef {
	var loci []locusRef
	seen := make(map[locusRef]bool)
	for i := len(comments) - 1; i >= 0; i-- {
		for _, match := range reviewPathPattern.FindAllStringSubmatch(comments[i].body, -1) {
			ref, ok := resolveReviewLine(match, files)
			if !ok || seen[ref] {
				continue
			}
			seen[ref] = true
			loci = append(loci, ref)
		}
	}
	return loci
}

func resolveReviewLine(match []string, files []diffFile) (locusRef, bool) {
	if match[2] == "" {
		return locusRef{}, false
	}
	path, reason := resolveReviewPath(strings.TrimPrefix(match[1], "./"), files)
	if reason != "" {
		return locusRef{}, false
	}
	return parseLocus(path + ":" + match[2])
}
