package org

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const (
	tenant = "acme"
	role   = "ic:contracts-org"
	work   = "github:acme/api#88"
	other  = "jira:PROJ-412"
	boss   = "lead:platform"
)

// chain builds well-formed chains so a test can mutate one thing and name one
// law. It fills seq, prev, incarnation and class from the folded state, which
// is exactly what a real writer does — so a fixture that refuses is refusing
// for the reason under test rather than for bookkeeping.
type chain struct {
	t     *testing.T
	state RoleState
	recs  []Record
}

func newChain(t *testing.T) *chain {
	t.Helper()
	return &chain{t: t}
}

// draft returns the record a writer at this tip would produce for a kind,
// without appending it.
func (c *chain) draft(kind string, mutate ...func(*Record)) Record {
	c.t.Helper()
	class, known := DeclaredClass(kind)
	if !known {
		class = ClassAdvisory
	}
	r := Record{
		V: Version, Scheme: Scheme,
		Seq: c.state.Seq + 1, Prev: c.state.Tip,
		Tenant: tenant, Role: role,
		Kind: kind, KindClass: class,
		Fence: c.state.Fence,
		At:    "2026-08-22T12:00:00Z",
	}
	if law, ok := laws[kind]; !ok || law.incarnation == required {
		r.Incarnation = c.state.Holder
	}
	for _, m := range mutate {
		m(&r)
	}
	return r
}

// add appends a record that must be admitted.
func (c *chain) add(kind string, mutate ...func(*Record)) *chain {
	c.t.Helper()
	r := c.draft(kind, mutate...)
	next, err := Advance(c.state, r)
	if err != nil {
		c.t.Fatalf("%s at seq %d was refused: %v", kind, r.Seq, err)
	}
	c.state, c.recs = next, append(c.recs, r)
	return c
}

// refuses asserts the reason a record is rejected with, and leaves the chain
// untouched so a test can continue from the same tip.
func (c *chain) refuses(reason, kind string, mutate ...func(*Record)) {
	c.t.Helper()
	r := c.draft(kind, mutate...)
	_, err := Advance(c.state, r)
	if got := RefusalReason(err); got != reason {
		c.t.Fatalf("%s at seq %d: want %q, got %q (%v)", kind, r.Seq, reason, got, err)
	}
}

func charter(mutate ...func(*Terms)) func(*Record) {
	return func(r *Record) {
		t := Terms{Scope: []string{work}, Tier: "T2", Supervisors: []string{boss}}
		for _, m := range mutate {
			m(&t)
		}
		r.Terms = &t
	}
}

func subject(s Subject) func(*Record) {
	return func(r *Record) { r.Subject = s }
}

// chartered is the common prelude: a role exists, an incarnation holds it, and
// it has been assigned one work item.
func chartered(t *testing.T) *chain {
	t.Helper()
	return newChain(t).
		add(KindCharter, charter()).
		add(KindAttach).
		add(KindAssign, subject(Subject{Work: work, Digest: "sha256:beef"}))
}

// TestHappyPath walks the state machine end to end and checks the derived state
// at each stop.
func TestHappyPath(t *testing.T) {
	c := newChain(t).add(KindCharter, charter())
	if c.state.Phase != PhaseChartered {
		t.Fatalf("after charter: phase %q", c.state.Phase)
	}
	if c.state.Terms.Tier != "T2" || !slices.Contains(c.state.Terms.Scope, work) {
		t.Fatalf("charter terms were not inherited: %+v", c.state.Terms)
	}

	c.add(KindAttach)
	if c.state.Phase != PhaseHeld || c.state.Holder == "" {
		t.Fatalf("after attach: phase %q holder %q", c.state.Phase, c.state.Holder)
	}
	// The incarnation id is the digest of the record that minted it — not a
	// counter a re-reading writer could copy by accident.
	attachDigest, err := DigestOf(c.recs[1])
	if err != nil {
		t.Fatal(err)
	}
	if c.state.Holder != attachDigest {
		t.Errorf("holder %q is not the attach record's digest %q", c.state.Holder, attachDigest)
	}

	c.add(KindAssign, subject(Subject{Work: work, Digest: "sha256:beef"}))
	if !c.state.Holds(work) || c.state.Phase != PhaseHeld {
		t.Fatalf("after assign: held=%v phase=%q", c.state.Held, c.state.Phase)
	}

	c.add(KindClaim, subject(Subject{Work: work}))
	if c.state.Phase != PhaseActive || c.state.Active != work {
		t.Fatalf("after claim: phase %q active %q", c.state.Phase, c.state.Active)
	}

	c.add(KindComplete, subject(Subject{Work: work}))
	if c.state.Phase != PhaseHeld || c.state.Active != "" {
		t.Fatalf("after complete: phase %q active %q", c.state.Phase, c.state.Active)
	}
	if c.state.Holds(work) {
		t.Error("completed work is still held")
	}

	c.add(KindRetire)
	if c.state.Phase != PhaseRetired {
		t.Fatalf("after retire: phase %q", c.state.Phase)
	}
	c.refuses(ReasonRetired, KindNote)
}

// TestYieldKeepsTheWorkHeld separates the three terminals: yield leaves the
// work held for later, complete and abandon drop it.
func TestYieldKeepsTheWorkHeld(t *testing.T) {
	c := chartered(t).add(KindClaim, subject(Subject{Work: work}))
	c.add(KindYield, subject(Subject{Work: work}))
	if !c.state.Holds(work) {
		t.Error("yield dropped the work; it means unfinished, not abandoned")
	}
	if c.state.Phase != PhaseHeld || c.state.Active != "" {
		t.Errorf("after yield: phase %q active %q", c.state.Phase, c.state.Active)
	}
	c.add(KindClaim, subject(Subject{Work: work}))
	c.add(KindAbandon, subject(Subject{Work: work}))
	if c.state.Holds(work) {
		t.Error("abandon left the work held")
	}
}

// TestCrashTakeoverAndTheStaleWriter is the fixture the whole design exists
// for, and it pins the check ORDER that makes it diagnosable.
//
// An incarnation claims work and dies without a terminal record. A supervisor
// takes over. The dead incarnation wakes up and writes — having re-read the
// tip, so its seq and prev are PERFECTLY CORRECT. A reducer that checked chain
// position first would call that chain healthy and let the write through; the
// only thing wrong with it is who is writing.
func TestCrashTakeoverAndTheStaleWriter(t *testing.T) {
	c := chartered(t).add(KindClaim, subject(Subject{Work: work}))
	dead := c.state.Holder

	c.add(KindTakeover, subject(Subject{Party: boss}))
	if c.state.Holder == dead {
		t.Fatal("takeover did not displace the holder")
	}
	if c.state.Dangling != work {
		t.Fatalf("the orphaned claim was lost: dangling %q", c.state.Dangling)
	}
	if c.state.Phase != PhaseHeld || c.state.Active != "" {
		t.Fatalf("after takeover: phase %q active %q", c.state.Phase, c.state.Active)
	}

	// The stale writer, having re-read the tip. Everything about this record is
	// right except its identity.
	stale := c.draft(KindNote)
	stale.Incarnation = dead
	if stale.Prev != c.state.Tip || stale.Seq != c.state.Seq+1 {
		t.Fatal("the fixture is not exercising a correctly positioned stale write")
	}
	if _, err := Advance(c.state, stale); RefusalReason(err) != ReasonStaleIncarnation {
		t.Fatalf("a correctly positioned stale write: want %q, got %q (%v)",
			ReasonStaleIncarnation, RefusalReason(err), err)
	}

	// And the same writer WITHOUT re-reading: identity still wins, so the
	// diagnosis does not depend on how stale the writer's read was.
	behind := c.draft(KindNote)
	behind.Incarnation, behind.Prev, behind.Seq = dead, digestOfString("old tip"), c.state.Seq
	if _, err := Advance(c.state, behind); RefusalReason(err) != ReasonStaleIncarnation {
		t.Fatalf("an out-of-date stale write: want %q, got %q", ReasonStaleIncarnation, RefusalReason(err))
	}

	// L3: the successor must discharge the inherited obligation before it may
	// claim anything of its own.
	c.refuses(ReasonDanglingClaim, KindClaim, subject(Subject{Work: work}))
	c.refuses(ReasonClaimSubjectMismatch, KindYield, subject(Subject{Work: other}))
	c.add(KindYield, subject(Subject{Work: work}))
	if c.state.Dangling != "" {
		t.Fatalf("the dangling claim survived its terminal: %q", c.state.Dangling)
	}
	c.add(KindClaim, subject(Subject{Work: work}))
	if c.state.Phase != PhaseActive {
		t.Fatalf("the successor could not claim after resolving: phase %q", c.state.Phase)
	}
}

// TestSecondIncarnationIsRefused is P1's gate stated as a test: two
// incarnations of one role, and the second is refused with the log uncorrupted.
func TestSecondIncarnationIsRefused(t *testing.T) {
	c := chartered(t)
	before := c.state
	c.refuses(ReasonAlreadyHeld, KindAttach)
	if !sameState(c.state, before) {
		t.Error("the refused attach changed the state")
	}
	c.refuses(ReasonNotSupervisor, KindTakeover, subject(Subject{Party: "ic:someone-else"}))
}

func TestChainLaws(t *testing.T) {
	t.Run("genesis must be a charter", func(t *testing.T) {
		newChain(t).refuses(ReasonGenesisMisplaced, KindAttach)
	})
	t.Run("only one charter", func(t *testing.T) {
		newChain(t).add(KindCharter, charter()).
			refuses(ReasonGenesisMisplaced, KindCharter, charter())
	})
	t.Run("seq gap", func(t *testing.T) {
		chartered(t).refuses(ReasonSeqGap, KindNote, func(r *Record) { r.Seq += 3 })
	})
	t.Run("seq duplicate", func(t *testing.T) {
		chartered(t).refuses(ReasonSeqGap, KindNote, func(r *Record) { r.Seq-- })
	})
	t.Run("prev mismatch", func(t *testing.T) {
		chartered(t).refuses(ReasonPrevMismatch, KindNote, func(r *Record) { r.Prev = digestOfString("wrong") })
	})
	t.Run("tenant mismatch", func(t *testing.T) {
		chartered(t).refuses(ReasonTenantMismatch, KindNote, func(r *Record) { r.Tenant = "other-corp" })
	})
	t.Run("role mismatch", func(t *testing.T) {
		chartered(t).refuses(ReasonRoleMismatch, KindNote, func(r *Record) { r.Role = "ic:someone-else" })
	})
	t.Run("fence regression", func(t *testing.T) {
		c := chartered(t).add(KindNote, func(r *Record) { r.Fence = 9 })
		c.refuses(ReasonFenceRegression, KindNote, func(r *Record) { r.Fence = 2 })
	})
	t.Run("claim while acting", func(t *testing.T) {
		c := chartered(t).
			add(KindAssign, subject(Subject{Work: other, Digest: "sha256:cafe"})).
			add(KindClaim, subject(Subject{Work: work}))
		c.refuses(ReasonClaimActive, KindClaim, subject(Subject{Work: other}))
	})
	t.Run("claim work that is not held", func(t *testing.T) {
		chartered(t).refuses(ReasonWorkNotHeld, KindClaim, subject(Subject{Work: other}))
	})
	t.Run("assign twice", func(t *testing.T) {
		chartered(t).refuses(ReasonWorkAlreadyHeld, KindAssign,
			subject(Subject{Work: work, Digest: "sha256:beef"}))
	})
	t.Run("unassign what is not held", func(t *testing.T) {
		chartered(t).refuses(ReasonWorkNotHeld, KindUnassign, subject(Subject{Work: other}))
	})
	t.Run("unassign the active claim", func(t *testing.T) {
		c := chartered(t).add(KindClaim, subject(Subject{Work: work}))
		c.refuses(ReasonClaimActive, KindUnassign, subject(Subject{Work: work}))
	})
	t.Run("terminal with no claim", func(t *testing.T) {
		chartered(t).refuses(ReasonNoClaim, KindYield, subject(Subject{Work: work}))
	})
	t.Run("terminal naming other work", func(t *testing.T) {
		c := chartered(t).add(KindClaim, subject(Subject{Work: work}))
		c.refuses(ReasonClaimSubjectMismatch, KindComplete, subject(Subject{Work: other}))
	})
	t.Run("release while acting", func(t *testing.T) {
		c := chartered(t).add(KindClaim, subject(Subject{Work: work}))
		c.refuses(ReasonClaimActive, KindRelease)
	})
	t.Run("retire holding work", func(t *testing.T) {
		chartered(t).refuses(ReasonOpenWork, KindRetire)
	})
	t.Run("claim before the role is held", func(t *testing.T) {
		// A well-formed claim from an incarnation nobody ever attached. The
		// record is valid on its own terms; what is wrong is that the role has
		// no holder for it to be.
		newChain(t).add(KindCharter, charter()).
			refuses(ReasonNotHeld, KindClaim, func(r *Record) {
				r.Subject = Subject{Work: work}
				r.Incarnation = digestOfString("an incarnation from nowhere")
			})
	})
	t.Run("annul something other than the tip", func(t *testing.T) {
		chartered(t).refuses(ReasonAnnulUnknown, KindAnnul,
			subject(Subject{Target: digestOfString("some old record")}))
	})
	t.Run("annul the tip", func(t *testing.T) {
		c := chartered(t)
		c.add(KindAnnul, subject(Subject{Target: c.state.Tip}))
	})
	t.Run("min reader above this reader", func(t *testing.T) {
		newChain(t).refuses(ReasonMinReader, KindCharter, charter(func(tm *Terms) { tm.MinReader = Version + 1 }))
	})
	t.Run("min reader may not decrease", func(t *testing.T) {
		c := newChain(t).
			add(KindCharter, charter(func(tm *Terms) { tm.MinReader = Version })).
			add(KindAttach)
		c.refuses(ReasonMinReader, KindRecharter, charter(func(tm *Terms) { tm.MinReader = 0 }))
	})
}

// TestEffectObligations pins the bound that makes recovery a fixed procedure
// over one record rather than a search.
func TestEffectObligations(t *testing.T) {
	c := chartered(t).add(KindClaim, subject(Subject{Work: work}))
	c.add(KindIntentRef, subject(Subject{Effect: "eff_1"}))
	if len(c.state.OpenIntents) != 1 {
		t.Fatalf("open intents %v", c.state.OpenIntents)
	}
	c.refuses(ReasonOpenIntent, KindIntentRef, subject(Subject{Effect: "eff_2"}))
	c.refuses(ReasonOpenIntent, KindIntentRef, subject(Subject{Effect: "eff_1"}))
	c.refuses(ReasonIntentUnknown, KindResolution, subject(Subject{Effect: "eff_9"}))

	// An open effect blocks the NEXT claim: a replacement must not start new
	// work while an obligation from the last one is unresolved.
	c.add(KindYield, subject(Subject{Work: work}))
	c.refuses(ReasonOpenIntent, KindClaim, subject(Subject{Work: work}))
	c.add(KindResolution, subject(Subject{Effect: "eff_1"}))
	if len(c.state.OpenIntents) != 0 {
		t.Fatalf("resolution left %v open", c.state.OpenIntents)
	}
	c.add(KindClaim, subject(Subject{Work: work}))
}

func TestEscalationsClose(t *testing.T) {
	c := chartered(t).add(KindEscalation)
	esc := c.state.Tip
	if len(c.state.OpenEscalations) != 1 {
		t.Fatalf("open escalations %v", c.state.OpenEscalations)
	}
	c.refuses(ReasonEscalationUnknown, KindResolution, subject(Subject{Target: digestOfString("another")}))
	c.add(KindResolution, subject(Subject{Target: esc}))
	if len(c.state.OpenEscalations) != 0 {
		t.Fatalf("resolution left %v open", c.state.OpenEscalations)
	}
}

// TestRevokeOrphansTheClaim proves the no-revoke-endpoint story: appending a
// revoke advances the fence and drops the holder, with nothing sent to it.
func TestRevokeOrphansTheClaim(t *testing.T) {
	c := chartered(t).add(KindClaim, subject(Subject{Work: work}))
	dead := c.state.Holder
	c.add(KindRevoke, func(r *Record) { r.Fence = c.state.Fence + 1 })
	if c.state.Holder != "" || c.state.Phase != PhaseChartered {
		t.Fatalf("after revoke: holder %q phase %q", c.state.Holder, c.state.Phase)
	}
	if c.state.Dangling != work {
		t.Errorf("revoke lost the orphaned claim: %q", c.state.Dangling)
	}
	stale := c.draft(KindNote)
	stale.Incarnation = dead
	if _, err := Advance(c.state, stale); RefusalReason(err) != ReasonNotHeld {
		t.Errorf("a revoked incarnation's write: want %q, got %q", ReasonNotHeld, RefusalReason(err))
	}
}

// TestDegradedTracksTheTip surfaces that a resume from here is thinner than it
// looks: the host recorded mechanical activity the distiller has not folded.
func TestDegradedTracksTheTip(t *testing.T) {
	c := chartered(t).add(KindMark)
	if !c.state.Degraded {
		t.Error("a mark tip did not set Degraded")
	}
	c.add(KindCheckpoint)
	if c.state.Degraded {
		t.Error("Degraded survived a checkpoint")
	}
}

// TestUnknownAdvisoryKindFoldsAsSkipped is the version-skew guarantee from the
// fold's side: a future advisory kind changes nothing but the tip.
func TestUnknownAdvisoryKindFoldsAsSkipped(t *testing.T) {
	c := chartered(t)
	before := c.state
	c.add("telemetry-v2", func(r *Record) { r.KindClass = ClassAdvisory })
	if c.state.Phase != before.Phase || c.state.Holder != before.Holder || c.state.Active != before.Active {
		t.Error("an unknown advisory kind moved the state machine")
	}
	if c.state.Seq != before.Seq+1 || c.state.Tip == before.Tip {
		t.Error("an unknown advisory kind did not advance the chain")
	}
}

// TestFoldOfPrefixPlusRemainder is the property snapshots and seals depend on:
// folding a prefix and continuing gives the same answer as folding the whole
// chain. Without it a seal could disagree with a cold read of the same records.
func TestFoldOfPrefixPlusRemainder(t *testing.T) {
	c := chartered(t).
		add(KindClaim, subject(Subject{Work: work})).
		add(KindIntentRef, subject(Subject{Effect: "eff_1"})).
		add(KindResolution, subject(Subject{Effect: "eff_1"})).
		add(KindYield, subject(Subject{Work: work})).
		add(KindSeal).
		add(KindClaim, subject(Subject{Work: work})).
		add(KindTakeover, subject(Subject{Party: boss})).
		add(KindAbandon, subject(Subject{Work: work}))

	whole, err := Reduce(c.recs)
	if err != nil {
		t.Fatalf("fold the whole chain: %v", err)
	}
	rapid.Check(t, func(rt *rapid.T) {
		cut := rapid.IntRange(0, len(c.recs)).Draw(rt, "cut")
		head, err := Reduce(c.recs[:cut])
		if err != nil {
			rt.Fatalf("fold prefix of %d: %v", cut, err)
		}
		for _, r := range c.recs[cut:] {
			head, err = Advance(head, r)
			if err != nil {
				rt.Fatalf("continue from %d: %v", cut, err)
			}
		}
		if !sameState(head, whole) {
			rt.Fatalf("prefix %d + remainder != whole\n got  %+v\n want %+v", cut, head, whole)
		}
	})
}

func sameState(a, b RoleState) bool {
	return a.Tenant == b.Tenant && a.Role == b.Role && a.Phase == b.Phase &&
		a.Holder == b.Holder && a.Fence == b.Fence && a.Active == b.Active &&
		a.Dangling == b.Dangling && a.Seq == b.Seq && a.Tip == b.Tip &&
		a.Seal == b.Seal && a.NextDue == b.NextDue && a.Degraded == b.Degraded &&
		slices.Equal(a.Held, b.Held) &&
		slices.Equal(a.OpenIntents, b.OpenIntents) &&
		slices.Equal(a.OpenEscalations, b.OpenEscalations) &&
		slices.Equal(a.Annulled, b.Annulled)
}

// TestReduceOnlyEverRefuses is the total-function property: over arbitrary
// record sequences the fold never panics and never returns an error that is not
// a named refusal. An unnamed error would be a law nobody can branch on.
func TestReduceOnlyEverRefuses(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 12).Draw(rt, "n")
		records := make([]Record, 0, n)
		for range n {
			records = append(records, arbitraryRecord(rt))
		}
		state, err := Reduce(records)
		if err == nil {
			if state.Phase == PhaseActive && state.Active == "" {
				rt.Fatal("Active phase with no active work: the L2 invariant is broken")
			}
			if state.Phase == PhaseVoid && len(records) > 0 {
				rt.Fatal("records folded to the void state")
			}
			return
		}
		if RefusalReason(err) == "" {
			rt.Fatalf("unnamed error from the fold: %v", err)
		}
		if !slices.Contains(Reasons, RefusalReason(err)) {
			rt.Fatalf("refusal %q is not in the frozen vocabulary", RefusalReason(err))
		}
	})
}

func arbitraryRecord(rt *rapid.T) Record {
	kinds := append(slices.Sorted(maps.Keys(structuralKinds)), slices.Sorted(maps.Keys(advisoryKinds))...)
	kind := rapid.SampledFrom(kinds).Draw(rt, "kind")
	class, _ := DeclaredClass(kind)
	return Record{
		V:      int64(rapid.SampledFrom([]int{Version, Version, 99}).Draw(rt, "v")),
		Scheme: Scheme,
		Seq:    int64(rapid.IntRange(0, 4).Draw(rt, "seq")),
		Prev:   rapid.SampledFrom([]string{"", digestOfString("a"), digestOfString("b")}).Draw(rt, "prev"),
		Tenant: tenant, Role: role,
		Kind: kind, KindClass: class,
		Incarnation: rapid.SampledFrom([]string{"", digestOfString("i1"), digestOfString("i2")}).Draw(rt, "inc"),
		Fence:       int64(rapid.IntRange(0, 3).Draw(rt, "fence")),
		Subject: Subject{
			Work:   rapid.SampledFrom([]string{"", work, other}).Draw(rt, "work"),
			Digest: rapid.SampledFrom([]string{"", "sha256:beef"}).Draw(rt, "digest"),
			Party:  rapid.SampledFrom([]string{"", boss}).Draw(rt, "party"),
			Effect: rapid.SampledFrom([]string{"", "eff_1"}).Draw(rt, "effect"),
			Target: rapid.SampledFrom([]string{"", digestOfString("t")}).Draw(rt, "target"),
		},
		Terms: rapid.SampledFrom([]*Terms{nil, {Scope: []string{work}, Supervisors: []string{boss}}}).Draw(rt, "terms"),
	}
}

// chainLevelReasons are the laws Admissible owns. Together with
// recordLevelReasons they must cover the frozen vocabulary.
var chainLevelReasons = []string{
	ReasonGenesisMisplaced,
	ReasonSeqGap,
	ReasonPrevMismatch,
	ReasonStaleIncarnation,
	ReasonTenantMismatch,
	ReasonRoleMismatch,
	ReasonNotHeld,
	ReasonAlreadyHeld,
	ReasonNotSupervisor,
	ReasonClaimActive,
	ReasonNoClaim,
	ReasonClaimSubjectMismatch,
	ReasonWorkNotHeld,
	ReasonWorkAlreadyHeld,
	ReasonDanglingClaim,
	ReasonOpenIntent,
	ReasonIntentUnknown,
	ReasonEscalationUnknown,
	ReasonRetired,
	ReasonOpenWork,
	ReasonMinReader,
	ReasonFenceRegression,
	ReasonAnnulUnknown,
}

// The four tests below exist because mutation testing found them missing.
// Line coverage was 94.6% and every one of these lines was "covered" — executed
// by some test that never asserted on what it did. That gap is the whole
// argument for running mutants rather than trusting a coverage number.

// TestMinReaderMayStayEqual pins the EQUALITY case of monotonicity. The refusal
// test next to it only ever exercised a decrease, so mutating `<` to `<=` —
// which would refuse a recharter that left min_reader alone — survived. Monotone
// means non-decreasing, and "unchanged" is the common case.
func TestMinReaderMayStayEqual(t *testing.T) {
	c := newChain(t).
		add(KindCharter, charter(func(tm *Terms) { tm.MinReader = Version })).
		add(KindAttach)
	c.add(KindRecharter, charter(func(tm *Terms) { tm.MinReader = Version }))
	if c.state.Terms.MinReader != Version {
		t.Errorf("min_reader %d after an equal recharter", c.state.Terms.MinReader)
	}
}

// TestNextDueTracksTheLastDeclaredDeadline covers the field derived liveness is
// computed against. Nothing asserted on it before, so inverting the guard that
// sets it survived — and a liveness deadline that silently stopped advancing is
// a role that reads as alive forever.
func TestNextDueTracksTheLastDeclaredDeadline(t *testing.T) {
	c := chartered(t)
	if c.state.NextDue != "" {
		t.Fatalf("a chain that declared no deadline has one: %q", c.state.NextDue)
	}
	const due = "2026-08-22T13:00:00Z"
	c.add(KindNote, func(r *Record) { r.NextDue = due })
	if c.state.NextDue != due {
		t.Errorf("NextDue %q, want %q", c.state.NextDue, due)
	}
	// A record that declares no deadline leaves the last one standing rather
	// than clearing it: silence is not a retraction.
	c.add(KindNote)
	if c.state.NextDue != due {
		t.Errorf("a record with no deadline erased the standing one: %q", c.state.NextDue)
	}
	const later = "2026-08-22T15:00:00Z"
	c.add(KindMark, func(r *Record) { r.NextDue = later })
	if c.state.NextDue != later {
		t.Errorf("NextDue %q, want %q", c.state.NextDue, later)
	}
}

// TestRefusalErrorRendersBothForms covers the seq-0 branch, which no test
// reached: a refusal not about one particular record must not claim "at seq 0".
func TestRefusalErrorRendersBothForms(t *testing.T) {
	withSeq := (&Refusal{Reason: ReasonSeqGap, Seq: 7, Detail: "detail"}).Error()
	if !strings.Contains(withSeq, ReasonSeqGap) || !strings.Contains(withSeq, "seq 7") {
		t.Errorf("seq-bearing refusal rendered as %q", withSeq)
	}
	withoutSeq := (&Refusal{Reason: ReasonMinReader, Detail: "detail"}).Error()
	if strings.Contains(withoutSeq, "seq") {
		t.Errorf("a refusal with no record claimed a seq: %q", withoutSeq)
	}
	if !strings.Contains(withoutSeq, ReasonMinReader) {
		t.Errorf("refusal dropped its reason: %q", withoutSeq)
	}
}

// TestShortAbbreviatesOnlyLongDigests covers the display helper's boundary. It
// is cosmetic, but an untested branch in a function every refusal message calls
// is a function nobody has watched work.
func TestShortAbbreviatesOnlyLongDigests(t *testing.T) {
	full := digestOfString("x")
	if abbreviated := short(full); !strings.HasSuffix(abbreviated, "…") || len(abbreviated) >= len(full) {
		t.Errorf("short(%q) = %q", full, abbreviated)
	}
	// Includes the exact boundary: 14 characters is passed through, 15 is cut.
	// Without the boundary case the comparison could be < or <= and no test
	// would notice, which is what the mutation run reported.
	for _, s := range []string{"", "sha256:beef", "12345678901234"} {
		if got := short(s); got != s {
			t.Errorf("short(%q) = %q; a digest of %d chars is passed through whole", s, got, len(s))
		}
	}
	if got := short("123456789012345"); got != "12345678901234…" {
		t.Errorf("short(15 chars) = %q; one past the boundary must be cut", got)
	}
}
