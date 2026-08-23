package org

import "slices"

// This file is the fold: the pure function from a role's chain to what the role
// currently is. It is the whole ownership mechanism — to act you must append,
// to append you must present the tip you read — expressed as a reducer so that
// "holding the tip is being the role" is a property of code rather than a
// convention a runtime is trusted to follow.
//
// It validates history. It never chooses a transition, and it performs no I/O:
// given the same records it returns the same state on any machine, which is
// what makes a verdict computed from it reproducible.
//
// # What this fold does NOT compute
//
// The narrative projection — what the role is doing, what it decided, what is
// open, what is next — lives in record BODIES, which are erasable blobs this
// leaf package cannot read. That projection is the runtime's, built from blob
// content. What is here is the structural state: who holds the role, what work
// it holds, which one it is acting on, what obligations are open. Those are the
// facts ownership and authority are decided from, and they are exactly the ones
// that must survive a body erasure.

// Phase is the role's position in the state machine.
//
//	Chartered ──attach──▶ Held(inc) ──retire──▶ Retired
//	                        │    ▲
//	               claim(w) │    │ yield · complete · abandon
//	                        ▼    │
//	                 Active(inc, w)
type Phase string

// The phases. PhaseVoid is the zero value: a chain that has recorded nothing.
const (
	PhaseVoid      Phase = ""
	PhaseChartered Phase = "chartered"
	PhaseHeld      Phase = "held"
	PhaseActive    Phase = "active"
	PhaseRetired   Phase = "retired"
)

// Assignment is one work item the role holds. Holding is not acting: a role may
// hold many and act on exactly one.
type Assignment struct {
	// Work is the work URI.
	Work string
	// Digest pins the item's content at assignment time. A later assign of the
	// same URI with a different digest is drift, which the work plane surfaces;
	// this fold records the digest it was given and does not interpret it.
	Digest string
	// Party is who it was assigned to, empty when the role assigned itself.
	Party string
}

// RoleState is the folded role: everything ownership and authority are decided
// from, and nothing else.
//
// Every field is bounded. The two sets that grow — Held and Open* — each have
// an explicit closing record, so a long-lived role's state does not grow with
// the length of its chain. That is not an optimization; the fold's entire
// purpose is to fit in a fresh context window.
type RoleState struct {
	Tenant string
	Role   string
	Phase  Phase
	// Terms are inherited from the charter and changed only by a recharter.
	Terms Terms
	// Holder is the incarnation currently bound to the role: the digest of the
	// attach or takeover that minted it. Empty unless Phase is Held or Active.
	Holder string
	// Fence is the high-water mark of chain positions authority has been minted
	// at. A record below it is a displaced writer spending a dead grant.
	Fence int64
	// Active is the one work item being acted on. Non-empty exactly when Phase
	// is Active — which is what lets an effect stamp DERIVE its work reference
	// from state instead of accepting one from the caller, deleting a whole
	// class of misattribution rather than documenting it.
	Active string
	// Dangling is a claim orphaned by a takeover or revoke: the predecessor
	// stopped without a terminal record. The successor must resolve it before
	// it may claim anything, so silent disappearance is not representable.
	//
	// It may name work that is no longer in Held. Unassign blocks only the
	// ACTIVE claim, so a successor can drop the work and still owe the close —
	// which is correct: the obligation is to record how the claim ended, and
	// that survives the work leaving the role's plate.
	Dangling string
	// Held is the work the role holds, in the order it was assigned.
	Held []Assignment
	// OpenIntents are effect ids issued and not yet resolved. Because the role
	// is serialized there is at most one outstanding unknown effect, which
	// makes recovery a fixed procedure over one record rather than a search.
	OpenIntents []string
	// OpenEscalations are escalation record digests awaiting a resolution.
	OpenEscalations []string
	// Annulled are record digests withdrawn by a later annul.
	Annulled []string
	// Seq and Tip are the chain position and digest a writer must present.
	Seq int64
	Tip string
	// Seal is the digest of the most recent seal, the point a cold read starts
	// from and the boundary behind which bodies become erasable.
	Seal string
	// NextDue is the deadline the last writer declared for its own next append.
	// Liveness is derived against this, never from self-report, so an
	// incarnation that hangs without exiting is correctly seen as dead.
	NextDue string
	// Degraded reports that the tip is a mechanical mark rather than a
	// distilled record: the host observed activity the distiller has not yet
	// folded. A resume from here is thinner than it looks, and saying so is
	// better than a reader assuming otherwise.
	Degraded bool
}

// held reports the index of a work URI in the held set, or -1.
func (s RoleState) held(work string) int {
	return slices.IndexFunc(s.Held, func(a Assignment) bool { return a.Work == work })
}

// Holds reports whether the role holds a work item.
func (s RoleState) Holds(work string) bool { return s.held(work) >= 0 }

// phaseLaw is the set of phases each structural kind may be written from.
// Stating it as data keeps the state machine enumerable: the conformance test
// walks it to prove every structural kind has one, so a kind added without a
// phase law fails the build rather than being admitted from anywhere.
var phaseLaw = map[string][]Phase{
	KindCharter:    {PhaseVoid},
	KindAttach:     {PhaseChartered},
	KindTakeover:   {PhaseHeld, PhaseActive},
	KindRevoke:     {PhaseHeld, PhaseActive},
	KindClaim:      {PhaseHeld},
	KindYield:      {PhaseHeld, PhaseActive},
	KindComplete:   {PhaseHeld, PhaseActive},
	KindAbandon:    {PhaseHeld, PhaseActive},
	KindRelease:    {PhaseHeld},
	KindRetire:     {PhaseHeld},
	KindRecharter:  {PhaseHeld, PhaseActive},
	KindAssign:     {PhaseHeld, PhaseActive},
	KindUnassign:   {PhaseHeld, PhaseActive},
	KindIntentRef:  {PhaseActive},
	KindEscalation: {PhaseHeld, PhaseActive},
	KindResolution: {PhaseHeld, PhaseActive},
	KindSeal:       {PhaseHeld, PhaseActive},
	KindSplit:      {PhaseHeld},
	KindMerge:      {PhaseHeld},
	KindDelegate:   {PhaseHeld, PhaseActive},
	KindAnnul:      {PhaseHeld, PhaseActive},
}

// Reduce folds a chain into the role it describes, refusing the first record
// that is inadmissible. An empty chain reduces to the zero state.
//
// Reduce is exactly a left fold over Advance and nothing more, so folding a
// prefix and continuing from that state gives the same answer as folding the
// whole chain. Snapshots and seals depend on that being true by construction
// rather than by testing every path.
func Reduce(records []Record) (RoleState, error) {
	var state RoleState
	for _, r := range records {
		next, err := Advance(state, r)
		if err != nil {
			return RoleState{}, err
		}
		state = next
	}
	return state, nil
}

// Advance admits one record against a state and applies it. It is the single
// entry point: there is no way to apply a record without admitting it first,
// which is what keeps the laws from becoming something a caller can skip.
func Advance(state RoleState, r Record) (RoleState, error) {
	if err := Admissible(state, r); err != nil {
		return RoleState{}, err
	}
	digest, err := DigestOf(r)
	if err != nil {
		return RoleState{}, err
	}
	return apply(state, r, digest), nil
}

// Admissible reports whether r may extend state, without applying it. A writer
// calls this to fail early; the home calls Advance under its lock.
//
// The check order is load-bearing. Identity is settled BEFORE chain position,
// because the stale writer that matters is the one that re-read the tip and
// therefore presents a perfectly correct prev. Checking position first reports
// that chain as healthy and misdiagnoses the exact failure this exists to catch.
func Admissible(state RoleState, r Record) error {
	if err := checkNamespace(state, r); err != nil {
		return err
	}
	// Terminal comes before well-formedness on purpose. A retired chain accepts
	// nothing, so what is wrong with the record is not the useful fact — and a
	// retired role has no holder, which would otherwise make every late write
	// report a missing incarnation instead of the closed chain that caused it.
	if state.Phase == PhaseRetired {
		return refuse(ReasonRetired, r.Seq, "the role is retired; %s cannot extend it", r.Kind)
	}
	if err := ValidateRecord(r); err != nil {
		return err
	}
	if err := checkWriter(state, r); err != nil {
		return err
	}
	if err := checkPosition(state, r); err != nil {
		return err
	}
	if r.KindClass != ClassStructural {
		return nil
	}
	if err := checkPhase(state, r); err != nil {
		return err
	}
	return checkTransition(state, r)
}

// checkNamespace pins a chain to one role in one tenant. The zero state adopts
// whatever the genesis record declares.
func checkNamespace(state RoleState, r Record) error {
	if state.Phase == PhaseVoid {
		return nil
	}
	if r.Tenant != state.Tenant {
		return refuse(ReasonTenantMismatch, r.Seq, "tenant %q, chain is %q", r.Tenant, state.Tenant)
	}
	if r.Role != state.Role {
		return refuse(ReasonRoleMismatch, r.Seq, "role %q, chain is %q", r.Role, state.Role)
	}
	return nil
}

// checkWriter settles identity: is this writer the one that currently holds the
// role, and is its authority current. Both are checked before chain position.
func checkWriter(state RoleState, r Record) error {
	if err := checkFence(state, r); err != nil {
		return err
	}
	if r.Incarnation == "" {
		return nil
	}
	if state.Holder == "" {
		return refuse(ReasonNotHeld, r.Seq, "%s names an incarnation but nobody holds the role", r.Kind)
	}
	if r.Incarnation != state.Holder {
		return refuse(ReasonStaleIncarnation, r.Seq,
			"incarnation %s was displaced; the role is held by %s", short(r.Incarnation), short(state.Holder))
	}
	return nil
}

// checkFence enforces the high-water rule. Authority is computed from chain
// position rather than stored, so a fence that goes backwards is a credential
// minted before a takeover being spent after it.
func checkFence(state RoleState, r Record) error {
	if r.Fence >= state.Fence {
		return nil
	}
	return refuse(ReasonFenceRegression, r.Seq, "fence %d is below the high-water mark %d", r.Fence, state.Fence)
}

// checkPosition is the compare-and-swap: contiguous sequence and a prev that is
// the tip the writer had to read first. One condition covers both a gap and a
// duplicate.
func checkPosition(state RoleState, r Record) error {
	if r.Seq != state.Seq+1 {
		return refuse(ReasonSeqGap, r.Seq, "chain is at seq %d; next must be %d", state.Seq, state.Seq+1)
	}
	if r.Prev != state.Tip {
		return refuse(ReasonPrevMismatch, r.Seq, "prev %s is not the tip %s", short(r.Prev), short(state.Tip))
	}
	return nil
}

// checkPhase enforces genesis placement and the state machine's arrows.
func checkPhase(state RoleState, r Record) error {
	if r.Kind == KindCharter && state.Phase != PhaseVoid {
		return refuse(ReasonGenesisMisplaced, r.Seq, "a chain has exactly one charter, at seq 1")
	}
	if r.Kind != KindCharter && state.Phase == PhaseVoid {
		return refuse(ReasonGenesisMisplaced, r.Seq, "seq 1 must be a %s, got %s", KindCharter, r.Kind)
	}
	if slices.Contains(phaseLaw[r.Kind], state.Phase) {
		return nil
	}
	if r.Kind == KindAttach {
		return refuse(ReasonAlreadyHeld, r.Seq,
			"the role is held by %s; displacing a holder is a %s", short(state.Holder), KindTakeover)
	}
	if state.Phase == PhaseChartered {
		return refuse(ReasonNotHeld, r.Seq, "%s requires a holder", r.Kind)
	}
	return refuse(ReasonClaimActive, r.Seq, "%s cannot be written while acting on %s", r.Kind, state.Active)
}

// checkTransition applies each kind's own preconditions. Kinds with no
// precondition beyond their phase are absent, and default to admitted.
func checkTransition(state RoleState, r Record) error {
	switch r.Kind {
	case KindCharter:
		return checkCharter(r)
	case KindRecharter:
		return checkRecharter(state, r)
	case KindTakeover:
		return checkTakeover(state, r)
	case KindClaim:
		return checkClaim(state, r)
	case KindYield, KindComplete, KindAbandon:
		return checkTerminal(state, r)
	case KindAssign:
		return checkAssign(state, r)
	case KindUnassign:
		return checkUnassign(state, r)
	case KindIntentRef:
		return checkIntent(state, r)
	case KindResolution:
		return checkResolution(state, r)
	case KindRetire, KindSplit, KindMerge:
		return checkTeardown(state, r)
	case KindAnnul:
		return checkAnnul(state, r)
	}
	return nil
}

// checkCharter enforces the reader gate. A chain whose genesis demands a reader
// above this one is refused whole: interpreting part of it is how an old reader
// skips a takeover and concludes it still holds the role.
func checkCharter(r Record) error {
	if r.Terms.MinReader > Version {
		return refuse(ReasonMinReader, r.Seq,
			"chain requires reader version %d, this reader is %d; upgrade", r.Terms.MinReader, Version)
	}
	return nil
}

// checkRecharter keeps min_reader monotone. Lowering it would let an older
// reader back into a chain that has since recorded kinds it cannot classify.
func checkRecharter(state RoleState, r Record) error {
	if err := checkCharter(r); err != nil {
		return err
	}
	if r.Terms.MinReader < state.Terms.MinReader {
		return refuse(ReasonMinReader, r.Seq,
			"min_reader %d is below the chain's %d; it is monotone", r.Terms.MinReader, state.Terms.MinReader)
	}
	return nil
}

// checkTakeover enforces that only a charter-named supervisor may displace a
// holder. The charter is the only place this can be stated: asking the holder
// to authorize its own replacement is exactly the case where it is unreachable.
func checkTakeover(state RoleState, r Record) error {
	if slices.Contains(state.Terms.Supervisors, r.Subject.Party) {
		return nil
	}
	return refuse(ReasonNotSupervisor, r.Seq,
		"%s is not a supervisor of this role", r.Subject.Party)
}

// checkClaim enforces L2 and L3's inheritance rule: act on work you hold, one
// at a time, with no inherited obligation left unresolved.
func checkClaim(state RoleState, r Record) error {
	if state.Dangling != "" {
		return refuse(ReasonDanglingClaim, r.Seq,
			"a predecessor's claim on %s is unresolved; yield, complete or abandon it first", state.Dangling)
	}
	if len(state.OpenIntents) > 0 {
		return refuse(ReasonOpenIntent, r.Seq,
			"effect %s is still open; resolve it before claiming", state.OpenIntents[0])
	}
	if !state.Holds(r.Subject.Work) {
		return refuse(ReasonWorkNotHeld, r.Seq,
			"%s is not held; claiming chooses among assignments, it does not acquire one", r.Subject.Work)
	}
	return nil
}

// checkTerminal admits a claim terminal in either of its two modes: closing the
// claim this incarnation holds, or resolving one a predecessor orphaned.
func checkTerminal(state RoleState, r Record) error {
	if state.Phase == PhaseActive {
		if r.Subject.Work != state.Active {
			return refuse(ReasonClaimSubjectMismatch, r.Seq,
				"%s names %s; the active claim is %s", r.Kind, r.Subject.Work, state.Active)
		}
		return nil
	}
	if state.Dangling == "" {
		return refuse(ReasonNoClaim, r.Seq, "%s with no claim to end", r.Kind)
	}
	if r.Subject.Work != state.Dangling {
		return refuse(ReasonClaimSubjectMismatch, r.Seq,
			"%s names %s; the dangling claim is %s", r.Kind, r.Subject.Work, state.Dangling)
	}
	return nil
}

func checkAssign(state RoleState, r Record) error {
	if !state.Holds(r.Subject.Work) {
		return nil
	}
	return refuse(ReasonWorkAlreadyHeld, r.Seq, "%s is already held", r.Subject.Work)
}

func checkUnassign(state RoleState, r Record) error {
	if !state.Holds(r.Subject.Work) {
		return refuse(ReasonWorkNotHeld, r.Seq, "%s is not held", r.Subject.Work)
	}
	if r.Subject.Work != state.Active {
		return nil
	}
	return refuse(ReasonClaimActive, r.Seq, "%s is the active claim; end it before unassigning", r.Subject.Work)
}

// checkIntent bounds a role to one outstanding effect. That bound is what makes
// T2 tractable: recovery is a fixed procedure over a single record, not a search.
func checkIntent(state RoleState, r Record) error {
	if slices.Contains(state.OpenIntents, r.Subject.Effect) {
		return refuse(ReasonOpenIntent, r.Seq, "effect %s is already open", r.Subject.Effect)
	}
	if len(state.OpenIntents) == 0 {
		return nil
	}
	return refuse(ReasonOpenIntent, r.Seq,
		"effect %s is still open; a role has at most one outstanding effect", state.OpenIntents[0])
}

func checkResolution(state RoleState, r Record) error {
	if r.Subject.Effect != "" && !slices.Contains(state.OpenIntents, r.Subject.Effect) {
		return refuse(ReasonIntentUnknown, r.Seq, "effect %s is not open", r.Subject.Effect)
	}
	if r.Subject.Target != "" && !slices.Contains(state.OpenEscalations, r.Subject.Target) {
		return refuse(ReasonEscalationUnknown, r.Seq, "escalation %s is not open", short(r.Subject.Target))
	}
	return nil
}

// checkTeardown refuses to end a role that still owes something. Dropping work
// is an unassign, which is explicit and recorded; a retire that silently dropped
// three assignments would make them disappear from every roster at once.
//
// The dangling check is here because an exhaustive walk of the state space found
// its absence: takeover, then unassign, then retire reaches a RETIRED role with
// an unresolved claim. Nothing may extend a retired chain, so that obligation is
// lost permanently — the exact silent disappearance L3 says must not be
// representable. Neither 96% line coverage nor a 98.5% mutation score found it,
// because it needs one specific three-record sequence and no hand-written test
// thought to try it.
func checkTeardown(state RoleState, r Record) error {
	if state.Dangling != "" {
		return refuse(ReasonDanglingClaim, r.Seq,
			"%s would strand the unresolved claim on %s behind a terminal chain; discharge it first",
			r.Kind, state.Dangling)
	}
	if len(state.OpenIntents) > 0 {
		return refuse(ReasonOpenIntent, r.Seq,
			"%s would strand effect %s with no recorded outcome; resolve it first",
			r.Kind, state.OpenIntents[0])
	}
	if len(state.OpenEscalations) > 0 {
		return refuse(ReasonOpenEscalation, r.Seq,
			"%s would strand %d unanswered escalation(s); a human's answer cannot append to a terminal chain",
			r.Kind, len(state.OpenEscalations))
	}
	if len(state.Held) == 0 {
		return nil
	}
	return refuse(ReasonOpenWork, r.Seq,
		"the role still holds %d work item(s), starting with %s; unassign them first",
		len(state.Held), state.Held[0].Work)
}

// checkAnnul restricts an annul to the record it immediately follows.
//
// That is narrower than "withdraw any earlier record", and the narrowness is
// forced rather than chosen: verifying membership of an arbitrary digest would
// require the fold to retain every digest it has ever seen, and a fold whose
// size grows with the chain cannot do the one job it exists for. Withdrawing an
// older record needs a mechanism that reads the chain rather than the fold —
// the Audit pass — and does not exist yet. Refusing here is the honest half of
// that: an annul that cannot be verified is not quietly recorded as if it had
// been.
func checkAnnul(state RoleState, r Record) error {
	if r.Subject.Target == state.Tip {
		return nil
	}
	return refuse(ReasonAnnulUnknown, r.Seq,
		"%s is not the record this annul follows; a fold can only verify the tip", short(r.Subject.Target))
}

// apply advances the state. It runs only after Admissible, so it never fails
// and never re-checks: a second opinion here would be a second place for the
// law to live.
func apply(state RoleState, r Record, digest string) RoleState {
	state.Seq = r.Seq
	state.Tip = digest
	state.Degraded = r.Kind == KindMark && r.KindClass == ClassAdvisory
	state.Fence = max(state.Fence, r.Fence)
	if r.NextDue != "" {
		state.NextDue = r.NextDue
	}
	if r.KindClass != ClassStructural {
		return state
	}
	return applyStructural(state, r, digest)
}

func applyStructural(state RoleState, r Record, digest string) RoleState {
	switch r.Kind {
	case KindCharter:
		state.Tenant, state.Role = r.Tenant, r.Role
		state.Terms = *r.Terms
		state.Phase = PhaseChartered
	case KindRecharter:
		state.Terms = *r.Terms
	case KindAttach:
		state.Holder = digest
		state.Phase = PhaseHeld
	case KindTakeover:
		state = orphan(state)
		state.Holder = digest
		state.Phase = PhaseHeld
	case KindRevoke:
		state = orphan(state)
		state.Holder = ""
		state.Phase = PhaseChartered
	case KindRelease:
		state.Holder = ""
		state.Phase = PhaseChartered
	case KindClaim:
		state.Active = r.Subject.Work
		state.Phase = PhaseActive
	case KindYield:
		state = endClaim(state, r.Subject.Work)
	case KindComplete, KindAbandon:
		state = endClaim(state, r.Subject.Work)
		state = drop(state, r.Subject.Work)
	case KindAssign:
		state.Held = append(state.Held, Assignment{
			Work: r.Subject.Work, Digest: r.Subject.Digest, Party: r.Subject.Party,
		})
	case KindUnassign:
		state = drop(state, r.Subject.Work)
	case KindIntentRef:
		state.OpenIntents = append(state.OpenIntents, r.Subject.Effect)
	case KindEscalation:
		state.OpenEscalations = append(state.OpenEscalations, digest)
	case KindResolution:
		state.OpenIntents = without(state.OpenIntents, r.Subject.Effect)
		state.OpenEscalations = without(state.OpenEscalations, r.Subject.Target)
	case KindSeal:
		state.Seal = digest
	case KindAnnul:
		state.Annulled = append(state.Annulled, r.Subject.Target)
	case KindRetire, KindSplit, KindMerge:
		state.Holder = ""
		state.Phase = PhaseRetired
	}
	return state
}

// orphan records that a holder was displaced mid-claim. The claim does not
// vanish — it becomes an obligation the successor must discharge before it may
// claim anything of its own.
func orphan(state RoleState) RoleState {
	if state.Phase != PhaseActive {
		return state
	}
	state.Dangling = state.Active
	state.Active = ""
	return state
}

// endClaim closes whichever claim the terminal named: the active one, or the
// dangling one a predecessor left.
func endClaim(state RoleState, work string) RoleState {
	if state.Dangling == work {
		state.Dangling = ""
		return state
	}
	state.Active = ""
	state.Phase = PhaseHeld
	return state
}

func drop(state RoleState, work string) RoleState {
	i := state.held(work)
	if i < 0 {
		return state
	}
	state.Held = slices.Delete(slices.Clone(state.Held), i, i+1)
	return state
}

func without(items []string, drop string) []string {
	i := slices.Index(items, drop)
	if drop == "" || i < 0 {
		return items
	}
	return slices.Delete(slices.Clone(items), i, i+1)
}

// short abbreviates a digest for a message. Refusal detail is for humans; the
// Reason is what anything branches on.
func short(digest string) string {
	if len(digest) <= 14 {
		return digest
	}
	return digest[:14] + "…"
}
