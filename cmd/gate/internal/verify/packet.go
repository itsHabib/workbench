package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// SourceBudget bounds complete supplementary source across one run. Each file
// still passes the collector's separate 256 KiB text and blob-identity checks.
const SourceBudget = 512 * 1024

const requiredDiffBudget = 256 * 1024

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
	SourceHints     []string `json:"source_hints,omitempty"`
}

// JudgmentPacket exposes exactly the context checked before provider invocation.
// Missing required context leaves the existing escalation unjudged and repairable.
func JudgmentPacket(arts []state.Artifact, subject Subject) (Packet, error) {
	ctx, includedReviews, err := judgeContextWithReviewCoverage(arts)
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
	files, refs := packetRequirements(arts, active, index)
	if refs.needsIndex && !indexKnown {
		p.Missing = append(p.Missing, "exact-head file index unavailable; run gate evidence to discover real companion paths")
	}
	var b strings.Builder
	b.WriteString(ctx)
	p.SourceHints = uniquePacketStrings(refs.hints)
	writeReviewPathMetadata(&b, nil, nil, p.SourceHints)
	remaining := requiredDiffBudget
	indexPaths := make(map[string]bool)
	for _, name := range index {
		indexPaths[name] = true
	}
	for _, path := range uniquePacketStrings(refs.paths) {
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
	for _, ref := range refs.loci {
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
	p.Missing = append(p.Missing, writeRequiredReviews(&b, active, includedReviews)...)
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

type packetReferences struct {
	paths      []string
	loci       []locusRef
	hints      []string
	needsIndex bool
}

func packetRequirements(arts []state.Artifact, comments []recordedReview, index []string) ([]diffFile, packetReferences) {
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
	refs := packetReferences{loci: findingLoci(arts)}
	for _, c := range comments {
		var anchor reviewComment
		if json.Unmarshal(c.raw, &anchor) != nil || anchor.Path == "" {
			continue
		}
		refs.paths = append(refs.paths, anchor.Path)
		if anchor.Line > 0 {
			refs.loci = append(refs.loci, locusRef{path: anchor.Path, line: anchor.Line})
		}
	}
	var changed []string
	for _, f := range files {
		changed = append(changed, f.path)
	}
	for _, c := range comments {
		refs.addComment(c, changed, index)
	}
	for _, ref := range refs.loci {
		refs.paths = append(refs.paths, ref.path)
	}
	return files, refs
}

// An exact path wins. Otherwise callers must distinguish a unique basename
// from an ambiguous hint; the latter cannot require every candidate file.
func matchingPacketPaths(hint string, known []string) []string {
	exactOnly := strings.Contains(hint, "/")
	hint = strings.TrimPrefix(hint, "./")
	for _, name := range known {
		if name == hint {
			return []string{name}
		}
	}
	if exactOnly {
		return nil
	}
	var result []string
	for _, name := range known {
		if strings.HasSuffix(name, "/"+hint) {
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

func (refs *packetReferences) addComment(c recordedReview, changed, index []string) {
	for _, match := range reviewLineMatches(c.body) {
		refs.addHint(c.key(), match, changed, index)
	}
}

func (refs *packetReferences) addHint(review string, match, changed, index []string) {
	hint := strings.TrimPrefix(match[1], "./")
	matches := matchingPacketPaths(match[1], changed)
	if !strings.Contains(match[1], "/") && match[2] == "" {
		refs.addBareToken(review, hint, matches, index)
		return
	}
	// Only an exact diff path establishes which file a precise reference names.
	// A basename fallback, unique or not, is resolved against the complete index
	// before claiming coverage.
	if len(matches) != 1 || matches[0] != hint {
		refs.needsIndex = true
	}
	known := append(append([]string(nil), changed...), index...)
	matches = matchingPacketPaths(match[1], known)
	if len(matches) > 1 {
		refs.hints = append(refs.hints, fmt.Sprintf("review %s: ambiguous %s; candidates %v; no source selected or finding resolved", review, hint, matches))
		return
	}
	if len(matches) == 0 {
		return
	}
	refs.paths = append(refs.paths, matches[0])
	if ref, ok := parseLocus(matches[0] + ":" + match[2]); ok {
		refs.loci = append(refs.loci, ref)
	}
}

// addBareToken handles a line-less name without a directory. Such a token can
// name a command, symbol or example, so it resolves only among changed files:
// an unchanged repository blob never becomes required source because a review
// mentioned its name, even once the file index is recorded.
func (refs *packetReferences) addBareToken(review, hint string, changed, index []string) {
	if len(changed) == 1 {
		refs.paths = append(refs.paths, changed[0])
		return
	}
	if len(changed) > 1 {
		refs.hints = append(refs.hints, fmt.Sprintf("review %s: ambiguous %s; candidates %v; no source selected or finding resolved", review, hint, changed))
		return
	}
	if candidates := matchingPacketPaths(hint, index); len(candidates) > 0 {
		refs.hints = append(refs.hints, fmt.Sprintf("review %s: %s is an unchanged bare token; candidates %v are not represented as required source", review, hint, candidates))
	}
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

func writeRequiredReviews(b *strings.Builder, active []recordedReview, included map[string]bool) []string {
	var missing []string
	reviewRemaining := reviewContextCap
	for _, c := range active {
		if included[c.key()] {
			continue
		}
		entry := fmt.Sprintf("\n## Required source review (%s/%d; not authority)\n%s\n", c.evidence, c.index, scrub(string(c.raw)))
		if len(entry) > reviewRemaining {
			missing = append(missing, fmt.Sprintf("review %s/%d: exceeds required review budget", c.evidence, c.index))
			continue
		}
		b.WriteString(entry)
		// Record secondary-section coverage in the same per-packet identity set.
		included[c.key()] = true
		reviewRemaining -= len(entry)
	}
	return missing
}
