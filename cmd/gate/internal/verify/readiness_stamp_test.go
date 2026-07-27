package verify

import (
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/stamp"
)

// TestReadinessAuthorizedContextMatchesStamp pins readiness's knowledge of the
// authorization-stamp context to the value stamp actually posts, so the two can
// never drift: if the stamp context changes and this constant does not,
// readiness would start counting the stamp as real CI again.
func TestReadinessAuthorizedContextMatchesStamp(t *testing.T) {
	if authorizedContext != stamp.Context {
		t.Fatalf("readiness authorizedContext %q must equal stamp.Context %q, or the stamp re-enters readiness evidence",
			authorizedContext, stamp.Context)
	}
}

// TestReadinessExcludesAuthorizedStamp proves gate's own gate/authorized stamp
// is not independent CI: a rollup whose only entry is the stamp must escalate
// (no real CI recorded), never pass — otherwise a judge/resolve pass that
// stamped a head with no CI would let a later run wave it through. And the stamp
// (always success) never blocks.
func TestReadinessExcludesAuthorizedStamp(t *testing.T) {
	// Stamp-only rollup → escalate, exactly as a gate-only rollup does.
	v := readinessFor(t, map[string]any{
		"state": "OPEN", "mergeable": "MERGEABLE", "reviewDecision": "APPROVED",
		"statusCheckRollup": []map[string]any{
			{"context": stamp.Context, "state": "SUCCESS"},
		},
	})
	if v.Decision != DecisionEscalate {
		t.Fatalf("a stamp-only rollup must escalate (no real CI), got %s (%s)", v.Decision, v.Why)
	}

	// With real CI present, the stamp neither blocks nor is needed: readiness
	// passes on the real check alone, the stamp ignored.
	v = readinessFor(t, map[string]any{
		"state": "OPEN", "mergeable": "MERGEABLE", "reviewDecision": "APPROVED",
		"statusCheckRollup": []map[string]any{
			{"name": "ci", "conclusion": "SUCCESS"},
			{"context": stamp.Context, "state": "SUCCESS"},
		},
	})
	if v.Decision != DecisionPass {
		t.Fatalf("real CI green + stamp must pass, got %s (%s)", v.Decision, v.Why)
	}
}
