package verify

import (
	"strconv"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// The judge rules on findings anchored to a file:line at the exact head. Head-
// truncating the recorded diff drops the hunks a large multi-file PR carries near
// its tail — precisely the loci the judge is asked to rule on — so it cannot see
// whether the fix landed, defaults to "cannot confirm, will not clear a boundary-
// touching check", and blocks an already-fixed PR on procedure. The window here
// emits the current code around every cited locus FIRST — falling back to a
// tight window when the budget runs short, and never letting one refused window
// abandon the loci after it; the remaining budget carries the rest of the diff
// for context.
const (
	// judgeDiffCap bounds the whole diff quoted to the judge.
	judgeDiffCap = 48 * 1024
	// locusContext is the number of new-file lines kept on each side of a cited
	// line, so the code at the locus is visible even inside a very large hunk.
	locusContext = 40
	// tightContext is the fallback per-side window when the full window no
	// longer fits the remaining budget: enough to read the cited code itself,
	// small enough that dozens of loci still fit under the cap.
	tightContext = 5

	diffElision   = "[... diff trimmed to the cited lines ...]"
	diffTruncated = "[... diff truncated: budget exhausted; cited-locus windows shown above ...]"
)

// locusRef is one cited file:line a finding points at.
type locusRef struct {
	path string
	line int
}

// parseLocus splits a finding locus ("path:line") into its parts. A locus with
// no positive numeric line (issue-level comment, malformed) yields ok=false —
// there is no point in the diff to window around.
func parseLocus(s string) (locusRef, bool) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return locusRef{}, false
	}
	line, err := strconv.Atoi(s[i+1:])
	if err != nil || line <= 0 {
		return locusRef{}, false
	}
	return locusRef{path: s[:i], line: line}, true
}

// findingLoci collects, deduplicated, every file:line the verdict findings point
// at — the loci the diff window must guarantee the judge can see.
func findingLoci(arts []state.Artifact) []locusRef {
	seen := make(map[string]bool)
	var loci []locusRef
	for _, a := range arts {
		if a.Kind != state.KindVerdict {
			continue
		}
		v, err := Load(a)
		if err != nil {
			continue
		}
		for _, f := range v.Findings {
			if f.Locus == "" || seen[f.Locus] {
				continue
			}
			ref, ok := parseLocus(f.Locus)
			if !ok {
				continue
			}
			seen[f.Locus] = true
			loci = append(loci, ref)
		}
	}
	return loci
}

// diffHunk is one "@@" section: its header line, the new-file line its body
// starts at (0 when unreadable), and the body lines verbatim.
type diffHunk struct {
	header   string
	newStart int
	body     []string
}

// diffFile is one file's section of a unified diff: the "diff --git ... +++"
// preface and its hunks. path is the new-side path findings anchor against.
type diffFile struct {
	path    string
	preface []string
	hunks   []diffHunk
}

// parseUnifiedDiff splits a `git diff` into files and hunks. It is lenient: any
// unrecognized line is preserved in the current file's preface or hunk body, so
// an unusual header never drops content — worst case a section renders whole
// rather than windowed.
func parseUnifiedDiff(diff string) []diffFile {
	var files []diffFile
	var cur *diffFile
	var hunk *diffHunk
	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.hunks = append(cur.hunks, *hunk)
			hunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flushFile()
			cur = &diffFile{path: gitDiffPath(line), preface: []string{line}}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			flushHunk()
			hunk = &diffHunk{header: line, newStart: hunkNewStart(line)}
			continue
		}
		if hunk != nil {
			hunk.body = append(hunk.body, line)
			continue
		}
		cur.preface = append(cur.preface, line)
		if p, ok := plusPath(line); ok {
			cur.path = p
		}
	}
	flushFile()
	return files
}

// gitDiffPath reads the new-side path from a "diff --git a/X b/X" line. It is a
// best-effort default; a later "+++ b/X" preface line overrides it (plusPath),
// which is authoritative and handles renames.
func gitDiffPath(line string) string {
	fields := strings.TrimPrefix(line, "diff --git ")
	i := strings.LastIndex(fields, " b/")
	if i < 0 {
		return ""
	}
	return fields[i+len(" b/"):]
}

// plusPath reads the authoritative new-side path from a "+++ b/path" line. A
// deletion ("+++ /dev/null") carries no new path.
func plusPath(line string) (string, bool) {
	if !strings.HasPrefix(line, "+++ ") {
		return "", false
	}
	p := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
	if p == "" || p == "/dev/null" {
		return "", false
	}
	p = strings.TrimPrefix(p, "b/")
	// git appends a tab-separated timestamp on some diffs.
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	return p, true
}

// hunkNewStart reads the new-file starting line from a "@@ -a,b +c,d @@" header.
// An unreadable header yields 0, which makes the hunk ineligible for windowing
// (it renders whole if selected) rather than dropping it.
func hunkNewStart(header string) int {
	plus := strings.IndexByte(header, '+')
	if plus < 0 {
		return 0
	}
	rest := header[plus+1:]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// advancesNewFile reports whether a hunk body line occupies a new-file line:
// context and added lines do, removed lines and the "\ No newline" marker do not.
func advancesNewFile(line string) bool {
	return !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "\\")
}

// newLineSpan counts the new-file lines this hunk's body represents.
func (h diffHunk) newLineSpan() int {
	n := 0
	for _, l := range h.body {
		if advancesNewFile(l) {
			n++
		}
	}
	return n
}

// covers returns the cited lines that fall inside this hunk's new-file range.
func (h diffHunk) covers(targets []int) []int {
	if h.newStart == 0 {
		return nil
	}
	end := h.newStart + h.newLineSpan() - 1
	var hit []int
	for _, t := range targets {
		if t >= h.newStart && t <= end {
			hit = append(hit, t)
		}
	}
	return hit
}

// render writes the whole hunk — header then body — each line newline-terminated.
func (h diffHunk) render() string {
	var b strings.Builder
	b.WriteString(h.header)
	b.WriteByte('\n')
	for _, l := range h.body {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// window renders the hunk trimmed to ctx new-file lines around each covered
// target, standing an elision marker in for every trimmed span — including the
// span between two distant targets, so one hunk covering far-apart loci never
// renders the whole stretch separating them. A hunk with no covered target — or
// one small enough that nothing trims — renders whole.
func (h diffHunk) window(targets []int, ctx int) string {
	hit := h.covers(targets)
	if len(hit) == 0 {
		return h.render()
	}
	keep := make([]bool, len(h.body))
	kept := false
	newLine := h.newStart
	for i, l := range h.body {
		if nearAny(newLine, hit, ctx) {
			keep[i] = true
			kept = true
		}
		if advancesNewFile(l) {
			newLine++
		}
	}
	if !kept {
		return h.render()
	}
	var b strings.Builder
	b.WriteString(h.header)
	b.WriteByte('\n')
	elided := false
	for i, l := range h.body {
		if !keep[i] {
			if !elided {
				b.WriteString(diffElision)
				b.WriteByte('\n')
				elided = true
			}
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
		elided = false
	}
	return b.String()
}

func nearAny(line int, targets []int, ctx int) bool {
	for _, t := range targets {
		d := line - t
		if d < 0 {
			d = -d
		}
		if d <= ctx {
			return true
		}
	}
	return false
}

// diffWriter accumulates rendered diff chunks under a shared byte budget, so the
// cited-locus windows written first can never be the part a later truncation
// drops. Each file's preface is written at most once, ahead of its first chunk.
type diffWriter struct {
	b         strings.Builder
	budget    int
	files     []diffFile
	written   []bool
	truncated bool
}

// emit writes one hunk chunk for files[fi], prefixed by the file preface the
// first time that file appears. It returns false once the budget cannot hold the
// chunk, marking the render truncated.
func (w *diffWriter) emit(fi int, chunk string) bool {
	if !w.written[fi] {
		chunk = filePreface(w.files[fi]) + chunk
	}
	if len(chunk) > w.budget {
		w.truncated = true
		return false
	}
	w.b.WriteString(chunk)
	w.budget -= len(chunk)
	w.written[fi] = true
	return true
}

func filePreface(f diffFile) string {
	if len(f.preface) == 0 {
		return "diff --git a/" + f.path + " b/" + f.path + "\n"
	}
	return strings.Join(f.preface, "\n") + "\n"
}

// renderJudgeDiff quotes the recorded diff for the judge with the current code at
// every cited locus guaranteed present. Cited-locus windows are emitted first and
// are never the part truncated; the remaining budget carries the rest of the diff
// so the judge still sees the change as a whole.
func renderJudgeDiff(diff string, loci []locusRef) string {
	files := parseUnifiedDiff(diff)
	cited := make(map[string][]int)
	for _, l := range loci {
		cited[l.path] = append(cited[l.path], l.line)
	}
	w := &diffWriter{budget: judgeDiffCap, files: files, written: make([]bool, len(files))}

	// Tranche 1: windows around cited loci, so they are shown before anything can
	// exhaust the budget. The width is decided over the whole set: full windows
	// when they all fit, tight ones for every locus when they cannot — many small
	// windows ground the judge better than a few big ones that crowd the rest
	// out. A refused window never abandons the loci after it.
	emitted := make(map[[2]int]bool)
	width := citedWidth(files, cited)
	for fi := range files {
		targets := cited[files[fi].path]
		if len(targets) == 0 {
			continue
		}
		for hi := range files[fi].hunks {
			h := files[fi].hunks[hi]
			if len(h.covers(targets)) == 0 {
				continue
			}
			if w.emit(fi, h.window(targets, width)) {
				emitted[[2]int{fi, hi}] = true
			}
		}
	}

	// Tranche 2: the rest of the diff — hunks whole, and the preface alone for a
	// file that carries none (binary, mode-only, rename-only, submodule), so
	// those changes stay visible to the judge — until the budget runs out.
	for fi := range files {
		if len(files[fi].hunks) == 0 && !w.written[fi] {
			if !w.emit(fi, "") {
				return w.result()
			}
			continue
		}
		for hi := range files[fi].hunks {
			if emitted[[2]int{fi, hi}] {
				continue
			}
			if !w.emit(fi, files[fi].hunks[hi].render()) {
				return w.result()
			}
		}
	}
	return w.result()
}

// citedWidth picks the per-side window width for the cited-locus tranche: full
// when every cited window (with its file preface) fits the budget together,
// tight otherwise — so a large cited set degrades uniformly instead of showing
// the first loci in full and dropping the last ones entirely.
func citedWidth(files []diffFile, cited map[string][]int) int {
	total := 0
	for fi := range files {
		targets := cited[files[fi].path]
		if len(targets) == 0 {
			continue
		}
		counted := false
		for _, h := range files[fi].hunks {
			if len(h.covers(targets)) == 0 {
				continue
			}
			if !counted {
				total += len(filePreface(files[fi]))
				counted = true
			}
			total += len(h.window(targets, locusContext))
		}
	}
	if total > judgeDiffCap {
		return tightContext
	}
	return locusContext
}

func (w *diffWriter) result() string {
	if w.truncated {
		w.b.WriteString(diffTruncated)
		w.b.WriteByte('\n')
	}
	return w.b.String()
}
