package foundation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ExecSpec is one child-process run: stdin from a workspace file, stdout
// committed to a workspace file only if the process succeeds.
type ExecSpec struct {
	Label  string // caller's opaque name, used in errors and fault injection
	Argv   []string
	Stdin  string
	Output string
}

// Exec runs a child process in the workspace directory with a minimal
// environment (PATH and LC_ALL=C). Stdout goes to a temporary file that
// is renamed over Output on success; on failure the previous output, if
// any, is left untouched. The child itself is not sandboxed: it can read
// or write anything its user can. Confinement covers this package's own
// file operations, not arbitrary commands.
func (w *Workspace) Exec(ctx context.Context, s ExecSpec) (FileState, error) {
	if len(s.Argv) == 0 {
		return FileState{}, fmt.Errorf("%s: empty command", s.Label)
	}
	if w.fault.Fail == s.Label {
		return FileState{}, fmt.Errorf("%s: injected failure (WB_FAULT)", s.Label)
	}
	in, err := local(s.Stdin)
	if err != nil {
		return FileState{}, err
	}
	out, err := local(s.Output)
	if err != nil {
		return FileState{}, err
	}
	stdin, err := w.root.Open(in)
	if err != nil {
		return FileState{}, fmt.Errorf("%s: open stdin: %w", s.Label, err)
	}
	defer func() { _ = stdin.Close() }()

	var stderr capped
	err = w.commit(out, func(f *os.File) error {
		cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
		cmd.Dir = w.dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
		cmd.Stdin = stdin
		cmd.Stdout = f
		cmd.Stderr = &stderr
		return cmd.Run()
	})
	if err != nil {
		return FileState{}, fmt.Errorf("%s: %s: %w%s", s.Label, strings.Join(s.Argv, " "), err, stderr.suffix())
	}
	if w.fault.Crash == s.Label {
		crash(s.Label)
	}
	return w.ReadFile(s.Output)
}

// capped keeps the first 2 KiB of a child's stderr for error messages.
type capped struct{ b []byte }

func (c *capped) Write(p []byte) (int, error) {
	room := 2048 - len(c.b)
	if room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (c *capped) suffix() string {
	s := strings.TrimSpace(string(c.b))
	if s == "" {
		return ""
	}
	return ": " + s
}
