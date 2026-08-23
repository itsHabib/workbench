package org

import (
	"errors"
	"slices"
	"testing"
)

// Each case mutates exactly ONE thing about a well-formed record, so a failure
// names one law. The reason is asserted as a value, never as a substring of
// prose — that is the whole point of the refusal type.
func TestValidateRecord(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Record)
		reason string
	}{
		{"valid advisory", func(*Record) {}, ""},
		{"valid structural", func(r *Record) {
			r.Kind, r.KindClass = KindRelease, ClassStructural
		}, ""},

		{"wrong version", func(r *Record) { r.V = 99 }, ReasonSchemeUnsupported},
		{"wrong scheme", func(r *Record) { r.Scheme = "canon/v0" }, ReasonSchemeUnsupported},
		{"unknown structural kind", func(r *Record) {
			r.Kind, r.KindClass = "teleport", ClassStructural
		}, ReasonSchemeUnsupported},
		{"unknown class on an unknown kind", func(r *Record) {
			r.Kind, r.KindClass = "teleport", "whatever"
		}, ReasonSchemeUnsupported},
		{"unknown advisory kind is admitted", func(r *Record) {
			r.Kind, r.KindClass = "sonnet", ClassAdvisory
		}, ""},
		{"known kind mislabelled advisory", func(r *Record) {
			r.Kind, r.KindClass = KindTakeover, ClassAdvisory
		}, ReasonKindClassMismatch},
		{"known kind mislabelled structural", func(r *Record) {
			r.Kind, r.KindClass = KindNote, ClassStructural
		}, ReasonKindClassMismatch},

		{"seq below one", func(r *Record) { r.Seq = 0 }, ReasonSeqGap},
		{"negative fence", func(r *Record) { r.Fence = -1 }, ReasonFenceInvalid},
		{"empty tenant", func(r *Record) { r.Tenant = "" }, ReasonMalformedID},
		{"uppercase role", func(r *Record) { r.Role = "IC:Thing" }, ReasonMalformedID},
		{"role with empty segment", func(r *Record) { r.Role = "ic::thing" }, ReasonMalformedID},
		{"namespaced role is fine", func(r *Record) { r.Role = "human:mh" }, ""},

		{"ref without uri", func(r *Record) { r.Refs = []Ref{{Trust: TrustInternal}} }, ReasonMalformedRef},
		{"ref without trust", func(r *Record) { r.Refs = []Ref{{URI: "x:1"}} }, ReasonMalformedRef},
		{"ref with invented trust", func(r *Record) {
			r.Refs = []Ref{{URI: "x:1", Trust: "probably-fine"}}
		}, ReasonMalformedRef},

		{"advisory naming no incarnation", func(r *Record) { r.Incarnation = "" }, ReasonIncarnationMissing},
		{"incarnation that is not a digest", func(r *Record) { r.Incarnation = "session-4" }, ReasonMalformedID},
		{"attach naming an incarnation", func(r *Record) {
			r.Kind, r.KindClass = KindAttach, ClassStructural
		}, ReasonIncarnationUnexpected},
		{"takeover naming an incarnation", func(r *Record) {
			r.Kind, r.KindClass = KindTakeover, ClassStructural
			r.Subject.Party = "lead:platform"
		}, ReasonIncarnationUnexpected},

		{"claim without work", func(r *Record) {
			r.Kind, r.KindClass = KindClaim, ClassStructural
		}, ReasonSubjectMissing},
		{"claim with a schemeless work ref", func(r *Record) {
			r.Kind, r.KindClass = KindClaim, ClassStructural
			r.Subject.Work = "PROJ-412"
		}, ReasonMalformedWorkURI},
		{"claim with a work uri", func(r *Record) {
			r.Kind, r.KindClass = KindClaim, ClassStructural
			r.Subject.Work = "jira:PROJ-412"
		}, ""},
		{"note carrying a subject", func(r *Record) { r.Subject.Work = "jira:PROJ-412" }, ReasonSubjectUnexpected},
		{"assign without a digest", func(r *Record) {
			r.Kind, r.KindClass = KindAssign, ClassStructural
			r.Subject.Work = "github:acme/api#88"
		}, ReasonSubjectMissing},
		{"assign complete", func(r *Record) {
			r.Kind, r.KindClass = KindAssign, ClassStructural
			r.Subject.Work, r.Subject.Digest = "github:acme/api#88", "sha256:beef"
		}, ""},
		{"takeover without a supervisor", func(r *Record) {
			r.Kind, r.KindClass, r.Incarnation = KindTakeover, ClassStructural, ""
		}, ReasonSubjectMissing},

		{"resolution closing both", func(r *Record) {
			r.Kind, r.KindClass = KindResolution, ClassStructural
			r.Subject.Effect, r.Subject.Target = "eff_1", "esc_1"
		}, ReasonSubjectUnexpected},
		{"resolution closing neither", func(r *Record) {
			r.Kind, r.KindClass = KindResolution, ClassStructural
		}, ReasonSubjectMissing},
		{"resolution closing an intent", func(r *Record) {
			r.Kind, r.KindClass = KindResolution, ClassStructural
			r.Subject.Effect = "eff_1"
		}, ""},

		{"note carrying terms", func(r *Record) { r.Terms = &Terms{Scope: []string{"x:1"}} }, ReasonCharterImmutable},
		{"checkpoint rewriting the charter", func(r *Record) {
			r.Kind, r.KindClass = KindCheckpoint, ClassAdvisory
			r.Terms = &Terms{Scope: []string{"x:1"}}
		}, ReasonCharterImmutable},
		{"charter without terms", func(r *Record) {
			r.Kind, r.KindClass, r.Incarnation = KindCharter, ClassStructural, ""
		}, ReasonTermsMissing},
		{"charter with an empty scope", func(r *Record) {
			r.Kind, r.KindClass, r.Incarnation = KindCharter, ClassStructural, ""
			r.Terms = &Terms{Tier: "T1"}
		}, ReasonScopeEmpty},
		{"charter with a schemeless scope entry", func(r *Record) {
			r.Kind, r.KindClass, r.Incarnation = KindCharter, ClassStructural, ""
			r.Terms = &Terms{Scope: []string{"the api"}}
		}, ReasonMalformedWorkURI},
		{"charter with a malformed supervisor", func(r *Record) {
			r.Kind, r.KindClass, r.Incarnation = KindCharter, ClassStructural, ""
			r.Terms = &Terms{Scope: []string{"x:1"}, Supervisors: []string{"Lead Platform"}}
		}, ReasonMalformedID},

		{"body without a class", func(r *Record) { r.BodyDigest = digestOfString("body") }, ReasonBodyClassMissing},
		{"class without a body", func(r *Record) { r.BodyClass = "internal" }, ReasonBodyClassMissing},
		{"body digest that is not a digest", func(r *Record) {
			r.BodyDigest, r.BodyClass = "blob-4", "internal"
		}, ReasonMalformedID},
		{"classified body", func(r *Record) {
			r.BodyDigest, r.BodyClass = digestOfString("body"), "internal"
		}, ""},
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRecord()
			tc.mutate(&r)
			err := ValidateRecord(r)
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("want admitted, got %v", err)
				}
				return
			}
			if got := RefusalReason(err); got != tc.reason {
				t.Fatalf("want reason %q, got %q (%v)", tc.reason, got, err)
			}
		})
		seen[tc.reason] = true
	}

	// Every record-level law must have a fixture. A law with no failing case is
	// a law nobody has ever seen fire.
	for _, reason := range recordLevelReasons {
		if !seen[reason] {
			t.Errorf("no fixture produces %q", reason)
		}
	}
}

// recordLevelReasons are the laws ValidateRecord owns. The chain-level ones are
// reduce_test.go's to cover.
var recordLevelReasons = []string{
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
}

// TestRefusalIsMatchable proves a caller can branch on a reason with errors.Is
// without knowing the seq or the prose, which is what makes the vocabulary
// usable from another package.
func TestRefusalIsMatchable(t *testing.T) {
	r := validRecord()
	r.Incarnation = ""
	err := ValidateRecord(r)
	if !errors.Is(err, &Refusal{Reason: ReasonIncarnationMissing}) {
		t.Errorf("errors.Is did not match on reason alone: %v", err)
	}
	if errors.Is(err, &Refusal{Reason: ReasonSeqGap}) {
		t.Error("errors.Is matched the wrong reason")
	}
	if RefusalReason(errors.New("not a refusal")) != "" {
		t.Error("RefusalReason invented a reason for a plain error")
	}
}

// TestValidateRejectsEveryStructuralKindWithAnEmptySubject sweeps the whole
// kind set rather than trusting the hand-written cases to be exhaustive: every
// structural kind with a required subject member must refuse an empty one.
func TestValidateRejectsEveryStructuralKindWithAnEmptySubject(t *testing.T) {
	for kind, law := range laws {
		requires := law.work == required || law.digest == required ||
			law.party == required || law.effect == required ||
			law.target == required || law.oneOfEffectTarget
		if !requires {
			continue
		}
		r := validRecord()
		r.Kind, r.KindClass = kind, ClassStructural
		r.Subject = Subject{}
		if law.incarnation == forbidden {
			r.Incarnation = ""
		}
		if law.terms == required {
			r.Terms = &Terms{Scope: []string{"x:1"}}
		}
		if got := RefusalReason(ValidateRecord(r)); got != ReasonSubjectMissing {
			t.Errorf("%s with an empty subject: want %q, got %q", kind, ReasonSubjectMissing, got)
		}
	}
}

// TestValidateIgnoresAt pins the house law that no refusal may depend on a
// clock the refusing party does not own. The home stamps At; nothing here may
// read it.
func TestValidateIgnoresAt(t *testing.T) {
	for _, at := range []string{"", "not a timestamp", "1970-01-01T00:00:00Z", "2999-12-31T23:59:59Z"} {
		r := validRecord()
		r.At = at
		if err := ValidateRecord(r); err != nil {
			t.Errorf("At=%q was refused: %v", at, err)
		}
	}
}

// TestUnknownAdvisoryKindsSurvive is the schema-evolution guarantee from the
// reader's side: a kind this build has never heard of is admitted when it is
// declared advisory, and refused when it is declared structural. The asymmetry
// is the whole story — skipping an unknown transition is how two holders happen.
func TestUnknownAdvisoryKindsSurvive(t *testing.T) {
	future := []string{"sonnet", "telemetry", "vibe"}
	for _, kind := range future {
		advisory := validRecord()
		advisory.Kind, advisory.KindClass = kind, ClassAdvisory
		if err := ValidateRecord(advisory); err != nil {
			t.Errorf("advisory %q was refused: %v", kind, err)
		}
		structural := validRecord()
		structural.Kind, structural.KindClass = kind, ClassStructural
		if got := RefusalReason(ValidateRecord(structural)); got != ReasonSchemeUnsupported {
			t.Errorf("structural %q: want %q, got %q", kind, ReasonSchemeUnsupported, got)
		}
	}
}

// TestReasonsPartition proves the two per-file reason lists together cover the
// frozen vocabulary, so a new reason cannot be added without landing in one of
// the suites that must exercise it.
func TestReasonsPartition(t *testing.T) {
	covered := append(slices.Clone(recordLevelReasons), chainLevelReasons...)
	for _, reason := range Reasons {
		if !slices.Contains(covered, reason) {
			t.Errorf("reason %q is in neither the record-level nor the chain-level suite", reason)
		}
	}
	for _, reason := range covered {
		if !slices.Contains(Reasons, reason) {
			t.Errorf("a suite claims reason %q, which is not in the frozen vocabulary", reason)
		}
	}
}
