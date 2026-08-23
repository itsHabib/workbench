package org

import (
	"errors"
	"fmt"
)

// A refusal is a value, not a sentence.
//
// Every law this package enforces refuses with a stable identifier a caller can
// branch on, a test can pin, and an operator can grep for. Prose goes in the
// detail; the identifier never changes without a deliberate scheme bump, which
// is why the identifier set is frozen into a golden digest in the tests.
//
// This is hack-obligation's shape, kept because it earned its keep: a refusal
// that is only a formatted string forces every consumer to match on prose, and
// prose is exactly the part a well-meaning edit changes.

// Refusal is one law's rejection: the stable Reason, the record it fired on,
// and human detail.
type Refusal struct {
	// Reason is the stable identifier. Compare against the Reason* constants.
	Reason string
	// Seq is the chain position of the offending record, 0 when the refusal is
	// not about one particular record.
	Seq int64
	// Detail is prose for a human. Nothing may branch on it.
	Detail string
}

// Error renders the refusal. The Reason leads so a truncated log line still
// carries the identifier.
func (r *Refusal) Error() string {
	if r.Seq == 0 {
		return fmt.Sprintf("org: %s: %s", r.Reason, r.Detail)
	}
	return fmt.Sprintf("org: %s at seq %d: %s", r.Reason, r.Seq, r.Detail)
}

// Is lets errors.Is match two refusals by Reason, so a caller can write
// errors.Is(err, &org.Refusal{Reason: org.ReasonSeqGap}) without knowing the
// seq or the detail.
func (r *Refusal) Is(target error) bool {
	var t *Refusal
	if !errors.As(target, &t) {
		return false
	}
	return t.Reason == r.Reason
}

// refuse builds a refusal. Unexported: every refusal in this package names one
// of the frozen reasons, and a constructor taking an arbitrary string would
// make that a convention rather than a property.
func refuse(reason string, seq int64, format string, args ...any) *Refusal {
	return &Refusal{Reason: reason, Seq: seq, Detail: fmt.Sprintf(format, args...)}
}

// RefusalReason returns the stable identifier of a refusal, or "" if err is not
// one. This is the machine-readable accessor consumers should use rather than
// matching on Error().
func RefusalReason(err error) string {
	var r *Refusal
	if !errors.As(err, &r) {
		return ""
	}
	return r.Reason
}

// The frozen refusal vocabulary.
//
// Record-level laws (validate.go) — a single record is malformed on its own
// terms, independent of any chain.
const (
	// ReasonSchemeUnsupported fires when a record declares a structural kind
	// this reader does not know, or a scheme or version it cannot interpret.
	// The remedy is always an upgrade, never a fix to the chain.
	ReasonSchemeUnsupported = "scheme_unsupported"
	// ReasonKindClassMismatch fires when a record's declared class disagrees
	// with the class this reader has for that kind. A writer that mislabels a
	// takeover as advisory would get it silently skipped by every older reader.
	ReasonKindClassMismatch = "kind_class_mismatch"
	// ReasonMalformedID fires on a role, tenant, or incarnation id that does
	// not match the grammar.
	ReasonMalformedID = "malformed_id"
	// ReasonMalformedRef fires on a ref whose URI is empty or whose trust label
	// is not one of the three.
	ReasonMalformedRef = "malformed_ref"
	// ReasonMalformedWorkURI fires on a work reference that is not a
	// scheme-qualified URI. The scheme is the field that decides whether this
	// substrate can attach to work an organization already has.
	ReasonMalformedWorkURI = "malformed_work_uri"
	// ReasonSubjectMissing fires when a kind's required subject member is
	// absent — a claim with no work, an assign with no assignee.
	ReasonSubjectMissing = "subject_missing"
	// ReasonSubjectUnexpected fires when a kind carries a subject member its
	// law does not define. Refusing rather than ignoring keeps a writer from
	// encoding meaning a reader will not look at.
	ReasonSubjectUnexpected = "subject_unexpected"
	// ReasonCharterImmutable fires when a record that is not a charter or
	// recharter carries Terms.
	ReasonCharterImmutable = "charter_immutable"
	// ReasonTermsMissing fires when a charter or recharter carries no Terms.
	ReasonTermsMissing = "terms_missing"
	// ReasonScopeEmpty fires when a charter's scope names no work reference.
	// There is no unscoped role (L1).
	ReasonScopeEmpty = "scope_empty"
	// ReasonIncarnationMissing fires when a record that must name its writer
	// does not. ReasonIncarnationUnexpected fires when a charter does.
	ReasonIncarnationMissing    = "incarnation_missing"
	ReasonIncarnationUnexpected = "incarnation_unexpected"
	// ReasonFenceInvalid fires on a negative fence.
	ReasonFenceInvalid = "fence_invalid"
	// ReasonBodyClassMissing fires when a record references a body blob without
	// classifying it. An unclassified blob has no retention or erasure rule,
	// and a blob store cannot invent one later.
	ReasonBodyClassMissing = "body_class_missing"
)

// Chain-level laws (reduce.go) — a record is well-formed but inadmissible
// given the history before it.
const (
	// ReasonGenesisMisplaced fires when seq 1 is not a charter, or a charter
	// appears anywhere but seq 1. Exactly one genesis, at the front.
	ReasonGenesisMisplaced = "genesis_misplaced"
	// ReasonSeqGap fires when a record's seq is not exactly one past the tip.
	// One condition covers both a gap and a duplicate.
	ReasonSeqGap = "seq_gap"
	// ReasonPrevMismatch fires when a record's prev is not the tip's digest.
	// This is the compare-and-swap, and it is the ownership mechanism.
	ReasonPrevMismatch = "prev_mismatch"
	// ReasonStaleIncarnation fires when an incarnation writes after being
	// displaced by a takeover or having released.
	//
	// It is checked BEFORE chain position, deliberately. The stale writer that
	// matters is the one that re-read the tip and therefore presents a
	// perfectly correct prev; a position-first order reports that chain as
	// healthy and misdiagnoses the one failure this law exists to catch.
	ReasonStaleIncarnation = "stale_incarnation"
	// ReasonTenantMismatch and ReasonRoleMismatch fire when a record's
	// namespace differs from the chain's. A chain is one role in one tenant.
	ReasonTenantMismatch = "tenant_mismatch"
	ReasonRoleMismatch   = "role_mismatch"
	// ReasonNotHeld fires when a transition requires a holder and there is
	// none — a claim on an unheld role, a release with nobody attached.
	ReasonNotHeld = "not_held"
	// ReasonAlreadyHeld fires when an attach lands on a role someone already
	// holds. The second incarnation is refused and the log is uncorrupted;
	// displacing a holder is a takeover, which is a different record with a
	// different authorization.
	ReasonAlreadyHeld = "already_held"
	// ReasonNotSupervisor fires when a takeover is written by a role the
	// charter does not name as a supervisor.
	ReasonNotSupervisor = "not_supervisor"
	// ReasonClaimActive fires when a claim lands while another is active. An
	// incarnation may hold many work items and act on exactly one (L2) — and
	// that is what lets an effect stamp DERIVE its work reference from state
	// instead of taking it from the caller.
	ReasonClaimActive = "claim_active"
	// ReasonNoClaim fires when a claim terminal lands with nothing active.
	ReasonNoClaim = "no_claim"
	// ReasonClaimSubjectMismatch fires when a claim terminal names a work item
	// other than the active one.
	ReasonClaimSubjectMismatch = "claim_subject_mismatch"
	// ReasonWorkNotHeld fires when a claim names work the role does not hold.
	// Claiming is choosing among assignments, never acquiring one.
	ReasonWorkNotHeld = "work_not_held"
	// ReasonWorkAlreadyHeld fires when an assign duplicates a held work item.
	ReasonWorkAlreadyHeld = "work_already_held"
	// ReasonDanglingClaim fires when an incarnation claims while a predecessor's
	// claim is still open. Silent disappearance is not a representable outcome
	// (L3): the successor must resolve the orphan before it may claim anything.
	ReasonDanglingClaim = "dangling_claim"
	// ReasonOpenIntent fires when a claim lands while an effect intent is still
	// open. A role has at most one outstanding unknown effect, which is what
	// makes recovery a fixed procedure over one record instead of a search.
	ReasonOpenIntent = "open_intent"
	// ReasonIntentUnknown fires when a resolution closes an intent that is not
	// open.
	ReasonIntentUnknown = "intent_unknown"
	// ReasonEscalationUnknown fires when a resolution closes an escalation that
	// is not open. A human's answer arriving for a question nobody asked is a
	// misrouted answer, and accepting it would let one escalation be closed by
	// another's resolution.
	ReasonEscalationUnknown = "escalation_unknown"
	// ReasonRetired fires on any record after the role is retired. Terminal
	// means terminal.
	ReasonRetired = "retired"
	// ReasonOpenWork fires when a retire lands while the role still holds work.
	// Dropping it is unassign, which is explicit and recorded.
	ReasonOpenWork = "open_work"
	// ReasonMinReader fires when the chain's charter demands a reader version
	// above this one. Refusing the whole chain is the point: interpreting part
	// of it is how two holders happen.
	ReasonMinReader = "min_reader"
	// ReasonFenceRegression fires when a record's fence is below the highest
	// this chain has seen. Authority is computed from chain position, so a
	// fence that goes backwards is a displaced writer spending a dead grant.
	ReasonFenceRegression = "fence_regression"
	// ReasonAnnulUnknown fires when an annul names a record that is not in the
	// chain before it.
	ReasonAnnulUnknown = "annul_unknown"
)

// Reasons is the frozen refusal vocabulary in a stable order. The tests digest
// it, so renaming or dropping an identifier is a test failure rather than a
// silent break of every consumer that branched on it.
var Reasons = []string{
	ReasonSchemeUnsupported,
	ReasonKindClassMismatch,
	ReasonMalformedID,
	ReasonMalformedRef,
	ReasonMalformedWorkURI,
	ReasonSubjectMissing,
	ReasonSubjectUnexpected,
	ReasonCharterImmutable,
	ReasonTermsMissing,
	ReasonScopeEmpty,
	ReasonIncarnationMissing,
	ReasonIncarnationUnexpected,
	ReasonFenceInvalid,
	ReasonBodyClassMissing,
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
