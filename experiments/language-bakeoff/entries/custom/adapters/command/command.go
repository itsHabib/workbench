// Package command provides "run command": one-shot work that runs a
// program with an optional workspace file on stdin and captures its stdout
// into a file the declaration owns.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

// Adapter is the command kind.
type Adapter struct{}

// Schema declares the command kind's attributes. Paths are workspace
// relative; cwd only sets the child's working directory.
func (Adapter) Schema() kind.Schema {
	return kind.Schema{
		Kind: "command",
		Fields: []kind.Field{
			{Name: "argv", Type: kind.List, Required: true},
			{Name: "stdin", Type: kind.Path},
			{Name: "cwd", Type: kind.Path},
			{Name: "stdout", Type: kind.OwnedPath, Required: true},
		},
		Exports: []string{"stdout"},
	}
}

// Observe digests the owned output as it is now.
func (Adapter) Observe(ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	return kind.ObservePath(ws, d.One("stdout"))
}

// Run starts argv as a child process. Stdout goes to a temporary file that
// replaces the owned output only on success, so a failed or interrupted run
// leaves the previous output in place. The child inherits wb's environment.
func (Adapter) Run(ctx context.Context, ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	argv := d.All("argv")
	if len(argv) == 0 {
		return kind.Fact{}, errors.New("argv is empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	dir, err := workdir(ws, d.One("cwd"))
	if err != nil {
		return kind.Fact{}, err
	}
	cmd.Dir = dir
	if in := d.One("stdin"); in != "" {
		f, err := ws.Root().Open(in)
		if err != nil {
			return kind.Fact{}, fmt.Errorf("stdin: %w", err)
		}
		defer f.Close()
		cmd.Stdin = f
	}
	out := d.One("stdout")
	f, tmp, err := ws.CreateTemp(out)
	if err != nil {
		return kind.Fact{}, fmt.Errorf("stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = f, &stderr
	if err := finish(cmd.Run(), f); err != nil {
		ws.Discard(tmp)
		return kind.Fact{}, failure(argv[0], err, stderr.String())
	}
	if err := ws.Commit(tmp, out); err != nil {
		return kind.Fact{}, fmt.Errorf("stdout: %w", err)
	}
	return kind.ObservePath(ws, out)
}

// finish flushes and closes the captured output, keeping the first error.
func finish(runErr error, f *os.File) error {
	if runErr != nil {
		f.Close()
		return runErr
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// workdir resolves cwd through the workspace root, so it cannot name a
// directory outside the workspace, even through a symlink.
func workdir(ws *foundation.Workspace, cwd string) (string, error) {
	if cwd == "" {
		return ws.Dir(), nil
	}
	info, err := ws.Root().Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd: %s is not a directory", cwd)
	}
	return filepath.Join(ws.Dir(), cwd), nil
}

func failure(program string, err error, stderr string) error {
	var exit *exec.ExitError
	msg := err.Error()
	if errors.As(err, &exit) && exit.Exited() {
		msg = fmt.Sprintf("exit status %d", exit.ExitCode())
	}
	if last := lastLine(stderr); last != "" {
		msg += ": " + last
	}
	return fmt.Errorf("%s: %s", program, msg)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
