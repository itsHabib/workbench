package org

import (
	"regexp"
	"strings"
)

// This file is record-level law: what makes ONE record well-formed on its own
// terms, with no reference to any history. Chain-level admission — sequence,
// prev, who holds what — is reduce.go's, and the split is deliberate: a record
// that is malformed in isolation can be rejected by a writer before it ever
// reaches the home's lock.
//
// Nothing here reads Record.At. Time is stamped by the home, and no refusal may
// depend on a clock the refusing party does not own.

var (
	// idPattern is the grammar for a tenant, role, or party id: colon-separated
	// lowercase segments. The colon is what lets human:mh, lead:platform and
	// ic:contracts-org all be role ids under one rule.
	idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(?::[a-z0-9][a-z0-9._-]*)*$`)
	// workURIPattern requires a scheme. That single requirement is what decides
	// whether this substrate can attach to work an organization already has, or
	// only ever works for one tracker.
	workURIPattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:\S+$`)
	// digestPattern matches the digest form digest.go produces.
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// need states whether a subject member is required, allowed, or refused for a
// kind.
type need uint8

const (
	forbidden need = iota
	optional
	required
)

// subjectLaw is one kind's subject shape. Stating it as data rather than as a
// switch means the law is enumerable — the conformance test walks it to prove
// every structural kind has one, so adding a kind without its law fails the
// build instead of defaulting to "anything goes".
type subjectLaw struct {
	work, digest, party, effect, target need
	// terms states whether the kind carries a charter.
	terms need
	// incarnation is forbidden for the three kinds whose OWN digest becomes an
	// incarnation id — a record cannot contain its own digest. The fold derives
	// the id for those; every other kind names the holder it wrote as.
	incarnation need
	// oneOfEffectTarget marks a kind that must carry exactly one of effect or
	// target, which a per-member need cannot express.
	oneOfEffectTarget bool
}

// laws is the per-kind subject shape for every structural kind. Advisory kinds
// share advisoryLaw: they decide nothing, so they carry no subject.
var laws = map[string]subjectLaw{
	KindCharter:    {terms: required, incarnation: forbidden},
	KindAttach:     {incarnation: forbidden},
	KindTakeover:   {party: required, incarnation: forbidden},
	KindClaim:      {work: required, incarnation: required},
	KindYield:      {work: required, incarnation: required},
	KindComplete:   {work: required, incarnation: required},
	KindAbandon:    {work: required, incarnation: required},
	KindRelease:    {incarnation: required},
	KindRetire:     {incarnation: required},
	KindRecharter:  {terms: required, incarnation: required},
	KindAssign:     {work: required, digest: required, party: optional, incarnation: required},
	KindUnassign:   {work: required, incarnation: required},
	KindIntentRef:  {effect: required, incarnation: required},
	KindEscalation: {incarnation: required},
	KindResolution: {effect: optional, target: optional, incarnation: required, oneOfEffectTarget: true},
	KindSeal:       {incarnation: required},
	KindSplit:      {party: required, incarnation: required},
	KindMerge:      {party: required, incarnation: required},
	KindDelegate:   {party: required, incarnation: required},
	KindRevoke:     {party: optional, incarnation: required},
	KindAnnul:      {target: required, incarnation: required},
}

// advisoryLaw is every advisory kind's shape: a writer, and nothing structural.
var advisoryLaw = subjectLaw{incarnation: required}

// ValidateRecord enforces the record-level laws. It is pure and I/O-free, and
// it decides nothing about transitions — a record can be perfectly valid here
// and still be inadmissible against a history.
func ValidateRecord(r Record) error {
	if err := checkEnvelope(r); err != nil {
		return err
	}
	law, err := lawFor(r)
	if err != nil {
		return err
	}
	if err := checkIdentity(r, law); err != nil {
		return err
	}
	if err := checkSubject(r, law); err != nil {
		return err
	}
	if err := checkTerms(r, law); err != nil {
		return err
	}
	return checkBody(r)
}

// checkEnvelope validates the fields every record carries regardless of kind.
func checkEnvelope(r Record) error {
	if r.V != Version {
		return refuse(ReasonSchemeUnsupported, r.Seq, "record version %d, this reader accepts %d", r.V, Version)
	}
	if r.Scheme != Scheme {
		return refuse(ReasonSchemeUnsupported, r.Seq, "scheme %q, this reader accepts %q", r.Scheme, Scheme)
	}
	if r.Seq < 1 {
		return refuse(ReasonSeqGap, r.Seq, "seq must be at least 1")
	}
	if r.Fence < 0 {
		return refuse(ReasonFenceInvalid, r.Seq, "fence %d is negative", r.Fence)
	}
	if !idPattern.MatchString(r.Tenant) {
		return refuse(ReasonMalformedID, r.Seq, "tenant %q", r.Tenant)
	}
	if !idPattern.MatchString(r.Role) {
		return refuse(ReasonMalformedID, r.Seq, "role %q", r.Role)
	}
	return checkRefs(r)
}

// checkRefs validates every reference's shape and trust label. An unlabelled
// ref is refused rather than defaulted: defaulting to internal would launder
// external content into the fold, which is the one thing the label exists to
// prevent.
func checkRefs(r Record) error {
	for i, ref := range r.Refs {
		if ref.URI == "" {
			return refuse(ReasonMalformedRef, r.Seq, "refs[%d] has no uri", i)
		}
		if ref.Trust != TrustOperator && ref.Trust != TrustInternal && ref.Trust != TrustExternal {
			return refuse(ReasonMalformedRef, r.Seq, "refs[%d] trust %q is not one of %s, %s, %s",
				i, ref.Trust, TrustOperator, TrustInternal, TrustExternal)
		}
	}
	return nil
}

// lawFor resolves a record's subject law from its kind and declared class.
//
// The class is read from the WIRE, not from this reader's table, because the
// kinds that need classifying correctly are exactly the ones this reader does
// not know. An unknown structural kind refuses; an unknown advisory kind is
// admitted with the advisory law and skipped by the fold.
func lawFor(r Record) (subjectLaw, error) {
	declared, known := DeclaredClass(r.Kind)
	if known && declared != r.KindClass {
		return subjectLaw{}, refuse(ReasonKindClassMismatch, r.Seq,
			"kind %q is %s, record declares %s", r.Kind, declared, r.KindClass)
	}
	if r.KindClass == ClassStructural {
		law, ok := laws[r.Kind]
		if !ok {
			return subjectLaw{}, refuse(ReasonSchemeUnsupported, r.Seq,
				"unknown structural kind %q; upgrade this reader", r.Kind)
		}
		return law, nil
	}
	if r.KindClass == ClassAdvisory {
		return advisoryLaw, nil
	}
	return subjectLaw{}, refuse(ReasonSchemeUnsupported, r.Seq,
		"kind_class %q is neither %s nor %s", r.KindClass, ClassStructural, ClassAdvisory)
}

// checkIdentity enforces the incarnation rule. The three kinds that MINT an
// incarnation carry none, because an incarnation id is the digest of the record
// that minted it and a record cannot contain its own digest.
func checkIdentity(r Record, law subjectLaw) error {
	if law.incarnation == forbidden && r.Incarnation != "" {
		return refuse(ReasonIncarnationUnexpected, r.Seq,
			"%s mints an incarnation and must not name one", r.Kind)
	}
	if law.incarnation != required {
		return nil
	}
	if r.Incarnation == "" {
		return refuse(ReasonIncarnationMissing, r.Seq, "%s must name the incarnation that wrote it", r.Kind)
	}
	if !digestPattern.MatchString(r.Incarnation) {
		return refuse(ReasonMalformedID, r.Seq,
			"incarnation %q is not a digest; ids are the digest of the record that minted them", r.Incarnation)
	}
	return nil
}

// subjectMember pairs one member's value with the law for it, so checkSubject
// is one loop rather than five near-identical blocks.
type subjectMember struct {
	name  string
	value string
	need  need
	check func(string) bool
}

func checkSubject(r Record, law subjectLaw) error {
	members := []subjectMember{
		{"work", r.Subject.Work, law.work, workURIPattern.MatchString},
		{"digest", r.Subject.Digest, law.digest, nonEmpty},
		{"party", r.Subject.Party, law.party, idPattern.MatchString},
		{"effect", r.Subject.Effect, law.effect, nonEmpty},
		{"target", r.Subject.Target, law.target, nonEmpty},
	}
	for _, m := range members {
		if err := checkMember(r, m); err != nil {
			return err
		}
	}
	return checkOneOf(r, law)
}

func checkMember(r Record, m subjectMember) error {
	if m.value == "" && m.need == required {
		return refuse(ReasonSubjectMissing, r.Seq, "%s requires subject.%s", r.Kind, m.name)
	}
	if m.value != "" && m.need == forbidden {
		return refuse(ReasonSubjectUnexpected, r.Seq, "%s does not carry subject.%s", r.Kind, m.name)
	}
	if m.value == "" || m.check(m.value) {
		return nil
	}
	if m.name == "work" {
		return refuse(ReasonMalformedWorkURI, r.Seq, "subject.work %q needs a scheme, e.g. github:acme/api#88", m.value)
	}
	return refuse(ReasonMalformedID, r.Seq, "subject.%s %q", m.name, m.value)
}

// checkOneOf enforces the exclusive-or a per-member need cannot state: a
// resolution closes an escalation or an intent, exactly one.
func checkOneOf(r Record, law subjectLaw) error {
	if !law.oneOfEffectTarget {
		return nil
	}
	both := r.Subject.Effect != "" && r.Subject.Target != ""
	neither := r.Subject.Effect == "" && r.Subject.Target == ""
	if both {
		return refuse(ReasonSubjectUnexpected, r.Seq, "%s carries both subject.effect and subject.target; it closes exactly one", r.Kind)
	}
	if neither {
		return refuse(ReasonSubjectMissing, r.Seq, "%s requires exactly one of subject.effect or subject.target", r.Kind)
	}
	return nil
}

// checkTerms enforces charter immutability. Only a charter or a recharter
// carries terms; a checkpoint that tried to rewrite the goal is refused here
// rather than quietly winning, which is what keeps a role's purpose from being
// re-summarized into something else over a dozen incarnations.
func checkTerms(r Record, law subjectLaw) error {
	if r.Terms == nil && law.terms == required {
		return refuse(ReasonTermsMissing, r.Seq, "%s must carry terms", r.Kind)
	}
	if r.Terms != nil && law.terms == forbidden {
		return refuse(ReasonCharterImmutable, r.Seq,
			"%s carries terms; only %s and %s may", r.Kind, KindCharter, KindRecharter)
	}
	if r.Terms == nil {
		return nil
	}
	return checkScope(r)
}

// checkScope enforces L1's precondition: there is no unscoped role. A charter
// whose scope names no work reference would produce an incarnation that exists
// without owned work, which the state machine has no state for.
func checkScope(r Record) error {
	if len(r.Terms.Scope) == 0 {
		return refuse(ReasonScopeEmpty, r.Seq, "a charter's scope must name at least one work reference")
	}
	for i, s := range r.Terms.Scope {
		if !workURIPattern.MatchString(s) {
			return refuse(ReasonMalformedWorkURI, r.Seq, "terms.scope[%d] %q needs a scheme", i, s)
		}
	}
	for i, sup := range r.Terms.Supervisors {
		if !idPattern.MatchString(sup) {
			return refuse(ReasonMalformedID, r.Seq, "terms.supervisors[%d] %q", i, sup)
		}
	}
	return nil
}

// checkBody enforces that a referenced blob is classified. An unclassified blob
// has no retention or erasure rule, and the store cannot invent one after the
// fact — which is precisely when someone asks for the erasure.
func checkBody(r Record) error {
	if r.BodyDigest == "" && strings.TrimSpace(r.BodyClass) != "" {
		return refuse(ReasonBodyClassMissing, r.Seq, "body_class without body_digest")
	}
	if r.BodyDigest == "" {
		return nil
	}
	if !digestPattern.MatchString(r.BodyDigest) {
		return refuse(ReasonMalformedID, r.Seq, "body_digest %q", r.BodyDigest)
	}
	if strings.TrimSpace(r.BodyClass) == "" {
		return refuse(ReasonBodyClassMissing, r.Seq, "body_digest without body_class; an unclassified blob has no retention rule")
	}
	return nil
}

func nonEmpty(s string) bool { return s != "" }
