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
// An input with no parseable file headers (empty or unrecognized bytes) and a
// scanner error (including a line over the 16 MiB buffer cap) both return an
// error — callers must fail closed rather than classify a missing or truncated
// diff as T0. A valid diff whose files carry no hunks (mode-only, rename-only,
// binary) is real input, not an operational failure — it parses and classifies.
func ParseUnifiedDiff(r io.Reader) (Diff, error) {
	var p diffParser
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		p.handle(sc.Text())
	}
	p.flush()
	if err := sc.Err(); err != nil {
		return Diff{}, fmt.Errorf("scanning unified diff: %w", err)
	}
	if len(p.d.Files) == 0 {
		return Diff{}, fmt.Errorf("no file headers parsed: empty or unrecognized diff input")
	}
	return p.d, nil
}

// diffParser accumulates FileChanges while scanning a unified diff.
type diffParser struct {
	d           Diff
	cur         *FileChange
	newFileMode bool // "new file mode" seen; New trusted only with --- /dev/null
}

func (p *diffParser) flush() {
	if p.cur == nil {
		return
	}
	p.d.Files = append(p.d.Files, *p.cur)
	p.cur = nil
}

func (p *diffParser) handle(line string) {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.startFile(line)
	case strings.HasPrefix(line, "new file mode "):
		p.newFileMode = true
	case strings.HasPrefix(line, "rename to "):
		p.setRenameTo(line)
	case strings.HasPrefix(line, "rename from "):
		p.setRenameFrom(line)
	case strings.HasPrefix(line, "+++ "):
		p.setNewPath(line)
	case strings.HasPrefix(line, "--- "):
		p.setOldPath(line)
	case strings.HasPrefix(line, "@@"):
		p.markHunk()
	case strings.HasPrefix(line, "+"):
		p.addBody(OpAdd, strings.TrimPrefix(line, "+"))
	case strings.HasPrefix(line, "-"):
		p.addBody(OpDel, strings.TrimPrefix(line, "-"))
	case strings.HasPrefix(line, " "):
		p.addBody(OpCtx, line[1:])
	}
}

func (p *diffParser) startFile(line string) {
	p.flush()
	p.newFileMode = false
	// seed both sides from the header; the +++/--- lines refine them. Both
	// are classified (union) so a rename to a benign path can't shed the
	// signals its old path carried.
	oldPath, newPath := gitHeaderPaths(line)
	p.cur = &FileChange{Path: newPath, OldPath: oldPath}
}

func (p *diffParser) setRenameTo(line string) {
	if p.cur == nil {
		return
	}
	p.cur.Path = stripAB(strings.TrimPrefix(line, "rename to "))
}

func (p *diffParser) setRenameFrom(line string) {
	if p.cur == nil {
		return
	}
	p.cur.OldPath = stripAB(strings.TrimPrefix(line, "rename from "))
}

func (p *diffParser) setNewPath(line string) {
	path := strings.TrimPrefix(line, "+++ ")
	if path == "/dev/null" || p.cur == nil {
		return
	}
	p.cur.Path = stripAB(path)
}

// setOldPath records the old-path header. "--- /dev/null" is the only proof of
// a real creation — "new file mode" alone alongside a real old path is an edit.
func (p *diffParser) setOldPath(line string) {
	if p.cur == nil {
		return
	}
	path := strings.TrimPrefix(line, "--- ")
	if path == "/dev/null" {
		p.cur.New = p.newFileMode
		return
	}
	p.cur.OldPath = stripAB(path)
}

func (p *diffParser) markHunk() {
	if p.cur == nil {
		return
	}
	p.cur.Lines = append(p.cur.Lines, DiffLine{Op: OpHunk})
}

func (p *diffParser) addBody(op LineOp, body string) {
	if p.cur == nil {
		return
	}
	switch op {
	case OpAdd:
		p.cur.Added = append(p.cur.Added, body)
	case OpDel:
		p.cur.Removed = append(p.cur.Removed, body)
	}
	p.cur.Lines = append(p.cur.Lines, DiffLine{Op: op, Body: body})
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
