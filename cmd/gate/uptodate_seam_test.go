package main

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// The up-to-date preflight spans two packages that never import each other:
// evidence.Protection writes the branch-protection read, verify.UpToDate reads
// it back, and the only thing joining them is the JSON body of one artifact
// (evidence.ProtectionBody on the way out, verify's unexported mirror on the
// way in — see the note on that type). Each side is thoroughly tested against
// hand-written JSON, which is exactly why neither notices when the two stop
// agreeing: rename ProtectionBody.Strict's tag and every package suite in
// cmd/gate still passes while the rung silently degrades to "no requirement"
// and passes a BEHIND head on a strict base to a merge GitHub will refuse.
//
// A fail-open that no test can see is the failure this rung exists to prevent,
// so the seam gets its own test where the two packages are actually composed —
// gateRun records the protection read and hands its id straight to the rung.
//
// These tests deliberately construct ProtectionBody through the evidence
// package's exported surface rather than a literal map: a map would re-state
// the tag names and pin nothing.

// seamStore opens throwaway state and records the PR view the rung reads
// alongside its protection evidence.
func seamStore(t *testing.T, mergeState string) (*state.Store, string) {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	view, err := st.Append(state.KindEvidence, "run_seam", nil, map[string]any{
		"data": map[string]any{
			"state":            "OPEN",
			"mergeStateStatus": mergeState,
			"baseRefName":      "main",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, view.ID
}

// upToDateOverRecordedProtection records body exactly as evidence.Protection
// would and drives the rung over it, returning the composed verdict.
func upToDateOverRecordedProtection(t *testing.T, mergeState string, body evidence.ProtectionBody) verify.Verdict {
	t.Helper()
	st, viewID := seamStore(t, mergeState)
	prot, err := st.Append(state.KindEvidence, "run_seam", nil, body)
	if err != nil {
		t.Fatal(err)
	}
	art, err := verify.UpToDate(st, "run_seam", viewID, prot.ID,
		verify.Subject{Repo: "o/r", Number: 7})
	if err != nil {
		t.Fatal(err)
	}
	v, err := verify.Load(art)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// strictBody is the read evidence records for a base like itsHabib/ship's
// main, whose classic protection carries required_status_checks.strict.
func strictBody() evidence.ProtectionBody {
	return evidence.ProtectionBody{
		PR:       evidence.PRRef{Repo: "o/r", Number: 7},
		BaseRef:  "main",
		Readable: true,
		Strict:   true,
		Source:   evidence.ProtectionSourceBranch,
	}
}

// The load-bearing direction: a strict read written by evidence must still be
// strict when verify reads it back, or the block never fires in production no
// matter how green both packages' own suites are.
func TestUpToDateSeamBlocksOnRecordedStrictProtection(t *testing.T) {
	v := upToDateOverRecordedProtection(t, "BEHIND", strictBody())
	if v.Decision != verify.DecisionBlock {
		t.Fatalf("a recorded strict read must reach the rung as strict, got %s (%s)", v.Decision, v.Why)
	}
	// The base ref and source travel the same seam as the strictness bit and
	// fail the same silent way, so the verdict must still name both.
	for _, want := range []string{"main", evidence.ProtectionSourceBranch} {
		if !strings.Contains(v.Why, want) {
			t.Fatalf("the block lost %q crossing the seam: %s", want, v.Why)
		}
	}
	// BEHIND is a literal in upToDateWhy's format string, not a field read back
	// across the seam — this pins the block's message, not the contract.
	if !strings.Contains(v.Why, "BEHIND") {
		t.Fatalf("the block must say what it found: %s", v.Why)
	}
}

// The other direction, so the seam is pinned rather than merely satisfied: a
// reader that ignored the recorded body entirely — or inverted it — would pass
// the test above only by blocking everything. Non-strict must still pass, and
// say so for the reason it actually read.
func TestUpToDateSeamPassesOnRecordedNonStrictProtection(t *testing.T) {
	body := strictBody()
	body.Strict = false
	v := upToDateOverRecordedProtection(t, "BEHIND", body)
	if v.Decision != verify.DecisionPass {
		t.Fatalf("BEHIND alone must not block, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "does not require up-to-date heads") {
		t.Fatalf("the pass must record the read that carried it: %s", v.Why)
	}
}

// Readable crosses the same seam and fails open the same way: an unreadable
// read that arrived as readable would report a protection fact gate never
// established. The degraded text is the only thing distinguishing the two
// passes, so it is the assertion.
func TestUpToDateSeamCarriesUnreadableProtection(t *testing.T) {
	v := upToDateOverRecordedProtection(t, "BEHIND", evidence.ProtectionBody{
		PR:      evidence.PRRef{Repo: "o/r", Number: 7},
		BaseRef: "main",
		Note:    "HTTP 403: Resource not accessible",
	})
	if v.Decision != verify.DecisionPass {
		t.Fatalf("an unread setting must not block a merge, got %s (%s)", v.Decision, v.Why)
	}
	for _, want := range []string{"unreadable", "HTTP 403"} {
		if !strings.Contains(v.Why, want) {
			t.Fatalf("the degradation lost %q crossing the seam: %s", want, v.Why)
		}
	}
}
