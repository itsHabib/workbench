package main

import (
	"strings"
	"testing"
)

// replayPolicyPath points at the phase-2 replay gate's authored policy
// (internal/replay/testdata/dispatch-policy.json) — the same policy §5 of the
// TDD references as the shipped example. It deliberately has no catch-all
// rule (internal/replay's negative control depends on that), so `validate`
// is expected to succeed with a warning: exit 1, not exit 0.
const replayPolicyPath = "internal/replay/testdata/dispatch-policy.json"

// TestReplayPolicyValidates asserts the phase-2 gate's authored policy passes
// dispatch validate end-to-end through the real CLI path (run(), the same
// entry point os.Exit(run(...)) uses) — not just policy.Load in isolation.
// The TDD's phase-2 bar (§11) requires the policy to pass fail-closed
// validation; "valid, with the expected no-catch-all warning" is exit 1.
func TestReplayPolicyValidates(t *testing.T) {
	code, out, errb := invoke("", "validate", "--policy", replayPolicyPath)
	if code != 0 && code != 1 {
		t.Fatalf("dispatch validate --policy %s: exit = %d, want 0 or 1 (stdout=%q stderr=%q)", replayPolicyPath, code, out, errb)
	}
	if !strings.Contains(out, `"valid":true`) {
		t.Fatalf("validate stdout must report valid:true, got %q", out)
	}
	if code == 1 && !strings.Contains(errb, "no catch-all") {
		t.Fatalf("exit 1 must carry the no-catch-all warning, got stderr=%q", errb)
	}
}
