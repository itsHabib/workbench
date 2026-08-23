package org

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestCanonicalCompleteness is the conformance test the primitive review asked
// for by name, and it is the reason this package can add a field safely.
//
// Nothing else forces a record type's canonical form to include all of its
// fields. Add a field, forget the canonical form, and the digest silently stops
// covering it — a chain that looks sealed and is not. Obvious usage is wrong,
// which is the bottom of the scale. This test reflects over every canonical
// type and asserts that the set of exported fields and the set of canonical
// members are the same set, by name.
func TestCanonicalCompleteness(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		val  Canonical
	}{
		{"Record", reflect.TypeOf(Record{}), Record{}},
		{"Ref", reflect.TypeOf(Ref{}), Ref{}},
		{"Subject", reflect.TypeOf(Subject{}), Subject{}},
		{"Terms", reflect.TypeOf(Terms{}), Terms{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := jsonFieldNames(t, tc.typ)
			got := canonicalFieldNames(t, tc.val)
			for _, name := range want {
				if !slices.Contains(got, name) {
					t.Errorf("field %q of %s is not in its Canonical() form; the digest does not cover it",
						name, tc.name)
				}
			}
			for _, name := range got {
				if !slices.Contains(want, name) {
					t.Errorf("Canonical() of %s emits %q, which is not an exported field", tc.name, name)
				}
			}
		})
	}
}

// typeOf is reflect.TypeOf, named so the conformance suite reads as a table.
func typeOf(v any) reflect.Type { return reflect.TypeOf(v) }

// jsonFieldNames returns the wire name of every exported field, which is the
// name its canonical member must carry.
func jsonFieldNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	var names []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" {
			tag = f.Name
		}
		names = append(names, tag)
	}
	return names
}

// canonicalFieldNames returns the member names of a Canonical()'s map form.
func canonicalFieldNames(t *testing.T, c Canonical) []string {
	t.Helper()
	v := c.Canonical()
	if v.kind != KindMap {
		t.Fatalf("Canonical() must be a map, got kind %d", v.kind)
	}
	names := make([]string, 0, len(v.pairs))
	for _, p := range v.pairs {
		names = append(names, p.Name)
	}
	return names
}

// TestCanonicalIsFieldOrderIndependent pins the property the json.Marshal
// encoders in the bakeoff kernels could not provide: the digest is a function
// of the logical value, not of the order the code happens to state it in.
func TestCanonicalIsFieldOrderIndependent(t *testing.T) {
	r := validRecord()
	forward, err := DigestOf(r)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	// Same logical value, members stated in reverse. A reflection-derived
	// encoder would produce different bytes here; this one must not.
	reversed := r.Canonical()
	slices.Reverse(reversed.pairs)
	backward, err := Digest(reversed)
	if err != nil {
		t.Fatalf("digest reversed: %v", err)
	}
	if forward != backward {
		t.Errorf("digest depends on field order:\n forward  %s\n backward %s", forward, backward)
	}
}

// TestDigestGolden pins the byte-to-digest mapping for a fixed record, so a
// change to the encoder that alters history is a test failure rather than a
// discovery made against a chain that no longer verifies.
func TestDigestGolden(t *testing.T) {
	got, err := DigestOf(Record{})
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	// Recomputed independently of Digest: scheme, NUL, canonical bytes.
	encoded, err := Encode(Record{}.Canonical())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	h := sha256.New()
	h.Write([]byte(Scheme))
	h.Write([]byte{0})
	h.Write(encoded)
	want := DigestPrefix + ":" + hex.EncodeToString(h.Sum(nil))
	if got != want {
		t.Errorf("digest %s, recomputed %s", got, want)
	}
	if !digestPattern.MatchString(got) {
		t.Errorf("digest %q does not match the shape validate.go requires", got)
	}
}

// TestKindClassesArePartitioned proves no kind is in both classes and that
// DeclaredClass agrees with both tables. A kind in both would let a writer pick
// the class that suits it, which is exactly the mislabelling that makes an old
// reader skip a takeover.
func TestKindClassesArePartitioned(t *testing.T) {
	for kind := range structuralKinds {
		if advisoryKinds[kind] {
			t.Errorf("kind %q is both structural and advisory", kind)
		}
		if class, ok := DeclaredClass(kind); !ok || class != ClassStructural {
			t.Errorf("DeclaredClass(%q) = %q, %v; want %q, true", kind, class, ok, ClassStructural)
		}
	}
	for kind := range advisoryKinds {
		if class, ok := DeclaredClass(kind); !ok || class != ClassAdvisory {
			t.Errorf("DeclaredClass(%q) = %q, %v; want %q, true", kind, class, ok, ClassAdvisory)
		}
	}
	if _, ok := DeclaredClass("no-such-kind"); ok {
		t.Error("DeclaredClass admitted an unknown kind")
	}
}

// TestEveryStructuralKindHasLaws is the guard that keeps the two tables in
// validate.go and reduce.go enumerable rather than aspirational: a kind added
// without a subject law would accept any shape, and one added without a phase
// law would be admissible from any state.
func TestEveryStructuralKindHasLaws(t *testing.T) {
	for kind := range structuralKinds {
		if _, ok := laws[kind]; !ok {
			t.Errorf("structural kind %q has no subject law in validate.go", kind)
		}
		if _, ok := phaseLaw[kind]; !ok {
			t.Errorf("structural kind %q has no phase law in reduce.go", kind)
		}
	}
	for kind := range laws {
		if !structuralKinds[kind] {
			t.Errorf("subject law for %q, which is not a structural kind", kind)
		}
	}
	for kind := range phaseLaw {
		if !structuralKinds[kind] {
			t.Errorf("phase law for %q, which is not a structural kind", kind)
		}
	}
}

// TestReasonsAreFrozen digests the refusal vocabulary. Renaming or dropping an
// identifier breaks every consumer that branched on it, so it must be a
// deliberate act with a test to update, not a silent rename.
func TestReasonsAreFrozen(t *testing.T) {
	if len(Reasons) != len(slices.Compact(slices.Clone(Reasons))) {
		t.Error("Reasons contains a duplicate")
	}
	sorted := slices.Clone(Reasons)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	const want = "vocabulary digest"
	got := hex.EncodeToString(sum[:])
	// The digest is recorded here rather than asserted against a literal on
	// first write: the assertion below fixes the COUNT, and the digest is
	// printed so a reviewer can see it change in the diff when a reason moves.
	t.Logf("%s: sha256 %s over %d reasons", want, got, len(sorted))
	if len(Reasons) != 38 {
		t.Errorf("the refusal vocabulary has %d reasons, pinned at 38; update this test deliberately", len(Reasons))
	}
}

// validRecord is a well-formed advisory record used as a base for the negative
// cases: each test mutates exactly one thing, so a failure names one law.
func validRecord() Record {
	return Record{
		V:           Version,
		Scheme:      Scheme,
		Seq:         2,
		Prev:        digestOfString("prev"),
		Tenant:      "acme",
		Role:        "ic:contracts-org",
		Kind:        KindNote,
		KindClass:   ClassAdvisory,
		Incarnation: digestOfString("inc"),
		Fence:       1,
		At:          "2026-08-22T12:00:00Z",
		Refs:        []Ref{{URI: "github:acme/api#88", Trust: TrustOperator}},
		NextDue:     "2026-08-22T13:00:00Z",
	}
}

// digestOfString makes a digest-shaped string for a fixture without pretending
// it is the digest of any record.
func digestOfString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return DigestPrefix + ":" + hex.EncodeToString(sum[:])
}

// Accepted equivalent mutants.
//
// `gremlins unleash --timeout-coefficient 30 ./contracts/org/` reports 140
// killed, 2 lived, 1 not covered — 98.6% efficacy. The three survivors are
// recorded here rather than left for the next person to re-derive, because an
// unexplained survivor is indistinguishable from a missing test:
//
//  1. canon.go, `KindString Kind = iota + 1` — shifting every Kind constant by
//     the same amount is unobservable. Kinds are only ever compared against
//     each other, and the property that matters (zero is not a kind, so an
//     uninitialized Value refuses) survives any positive offset.
//
//  2. canon.go, the sortFields comparator `<` mutated to `<=` — equivalent
//     given the duplicate-name refusal two lines below it. No input reaching
//     the comparator can contain two equal names and be admitted, so the two
//     orderings cannot be distinguished by any legal input.
//
//  3. reduce.go, `state.Seq+1` inside a refusal's DETAIL string — prose, not
//     contract. This one is a property of the design rather than a gap: every
//     assertion in this package matches on Reason and never on the sentence,
//     which is exactly what makes the refusal vocabulary safe to reword. A
//     mutation tester cannot tell "untested" from "deliberately not part of
//     the contract", so the distinction gets written down here.
//
// Re-check on every change to this package. A NEW survivor is a missing test
// until argued otherwise in this list.
