package driverstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Canonical returns the pinned canonical byte encoding of an event, the exact
// bytes the hash chain is computed over (spec §5, review M3). The rules, kept
// dead simple so ship's independent TS emitter reproduces them:
//
//   - UTF-8 JSON object, every field present, in Event struct-declaration order.
//   - No insignificant whitespace.
//   - Hash is always the empty string in the canonical form.
//   - Body is spliced in as its raw bytes VERBATIM — never re-marshalled, so the
//     writer's bytes are the canonical bytes. A nil body encodes as null.
//   - Scalars are JSON-encoded with HTML escaping OFF, so <, > and & survive as
//     themselves across languages.
//
// The reference vector under testdata/ pins one event to its canonical bytes
// and hash; the conformance suite fails if either drifts.
func Canonical(e Event) []byte {
	e.Hash = ""
	return encode(e)
}

// EncodeEvent is the persistence encoding: the canonical layout with the real
// Hash, ready to write as a ledger line. Writers MUST persist these exact
// bytes — json.Marshal compacts a json.RawMessage body, silently altering the
// very bytes the hash covers, so a marshal-persisted event with insignificant
// body whitespace would fail chain verification on read-back.
func EncodeEvent(e Event) []byte {
	return encode(e)
}

func encode(e Event) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	b.WriteString(`"id":`)
	b.Write(jsonScalar(e.ID))
	b.WriteString(`,"run":`)
	b.Write(jsonScalar(e.Run))
	b.WriteString(`,"v":`)
	b.Write(jsonScalar(e.V))
	b.WriteString(`,"kind":`)
	b.Write(jsonScalar(string(e.Kind)))
	b.WriteString(`,"stream":`)
	b.Write(jsonScalar(e.Stream))
	b.WriteString(`,"time":`)
	b.Write(jsonScalar(e.Time))
	b.WriteString(`,"actor":`)
	b.Write(jsonScalar(e.Actor))
	b.WriteString(`,"ext_ref":`)
	b.Write(jsonScalar(e.ExtRef))
	b.WriteString(`,"body":`)
	b.Write(rawBody(e.Body))
	b.WriteString(`,"prev":`)
	b.Write(jsonScalar(e.Prev))
	b.WriteString(`,"hash":`)
	b.Write(jsonScalar(e.Hash))
	b.WriteByte('}')
	return b.Bytes()
}

// ComputeHash is the chain seal: SHA-256 over Canonical(e), hex-encoded. Prev is
// part of the canonical bytes, so each hash commits to the whole prior chain.
func ComputeHash(e Event) string {
	sum := sha256.Sum256(Canonical(e))
	return hex.EncodeToString(sum[:])
}

// jsonScalar encodes one scalar with HTML escaping off and the trailing newline
// json.Encoder appends trimmed. Strings and time.Time never fail to encode.
func jsonScalar(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func rawBody(body json.RawMessage) []byte {
	if len(body) == 0 {
		return []byte("null")
	}
	return []byte(body)
}
