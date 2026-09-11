// Package fleetcli reads Fleet projections through bounded CLI calls. It owns no state.
package fleetcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Runner executes one CLI with optional stdin and an explicit Fleet state root.
type Runner func(context.Context, string, string, []byte, ...string) ([]byte, error)

// Client composes Fleet evidence and TraceLens diagnostics without importing either tool.
type Client struct {
	bin, lens, state string
	run              Runner
}

// New configures the binaries and Fleet root. Empty root inherits Fleet's environment.
func New(bin, lens, state string, run Runner) *Client {
	if run == nil {
		run = execute
	}
	return &Client{bin: bin, lens: lens, state: state, run: run}
}

// State returns the configured state root, without touching it.
func (c *Client) State() string { return c.state }

var addressPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:@/-]{0,199}$`)

// Read invokes one of the fixed read-only projections, never an arbitrary CLI verb.
func (c *Client) Read(ctx context.Context, view, address string) ([]byte, error) {
	args := []string{"status", "--all", "--json"}
	switch view {
	case "status":
	case "report":
		args = []string{"run-report", "--since", "24h", "--json"}
	case "inspect", "trace":
		if !addressPattern.MatchString(address) {
			return nil, fmt.Errorf("invalid Fleet address")
		}
		args = []string{view, address, "--json"}
	default:
		return nil, fmt.Errorf("unknown Fleet projection")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := c.run(ctx, c.bin, c.state, nil, args...)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("Fleet returned invalid JSON")
	}
	return raw, nil
}

// Diagnose analyzes only the trace resolved by Fleet for a registered address.
// The browser cannot pass a path or executable, and no output is written to disk.
func (c *Client) Diagnose(ctx context.Context, address string) ([]byte, error) {
	raw, err := c.Read(ctx, "trace", address)
	if err != nil {
		return nil, err
	}
	var trace struct {
		Data        string `json:"data"`
		Source      string `json:"source"`
		Partial     bool   `json:"partial"`
		Coverage    string `json:"coverage"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(raw, &trace); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	verdict, err := c.run(ctx, c.lens, "", []byte(trace.Data), "-json", "-dialect", "auto")
	if err != nil {
		return nil, fmt.Errorf("trace diagnostics unavailable: %w", err)
	}
	if !json.Valid(verdict) {
		return nil, fmt.Errorf("TraceLens returned invalid JSON")
	}
	return json.Marshal(map[string]any{"verdict": json.RawMessage(verdict), "source": trace.Source, "fingerprint": trace.Fingerprint, "partial": trace.Partial, "coverage": trace.Coverage, "note": "Diagnostics describe retained trace evidence, not verified task completion."})
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, fmt.Errorf("CLI output exceeds 8 MiB")
	}
	return b.Buffer.Write(p)
}

func execute(ctx context.Context, bin, state string, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = os.Environ()
	if state != "" {
		env := []string{}
		for _, e := range cmd.Env {
			if !strings.HasPrefix(e, "FLEET_STATE=") {
				env = append(env, e)
			}
		}
		cmd.Env = append(env, "FLEET_STATE="+state)
	}
	cmd.Stdin = bytes.NewReader(input)
	var out, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}
