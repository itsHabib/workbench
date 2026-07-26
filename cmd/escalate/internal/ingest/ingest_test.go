package ingest

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/escalation"
)

// fakeRunner records the argv it was handed and returns a canned result, so the
// ingest policy is exercised without a real gate binary.
type fakeRunner struct {
	gotBin  string
	gotArgs []string
	out     []byte
	code    int
}

func (f *fakeRunner) run(_ context.Context, bin string, args ...string) ([]byte, int, error) {
	f.gotBin = bin
	f.gotArgs = args
	return f.out, f.code, nil
}

func validDecision() Decision {
	return Decision{
		Escalation: "esc_0123456789abcdef",
		Verdict:    escalation.DecisionPass,
		Who:        "operator",
		Why:        "the retry has a ceiling; safe to land",
		Grant:      "grt_00000000",
	}
}

// TestResolveBuildsGateArgv pins the seam: a valid decision shells the gate
// binary with the resolve verb, the state flag after the verb, and every field
// mapped to its gate flag. This is the contract the back-channel composes on.
func TestResolveBuildsGateArgv(t *testing.T) {
	fr := &fakeRunner{out: []byte(`{"outcome":"would_merge"}`), code: 0}
	c := New("gate", "/s/state", fr.run)

	out, code, err := c.Resolve(context.Background(), validDecision())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if code != 0 || string(out) != `{"outcome":"would_merge"}` {
		t.Fatalf("gate outcome must pass through verbatim: code %d out %s", code, out)
	}
	if fr.gotBin != "gate" {
		t.Fatalf("bin = %q, want gate", fr.gotBin)
	}
	want := []string{
		"resolve", "-state", "/s/state",
		"-escalation", "esc_0123456789abcdef",
		"-grant", "grt_00000000",
		"-decision", "pass",
		"-why", "the retry has a ceiling; safe to land",
		"-who", "operator",
	}
	if !reflect.DeepEqual(fr.gotArgs, want) {
		t.Fatalf("argv mismatch:\n got=%v\nwant=%v", fr.gotArgs, want)
	}
}

// TestResolvePassesThroughExitCode pins that gate's non-zero decision codes are
// surfaced, not swallowed: a re-park (exit 2) is a valid outcome the caller
// must see, not an error the back-channel hides.
func TestResolvePassesThroughExitCode(t *testing.T) {
	fr := &fakeRunner{out: []byte(`{"outcome":"parked_for_judgment"}`), code: 2}
	c := New("gate", "", fr.run)
	_, code, err := c.Resolve(context.Background(), validDecision())
	if err != nil {
		t.Fatalf("a non-zero gate exit is a decision, not an error: %v", err)
	}
	if code != 2 {
		t.Fatalf("exit code must pass through, got %d", code)
	}
	// No -state in the argv when the client carries none.
	if contains(fr.gotArgs, "-state") {
		t.Fatalf("no -state flag expected when state is empty, got %v", fr.gotArgs)
	}
}

// TestResolveRejectsBadInput pins the policy layer: a junk escalation id, an
// out-of-vocabulary decision, and missing provenance are all rejected BEFORE a
// subprocess is spawned, so gate never sees a malformed resolution.
func TestResolveRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Decision)
		want string
	}{
		{"bad escalation id", func(d *Decision) { d.Escalation = "run_123" }, "invalid escalation id"},
		{"empty escalation id", func(d *Decision) { d.Escalation = "" }, "invalid escalation id"},
		{"bad decision", func(d *Decision) { d.Verdict = "maybe" }, "must be"},
		{"missing grant", func(d *Decision) { d.Grant = "" }, "grant is required"},
		{"missing who", func(d *Decision) { d.Who = "" }, "who is required"},
		{"missing why", func(d *Decision) { d.Why = "" }, "why is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{}
			c := New("gate", "", fr.run)
			d := validDecision()
			tc.mut(&d)
			_, _, err := c.Resolve(context.Background(), d)
			if err == nil {
				t.Fatalf("%s must be rejected", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must mention %q", err, tc.want)
			}
			if fr.gotArgs != nil {
				t.Fatalf("gate must not be shelled on a rejected decision, got argv %v", fr.gotArgs)
			}
		})
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
