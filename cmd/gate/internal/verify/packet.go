package verify

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// SourceBudget bounds complete supplementary source across one run.
const SourceBudget = 256 * 1024

// SourceEvidence is exact-head content collected by Gate, not an author appendix.
type SourceEvidence struct {
	Subject Subject      `json:"subject"`
	Sources []SourceFile `json:"sources"`
}

// SourceFile carries the Git blob identity alongside its complete text.
type SourceFile struct {
	Path    string `json:"path"`
	Blob    string `json:"blob"`
	Content string `json:"content"`
}

// Packet reports mechanical coverage, not whether the evidence proves a fix.
type Packet struct {
	Complete bool     `json:"complete"`
	Missing  []string `json:"missing"`
	Context  string   `json:"context"`
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
	files, paths, loci := packetRequirements(arts, actionablePacketComments(comments))
	var b strings.Builder
	b.WriteString(ctx)
	remaining := SourceBudget
	for _, path := range uniquePacketStrings(paths) {
		if _, ok := sources[path]; ok {
			continue
		}
		text := packetFile(files, path)
		if text == "" || len(text) > remaining {
			p.Missing = append(p.Missing, path+": required diff unavailable or exceeds packet budget; collect exact-head source")
			continue
		}
		b.WriteString("\n## Required recorded diff (complete file section)\n```\n" + scrub(text) + "```\n")
		remaining -= len(text)
	}
	for _, ref := range loci {
		if src, ok := sources[ref.path]; ok && ref.line <= len(strings.Split(src.Content, "\n")) {
			continue
		}
		if !packetCoversLocus(files, ref) {
			p.Missing = append(p.Missing, fmt.Sprintf("%s:%d: cited line absent; collect exact-head source", ref.path, ref.line))
		}
	}
	for _, c := range comments {
		if !strings.Contains(ctx, scrub(string(c.raw))) {
			p.Missing = append(p.Missing, fmt.Sprintf("review %s/%d: omitted by context budget", c.evidence, c.index))
		}
	}
	for _, a := range arts {
		var s SourceEvidence
		if a.Kind != state.KindEvidence || json.Unmarshal(a.Body, &s) != nil || len(s.Sources) == 0 {
			continue
		}
		for _, f := range s.Sources {
			fmt.Fprintf(&b, "\n## Exact-head source %s (%s, blob %s, evidence %s)\n```\n%s\n```\n", scrub(f.Path), subject.HeadSHA, f.Blob, a.ID, scrub(f.Content))
		}
	}
	p.Context = b.String()
	p.Missing = uniquePacketStrings(p.Missing)
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

func packetRequirements(arts []state.Artifact, comments []recordedReview) ([]diffFile, []string, []locusRef) {
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
	paths, _ := reviewDiffPaths(comments, files)
	loci := append(findingLoci(arts), reviewLineHints(comments, files)...)
	for _, c := range comments {
		var anchor struct {
			Path string
			Line int
		}
		if json.Unmarshal(c.raw, &anchor) == nil && anchor.Path != "" {
			paths = append(paths, anchor.Path)
			if anchor.Line > 0 {
				loci = append(loci, locusRef{path: anchor.Path, line: anchor.Line})
			}
		}
	}
	for _, hint := range reviewPathHints(comments) {
		// Only file-shaped prose is a dependency. Commands, symbols and flags are not paths.
		if path.Ext(hint) != "" && !strings.ContainsAny(hint, " \t") && !strings.Contains(hint, "://") {
			resolved, reason := resolveReviewPath(hint, files)
			if reason == "absent from recorded diff" {
				paths = append(paths, hint)
			}
			if resolved != "" {
				paths = append(paths, resolved)
			}
		}
	}
	// Ambiguous basenames require all matching files. Never choose an arbitrary README.
	for _, hint := range reviewPathHints(comments) {
		for _, f := range files {
			if strings.HasSuffix(f.path, "/"+hint) {
				paths = append(paths, f.path)
			}
		}
	}
	for _, ref := range loci {
		paths = append(paths, ref.path)
	}
	return files, paths, loci
}

// Source prose is not review authority. Only unresolved reviewer comments select
// companion requirements; author chatter cannot add mandatory dependencies.
func actionablePacketComments(comments []recordedReview) []recordedReview {
	var result []recordedReview
	for _, c := range comments {
		var meta struct {
			IsBot    bool `json:"is_bot"`
			Resolved bool `json:"resolved"`
		}
		if json.Unmarshal(c.raw, &meta) == nil && meta.IsBot && !meta.Resolved {
			result = append(result, c)
		}
	}
	return result
}
