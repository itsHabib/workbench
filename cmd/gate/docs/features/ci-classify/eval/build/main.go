// s2build turns saved `gh run view --log-failed` logs into eval chunks: it
// reproduces the ci-classifier's real trimming (group by job/step, tail each
// failed step, strip GitHub's ISO timestamp prefix, cap length) so the eval
// input matches exactly what the ship seam will feed the model at runtime.
//
// Pass 1 (no labels.tsv): writes chunks/<base>.txt + review-compact.txt.
// Pass 2 (labels.tsv present): also emits ci-chunks.jsonl for labeled bases.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	tailLines = 60   // errors live at the end of a step; proven trim from the POC
	maxChars  = 8000 // keep the tail; a small model degrades past this density
)

// strips an optional BOM + GitHub Actions ISO-8601 timestamp prefix per line.
var tsRe = regexp.MustCompile(`^\x{FEFF}?\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z ?`)

// strongRe marks a line that names a real cause; wrapRe marks generic wrapper /
// teardown lines that are NOT the cause (a runner-error tail, container cleanup).
// The chunk anchors on the LAST strong, non-wrapper line so the decisive cause
// sits at the end of the excerpt instead of being buried under wrapper noise —
// blind-tail trimming misclassifies coverage-under-pnpm and service-container
// failures because the true cause scrolls off the tail.
var strongRe = regexp.MustCompile(`(?i)(error\[e?\d|error:|\bfail(ed|ure)?\b|assertion|panic:|not implemented|could not compile|does not meet|coverage for|psql:? *error|ebusy|resource busy|429 too many|could not resolve|etimedout|econnrefused|starting up|shutting down|✖|##\[error\])`)
var wrapRe = regexp.MustCompile(`(?i)(elifecycle|err_pnpm_recursive|make: \*\*\*|process completed with exit code|command failed with exit code|npm error code|waiting for other jobs|cleaning up orphan|post job cleanup)`)

// stepWindow returns an error-anchored window over one step's lines, falling
// back to the plain tail when no cause line is present.
func stepWindow(lines []string) []string {
	anchor := -1
	for i, l := range lines {
		if strongRe.MatchString(l) && !wrapRe.MatchString(l) {
			anchor = i
		}
	}
	if anchor < 0 {
		if len(lines) > tailLines {
			return lines[len(lines)-tailLines:]
		}
		return lines
	}
	lo := anchor - 40
	if lo < 0 {
		lo = 0
	}
	hi := anchor + 5
	if hi > len(lines) {
		hi = len(lines)
	}
	return lines[lo:hi]
}

func extract(raw string) (chunk string, steps int) {
	byStep := map[string][]string{}
	var order []string
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		key := parts[0] + " / " + parts[1]
		if _, seen := byStep[key]; !seen {
			order = append(order, key)
		}
		byStep[key] = append(byStep[key], tsRe.ReplaceAllString(parts[2], ""))
	}
	var b strings.Builder
	for _, key := range order {
		// blind tail: the eval baseline. Error-anchored selection (stepWindow)
		// was measured worse here — the ship chunker needs its own tuned pass.
		lines := byStep[key]
		if len(lines) > tailLines {
			lines = lines[len(lines)-tailLines:]
		}
		fmt.Fprintf(&b, "=== failed step: %s\n%s\n\n", key, strings.Join(lines, "\n"))
	}
	_ = stepWindow // kept for the design record; not used by the locked baseline
	out := strings.TrimSpace(b.String())
	if len(out) > maxChars {
		out = strings.TrimSpace(out[len(out)-maxChars:])
	}
	return out, len(order)
}

func main() {
	work := ".."
	logsDir := filepath.Join(work, "logs")
	chunksDir := filepath.Join(work, "chunks")
	_ = os.MkdirAll(chunksDir, 0o755)

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// optional labels: base \t bucket \t note
	labels := map[string][2]string{}
	if b, err := os.ReadFile(filepath.Join(work, "labels.tsv")); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			f := strings.SplitN(ln, "\t", 3)
			note := ""
			if len(f) == 3 {
				note = f[2]
			}
			if len(f) >= 2 {
				labels[strings.TrimSpace(f[0])] = [2]string{strings.TrimSpace(f[1]), note}
			}
		}
	}

	var review strings.Builder
	var jsonl strings.Builder
	var names []string
	chunks := map[string]string{}
	stepCount := map[string]int{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(logsDir, e.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "READ FAIL %s: %v\n", e.Name(), err)
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".log")
		chunk, steps := extract(string(raw))
		if chunk == "" {
			fmt.Printf("EMPTY  %s\n", base)
			continue
		}
		names = append(names, base)
		chunks[base] = chunk
		stepCount[base] = steps
		_ = os.WriteFile(filepath.Join(chunksDir, base+".txt"), []byte(chunk), 0o644)
	}
	sort.Strings(names)

	for _, base := range names {
		chunk := chunks[base]
		// compact review: last 28 lines of the chunk (the error usually sits here)
		lines := strings.Split(chunk, "\n")
		if len(lines) > 28 {
			lines = lines[len(lines)-28:]
		}
		lbl := "?"
		if l, ok := labels[base]; ok {
			lbl = l[0]
		}
		fmt.Fprintf(&review, "##### %s  [chars=%d steps=%d]  label=%s\n%s\n\n",
			base, len(chunk), stepCount[base], lbl, strings.Join(lines, "\n"))

		if l, ok := labels[base]; ok {
			row, _ := json.Marshal(map[string]string{
				"input":    chunk,
				"expected": l[0],
				"meta":     strings.TrimSpace(base + " " + l[1]),
			})
			jsonl.Write(row)
			jsonl.WriteByte('\n')
		}
	}

	_ = os.WriteFile(filepath.Join(work, "review-compact.txt"), []byte(review.String()), 0o644)
	fmt.Printf("chunks: %d\n", len(names))
	if len(labels) > 0 {
		_ = os.WriteFile(filepath.Join(work, "ci-chunks.jsonl"), []byte(jsonl.String()), 0o644)
		labeled := 0
		for _, b := range names {
			if _, ok := labels[b]; ok {
				labeled++
			}
		}
		fmt.Printf("labeled rows written: %d\n", labeled)
	}
}
