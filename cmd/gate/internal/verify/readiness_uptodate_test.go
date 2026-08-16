package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// behindView is an otherwise-perfect PR — open, no conflicts, approved, green
// CI — that GitHub reports as BEHIND its base. Every case below varies only the
// protection evidence, so the up-to-date rule is the only thing under test.
func behindView() map[string]any {
	return map[string]any{
		"state": "OPEN", "mergeable": "MERGEABLE", "mergeStateStatus": "BEHIND",
		"baseRefName": "main", "reviewDecision": "APPROVED",
		"statusCheckRollup": []map[string]any{{"name": "ci", "conclusion": "SUCCESS"}},
	}
}

// readinessWithProtection drives readiness with both view and base-protection
// evidence recorded, the way a real gate run does.
func readinessWithProtection(t *testing.T, view, protection map[string]any) Verdict {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": view})
	if err != nil {
		t.Fatal(err)
	}
	prot, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"protection": protection})
	if err != nil {
		t.Fatal(err)
	}
	art, _, err := Readiness(st, "run_t", evd.ID, "", prot.ID, subj, false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// A behind branch on a base that provably requires up-to-date branches is the
// friction case: gate used to emit a `gh pr merge` GitHub then rejected. It
// must block instead, and the block must carry the full refresh sequence —
// refresh, CI, re-gate — because refreshing moves the head and invalidates both
// the CI gate read and the pinned --match-head-commit.
func TestReadinessBehindWithStrictProtectionBlocks(t *testing.T) {
	v := readinessWithProtection(t, behindView(), map[string]any{
		"base": "main", "require_up_to_date": true, "known": true,
	})
	if v.Decision != DecisionBlock {
		t.Fatalf("behind + required-up-to-date must block, got %s (%s)", v.Decision, v.Why)
	}
	for _, want := range []string{"behind main", "up to date", upToDateRefresh} {
		if !strings.Contains(v.Why, want) {
			t.Errorf("block must prescribe the refresh sequence; %q missing from %q", want, v.Why)
		}
	}
}

// The load-bearing negative: BEHIND alone is not a merge obstacle. A repo that
// proves it does NOT require up-to-date branches merges a behind PR happily,
// and blocking there would invent a refresh cycle on every run.
func TestReadinessBehindWithoutRequirementPasses(t *testing.T) {
	v := readinessWithProtection(t, behindView(), map[string]any{
		"base": "main", "require_up_to_date": false, "known": true,
	})
	if v.Decision != DecisionPass {
		t.Fatalf("behind with a proven-absent requirement must pass, got %s (%s)", v.Decision, v.Why)
	}
}

// Unreadable protection proves nothing in either direction — GitHub answers 404
// both for an unprotected branch and for a token without admin. Neither pass
// (which may emit a rejected merge command) nor block (a refresh cycle per run
// everywhere) is honest, so readiness escalates: gate's vocabulary for "cannot
// verify", which a judge can settle.
func TestReadinessBehindWithUnknownProtectionEscalates(t *testing.T) {
	v := readinessWithProtection(t, behindView(), map[string]any{
		"base": "main", "known": false, "reason": "branch protection not readable",
	})
	if v.Decision != DecisionEscalate {
		t.Fatalf("behind with unprovable protection must escalate, got %s (%s)", v.Decision, v.Why)
	}
	for _, want := range []string{"behind main", "not readable", upToDateRefresh} {
		if !strings.Contains(v.Why, want) {
			t.Errorf("escalation must name the gap and the fix; %q missing from %q", want, v.Why)
		}
	}
}

// A branch that is current is unaffected no matter what protection says — the
// rule keys on BEHIND, not on the existence of a requirement.
func TestReadinessCurrentBranchUnaffectedByStrictProtection(t *testing.T) {
	view := behindView()
	view["mergeStateStatus"] = "CLEAN"
	v := readinessWithProtection(t, view, map[string]any{
		"base": "main", "require_up_to_date": true, "known": true,
	})
	if v.Decision != DecisionPass {
		t.Fatalf("a current branch must pass under strict protection, got %s (%s)", v.Decision, v.Why)
	}
}

// A MERGED subject has no live merge state; gating one (backtest) judges its
// recorded evidence. Same exemption the mergeability block takes.
func TestReadinessMergedSubjectExemptFromUpToDate(t *testing.T) {
	view := behindView()
	view["state"] = "MERGED"
	v := readinessWithProtection(t, view, map[string]any{
		"base": "main", "require_up_to_date": true, "known": true,
	})
	if v.Decision != DecisionPass {
		t.Fatalf("merged subject must stay evaluable, got %s (%s)", v.Decision, v.Why)
	}
}

// Callers that record no protection evidence must behave exactly as they did
// before this existed — a behind branch is unproven, not blocked.
func TestReadinessNoProtectionEvidenceDoesNotBlock(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": behindView()})
	if err != nil {
		t.Fatal(err)
	}
	art, _, err := Readiness(st, "run_t", evd.ID, "", "", subj, false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision == DecisionBlock {
		t.Fatalf("absent protection evidence must not block, got %s (%s)", v.Decision, v.Why)
	}
}

// Protection evidence that exists but cannot be parsed is an infrastructure
// failure, and must never read as "no requirement" — that would silently
// downgrade a block to a pass on the one fact deciding whether the emitted
// merge command can land.
func TestReadinessMalformedProtectionEvidenceErrors(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": behindView()})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"unrelated": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Readiness(st, "run_t", evd.ID, "", bad.ID, subj, false); err == nil {
		t.Fatal("protection evidence with no protection field must error, not read as no-requirement")
	}
}

// The verdict must name the protection artifact it judged, so provenance
// traversal can reach what readiness actually read.
func TestReadinessRecordsProtectionParent(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": behindView()})
	if err != nil {
		t.Fatal(err)
	}
	prot, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{
		"protection": map[string]any{"base": "main", "require_up_to_date": true, "known": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	art, _, err := Readiness(st, "run_t", evd.ID, "", prot.ID, subj, false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range art.Parents {
		if p == prot.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("readiness verdict must parent the protection evidence it judged, got %v", art.Parents)
	}
}
