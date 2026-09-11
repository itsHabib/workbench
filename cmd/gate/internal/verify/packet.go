package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// SourceBudget bounds complete supplementary source across one run.
const SourceBudget = 256 * 1024

// SourceEvidence is exact-head content collected by Gate, not an author appendix.
type SourceEvidence struct {
	Subject       Subject      `json:"subject"`
	Sources       []SourceFile `json:"sources"`
	FileIndex     []string     `json:"file_index,omitempty"`
	IndexComplete bool         `json:"index_complete,omitempty"`
}

// SourceFile carries the Git blob identity alongside its complete text.
type SourceFile struct {
	Path    string `json:"path"`
	Blob    string `json:"blob"`
	Content string `json:"content"`
}

// Packet reports mechanical coverage, not whether the evidence proves a fix.
type Packet struct {
	Complete        bool     `json:"complete"`
	Missing         []string `json:"missing"`
	Context         string   `json:"context"`
	RequiredSources []string `json:"required_sources"`
}

// JudgmentPacket exposes exactly the context checked before provider invocation.
// Missing required context leaves the existing escalation unjudged and repairable.
func JudgmentPacket(arts []state.Artifact, subject Subject) (Packet, error) {
	ctx, err := judgeContext(arts)
	if err != nil {
		return Packet{}, err
	}
	p := Packet{Context: ctx, Missing: []string{}}
	comments, err := recordedReviewComments(arts)
	if err != nil {
		return Packet{}, err
	}
	sources, err := packetSources(arts, subject)
	if err != nil {
		return Packet{}, err
	}
	active := actionablePacketComments(comments, subject.HeadSHA)
	index, indexKnown, err := packetIndex(arts, subject)
	if err != nil {
		return Packet{}, err
	}
	files, paths, loci, needsIndex := packetRequirements(arts, active, index)
	if needsIndex && !indexKnown {
		p.Missing = append(p.Missing, "exact-head file index unavailable; run gate evidence to discover real companion paths")
	}
	var b strings.Builder
	b.WriteString(ctx)
	remaining := SourceBudget
	indexPaths := make(map[string]bool)
	for _, name := range index {
		indexPaths[name] = true
	}
	for _, path := range uniquePacketStrings(paths) {
		if _, ok := sources[path]; ok {
			continue
		}
		if indexKnown && !indexPaths[path] {
			fmt.Fprintf(&b, "\n## Exact-head file index: %s has no file blob at %s\n", scrub(path), subject.HeadSHA)
			continue
		}
		text := packetFile(files, path)
		if text == "" || len(text) > remaining {
			p.Missing = append(p.Missing, path+": required diff unavailable or exceeds packet budget; collect exact-head source")
			p.RequiredSources = append(p.RequiredSources, path)
			continue
		}
		b.WriteString("\n## Required recorded diff (complete file section)\n```\n" + scrub(text) + "```\n")
		remaining -= len(text)
	}
	for _, ref := range loci {
		if _, ok := sources[ref.path]; ok {
			continue
		}
		if indexKnown && !indexPaths[ref.path] {
			continue
		}
		if !packetCoversLocus(files, ref) {
			p.Missing = append(p.Missing, fmt.Sprintf("%s:%d: cited line absent; collect exact-head source", ref.path, ref.line))
			p.RequiredSources = append(p.RequiredSources, ref.path)
		}
	}
	p.Missing = append(p.Missing, writeRequiredReviews(&b, active)...)
	writePacketSources(&b, arts, subject)
	p.Context = b.String()
	p.Missing = uniquePacketStrings(p.Missing)
	p.RequiredSources = uniquePacketStrings(p.RequiredSources)
	p.Complete = len(p.Missing) == 0
	return p, nil
}

func packetSources(arts []state.Artifact, subject Subject) (map[string]SourceFile, error) {
	sources := make(map[string]SourceFile)
	for _, a := range arts {
		var s SourceEvidence
		if a.Kind != state.KindEvidence || json.Unmarshal(a.Body, &s) != nil || len(s.Sources) == 0 {
			continue
		}
		if s.Subject != subject {
			return nil, fmt.Errorf("judgment_evidence_subject_mismatch: %s", a.ID)
		}
		for _, f := range s.Sources {
			sources[f.Path] = f
		}
	}
	return sources, nil
}

func packetFile(files []diffFile, path string) string {
	for _, f := range files {
		if f.path != path {
			continue
		}
		var b strings.Builder
		b.WriteString(filePreface(f))
		for _, h := range f.hunks {
			b.WriteString(h.render())
		}
		return b.String()
	}
	return ""
}

func packetCoversLocus(files []diffFile, ref locusRef) bool {
	for _, f := range files {
		if f.path != ref.path {
			continue
		}
		// A complete deletion section proves the current file (and any old line) is absent.
		if strings.Contains(filePreface(f), "+++ /dev/null\n") {
			return true
		}
		for _, h := range f.hunks {
			if len(h.covers([]int{ref.line})) > 0 {
				return true
			}
		}
	}
	return false
}

func uniquePacketStrings(values []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func packetRequirements(arts []state.Artifact, comments []recordedReview, index []string) ([]diffFile, []string, []locusRef, bool) {
	var files []diffFile
	for _, a := range arts {
		if a.Kind != state.KindEvidence {
			continue
		}
		var d struct {
			Diff string `json:"diff"`
		}
		if json.Unmarshal(a.Body, &d) == nil {
			files = append(files, parseUnifiedDiff(d.Diff)...)
		}
	}
	var paths []string
	loci := findingLoci(arts)
	for _, c := range comments {
		var anchor reviewComment
		if json.Unmarshal(c.raw, &anchor) != nil || anchor.Path == "" {
			continue
		}
		paths = append(paths, anchor.Path)
		if anchor.Line > 0 {
			loci = append(loci, locusRef{path: anchor.Path, line: anchor.Line})
		}
	}
	var changed []string
	for _, f := range files {
		changed = append(changed, f.path)
	}
	loci = append(loci, packetLineHints(comments, changed, index)...)
	needsIndex := false
	for _, hint := range reviewPathHints(comments) {
		matches := matchingPacketPaths(hint, changed)
		if len(matches) == 0 {
			needsIndex = true
			matches = matchingPacketPaths(hint, index)
		}
		paths = append(paths, matches...)
	}
	for _, ref := range loci {
		paths = append(paths, ref.path)
	}
	return files, paths, loci, needsIndex
}

// Explicit paths are exact. A bare basename selects all matching recorded paths.
func matchingPacketPaths(hint string, known []string) []string {
	var result []string
	for _, name := range known {
		if name == hint || (!strings.Contains(hint, "/") && strings.HasSuffix(name, "/"+hint)) {
			result = append(result, name)
		}
	}
	return uniquePacketStrings(result)
}

func packetIndex(arts []state.Artifact, subject Subject) ([]string, bool, error) {
	for i := len(arts) - 1; i >= 0; i-- {
		a := arts[i]
		var s SourceEvidence
		if a.Kind != state.KindEvidence || json.Unmarshal(a.Body, &s) != nil || !s.IndexComplete {
			continue
		}
		if s.Subject != subject {
			return nil, false, fmt.Errorf("judgment_evidence_subject_mismatch: %s", a.ID)
		}
		return s.FileIndex, true, nil
	}
	return nil, false, nil
}

// Use the same stale/coordinator exclusions as the review rung. Historical
// comments remain context; they do not add mandatory source requirements.
func actionablePacketComments(comments []recordedReview, head string) []recordedReview {
	var result []recordedReview
	for _, c := range comments {
		var meta reviewComment
		if json.Unmarshal(c.raw, &meta) != nil || !meta.IsBot || staleComment(meta.Resolved, meta.CommitID, head) {
			continue
		}
		if strings.Contains(meta.Body, "review-coordinator-verdict") {
			continue
		}
		result = append(result, c)
	}
	return result
}

func packetLineHints(comments []recordedReview, changed, index []string) []locusRef {
	var result []locusRef
	for _, c := range comments {
		result = append(result, packetCommentLines(c.body, changed, index)...)
	}
	return result
}

func packetCommentLines(body string, changed, index []string) []locusRef {
	var result []locusRef
	for _, match := range reviewLineMatches(body) {
		if match[2] == "" {
			continue
		}
		paths := matchingPacketPaths(strings.TrimPrefix(match[1], "./"), changed)
		if len(paths) == 0 {
			paths = matchingPacketPaths(strings.TrimPrefix(match[1], "./"), index)
		}
		for _, name := range paths {
			if ref, ok := parseLocus(name + ":" + match[2]); ok {
				result = append(result, ref)
			}
		}
	}
	return result
}

func writePacketSources(b *strings.Builder, arts []state.Artifact, subject Subject) {
	for _, a := range arts {
		var s SourceEvidence
		if a.Kind != state.KindEvidence || json.Unmarshal(a.Body, &s) != nil || len(s.Sources) == 0 {
			continue
		}
		for _, f := range s.Sources {
			fmt.Fprintf(b, "\n## Exact-head source %s (%s, blob %s, evidence %s)\n```\n%s\n```\n", scrub(f.Path), subject.HeadSHA, f.Blob, a.ID, scrub(f.Content))
		}
	}
}

func writeRequiredReviews(b *strings.Builder, active []recordedReview) []string {
	var missing []string
	reviewRemaining := reviewContextCap
	for _, c := range active {
		entry := fmt.Sprintf("\n## Required source review (%s/%d; not authority)\n%s\n", c.evidence, c.index, scrub(string(c.raw)))
		if len(entry) > reviewRemaining {
			missing = append(missing, fmt.Sprintf("review %s/%d: exceeds required review budget", c.evidence, c.index))
			continue
		}
		b.WriteString(entry)
		reviewRemaining -= len(entry)
	}
	return missing
}
