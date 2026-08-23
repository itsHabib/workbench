// Package ingest is the resolution back-channel's only mechanism: it validates
// the decision a notification carried back for a parked escalation and drives
// gate's `resolve` verb to record it. It composes through gate's CLI seam —
// shelling the gate binary and returning gate's own JSON outcome + exit code —
// exactly as the console composes through gate's read seam. It never imports
// gate's decision code and never writes into gate's log: only gate writes into
// gate's log, so the EFFECT runs in gate; this component owns the INGEST.
//
// That split is the whole point of the Escalation plane's return path. flare
// routes an escalation out (Observability) and is forbidden from writing
// decisions (Amendment 3); the decision that comes back needs a home that is
// NOT the router. This is that home — a separate component, so a notification's
// acknowledgement closes the agent→human→agent loop without flare ever touching
// a decision.
package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/contracts/escalation"
)

// Runner executes the gate binary with args and returns its stdout, gate's exit
// code, and an error only when the binary could not be run at all. A non-zero
// gate exit is NOT an error here — 0/1/2/3/4 is gate's decision contract, which
// this back-channel passes through faithfully rather than reinterpreting.
// Injectable so the ingest policy is tested without a real gate on PATH.
type Runner func(ctx context.Context, bin string, args ...string) ([]byte, int, error)

// Decision is the acknowledgement a notification carries back for a parked
// escalation: the escalation id a human acted on, the pass/block they chose,
// who they are, their one-line reasoning, and the grant the resolution runs
// under. It is the shape the back-channel ingests — from a Slack action, a
// webhook POST, a future gate UI — before driving gate's resolution seam. It is
// deliberately transport-agnostic: the POC fills it from CLI flags.
type Decision struct {
	Escalation string
	Verdict    string // pass | block
	Who        string
	// Method is the CHANNEL the decision arrived through — the transport's own
	// account of how it established Who, which gate records on the judgment
	// alongside the identity. Who alone cannot separate a verified Slack callback
	// from a string typed at a shell by an agent that also had one, and it is
	// exactly that distinction a separation-of-duties question asks about. The
	// transport is the only layer that knows, so it is the layer that says.
	Method string
	Why    string
	Grant  string
}

// Client is the resolution back-channel over one gate binary and state dir. Its
// zero value is not useful (it needs a binary); construct it with New.
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

// escIDRe bounds what Resolve forwards to gate: an escalation id is esc_ + hex.
// Exec args are not shell-interpreted, so this is defense in depth plus a clean
// rejection of a junk id before a subprocess is spawned.
var escIDRe = regexp.MustCompile(`^esc_[0-9a-f]+$`)

// Resolve validates the ingested decision and drives `gate resolve`, returning
// gate's own JSON outcome and exit code. This is the whole back-channel: a
// notification carried an id a human acted on; this turns that acknowledgement
// into gate's resolution seam. Validation is policy (a mistyped id or an
// out-of-vocabulary verdict never reaches a subprocess); shelling gate is
// mechanism. The loop closes — a judgment recorded, the resolution stamped —
// without this component ever writing a decision itself.
func (c *Client) Resolve(ctx context.Context, d Decision) ([]byte, int, error) {
	if err := validate(d); err != nil {
		return nil, 0, err
	}
	return c.run(ctx, c.bin, c.args(d)...)
}

// validate enforces the ingest law: a well-formed escalation id, a decision in
// gate's pass/block vocabulary (shared via the contract, not re-spelled here),
// and the provenance the resolution stamp requires. It is the policy layer; a
// caller that skips it would let a junk decision reach gate.
func validate(d Decision) error {
	if !escIDRe.MatchString(d.Escalation) {
		return fmt.Errorf("ingest: invalid escalation id %q", d.Escalation)
	}
	if d.Verdict != escalation.DecisionPass && d.Verdict != escalation.DecisionBlock {
		return fmt.Errorf("ingest: decision %q must be %q or %q", d.Verdict, escalation.DecisionPass, escalation.DecisionBlock)
	}
	if d.Grant == "" {
		return fmt.Errorf("ingest: grant is required")
	}
	if d.Who == "" {
		return fmt.Errorf("ingest: who is required (the judgment records who decided)")
	}
	if d.Method == "" {
		return fmt.Errorf("ingest: method is required (the judgment records how the decider was established)")
	}
	if d.Why == "" {
		return fmt.Errorf("ingest: why is required")
	}
	return nil
}

// args builds gate's argv: the resolve subcommand FIRST (gate dispatches on
// os.Args[1]), then the shared -state flag when set, then the decision. gate
// parses -state as a flag of the subcommand, so it must follow the verb.
func (c *Client) args(d Decision) []string {
	a := []string{"resolve"}
	if c.state != "" {
		a = append(a, "-state", c.state)
	}
	return append(a,
		"-escalation", d.Escalation,
		"-grant", d.Grant,
		"-decision", d.Verdict,
		"-why", d.Why,
		"-who", d.Who,
		"-method", d.Method,
	)
}

// execRunner runs the gate binary for real. A non-zero exit is gate's decision
// contract, so its code is returned (never an error); only a failure to START
// the binary is an error. gate's stderr streams straight through to escalate's
// stderr, so a hard error (exit 4 — e.g. a well-formed but nonexistent
// escalation id) still shows the operator gate's own diagnostic instead of a
// silent nonzero exit with empty stdout. gate's JSON outcome is captured on
// stdout so the caller can re-emit it.
func execRunner(ctx context.Context, bin string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return stdout.Bytes(), ee.ExitCode(), nil
	}
	return stdout.Bytes(), 0, fmt.Errorf("escalate: run gate %s: %w", strings.Join(args, " "), err)
}
