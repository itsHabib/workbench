package org

import (
	"fmt"
	"maps"
	"slices"
	"testing"
)

// Exhaustive state-space enumeration — model checking without a model checker.
//
// The fold is a state machine small enough to enumerate completely: five
// phases, twenty-six kinds, and a handful of booleans. So rather than sampling
// it with property tests and hoping, this walks EVERY reachable state and
// EVERY transition out of each one, and asserts three things that a Go type
// cannot express:
//
//  1. TOTALITY — every (state, record) pair is decided. Never a panic, never an
//     unnamed error, never a silent admission. A transition nobody thought
//     about is the failure mode a table-driven test cannot find, because you
//     only write cases for combinations you thought of.
//
//  2. INVARIANTS — the illegal states Go permits are unreachable in fact.
//     RoleState CAN represent Phase=Active with no active work; the point is
//     that no sequence of admitted records produces one.
//
//  3. A PINNED FRONTIER — the reachable state count is asserted, so a change
//     that opens a new state is something a reviewer sees rather than something
//     that arrives quietly.
//
// This is what §3.9 wants proved before it reaches for Lean. Lean's job is to
// make illegal states UNCONSTRUCTIBLE and emit a transition table other
// languages consume; that matters when a second implementation exists. Until
// then, exhaustive enumeration proves the same reachability facts, runs in CI
// on every commit, and cannot drift from the code the way an external model
// can. The concurrency this design does need a model checker for — two
// incarnations racing at the home, a fence high-water mark across verifiers,
// intent/attempt/outcome under crashes — is not in this package. It has no
// interleavings at all: it is a left fold over a linear sequence.

// sig is a RoleState abstracted to a finite signature. Digests, work URIs and
// prose are dropped; what remains is the shape the laws are stated over, which
// is what makes the space finite and the walk exhaustive.
type sig struct {
	Phase    Phase
	Holder   bool
	Active   bool
	Dangling bool
	Held     int
	Intents  int
	Escs     int
}

func signature(s RoleState) sig {
	return sig{
		Phase: s.Phase, Holder: s.Holder != "", Active: s.Active != "",
		Dangling: s.Dangling != "", Held: min(len(s.Held), 2),
		Intents: min(len(s.OpenIntents), 2), Escs: min(len(s.OpenEscalations), 2),
	}
}

// invariants are the laws stated as properties of a state rather than as checks
// on a record. Every one of them is something RoleState can represent and the
// machine must never reach.
var invariants = []struct {
	name  string
	holds func(RoleState) bool
}{
	{"Active phase iff active work", func(s RoleState) bool {
		return (s.Phase == PhaseActive) == (s.Active != "")
	}},
	{"a holder exists exactly while held or active", func(s RoleState) bool {
		held := s.Phase == PhaseHeld || s.Phase == PhaseActive
		return held == (s.Holder != "")
	}},
	{"never acting and dangling at once", func(s RoleState) bool {
		return s.Active == "" || s.Dangling == ""
	}},
	{"the active claim is always held work", func(s RoleState) bool {
		return s.Active == "" || s.Holds(s.Active)
	}},
	{"at most one outstanding effect", func(s RoleState) bool {
		return len(s.OpenIntents) <= 1
	}},
	{"a retired role holds nothing", func(s RoleState) bool {
		return s.Phase != PhaseRetired || len(s.Held) == 0
	}},
	{"a retired role owes nothing", func(s RoleState) bool {
		// Nothing may extend a retired chain, so an obligation that survives
		// into PhaseRetired can never be discharged. This invariant is the
		// state-space walk's first real find, promoted to a standing law.
		return s.Phase != PhaseRetired ||
			(s.Dangling == "" && len(s.OpenIntents) == 0 && len(s.OpenEscalations) == 0)
	}},
	{"a chain past genesis names its role", func(s RoleState) bool {
		return s.Phase == PhaseVoid || (s.Tenant != "" && s.Role != "")
	}},
	{"the tip advances with the sequence", func(s RoleState) bool {
		return (s.Seq == 0) == (s.Tip == "")
	}},
}

// TestStateSpaceIsTotalAndInvariant walks the whole reachable space.
func TestStateSpaceIsTotalAndInvariant(t *testing.T) {
	kinds := append(slices.Sorted(maps.Keys(structuralKinds)), slices.Sorted(maps.Keys(advisoryKinds))...)
	kinds = append(kinds, "a-kind-from-the-future")

	type node struct {
		state RoleState
		path  []string
	}
	seen := map[sig]bool{{}: true}
	queue := []node{{}}
	transitions, admitted := 0, 0

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, kind := range kinds {
			for _, subj := range subjectsFor(kind) {
				transitions++
				rec := draftFor(cur.state, kind, subj)
				next, err := Advance(cur.state, rec)
				if err != nil {
					// TOTALITY: a refusal must be one of the frozen reasons.
					// An unnamed error is a law no caller can branch on.
					if reason := RefusalReason(err); !slices.Contains(Reasons, reason) {
						t.Fatalf("unnamed refusal %v\n  after %v\n  on %s", err, cur.path, kind)
					}
					continue
				}
				admitted++
				path := append(slices.Clone(cur.path), kind)
				for _, inv := range invariants {
					if !inv.holds(next) {
						t.Fatalf("invariant %q violated\n  path: %v\n  state: %+v", inv.name, path, next)
					}
				}
				s := signature(next)
				if seen[s] {
					continue
				}
				seen[s] = true
				queue = append(queue, node{next, path})
			}
		}
	}

	t.Logf("explored %d transitions, %d admitted, %d reachable states", transitions, admitted, len(seen))

	// A pinned frontier. This number moving is not a failure — it is a change
	// to the state machine, and it should appear in a diff with an argument
	// attached rather than arriving unremarked.
	const wantStates = 86
	if len(seen) != wantStates {
		t.Errorf("reachable states = %d, pinned at %d; if this is intended, update the constant and say why in the commit",
			len(seen), wantStates)
	}
}

// TestEveryPhaseIsReachable proves no phase is dead code. A phase nothing can
// reach is a state machine that says more than it does.
func TestEveryPhaseIsReachable(t *testing.T) {
	reached := map[Phase]bool{}
	kinds := append(slices.Sorted(maps.Keys(structuralKinds)), slices.Sorted(maps.Keys(advisoryKinds))...)
	seen := map[sig]bool{{}: true}
	queue := []RoleState{{}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		reached[cur.Phase] = true
		for _, kind := range kinds {
			for _, subj := range subjectsFor(kind) {
				next, err := Advance(cur, draftFor(cur, kind, subj))
				if err != nil {
					continue
				}
				if s := signature(next); !seen[s] {
					seen[s] = true
					queue = append(queue, next)
				}
			}
		}
	}
	for _, p := range []Phase{PhaseVoid, PhaseChartered, PhaseHeld, PhaseActive, PhaseRetired} {
		if !reached[p] {
			t.Errorf("phase %q is unreachable", p)
		}
	}
}

// TestDanglingClaimIsAlwaysDischargeable is the L3 liveness property, and it is
// the one the design would be worst to get wrong: an obligation a successor
// cannot discharge is a role wedged forever, which is exactly the silent
// disappearance the law exists to prevent. From EVERY reachable state carrying
// a dangling claim there must be an admitted record that clears it.
func TestDanglingClaimIsAlwaysDischargeable(t *testing.T) {
	kinds := append(slices.Sorted(maps.Keys(structuralKinds)), slices.Sorted(maps.Keys(advisoryKinds))...)
	seen := map[sig]bool{{}: true}
	queue := []RoleState{{}}
	checked := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.Dangling != "" {
			checked++
			if !canDischarge(cur, kinds) {
				t.Fatalf("wedged: a dangling claim on %s cannot be discharged from %+v", cur.Dangling, cur)
			}
		}
		for _, kind := range kinds {
			for _, subj := range subjectsFor(kind) {
				next, err := Advance(cur, draftFor(cur, kind, subj))
				if err != nil {
					continue
				}
				if s := signature(next); !seen[s] {
					seen[s] = true
					queue = append(queue, next)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no reachable state carried a dangling claim; the property was never exercised")
	}
	t.Logf("checked %d reachable states carrying a dangling claim", checked)
}

// canDischarge searches forward for ANY admitted sequence that clears the
// dangling claim.
//
// It is a reachability search rather than a one-step check, and the difference
// is the whole point of the property. The first version of this test looked one
// record ahead and reported a wedge at {Phase: chartered, Dangling: set} — the
// state a revoke leaves behind. That state is correct and intended: revoke kills
// the credential, the obligation survives its holder, and the successor picks it
// up. Discharging it takes an attach FIRST and a terminal SECOND, which is
// exactly the inheritance L3 describes. A one-step property called that wedged;
// the machine was right and the property was wrong.
func canDischarge(s RoleState, kinds []string) bool {
	seen := map[sig]bool{signature(s): true}
	queue := []RoleState{s}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, kind := range kinds {
			for _, subj := range subjectsFor(kind) {
				next, err := Advance(cur, draftFor(cur, kind, subj))
				if err != nil {
					continue
				}
				if next.Dangling == "" {
					return true
				}
				if sg := signature(next); !seen[sg] {
					seen[sg] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return false
}

// subjectsFor returns the subject variants worth trying for a kind: enough to
// exercise both the admitted and refused branches without enumerating strings.
func subjectsFor(kind string) []Subject {
	const w1, w2 = "github:acme/api#88", "jira:PROJ-412"
	switch kind {
	case KindClaim, KindYield, KindComplete, KindAbandon, KindUnassign:
		return []Subject{{Work: w1}, {Work: w2}}
	case KindAssign:
		return []Subject{{Work: w1, Digest: "sha256:beef"}, {Work: w2, Digest: "sha256:cafe"}}
	case KindTakeover:
		return []Subject{{Party: "lead:platform"}, {Party: "ic:stranger"}}
	case KindSplit, KindMerge, KindDelegate:
		return []Subject{{Party: "ic:target"}}
	case KindIntentRef:
		return []Subject{{Effect: "eff_1"}, {Effect: "eff_2"}}
	case KindResolution:
		return []Subject{{Effect: "eff_1"}, {Target: "sha256:" + fmt.Sprintf("%064x", 1)}}
	case KindAnnul:
		return []Subject{{Target: "sha256:" + fmt.Sprintf("%064x", 2)}}
	case KindRevoke:
		return []Subject{{}}
	}
	return []Subject{{}}
}

// draftFor builds the record a correct writer at this state would produce for a
// kind — the same bookkeeping the chain helper does, standalone so the walk
// does not need a *testing.T per node.
func draftFor(s RoleState, kind string, subj Subject) Record {
	class, known := DeclaredClass(kind)
	if !known {
		class = ClassAdvisory
	}
	r := Record{
		V: Version, Scheme: Scheme,
		Seq: s.Seq + 1, Prev: s.Tip,
		Tenant: tenant, Role: role,
		Kind: kind, KindClass: class,
		Fence:   s.Fence,
		At:      "2026-08-22T12:00:00Z",
		Subject: subj,
	}
	if law, ok := laws[kind]; !ok || law.incarnation == required {
		r.Incarnation = s.Holder
	}
	if kind == KindCharter || kind == KindRecharter {
		r.Terms = &Terms{Scope: []string{"github:acme/api#88"}, Tier: "T2", Supervisors: []string{"lead:platform"}}
	}
	// A resolution can only close what is open, and an annul only the tip;
	// point them at real state so the admitted branch is reachable at all.
	if kind == KindResolution && subj.Target != "" && len(s.OpenEscalations) > 0 {
		r.Subject.Target = s.OpenEscalations[0]
	}
	if kind == KindAnnul && s.Tip != "" {
		r.Subject.Target = s.Tip
	}
	return r
}
