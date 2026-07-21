package state

import (
	"os"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// The state log is the gate's evidence of record: every merge decision is
// reconstructable from it, and audit is what makes a rewritten log detectable.
// The example tests pin one body-edit tamper; these generators assert the chain
// laws over any sequence of appends and any single-byte corruption — an audit
// that ever passed a changed log would launder a forged authorization.

var propKinds = []string{KindEvidence, KindVerdict, KindGrant, KindAction, KindEscalation, KindJudgment}

func propStore(t *rapid.T) *Store {
	dir, err := os.MkdirTemp("", "stateprop")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	// A fixed clock keeps Time out of the fuzz surface — the chain laws under
	// test are about content and linkage, not timestamps.
	st, err := Open(dir, func() time.Time { return time.Unix(1000, 0) })
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

// appendMany appends a rapid-drawn sequence of artifacts and returns them. Bodies
// are arbitrary JSON-able maps; parents are arbitrary provenance strings — none
// of it should ever make a clean append fail or a clean audit trip.
func appendMany(t *rapid.T, st *Store) []Artifact {
	n := rapid.IntRange(1, 6).Draw(t, "n")
	runs := []string{NewRunID(), NewRunID()}
	out := make([]Artifact, 0, n)
	for i := 0; i < n; i++ {
		kind := rapid.SampledFrom(propKinds).Draw(t, "kind")
		run := rapid.SampledFrom(runs).Draw(t, "run")
		parents := rapid.SliceOfN(rapid.StringMatching(`[a-z]{3,10}`), 0, 3).Draw(t, "parents")
		body := rapid.MapOfN(rapid.StringMatching(`[a-z]{1,6}`), rapid.String(), 0, 4).Draw(t, "body")
		a, err := st.Append(kind, run, parents, body)
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		out = append(out, a)
	}
	return out
}

// TestPropAppendThenAuditClean: any sequence of well-formed appends produces a
// log that audits clean, replays to exactly those artifacts in order, and chains
// each Prev to the previous Hash. This is the baseline the tamper properties
// corrupt away from.
func TestPropAppendThenAuditClean(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		st := propStore(t)
		want := appendMany(t, st)

		res, err := st.Audit()
		if err != nil {
			t.Fatalf("audit clean log: %v", err)
		}
		if !res.OK {
			t.Fatalf("clean log reported tampered at %s: %s", res.Artifact, res.Reason)
		}
		if len(res.All) != len(want) {
			t.Fatalf("audit replayed %d artifacts, appended %d", len(res.All), len(want))
		}
		prev := ""
		for i, a := range res.All {
			if a.ID != want[i].ID {
				t.Fatalf("artifact %d out of order: got %s want %s", i, a.ID, want[i].ID)
			}
			if a.Prev != prev {
				t.Fatalf("artifact %d prev %q does not link to prior hash %q", i, a.Prev, prev)
			}
			if hashArtifact(a) != a.Hash {
				t.Fatalf("artifact %d hash does not seal its bytes", i)
			}
			prev = a.Hash
		}
	})
}

// TestPropSingleByteFlipCaughtOrNoOp is the exact tamper guarantee, sharper than
// "any edit is caught": the chain seals each artifact's SEMANTIC projection
// (hashArtifact over id|kind|run|time|prev|parents|body), not the raw log bytes.
// So flipping any byte of the log either
//
//   - is caught — Audit returns not-OK, or the line stops being valid JSON and
//     Audit errors; OR
//   - is a semantic no-op — e.g. renaming an absent optional key, which re-parses
//     to the identical artifact.
//
// What must never happen: Audit returning OK while a re-parse shows changed
// content. That would launder a forged decision as intact.
func TestPropSingleByteFlipCaughtOrNoOp(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		st := propStore(t)
		sealed := appendMany(t, st)

		data, err := os.ReadFile(st.logPath())
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		if len(data) < 2 {
			return
		}
		// Exclude the trailing newline (a blank-line addition is not a content
		// edit the chain covers).
		idx := rapid.IntRange(0, len(data)-2).Draw(t, "flipIdx")
		delta := byte(rapid.IntRange(1, 255).Draw(t, "flipDelta"))
		data[idx] ^= delta
		if err := os.WriteFile(st.logPath(), data, 0o644); err != nil {
			t.Fatalf("write tampered log: %v", err)
		}

		res, err := st.Audit()
		if err != nil {
			return // a line stopped parsing — corruption caught as an error
		}
		if !res.OK {
			return // tamper caught — the common case
		}
		// Audit passed: the flip MUST be a semantic no-op. Prove every re-parsed
		// artifact seals identically to what was committed.
		got, err := st.List(nil)
		if err != nil {
			t.Fatalf("audit OK but re-scan failed: %v", err)
		}
		if len(got) != len(sealed) {
			t.Fatalf("flip at byte %d passed audit but changed the artifact count: %d != %d", idx, len(got), len(sealed))
		}
		for i := range got {
			if got[i].Hash != sealed[i].Hash || got[i].Prev != sealed[i].Prev || hashArtifact(got[i]) != hashArtifact(sealed[i]) {
				t.Fatalf("flip at byte %d (delta %d) passed audit but changed artifact %d's sealed content", idx, delta, i)
			}
		}
	})
}

// TestPropAdjacentReorderCaught: the chain pins order. Swapping any two adjacent
// log lines breaks a Prev link (artifacts carry unique ids, so a swap is never a
// no-op), and audit reports not-OK — a reordering that hid a later block behind
// an earlier pass would otherwise pass unnoticed.
func TestPropAdjacentReorderCaught(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		st := propStore(t)
		sealed := appendMany(t, st)
		if len(sealed) < 2 {
			return
		}
		data, err := os.ReadFile(st.logPath())
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		lines := splitLines(data)
		i := rapid.IntRange(0, len(lines)-2).Draw(t, "swapAt")
		lines[i], lines[i+1] = lines[i+1], lines[i]
		if err := os.WriteFile(st.logPath(), joinLines(lines), 0o644); err != nil {
			t.Fatalf("write reordered log: %v", err)
		}
		res, err := st.Audit()
		if err != nil {
			return // a swap that also broke parsing is still caught
		}
		if res.OK {
			t.Fatalf("audit missed an adjacent swap at line %d", i)
		}
	})
}

// splitLines returns the non-empty newline-terminated records of the log.
func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b != '\n' {
			continue
		}
		if i > start {
			out = append(out, append([]byte{}, data[start:i]...))
		}
		start = i + 1
	}
	if start < len(data) {
		out = append(out, append([]byte{}, data[start:]...))
	}
	return out
}

func joinLines(lines [][]byte) []byte {
	var out []byte
	for _, l := range lines {
		out = append(out, l...)
		out = append(out, '\n')
	}
	return out
}
