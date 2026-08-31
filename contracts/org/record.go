package org

// This file is the record vocabulary: the fixed-shape spine every continuity
// record carries, the closed kind set, and the two kind classes that make
// schema evolution safe.
//
// # Why the spine carries structure and not prose
//
// A record splits in two. The SPINE is fixed-shape, canonically encoded,
// hash-chained, and retained forever. The BODY is distilled prose, stored as a
// content-addressed blob, referenced here only by digest, and erasable —
// deleting a blob leaves the chain verifying.
//
// That split has a consequence this file is built around: the fold must be
// computable from the spine alone. A transition parameter that lived in the
// body would make a role's ownership state depend on a blob somebody is
// entitled to delete. So every identifier a transition needs — which work item
// a claim is about, which role may take over, which effect an intent names —
// lives on the spine in Subject or Terms, and only prose lives in the body.
//
// # Reconciliations
//
// The design vision (docs/features/org/vision.md) specifies the state machine
// in §3.9 and the kind list in §3.3, and the two were written at different
// times. Three gaps are closed here, deliberately and visibly, rather than
// left for a reader to guess:
//
//  1. §3.9's state machine names claim, yield and complete as transitions;
//     §3.3's structural kind list predates it and omits them. They are
//     structural kinds here — a reader that skipped one would believe a role
//     was idle while it was acting, which is the exact failure kind classes
//     exist to prevent.
//  2. §3.3 uses abandon for role teardown ("abandon names every open item");
//     §3.9 uses it as a claim terminal. The state machine wins: abandon
//     requires a work subject and ends a claim. Retiring a role with open
//     assignments is unassign-then-retire, which keeps every drop explicit.
//  3. §3.3 lists handoff among L3's terminal records but not among the kinds.
//     It is not a kind here: a handoff is release followed by the successor's
//     attach, and inventing a third spelling for a sequence that already has
//     one adds a state without adding a fact.
//
// Each is a judgment call made to ship, not a settled decision. They belong in
// the vision doc before P1's gate.

// Version is the record-spine version. It is distinct from Scheme (the
// canonical ENCODING version): the shape of a record and the bytes a shape
// hashes to change for different reasons and must be able to move separately.
const Version = 1

// SchemaVersion is the wire identifier readers gate on, mirroring the
// contracts/execution and contracts/authority posture.
const SchemaVersion = "org-record.v1"

// Kind classes. The class is carried on the wire rather than derived from a
// table, because the whole point is what an OLD reader does with a kind it has
// never heard of — and a reader that has to look the kind up in its own table
// cannot classify one that is not in it.
//
// The failure this prevents is specific: an old reader that silently skips an
// unknown takeover concludes it still holds the role. Two holders, produced by
// routine version skew, with every hash valid.
const (
	// ClassStructural marks a record that moves the state machine. An unknown
	// structural kind makes the fold refuse ReasonSchemeUnsupported.
	ClassStructural = "structural"
	// ClassAdvisory marks a record that carries narrative and decides nothing.
	// An unknown advisory kind is preserved verbatim and skipped.
	ClassAdvisory = "advisory"
)

// Structural kinds: the closed set of transitions. Adding one is a scheme
// change, because every existing reader must refuse it rather than skip it.
const (
	// KindCharter creates the role. It is genesis: seq 1, no prev, and the only
	// kind that may carry founding Terms.
	KindCharter = "charter"
	// KindAttach binds an incarnation to the role. Requires a charter whose
	// scope names at least one work reference (L1) and prev == tip.
	KindAttach = "attach"
	// KindClaim makes exactly one held work item the active one (L2).
	KindClaim = "claim"
	// KindYield ends the active claim with the work unfinished and still held.
	KindYield = "yield"
	// KindComplete ends the active claim asserting the work is done. Whether
	// that assertion is a fact is the work plane's question, not this one's.
	KindComplete = "complete"
	// KindAbandon ends the active claim and drops the work.
	KindAbandon = "abandon"
	// KindTakeover replaces the holder. Only a role the charter names as a
	// supervisor may write one.
	KindTakeover = "takeover"
	// KindRelease ends the hold cleanly, returning the role to Chartered.
	KindRelease = "release"
	// KindRetire is terminal for the role. Refused while any work is held.
	KindRetire = "retire"
	// KindRecharter changes the inherited terms. Authored under the parent
	// role's grant; a checkpoint attempting the same is ReasonCharterImmutable.
	KindRecharter = "recharter"
	// KindAssign adds a work item to the held set, pinning subject_digest so a
	// rewritten ticket reads as drift rather than being silently absorbed.
	KindAssign = "assign"
	// KindUnassign removes a work item from the held set.
	KindUnassign = "unassign"
	// KindIntentRef records that an effect intent is open against this role.
	// The fold tracks it so a replacement incarnation inherits the obligation.
	KindIntentRef = "intent-ref"
	// KindEscalation opens a question for a human. It closes with a resolution.
	KindEscalation = "escalation"
	// KindResolution closes an escalation, or closes an open intent.
	KindResolution = "resolution"
	// KindSeal content-addresses the folded state so a cold read is
	// last seal + tail, and bodies behind it become erasable.
	KindSeal = "seal"
	// KindSplit is terminal for the source role and genesis-referencing for the
	// targets, so lineage survives a reorg.
	KindSplit = "split"
	// KindMerge is terminal for the source role, folding its tail into a target.
	KindMerge = "merge"
	// KindDelegate records that authority was attenuated to a child role.
	KindDelegate = "delegate"
	// KindRevoke advances the fence, killing every credential minted against
	// the prior position with nothing sent to the holder.
	KindRevoke = "revoke"
	// KindAnnul marks an earlier record as withdrawn. The record stays; the
	// chain still verifies; the fold stops counting it.
	KindAnnul = "annul"
)

// Advisory kinds: narrative that decides nothing. An unknown one is preserved
// and skipped, which is safe precisely because none of them can move the
// machine.
const (
	// KindCheckpoint is the distilled state of a turn. Written by the host's
	// distiller, never by the working agent — a required end-of-turn tool call
	// is a verb wearing a costume, and verbs do not survive.
	KindCheckpoint = "checkpoint"
	// KindMark is a mechanical observation the host can make without the
	// model's cooperation: tool calls, files touched, effect ids, commands run.
	KindMark = "mark"
	// KindNote is free narrative.
	KindNote = "note"
	// KindReport is a generated summary.
	KindReport = "report"
	// KindMessage is inter-role correspondence.
	KindMessage = "message"
)

// Trust labels where a reference's content came from, so a decision derived
// from untrusted input is visible in the fold and in the audit rather than
// indistinguishable from one the operator made. The chain records CLAIMS by an
// incarnation; only broker outcomes are facts, and a prompt-injected checkpoint
// is durable and inherited. This label is one of the two structural mitigations
// that bound the blast radius — the other is that permitted effect classes are
// pinned in the charter.
const (
	TrustOperator = "operator"
	TrustInternal = "internal"
	TrustExternal = "external"
)

// Ref is one thing a record was derived from, carrying where it came from.
type Ref struct {
	// URI names the referent: a work URI, a blob digest, a URL.
	URI string `json:"uri"`
	// Trust is one of TrustOperator, TrustInternal, TrustExternal.
	Trust string `json:"trust"`
}

// Canonical states Ref's canonical shape. Field membership here is checked
// against the Go struct by reflection in the conformance test, so adding a
// field without adding it here fails the build rather than silently shrinking
// what the digest covers.
func (r Ref) Canonical() Value {
	return Map(
		F("uri", String(r.URI)),
		F("trust", String(r.Trust)),
	)
}

// Subject is a record's structural parameter: the identifiers its transition
// needs, and nothing else. It is on the spine, not in the body, because the
// fold must survive body erasure (see the file comment).
//
// It is a fixed shape with optional members rather than a per-kind union
// because a comparable struct keeps the fold's equality checks trivial and
// keeps one canonical form for every kind. Which members a kind requires is
// validate.go's law, not this type's.
type Subject struct {
	// Work is a work URI with a scheme: github:acme/api#88, jira:PROJ-412,
	// dossier:proj/phase/task. The scheme is what lets this substrate attach to
	// work an organization already has instead of only to the operator's own
	// tracker.
	Work string `json:"work,omitempty"`
	// Digest pins the work item's content at assignment time, so a renamed or
	// rewritten ticket produces drift rather than silent absorption.
	Digest string `json:"digest,omitempty"`
	// Party is a role id: the supervisor on a takeover, the assignee on an
	// assign, the child on a delegate, the target on a split or merge.
	Party string `json:"party,omitempty"`
	// Effect is an effect id, on intent-ref and on the resolution that closes
	// one.
	Effect string `json:"effect,omitempty"`
	// Target names an earlier record this one acts on: the annulled record, the
	// escalation a resolution closes.
	Target string `json:"target,omitempty"`
}

// Canonical states Subject's canonical shape.
func (s Subject) Canonical() Value {
	return Map(
		F("work", String(s.Work)),
		F("digest", String(s.Digest)),
		F("party", String(s.Party)),
		F("effect", String(s.Effect)),
		F("target", String(s.Target)),
	)
}

// Terms are the inherited charter: what the role is for and what it may spend.
// They live in the charter, are inherited by the fold, and change only by
// recharter. A checkpoint attempting to write one is ReasonCharterImmutable.
//
// The reason they are immutable from a checkpoint is not tidiness. If each
// incarnation could rewrite the goal, a role's purpose would be subject to a
// dozen rounds of lossy re-summarization with nothing to compare against.
type Terms struct {
	// Scope names the work references this role owns. A lead's scope is a
	// scope, a maintainer's an area, an IC's a task; one rule covers all three
	// because a work URI's scheme carries the difference.
	Scope []string `json:"scope,omitempty"`
	// Tier is inert and no longer settable. What is wrong with it is not that
	// a per-role ceiling is incoherent: compared against one change at a time,
	// a fixed ceiling would bound each independently, the way any capability
	// bound works.
	//
	// The first defect is that nothing enforces it — there is no tier refusal
	// reason, and no reduction consults it. The second is that enforcing it
	// would duplicate authorization that already exists where the evidence is.
	// Risk is a property of a change, not of the role holding it, and only the
	// change carries what a tier decision needs: the classifier floors a real
	// diff, and a grant ceilings the exact head (grant_tier_exceeded). A
	// role-level scalar would be a third decision point that has seen neither.
	// Read docs/auto-mode-defaults.md — "the classifier decides tier; a grant
	// decides ceiling; keep the two axes separate."
	//
	// The field survives only because Terms.Canonical projects it: dropping it
	// changes every charter digest and no existing chain would fold under
	// canon/v1. Retire it with the next scheme bump, not before.
	Tier string `json:"tier,omitempty"`
	// EffectClasses pins what the role may do to the outside world. It bounds
	// an injected incarnation to what the role could do anyway.
	EffectClasses []string `json:"effect_classes,omitempty"`
	// Supervisors names the roles that may write a takeover against this one.
	Supervisors []string `json:"supervisors,omitempty"`
	// SpendCeiling, CycleCeiling and ConcurrencyCeiling are the ceilings that
	// are about cost and process rather than security. CycleCeiling encodes the
	// two-fix-rounds rule as an attenuating dimension instead of as prose an
	// agent may or may not read.
	SpendCeiling       int64 `json:"spend_ceiling,omitempty"`
	CycleCeiling       int64 `json:"cycle_ceiling,omitempty"`
	ConcurrencyCeiling int64 `json:"concurrency_ceiling,omitempty"`
	// MinReader is the monotone minimum reader version for this chain, set at
	// genesis and raised only by recharter. A reader below it must refuse the
	// whole chain rather than interpret part of it.
	MinReader int64 `json:"min_reader,omitempty"`
	// Retire is the condition under which the role should be retired. Prose
	// short enough to be an identifier, not a paragraph; the paragraph is body.
	Retire string `json:"retire,omitempty"`
}

// Canonical states Terms' canonical shape.
func (t Terms) Canonical() Value {
	return Map(
		F("scope", Strings(t.Scope)),
		F("tier", String(t.Tier)),
		F("effect_classes", Strings(t.EffectClasses)),
		F("supervisors", Strings(t.Supervisors)),
		F("spend_ceiling", Int(t.SpendCeiling)),
		F("cycle_ceiling", Int(t.CycleCeiling)),
		F("concurrency_ceiling", Int(t.ConcurrencyCeiling)),
		F("min_reader", Int(t.MinReader)),
		F("retire", String(t.Retire)),
	)
}

// Record is one link in a role's continuity chain.
//
// To act you must append; to append you must present the tip you read. Prev is
// that presentation, and it is the whole ownership mechanism: two incarnations
// cannot both hold the tip.
type Record struct {
	// V is the spine version; Scheme is the canonical encoding version.
	V      int64  `json:"v"`
	Scheme string `json:"scheme"`
	// Seq is the chain position, contiguous from 1.
	Seq int64 `json:"seq"`
	// Prev is the digest of the record at Seq-1, empty at genesis.
	Prev string `json:"prev"`
	// Tenant is the root of every namespace and the partition key of every
	// chain, log, blob and anchor.
	Tenant string `json:"tenant"`
	// Role is the durable office this chain belongs to.
	Role string `json:"role"`
	// Kind and KindClass together let an old reader do the right thing with a
	// kind it has never seen. KindClass is on the wire on purpose.
	Kind      string `json:"kind"`
	KindClass string `json:"kind_class"`
	// Incarnation is the disposable session that wrote this record. Empty on
	// charter, which precedes every incarnation.
	Incarnation string `json:"incarnation,omitempty"`
	// Fence is the chain position the writer's authority was minted at.
	// Verifiers keep a per-role high-water mark; a lower fence is a regression.
	Fence int64 `json:"fence"`
	// At is stamped by the HOME, never by the agent. One law, house-shaped: no
	// refusal may depend on a clock the refusing party does not own — so
	// nothing in validate.go or reduce.go reads this field to refuse.
	At string `json:"at"`
	// BodyDigest references the erasable blob carrying this record's prose.
	// Empty means the record has no body, which is the normal case for a
	// structural record. BodyClass is the classification the blob store keys
	// retention and encryption on.
	BodyDigest string `json:"body_digest,omitempty"`
	BodyClass  string `json:"body_class,omitempty"`
	// Refs are what this record was derived from, each carrying its trust.
	Refs []Ref `json:"refs,omitempty"`
	// NextDue is the deadline the writer itself declares for its next append.
	// Liveness is derived against it — never from self-report — so a hung
	// incarnation that never exits is correctly seen as dead.
	NextDue string `json:"next_due,omitempty"`
	// Subject is the transition's structural parameter.
	Subject Subject `json:"subject,omitzero"`
	// Terms is the charter, present only on charter and recharter.
	Terms *Terms `json:"terms,omitempty"`
}

// Canonical states Record's canonical shape — the bytes the chain's digests are
// taken over.
//
// Every exported field appears here, and the conformance test proves it by
// reflection. That test is not decoration: nothing else forces a record type's
// canonical form to include all of its fields, and a field added without one
// here would silently stop being covered by the digest — a chain that looks
// sealed and is not.
//
// A nil Terms encodes as Null rather than being omitted, because "this role has
// no terms" and "this record does not carry terms" must not share a byte
// sequence with a future record that omits the key for a third reason.
func (r Record) Canonical() Value {
	refs := make([]Value, 0, len(r.Refs))
	for _, ref := range r.Refs {
		refs = append(refs, ref.Canonical())
	}
	terms := Null()
	if r.Terms != nil {
		terms = r.Terms.Canonical()
	}
	return Map(
		F("v", Int(r.V)),
		F("scheme", String(r.Scheme)),
		F("seq", Int(r.Seq)),
		F("prev", String(r.Prev)),
		F("tenant", String(r.Tenant)),
		F("role", String(r.Role)),
		F("kind", String(r.Kind)),
		F("kind_class", String(r.KindClass)),
		F("incarnation", String(r.Incarnation)),
		F("fence", Int(r.Fence)),
		F("at", String(r.At)),
		F("body_digest", String(r.BodyDigest)),
		F("body_class", String(r.BodyClass)),
		F("refs", List(refs...)),
		F("next_due", String(r.NextDue)),
		F("subject", r.Subject.Canonical()),
		F("terms", terms),
	)
}

// structuralKinds is the closed set of transitions. A kind absent from this map
// and declared structural is refused; a kind absent and declared advisory is
// skipped. That asymmetry is the entire schema-evolution story.
var structuralKinds = map[string]bool{
	KindCharter:    true,
	KindAttach:     true,
	KindClaim:      true,
	KindYield:      true,
	KindComplete:   true,
	KindAbandon:    true,
	KindTakeover:   true,
	KindRelease:    true,
	KindRetire:     true,
	KindRecharter:  true,
	KindAssign:     true,
	KindUnassign:   true,
	KindIntentRef:  true,
	KindEscalation: true,
	KindResolution: true,
	KindSeal:       true,
	KindSplit:      true,
	KindMerge:      true,
	KindDelegate:   true,
	KindRevoke:     true,
	KindAnnul:      true,
}

var advisoryKinds = map[string]bool{
	KindCheckpoint: true,
	KindMark:       true,
	KindNote:       true,
	KindReport:     true,
	KindMessage:    true,
}

// KnownStructural reports whether kind is a structural kind this reader
// understands.
func KnownStructural(kind string) bool { return structuralKinds[kind] }

// KnownAdvisory reports whether kind is an advisory kind this reader
// understands.
func KnownAdvisory(kind string) bool { return advisoryKinds[kind] }

// DeclaredClass reports the class a kind is declared under, for the kinds this
// reader knows. It exists so a WRITER can stamp the right class; a READER must
// use the class on the wire, since the kinds it needs to classify correctly are
// exactly the ones missing from its own table.
func DeclaredClass(kind string) (string, bool) {
	if structuralKinds[kind] {
		return ClassStructural, true
	}
	if advisoryKinds[kind] {
		return ClassAdvisory, true
	}
	return "", false
}
