package driverstate

import (
	"bytes"
	"encoding/json"
	"testing"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// Native fuzz targets on the mechanism's byte parsers — the functions that read
// possibly-crash-torn, possibly-adversarial ledger bytes off disk. These are the
// unexported guts (decodeLedger, trimTornTail), so the targets live in-package.
// The guarantee is absolute: NO input bytes may panic. A corrupt ledger is an
// error value or a trimmed prefix, never a crash that takes down a reader.

// FuzzDecodeLedger drives the chain-verifying line decoder with arbitrary bytes.
// It must never panic; whatever it accepts is a verified chain, so re-encoding
// each accepted event and decoding the result round-trips to the same canonical
// bytes.
func FuzzDecodeLedger(f *testing.F) {
	seedLedgers(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		events, err := decodeLedger(data)
		if err != nil {
			return // a broken chain is an error value — that is the contract
		}
		for _, e := range events {
			// An accepted event round-trips through the canonical encoder: encode,
			// decode the single event (chain-free — decodeLedger would re-check the
			// Prev link a standalone line cannot satisfy), and re-encode identically.
			line := dsc.EncodeEvent(e)
			e2, err := dsc.DecodeEvent(line)
			if err != nil {
				t.Fatalf("re-encoding an accepted event produced an undecodable line: %v\nline=%s", err, line)
			}
			if !bytes.Equal(dsc.EncodeEvent(e2), line) {
				t.Fatalf("EncodeEvent is not a fixed point for an accepted event:\n line=%s", line)
			}
		}
	})
}

// FuzzTornTailHeal drives the torn-tail trimmer with arbitrary bytes. It must
// never panic and always return a prefix that ends at a newline (or is empty)
// with no newline stranded past the boundary — the invariant readAndHealLedger
// relies on to keep only complete lines.
func FuzzTornTailHeal(f *testing.F) {
	seedLedgers(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		trimmed := trimTornTail(data)
		if len(trimmed) > len(data) {
			t.Fatal("trim grew the input")
		}
		if !bytes.Equal(trimmed, data[:len(trimmed)]) {
			t.Fatal("trim is not a prefix of the input")
		}
		if len(trimmed) > 0 && trimmed[len(trimmed)-1] != '\n' {
			t.Fatalf("a non-empty trim must end at a newline, ends with 0x%02x", trimmed[len(trimmed)-1])
		}
		if bytes.Contains(data[len(trimmed):], []byte("\n")) {
			t.Fatal("a newline was stranded past the trim boundary")
		}
	})
}

// seedLedgers seeds both targets with real committed lines and adversarial
// shapes: a torn (newline-free) tail, an empty file, and a bare newline.
//
// The valid line is built through ComputeHash/EncodeEvent, not hand-written
// JSON: decodeLedger verifies Hash == ComputeHash(e), so a placeholder hash
// would fail verifyLink on the first line and the fuzzer could never reach the
// accepted-event round-trip branch (mutating arbitrary bytes into the exact
// SHA-256 is infeasible). Anchoring the seed as a chain head (Prev empty, Hash
// sealed) is what lets successful parses actually get fuzzed.
func seedLedgers(f *testing.F) {
	f.Helper()
	e := Event{
		ID:    "evt_1",
		Run:   "dsr_1",
		V:     dsc.Version,
		Kind:  dsc.KindRunImported,
		Time:  baseTime,
		Actor: "session:a",
		Body:  json.RawMessage(`{"repo":"r","source":"s","manifest":{},"streams":[{"stream":"dss_1","doc_path":"d"}]}`),
		Prev:  "",
	}
	e.Hash = dsc.ComputeHash(e)
	l1 := dsc.EncodeEvent(e) // no trailing newline

	f.Add(append(append([]byte{}, l1...), '\n'))
	f.Add(l1) // no trailing newline — a torn tail
	f.Add([]byte("\n"))
	f.Add([]byte(""))
	f.Add([]byte(`{"id":"evt_1","v":`)) // truncated JSON
}
