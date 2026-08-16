package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// upToDateFor drives the rung over a recorded view and protection read. A nil
// protection map records no protection evidence at all — the pre-existing
// behaviour the rung must degrade to.
func upToDateFor(t *testing.T, view map[string]any, protection map[string]any) Verdict {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	viewArt, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": view})
	if err != nil {
		t.Fatal(err)
	}
	protectionID := ""
	if protection != nil {
		a, err := st.Append(state.KindEvidence, "run_t", nil, protection)
		if err != nil {
			t.Fatal(err)
		}
		protectionID = a.ID
	}
	art, err := UpToDate(st, "run_t", viewArt.ID, protectionID, subj)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func strictProtection() map[string]any {
	return map[string]any{"base_ref": "main", "readable": true, "strict": true, "source": "branch-protection"}
}

func behindView() map[string]any {
	return map[string]any{"state": "OPEN", "mergeStateStatus": "BEHIND", "baseRefName": "main"}
}

// The friction this rung exists for: a BEHIND head on a strict base is a merge
// GitHub refuses, so gate must say so instead of emitting the command.
func TestUpToDateBlocksBehindOnStrictBase(t *testing.T) {
	v := upToDateFor(t, behindView(), strictProtection())
	if v.Decision != DecisionBlock {
		t.Fatalf("BEHIND on a strict base must block, got %s (%s)", v.Decision, v.Why)
	}
	for _, want := range []string{"BEHIND", "CI", "gate again"} {
		if !strings.Contains(v.Why, want) {
			t.Fatalf("the block must prescribe the fix, missing %q: %s", want, v.Why)
		}
	}
	if len(v.Findings) != 1 || v.Findings[0].Severity != "block" {
		t.Fatalf("a block must carry its finding: %+v", v.Findings)
	}
}

// The subtlety: BEHIND is not by itself a reason to refresh. A rung that
// blocked on it would add a CI cycle to nearly every merge in the portfolio,
// where most bases are unprotected or non-strict.
func TestUpToDatePassesBehindWithoutAStrictRequirement(t *testing.T) {
	cases := []struct {
		name       string
		protection map[string]any
		wantWhy    string
	}{
		{
			name:       "protected but not strict",
			protection: map[string]any{"base_ref": "main", "readable": true, "source": "branch-protection"},
			wantWhy:    "does not require up-to-date heads",
		},
		{
			name:       "no protection at all",
			protection: map[string]any{"base_ref": "main", "readable": true, "source": "none"},
			wantWhy:    "does not require up-to-date heads",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := upToDateFor(t, behindView(), c.protection)
			if v.Decision != DecisionPass {
				t.Fatalf("BEHIND alone must not block, got %s (%s)", v.Decision, v.Why)
			}
			if !strings.Contains(v.Why, c.wantWhy) {
				t.Fatalf("the verdict must record what carried it: %s", v.Why)
			}
		})
	}
}

// A strict base is fine as long as the head is current — the common case on
// the one repo in the portfolio that is strict.
func TestUpToDatePassesCurrentHeadOnStrictBase(t *testing.T) {
	v := upToDateFor(t, map[string]any{
		"state": "OPEN", "mergeStateStatus": "CLEAN", "baseRefName": "main",
	}, strictProtection())
	if v.Decision != DecisionPass {
		t.Fatalf("a current head must pass, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "current") {
		t.Fatalf("the verdict must say the head was current: %s", v.Why)
	}
}

// Other merge states belong to other rungs (readiness blocks CONFLICTING, CI
// blocks a red rollup). Acting on them here would double-block on facts this
// rung did not establish.
func TestUpToDateIgnoresOtherMergeStates(t *testing.T) {
	for _, mergeState := range []string{"BLOCKED", "DIRTY", "UNSTABLE", "UNKNOWN", ""} {
		v := upToDateFor(t, map[string]any{
			"state": "OPEN", "mergeStateStatus": mergeState, "baseRefName": "main",
		}, strictProtection())
		if v.Decision != DecisionPass {
			t.Fatalf("merge state %q is not this rung's business, got %s (%s)", mergeState, v.Decision, v.Why)
		}
	}
}

// An unreadable protection endpoint (a token without admin scope) leaves the
// requirement unknown. Unknown degrades to the behaviour gate had before this
// rung existed — with the degradation recorded — never to a block.
func TestUpToDateDegradesWhenProtectionIsUnreadable(t *testing.T) {
	v := upToDateFor(t, behindView(), map[string]any{
		"base_ref": "main", "readable": false, "note": "HTTP 403: Resource not accessible",
	})
	if v.Decision != DecisionPass {
		t.Fatalf("an unread setting must not block a merge, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "unreadable") || !strings.Contains(v.Why, "HTTP 403") {
		t.Fatalf("the degradation must be visible in the verdict: %s", v.Why)
	}
}

func TestUpToDateWithoutProtectionEvidencePasses(t *testing.T) {
	v := upToDateFor(t, behindView(), nil)
	if v.Decision != DecisionPass {
		t.Fatalf("no recorded protection read must behave as before, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "unreadable") {
		t.Fatalf("the absent read must be recorded as unverified: %s", v.Why)
	}
}

// A merged subject has no live merge to preflight — backtest replays them.
func TestUpToDateExemptsMergedSubjects(t *testing.T) {
	v := upToDateFor(t, map[string]any{
		"state": "MERGED", "mergeStateStatus": "BEHIND", "baseRefName": "main",
	}, strictProtection())
	if v.Decision != DecisionPass {
		t.Fatalf("a merged PR has no merge to preflight, got %s (%s)", v.Decision, v.Why)
	}
}

// The rung is a code producer at T0 and names both evidence artifacts it read,
// so the decision stays reconstructable from state alone.
func TestUpToDateVerdictShape(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	viewArt, err := st.Append(state.KindEvidence, "run_t", nil,
		map[string]any{"data": behindView()})
	if err != nil {
		t.Fatal(err)
	}
	protArt, err := st.Append(state.KindEvidence, "run_t", nil, strictProtection())
	if err != nil {
		t.Fatal(err)
	}
	art, err := UpToDate(st, "run_t", viewArt.ID, protArt.ID, subj)
	if err != nil {
		t.Fatal(err)
	}
	if len(art.Parents) != 2 || art.Parents[0] != viewArt.ID || art.Parents[1] != protArt.ID {
		t.Fatalf("the verdict must name the evidence it judged: %v", art.Parents)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Producer.Class != ClassCode || v.Tier != "T0" {
		t.Fatalf("the preflight is a deterministic code rung: %+v", v.Producer)
	}
}

// Malformed protection evidence is schema drift, not a degraded read: hiding it
// behind the pass path is exactly how a broken rung goes unnoticed.
func TestUpToDateErrorsOnMalformedProtectionEvidence(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	viewArt, err := st.Append(state.KindEvidence, "run_t", nil,
		map[string]any{"data": behindView()})
	if err != nil {
		t.Fatal(err)
	}
	protArt, err := st.Append(state.KindEvidence, "run_t", nil,
		map[string]any{"readable": "yes-please"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpToDate(st, "run_t", viewArt.ID, protArt.ID, subj); err == nil {
		t.Fatal("malformed protection evidence must be an error, not a silent pass")
	}
}
