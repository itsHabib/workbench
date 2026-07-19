package floor

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ParseUnifiedDiff parses `git`/`gh pr diff` output into a Diff.
// It tracks the current file via the "+++ b/<path>" header and collects
// added ('+') and removed ('-') line bodies. It also keeps the ordered
// line stream (context included, hunk boundaries marked) — section-scoped
// signals like the dev-dependency discriminator need to know which section
// a changed line sits in, and only the ordered stream carries that.
//
// An empty input (no files / no hunks) and a scanner error (including a line
// over the 16 MiB buffer cap) both return an error — callers must fail closed
// rather than classify a missing or truncated diff as T0.
//
//nolint:gocognit,cyclop // see floor.Classify — same deferral.
func ParseUnifiedDiff(r io.Reader) (Diff, error) {
	var d Diff
	var cur *FileChange
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)

	flush := func() {
		if cur != nil {
			d.Files = append(d.Files, *cur)
			cur = nil
		}
	}

	// newFileMode records the "new file mode" header; New is only trusted once the
	// "--- /dev/null" old-side header confirms it. A diff carrying "new file mode"
	// alongside a real old path (--- a/<path>) is an edit, not a creation — the
	// migration grader must not treat it as a fresh file (adversarial: spoofed New
	// downgrades an edit to an applied migration).
	newFileMode := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			newFileMode = false
			// seed both sides from the header; the +++/--- lines refine them. Both
			// are classified (union) so a rename to a benign path can't shed the
			// signals its old path carried.
			oldPath, newPath := gitHeaderPaths(line)
			cur = &FileChange{Path: newPath, OldPath: oldPath}
		case strings.HasPrefix(line, "new file mode "):
			newFileMode = true
		case strings.HasPrefix(line, "rename to "):
			if cur != nil {
				cur.Path = stripAB(strings.TrimPrefix(line, "rename to "))
			}
		case strings.HasPrefix(line, "rename from "):
			if cur != nil {
				cur.OldPath = stripAB(strings.TrimPrefix(line, "rename from "))
			}
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p != "/dev/null" && cur != nil {
				cur.Path = stripAB(p)
			}
		case strings.HasPrefix(line, "--- "):
			// old-path header. "--- /dev/null" is the only proof of a real creation.
			p := strings.TrimPrefix(line, "--- ")
			if cur != nil {
				if p == "/dev/null" {
					cur.New = newFileMode
				} else {
					cur.OldPath = stripAB(p)
				}
			}
		case strings.HasPrefix(line, "@@"):
			if cur != nil {
				cur.Lines = append(cur.Lines, DiffLine{Op: OpHunk})
			}
		case strings.HasPrefix(line, "+"):
			if cur != nil {
				body := strings.TrimPrefix(line, "+")
				cur.Added = append(cur.Added, body)
				cur.Lines = append(cur.Lines, DiffLine{Op: OpAdd, Body: body})
			}
		case strings.HasPrefix(line, "-"):
			if cur != nil {
				body := strings.TrimPrefix(line, "-")
				cur.Removed = append(cur.Removed, body)
				cur.Lines = append(cur.Lines, DiffLine{Op: OpDel, Body: body})
			}
		case strings.HasPrefix(line, " "):
			if cur != nil {
				cur.Lines = append(cur.Lines, DiffLine{Op: OpCtx, Body: line[1:]})
			}
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return Diff{}, fmt.Errorf("scanning unified diff: %w", err)
	}
	if len(d.Files) == 0 {
		return Diff{}, fmt.Errorf("empty diff")
	}
	return d, nil
}

// gitHeaderPaths pulls the a/ (old) and b/ (new) paths from a
// "diff --git a/x b/y" line. Both are returned so path-keyed signals can be
// checked against the union (a rename can't launder a sensitive old path).
func gitHeaderPaths(line string) (oldPath, newPath string) {
	parts := strings.Fields(line)
	if len(parts) >= 4 {
		return stripAB(parts[len(parts)-2]), stripAB(parts[len(parts)-1])
	}
	if len(parts) >= 3 {
		return "", stripAB(parts[len(parts)-1])
	}
	return "", ""
}

// stripAB removes a leading a/ or b/ and any timestamp suffix.
func stripAB(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}
