package org

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The encoding is a specification, so the authoritative test is hand-written
// expected bytes rather than a round-trip through this package's own encoder.
// A round-trip test passes for any self-consistent encoder, including a wrong
// one; these bytes are what a second implementation must produce.
func TestEncodeProducesTheSpecifiedBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Value
		want string
	}{
		{"empty map", Map(), `{}`},
		{"empty list", List(), `[]`},
		{"null", Null(), `null`},
		{"true", Bool(true), `true`},
		{"false", Bool(false), `false`},
		{"zero", Int(0), `0`},
		{"negative", Int(-42), `-42`},
		{"int64 min", Int(-9223372036854775808), `-9223372036854775808`},
		{"int64 max", Int(9223372036854775807), `9223372036854775807`},
		{"simple string", String("checkpoint"), `"checkpoint"`},
		{"empty string", String(""), `""`},
		{
			"fields sort by name, not build order",
			Map(F("seq", Int(41)), F("kind", String("checkpoint")), F("annulled", Bool(false))),
			`{"annulled":false,"kind":"checkpoint","seq":41}`,
		},
		{
			"nested",
			Map(F("state", Map(F("next", Strings([]string{"eval", "ship"})))), F("role", String("org/ivy-ic-3"))),
			`{"role":"org/ivy-ic-3","state":{"next":["eval","ship"]}}`,
		},
		{
			"list order is preserved",
			Strings([]string{"c", "a", "b"}),
			`["c","a","b"]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("Encode = %s, want %s", got, tc.want)
			}
		})
	}
}

// The escape rule is the part most likely to diverge between implementations,
// and the part Go's own encoder gets wrong for this purpose. Each case here is
// a byte sequence a second implementation must reproduce exactly.
func TestStringEscapingIsMinimalAndExplicit(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"quote", "say \"hi\"", "\"say \\\"hi\\\"\""},
		{"backslash", "a\\b", "\"a\\\\b\""},
		{"newline", "a\nb", "\"a\\nb\""},
		{"tab", "a\tb", "\"a\\tb\""},
		{"carriage return", "a\rb", "\"a\\rb\""},
		{"backspace", "a\bb", "\"a\\bb\""},
		{"form feed", "a\fb", "\"a\\fb\""},
		{"nul", "a\x00b", "\"a\\u0000b\""},
		{"unit separator", "a\x1fb", "\"a\\u001fb\""},

		// The whole reason this encoder exists. encoding/json escapes all
		// three of these by default; a durable digest must not.
		{"ampersand is literal", "fix & ship", "\"fix & ship\""},
		{"angle brackets are literal", "a<b>c", "\"a<b>c\""},

		// DEL and the line separators are literal too. encoding/json escapes
		// U+2028 and U+2029 unconditionally as a JavaScript-embedding concern.
		{"delete is literal", "a\x7fb", "\"a\x7fb\""},
		{"line separator is literal", "a\u2028b", "\"a\u2028b\""},
		{"paragraph separator is literal", "a\u2029b", "\"a\u2029b\""},
		{"non-ascii is literal utf-8", "caf\u00e9 \u2713", "\"caf\u00e9 \u2713\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(String(tc.in))
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("Encode(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// The bug this package was written to avoid, pinned as a test so nobody
// "simplifies" the encoder back into encoding/json. If this ever passes,
// canon.go has been replaced by the thing it exists to replace.
func TestEncodingDiffersFromEncodingJSON(t *testing.T) {
	const s = "fix & ship <now>"
	ours, err := Encode(String(s))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	theirs, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(ours) == string(theirs) {
		t.Fatal("canonical encoding matches encoding/json; the HTML-escaping divergence is the point")
	}
	for _, escaped := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(string(ours), escaped) {
			t.Fatalf("canonical encoding emitted the HTML escape %s: %s", escaped, ours)
		}
	}
}

// Build order must not reach the bytes. This is the property json.Marshal
// cannot offer, because it emits struct fields in declaration order — so a
// field moved during a refactor silently invalidates every historical digest.
func TestFieldOrderDoesNotAffectBytes(t *testing.T) {
	a := Map(
		F("role", String("org/ivy-lead")),
		F("seq", Int(88)),
		F("kind", String("message.sent")),
	)
	b := Map(
		F("kind", String("message.sent")),
		F("role", String("org/ivy-lead")),
		F("seq", Int(88)),
	)
	ab, err := Encode(a)
	if err != nil {
		t.Fatalf("Encode a: %v", err)
	}
	bb, err := Encode(b)
	if err != nil {
		t.Fatalf("Encode b: %v", err)
	}
	if string(ab) != string(bb) {
		t.Fatalf("field order changed bytes:\n a = %s\n b = %s", ab, bb)
	}
}

// A map with two fields of one name has no single correct encoding, so the
// encoder refuses rather than picking one. "Last wins" would be a rule a second
// implementation has to reproduce exactly; refusal needs no agreement.
func TestAmbiguousMapsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Value
		want error
	}{
		{"duplicate field", Map(F("seq", Int(1)), F("seq", Int(2))), ErrDuplicateField},
		{"empty field name", Map(F("", Int(1))), ErrEmptyFieldName},
		{"unknown kind", Value{}, ErrUnknownKind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Encode(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Encode error = %v, want %v", err, tc.want)
			}
		})
	}
}

// Invalid UTF-8 is refused rather than replacement-charactered, because
// silently rewriting a byte changes what was recorded.
func TestInvalidUTF8IsRefused(t *testing.T) {
	_, err := Encode(String("\xff\xfe"))
	if !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("Encode error = %v, want ErrInvalidUTF8", err)
	}
}

// Refusal must survive nesting: an ambiguous map buried inside a list inside a
// map still refuses, rather than encoding the well-formed parts around it.
func TestRefusalPropagatesThroughNesting(t *testing.T) {
	v := Map(F("records", List(Map(F("x", Int(1)), F("x", Int(2))))))
	if _, err := Encode(v); !errors.Is(err, ErrDuplicateField) {
		t.Fatalf("nested duplicate not refused: %v", err)
	}
}

// Golden digests. These pin the byte-to-digest mapping including the scheme
// version, which is hashed in rather than merely recorded beside the digest.
// A change to the encoder, the scheme string, or the hash construction breaks
// these — which is the intent: they are the chain's compatibility contract.
// Golden digests, pinning the byte-to-digest mapping including the scheme
// version that is hashed in rather than recorded beside it. A change to the
// encoder, the scheme string, or the hash construction breaks these — the
// intent, since they are the chain's compatibility contract.
//
// Every value here is reproducible with standard tools, which is the whole
// portability claim in one line:
//
//	printf 'canon/v1\x00{}' | shasum -a 256
//
// If that command and this package ever disagree, this package is wrong.
func TestGoldenDigests(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        Value
		canonical string
		want      string
	}{
		{
			"empty map",
			Map(),
			`{}`,
			"sha256:b3b41bf6ea1bfbf0914c935001e6209d4540a28218169ac326c32c74cfa9dc19",
		},
		{
			"single int field",
			Map(F("seq", Int(41))),
			`{"seq":41}`,
			"sha256:501bb07164b7290bdddf6fac7c63ab4bb45bf4a73f60dac2018745951005f7bd",
		},
		{
			"string with an ampersand",
			String("fix & ship"),
			`"fix & ship"`,
			"sha256:0695916bb2ef75ab04a9ce00e80a8894dda77be10460d182170bbd99e1a9ea89",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Encode(tc.in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if string(b) != tc.canonical {
				t.Fatalf("canonical bytes = %s, want %s", b, tc.canonical)
			}
			got, err := Digest(tc.in)
			if err != nil {
				t.Fatalf("Digest: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Digest = %s\n            want %s", got, tc.want)
			}
		})
	}
}

// The scheme is inside the hash, not beside it. A reader that ignored a
// beside-the-digest scheme field would silently accept digests produced under
// different rules; hashing it in turns that into an ordinary mismatch.
func TestSchemeIsInsideTheHash(t *testing.T) {
	canonical, err := Encode(Map(F("seq", Int(1))))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	withScheme := DigestBytes(canonical)
	bare := rawSHA256(canonical)
	if withScheme == bare {
		t.Fatal("digest does not incorporate the scheme version")
	}
}

// Digest must not be confusable across shapes: two different values may never
// share a digest through a framing accident (the classic case is a list of two
// strings colliding with one concatenated string).
func TestDistinctShapesDigestDistinctly(t *testing.T) {
	seen := map[string]string{}
	for _, tc := range []struct {
		name string
		in   Value
	}{
		{"list of two", Strings([]string{"a", "b"})},
		{"one string", String("ab")},
		{"one string comma", String("a,b")},
		{"map", Map(F("a", String("b")))},
		{"int one", Int(1)},
		{"string one", String("1")},
		{"true", Bool(true)},
		{"string true", String("true")},
		{"null", Null()},
		{"string null", String("null")},
	} {
		d, err := Digest(tc.in)
		if err != nil {
			t.Fatalf("%s: Digest: %v", tc.name, err)
		}
		if prior, ok := seen[d]; ok {
			t.Fatalf("%s collides with %s at %s", tc.name, prior, d)
		}
		seen[d] = tc.name
	}
}

func TestDigestOfCanonical(t *testing.T) {
	rec := stubRecord{role: "org/lead-a", seq: 7}
	got, err := DigestOf(rec)
	if err != nil {
		t.Fatalf("DigestOf: %v", err)
	}
	want, err := Digest(rec.Canonical())
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if got != want {
		t.Fatalf("DigestOf = %s, want %s", got, want)
	}
}

type stubRecord struct {
	role string
	seq  int64
}

func (s stubRecord) Canonical() Value {
	return Map(
		F("role", String(s.role)),
		F("seq", Int(s.seq)),
	)
}

// rawSHA256 is the digest WITHOUT the scheme prefix, used only to prove the
// scheme actually participates in Digest.
func rawSHA256(b []byte) string {
	h := sha256.Sum256(b)
	return DigestPrefix + ":" + hex.EncodeToString(h[:])
}
