package driverstate

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// This file is the generative complement to conformance_test.go: the same
// encode/decode/hash laws the pinned reference vector nails down for ONE event,
// asserted here over thousands of rapid-generated events. An example proves a
// law holds for the case the author thought of; a property proves it holds for
// the ones they didn't. The canonical encoding is a cross-language chain anchor
// (ship's TS emitter must reproduce it byte-for-byte), so every structural law
// it rests on — field order, body-verbatim splicing, hash determinism,
// round-trip identity — earns a generator.

// canonicalOrder is the pinned field order canonical.go emits and every emitter
// must reproduce (contract §5). The order IS the contract, so a generative test
// asserts it directly rather than trusting the struct declaration.
var canonicalOrder = []string{
	"id", "run", "v", "kind", "stream", "time", "actor", "ext_ref", "body", "prev", "hash",
}

// genScalar generates a string that survives JSON encoding — arbitrary UTF-8,
// including the HTML-significant runes (<, >, &) whose escaping the canonical
// encoding deliberately turns OFF so they cross languages as themselves.
func genScalar() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.String(),
		rapid.SampledFrom([]string{"", "a", "<&>", "session:demo-01", "évt_ünïçödé", `"quoted"`, "tab\tnewline\n"}),
	)
}

// genBody generates a valid JSON body — the only bytes the encoder splices
// VERBATIM. A nil body (encoded as null) and objects with insignificant
// whitespace are both drawn, since the verbatim rule is exactly what makes
// whitespace load-bearing for the hash.
func genBody(t *rapid.T) json.RawMessage {
	switch rapid.IntRange(0, 3).Draw(t, "bodyShape") {
	case 0:
		return nil
	case 1:
		return json.RawMessage("null")
	case 2:
		m := rapid.MapOfN(
			rapid.StringMatching(`[a-z_]{1,8}`),
			rapid.OneOf[any](
				rapid.String().AsAny(),
				rapid.Int().AsAny(),
				rapid.Bool().AsAny(),
			),
			0, 5,
		).Draw(t, "bodyMap")
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal generated body: %v", err)
		}
		return raw
	default:
		// A whitespaced object: valid JSON whose exact bytes the encoder must
		// preserve, so read-back re-verifies the hash the writer sealed.
		s := rapid.StringMatching(`[a-z]{1,6}`).Draw(t, "wsKey")
		n := rapid.IntRange(-1000, 1000).Draw(t, "wsVal")
		return json.RawMessage("{ \"" + s + "\" :  " + itoa(n) + " }")
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// genEvent builds a decodable event: V is pinned to the accepted version (the
// version gate is the loud half of tolerance, tested separately), every scalar
// is arbitrary, Time is whole-second UTC as Append truncates it, and Kind ranges
// over the known set plus an unknown future kind (DecodeEvent tolerates kind, it
// gates only on version).
func genEvent(t *rapid.T) Event {
	kinds := append(append([]Kind{}, AllKinds()...), Kind("stream_teleported"))
	sec := rapid.Int64Range(0, 4102444800).Draw(t, "unixSec") // through year ~2100
	return Event{
		ID:     genScalar().Draw(t, "id"),
		Run:    genScalar().Draw(t, "run"),
		V:      Version,
		Kind:   rapid.SampledFrom(kinds).Draw(t, "kind"),
		Stream: genScalar().Draw(t, "stream"),
		Time:   time.Unix(sec, 0).UTC(),
		Actor:  genScalar().Draw(t, "actor"),
		ExtRef: genScalar().Draw(t, "extRef"),
		Body:   genBody(t),
		Prev:   genScalar().Draw(t, "prev"),
		Hash:   genScalar().Draw(t, "hash"),
	}
}

// TestPropEncodeDecodeIdentity: EncodeEvent is a normal form. Decoding an
// encoded event and re-encoding it reproduces the exact bytes, so persistence
// is lossless and the sealed hash survives read-back — the guarantee the whole
// hash chain rests on.
func TestPropEncodeDecodeIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := genEvent(t)
		enc := EncodeEvent(e)
		decoded, err := DecodeEvent(enc)
		if err != nil {
			t.Fatalf("encoded event must decode: %v\nbytes=%s", err, enc)
		}
		reEnc := EncodeEvent(decoded)
		if !bytes.Equal(enc, reEnc) {
			t.Fatalf("encode is not a fixed point:\n first=%s\nsecond=%s", enc, reEnc)
		}
		if got, want := ComputeHash(decoded), ComputeHash(e); got != want {
			t.Fatalf("hash changed across round-trip: got %s want %s", got, want)
		}
	})
}

// TestPropCanonicalFieldOrder: the canonical bytes carry every field exactly
// once, in the pinned order, for any event. Read structurally (a streaming
// decoder over the real object keys) so a value that happens to contain a field
// name never fools the assertion.
func TestPropCanonicalFieldOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := genEvent(t)
		keys := topLevelKeys(t, Canonical(e))
		if len(keys) != len(canonicalOrder) {
			t.Fatalf("want %d fields, got %d: %v", len(canonicalOrder), len(keys), keys)
		}
		for i, k := range canonicalOrder {
			if keys[i] != k {
				t.Fatalf("field %d = %q, want %q (order is the contract): %v", i, keys[i], k, keys)
			}
		}
	})
}

// TestPropHashIgnoresHashField: the hash seals every field but itself. For any
// event, overwriting the stored Hash cannot move Canonical or ComputeHash — the
// property that lets a reader recompute and compare against the stored seal.
func TestPropHashIgnoresHashField(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := genEvent(t)
		other := e
		other.Hash = "0123456789abcdef" + e.Hash
		if !bytes.Equal(Canonical(e), Canonical(other)) {
			t.Fatal("canonical bytes must not depend on the stored hash field")
		}
		if ComputeHash(e) != ComputeHash(other) {
			t.Fatal("computed hash must not depend on the stored hash field")
		}
	})
}

// TestPropBodyHashedVerbatim: the body is hashed as its raw bytes, never
// re-marshalled. Two events identical but for insignificant body whitespace
// therefore seal to DIFFERENT hashes — the exact reason EncodeEvent must persist
// the writer's bytes and json.Marshal (which compacts a RawMessage) would break
// the chain. The verbatim splice also means the body bytes appear intact inside
// the canonical bytes.
func TestPropBodyHashedVerbatim(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		key := rapid.StringMatching(`[a-z]{1,6}`).Draw(t, "key")
		val := rapid.IntRange(-1000, 1000).Draw(t, "val")
		compact := json.RawMessage(`{"` + key + `":` + itoa(val) + `}`)
		spaced := json.RawMessage(`{ "` + key + `" : ` + itoa(val) + ` }`)

		e := genEvent(t)
		e.Body = compact
		spacedEvent := e
		spacedEvent.Body = spaced

		if !bytes.Contains(Canonical(e), compact) {
			t.Fatalf("body must be spliced verbatim into canonical bytes:\n canon=%s\n body=%s", Canonical(e), compact)
		}
		if ComputeHash(e) == ComputeHash(spacedEvent) {
			t.Fatal("insignificant body whitespace must change the hash (bodies are hashed verbatim, not re-marshalled)")
		}
	})
}

// topLevelKeys returns the object keys of a JSON object in source order,
// skipping over each value regardless of its shape. It never mistakes a nested
// key or a string value for a top-level key, so it is a faithful witness of the
// canonical field order.
func topLevelKeys(t *rapid.T, data []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(data))
	open, err := dec.Token()
	if err != nil {
		t.Fatalf("read object open: %v (data=%s)", err, data)
	}
	if d, ok := open.(json.Delim); !ok || d != '{' {
		t.Fatalf("canonical bytes must be a JSON object, got %v", open)
	}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		key, ok := kt.(string)
		if !ok {
			t.Fatalf("object key is not a string: %v", kt)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value for %q: %v", key, err)
		}
	}
	return keys
}
