package driverstate

import (
	"bytes"
	"testing"
)

// Go native fuzzing (stdlib, no dependency, runs on every platform including
// Windows) drives the byte parsers with adversarial input the corpus never
// thought to write. The contract here is narrow and absolute: the tolerant
// readers must never panic on ANY bytes — a malformed ledger line is an error
// value, never a crash — and whatever they DO accept must round-trip through the
// canonical encoder unchanged.

// FuzzDecodeEvent asserts DecodeEvent never panics and that its accepted inputs
// re-encode idempotently: once an event decodes, EncodeEvent is a fixed point
// (decode∘encode∘decode == decode∘encode). A regression that let the decoder
// accept bytes the encoder cannot faithfully reproduce would surface here.
func FuzzDecodeEvent(f *testing.F) {
	seedFromCorpus(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		e, err := DecodeEvent(data)
		if err != nil {
			return // a rejected line is a value, not a crash — that is the guarantee
		}
		enc := EncodeEvent(e)
		e2, err := DecodeEvent(enc)
		if err != nil {
			t.Fatalf("re-encoding a decoded event produced undecodable bytes: %v\nenc=%s", err, enc)
		}
		if reEnc := EncodeEvent(e2); !bytes.Equal(enc, reEnc) {
			t.Fatalf("EncodeEvent is not a fixed point:\n first=%s\nsecond=%s", enc, reEnc)
		}
	})
}

// FuzzReadLedger asserts the multi-line tolerant reader never panics on
// arbitrary bytes: it returns events + skip-warnings + error, and a partial or
// malformed stream fails loudly (an error), never a crash. When it succeeds,
// every returned event carries a kind the version accepts (unknown kinds are
// skipped into warnings, not returned).
func FuzzReadLedger(f *testing.F) {
	seedFromCorpus(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		events, _, err := ReadLedger(data)
		if err != nil {
			return
		}
		for _, e := range events {
			if !e.Kind.Known() {
				t.Fatalf("ReadLedger returned an unknown kind %q; unknown kinds must be skipped, not returned", e.Kind)
			}
			if e.V != Version {
				t.Fatalf("ReadLedger returned a foreign version %q", e.V)
			}
		}
	})
}

// seedFromCorpus loads representative valid lines so the fuzzer starts from
// coverage-rich inputs and mutates outward, rather than rediscovering JSON from
// random bytes. A couple of hand-written malformed lines seed the reject paths.
func seedFromCorpus(f *testing.F) {
	f.Helper()
	valid := [][]byte{
		[]byte(`{"id":"evt_1","run":"dsr_1","v":"driver-state-v0.1.0","kind":"run_imported","time":"2026-07-16T12:00:00Z","actor":"session:a","body":{"repo":"r","source":"s","manifest":{},"streams":[{"stream":"dss_1","doc_path":"d"}]},"prev":"","hash":"h"}`),
		[]byte(`{"id":"evt_2","run":"dsr_1","v":"driver-state-v0.1.0","kind":"stream_pr_opened","stream":"dss_1","time":"2026-07-16T12:00:01Z","actor":"session:a","body":{"pr":7,"url":"u","head_sha":"abc"},"prev":"h","hash":"h2"}`),
		[]byte(`{"id":"evt_3","run":"dsr_1","v":"driver-state-v0.1.0","kind":"run_finished","time":"2026-07-16T12:00:02Z","actor":"session:a","body":null,"prev":"h2","hash":"h3"}`),
	}
	for _, v := range valid {
		f.Add(v)
	}
	// A two-line ledger and one with an unknown kind exercise the multi-line and
	// skip-warning paths.
	f.Add(bytes.Join(valid, []byte("\n")))
	f.Add([]byte(`{"id":"evt_x","run":"dsr_1","v":"driver-state-v0.1.0","kind":"stream_teleported","stream":"dss_1","time":"2026-07-16T12:00:00Z","actor":"a","body":null,"prev":"","hash":"h"}`))
	// Reject-path seeds: truncated JSON, wrong version, and empty.
	f.Add([]byte(`{"id":"evt_1","v":`))
	f.Add([]byte(`{"id":"e","v":"driver-state-v9.9.9","kind":"run_finished","time":"2026-07-16T12:00:00Z","actor":"a","body":null,"prev":"","hash":"h"}`))
	f.Add([]byte(``))
}
