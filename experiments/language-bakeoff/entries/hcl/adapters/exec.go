package adapters

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
)

// maxStderr bounds the stderr kept for error messages.
const maxStderr = 2048

// Exec is a task: run one child process with a declared stdin file and
// capture its stdout into a declared output file. It owns that output.
//
// The child runs in the workspace root with only PATH and LC_ALL=C in its
// environment, so the environment is not a hidden input. Output is written
// to a temp file and renamed into place only on success: a failed run never
// leaves a torn output behind.
type Exec struct{}

// Type implements adapter.Adapter.
func (Exec) Type() string { return "exec" }

// Kind implements adapter.Adapter.
func (Exec) Kind() adapter.Kind { return adapter.Task }

// Fields implements adapter.Adapter.
func (Exec) Fields() []adapter.Field {
	return []adapter.Field{
		{Name: "command", Type: adapter.StringList, Required: true},
		{Name: "stdin", Type: adapter.String},
		{Name: "stdout", Type: adapter.String, Required: true},
	}
}

// Resolve implements adapter.Adapter.
func (Exec) Resolve(cfg adapter.Values) (adapter.Values, error) {
	argv := cfg.List("command")
	if len(argv) == 0 || argv[0] == "" {
		return nil, &adapter.FieldError{Field: "command", Msg: "must name a program as its first element"}
	}
	out, err := adapter.CleanPath(cfg.Str("stdout"))
	if err != nil {
		return nil, &adapter.FieldError{Field: "stdout", Msg: err.Error()}
	}
	v := adapter.Values{"command": argv, "stdout": out}
	if cfg.Str("stdin") == "" {
		return v, nil
	}
	in, err := adapter.CleanPath(cfg.Str("stdin"))
	if err != nil {
		return nil, &adapter.FieldError{Field: "stdin", Msg: err.Error()}
	}
	v["stdin"] = in
	return v, nil
}

// Paths implements adapter.Adapter.
func (Exec) Paths(v adapter.Values) adapter.Paths {
	p := adapter.Paths{Owns: []string{v.Str("stdout")}}
	if in := v.Str("stdin"); in != "" {
		p.Reads = []string{in}
	}
	return p
}

// Run implements adapter.TaskAdapter.
func (Exec) Run(ctx context.Context, ws adapter.Workspace, v adapter.Values) error {
	outAbs, err := ws.Abs(v.Str("stdout"))
	if err != nil {
		return err
	}
	tmp, err := createTemp(outAbs, v.Str("stdout"))
	if err != nil {
		return err
	}
	defer removeTemp(tmp)

	argv := v.List("command")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = ws.Root()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
	cmd.Stdout = tmp
	stderr := &capped{max: maxStderr}
	cmd.Stderr = stderr
	stdin, err := openStdin(ws, v.Str("stdin"))
	if err != nil {
		_ = tmp.Close() // the open error is the one to report
		return err
	}
	if stdin != nil {
		defer func() { _ = stdin.Close() }() // read-only
		cmd.Stdin = stdin
	}
	if err := cmd.Run(); err != nil {
		_ = tmp.Close() // discarded: the output is never renamed into place
		return fmt.Errorf("%s: %w%s", argv[0], err, stderr.suffix())
	}
	return adapter.RelError(v.Str("stdout"), commit(tmp, outAbs, existingMode(outAbs)))
}

func openStdin(ws adapter.Workspace, rel string) (*os.File, error) {
	if rel == "" {
		return nil, nil
	}
	abs, err := ws.Abs(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, adapter.RelError(rel, err)
	}
	return f, nil
}

// capped keeps the first max bytes written to it.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	room := c.max - c.buf.Len()
	if room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *capped) suffix() string {
	s := strings.TrimSpace(c.buf.String())
	if s == "" {
		return ""
	}
	return ": " + s
}
