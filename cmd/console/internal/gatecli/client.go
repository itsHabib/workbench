// Package gatecli is the console's only data source: it shells the gate binary
// and hands back gate's own JSON projections. The console never reads gate's
// state files or imports its decision code — gate owns the projection, the
// console renders it. That keeps the workbench boundary law (compose through
// artifacts: exit codes + JSON, never a cross-tool import) and means the console
// cannot drift from gate's schema, because it does not parse it.
package gatecli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Runner executes the gate binary with args and returns its stdout, plus an
// error (carrying stderr) on a non-zero exit. It is injectable so the web layer
// is tested without a real gate on PATH.
type Runner func(ctx context.Context, bin string, args ...string) ([]byte, error)

// Client shells one gate binary against one state dir.
type Client struct {
	bin   string
	state string
	run   Runner
}

// New builds a client. A nil run uses the real exec runner.
func New(bin, state string, run Runner) *Client {
	if run == nil {
		run = execRunner
	}
	return &Client{bin: bin, state: state, run: run}
}

// runIDRe bounds what Explain forwards to gate: a run id is run_ + hex. Exec
// args are not shell-interpreted, so this is defense in depth plus a clean
// rejection of a junk path before a subprocess is spawned.
var runIDRe = regexp.MustCompile(`^run_[0-9a-f]+$`)

// args builds gate's argv: the subcommand FIRST (gate dispatches on os.Args[1]),
// then the shared -state flag when set, then the rest. gate parses -state as a
// flag of the subcommand, so it must follow the verb, never precede it.
func (c *Client) args(sub string, rest ...string) []string {
	a := []string{sub}
	if c.state != "" {
		a = append(a, "-state", c.state)
	}
	return append(a, rest...)
}

// Next returns the raw JSON of `gate next -json`.
func (c *Client) Next(ctx context.Context) ([]byte, error) {
	return c.run(ctx, c.bin, c.args("next", "-json")...)
}

// Explain returns the raw JSON of `gate explain -run <id> -json`.
func (c *Client) Explain(ctx context.Context, run string) ([]byte, error) {
	if !runIDRe.MatchString(run) {
		return nil, fmt.Errorf("gatecli: invalid run id %q", run)
	}
	return c.run(ctx, c.bin, c.args("explain", "-run", run, "-json")...)
}

// AuditStatus is the console's read of `gate audit`: the chain is intact, or a
// tamper reason to surface loudly.
type AuditStatus struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// Audit runs `gate audit`. gate prints "chain intact" and exits 0 when clean,
// or prints "TAMPERED: ..." on stdout and exits 4 on a broken chain — so a
// non-zero exit that carries a TAMPERED line is a finding to map, not an
// operational error to propagate. Any other non-zero exit is a real error.
func (c *Client) Audit(ctx context.Context) (AuditStatus, error) {
	out, err := c.run(ctx, c.bin, c.args("audit")...)
	text := strings.TrimSpace(string(out))
	if err == nil {
		return AuditStatus{OK: true, Reason: text}, nil
	}
	if strings.Contains(text, "TAMPERED") {
		return AuditStatus{OK: false, Reason: text}, nil
	}
	return AuditStatus{}, err
}

// execRunner runs the gate binary for real. It returns stdout even on a
// non-zero exit (Audit needs the TAMPERED line the failing run prints there),
// wrapping the exit error with the trimmed stderr for a diagnosable message.
func execRunner(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("gate %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
