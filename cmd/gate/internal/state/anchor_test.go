package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openAnchored returns a store whose anchor and key live OUTSIDE the state dir,
// mirroring the production custody split — a state-dir-only adversary cannot
// touch either.
func openAnchored(t *testing.T) *Store {
	t.Helper()
	keyDir := t.TempDir()
	st, err := OpenAnchored(
		t.TempDir(),
		func() time.Time { return time.Unix(1000, 0) },
		filepath.Join(keyDir, "anchor.json"),
		filepath.Join(keyDir, "anchor.key"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func appendN(t *testing.T, st *Store, n int) {
	t.Helper()
	run := NewRunID()
	for i := 0; i < n; i++ {
		if _, err := st.Append(KindEvidence, run, nil, map[string]int{"seq": i}); err != nil {
			t.Fatal(err)
		}
	}
}

// readLines returns the log's lines with trailing newlines stripped.
func readLines(t *testing.T, st *Store) [][]byte {
	t.Helper()
	f, err := os.Open(st.logPath())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		lines = append(lines, append([]byte(nil), sc.Bytes()...))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// writeLines rewrites the log from lines, each terminated by a newline exactly
// as Append writes them.
func writeLines(t *testing.T, st *Store, lines [][]byte) {
	t.Helper()
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(st.logPath(), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAuditIntactAnchored(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 5)
	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("clean anchored chain reported tampered: %+v", res)
	}
}

func TestAuditDetectsTruncation(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 6)
	lines := readLines(t, st)
	writeLines(t, st, lines[:len(lines)-2]) // drop the last 2 entries

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("audit reported intact after tail truncation")
	}
	if !strings.Contains(res.Reason, "truncation") {
		t.Fatalf("truncation not named in reason: %q", res.Reason)
	}
}

// TestAppendAfterTruncationErrors pins the P1 fix: a truncation must not be
// laundered by the next append. After the log is truncated, an Append refuses
// (rather than resealing the anchor at the shorter count) and Audit keeps
// reporting the loss instead of flipping back to intact.
func TestAppendAfterTruncationErrors(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 5)
	lines := readLines(t, st)
	writeLines(t, st, lines[:3]) // drop the last 2 entries

	run := NewRunID()
	_, err := st.Append(KindEvidence, run, nil, map[string]int{"seq": 99})
	if !errors.Is(err, ErrRebindTruncation) {
		t.Fatalf("append after truncation must refuse with ErrRebindTruncation, got %v", err)
	}

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("audit reported intact after a truncation was followed by an append (P1 launder)")
	}
}

// TestAuditEmptyAnchoredStoreIsIntact pins that `gate audit` on a brand-new
// anchored state dir — no appends, so neither the anchor key nor the record
// exists yet — reports an intact (empty) chain rather than erroring on the
// missing key.
func TestAuditEmptyAnchoredStoreIsIntact(t *testing.T) {
	st := openAnchored(t)
	res, err := st.Audit()
	if err != nil {
		t.Fatalf("audit on a fresh anchored store must not error: %v", err)
	}
	if !res.OK {
		t.Fatalf("empty anchored store must audit intact, got %+v", res)
	}
}

func TestAuditDetectsWholeLogDeletion(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 4)
	if err := os.Remove(st.logPath()); err != nil {
		t.Fatal(err)
	}

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("audit reported intact after whole-log deletion (anchor still present)")
	}
	if !strings.Contains(res.Reason, "deletion") {
		t.Fatalf("whole-log deletion not named as deletion in reason: %q", res.Reason)
	}
}

// TestAuditDetectsRehashedRewrite is the load-bearing case: an attacker with
// state-dir write recomputes every Prev/Hash so the chain replays cleanly, yet
// the keyed anchor (key outside the state dir) still catches it. The test also
// proves the negative — the same rewritten log passes a chain-only audit.
func TestAuditDetectsRehashedRewrite(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 5)

	all, err := st.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Forge the last artifact's body, then re-chain from that point so every
	// Prev/Hash is internally consistent.
	all[len(all)-1].Body = json.RawMessage(`{"seq":999,"forged":true}`)
	rechain(t, all)

	lines := make([][]byte, len(all))
	for i, a := range all {
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = raw
	}
	writeLines(t, st, lines)

	// Sanity: the rewritten chain replays cleanly on its own — the anchor is
	// the only thing that can catch this.
	if fault := replayChain(all); !fault.ok() {
		t.Fatalf("test setup: rehashed chain should replay clean, got %q", fault.reason)
	}

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("audit reported intact after a fully rehashed rewrite — anchor did not catch it")
	}
	if !strings.Contains(res.Reason, "rewrite") {
		t.Fatalf("rewrite not named in reason: %q", res.Reason)
	}
}

// rechain recomputes Prev and Hash down the slice so a mutated body produces a
// self-consistent chain — exactly what a rewrite-with-rehash attacker builds.
func rechain(t *testing.T, all []Artifact) {
	t.Helper()
	prev := ""
	for i := range all {
		all[i].Prev = prev
		all[i].Hash = hashArtifact(all[i])
		prev = all[i].Hash
	}
}

func TestAuditLoudWhenAnchorKeyMissing(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 3)
	if err := os.Remove(st.anchor.keyPath); err != nil {
		t.Fatal(err)
	}
	_, err := st.Audit()
	if !errors.Is(err, ErrAnchorKeyMissing) {
		t.Fatalf("want ErrAnchorKeyMissing on the verify path, got %v", err)
	}
	if _, statErr := os.Stat(st.anchor.keyPath); !os.IsNotExist(statErr) {
		t.Fatal("audit silently recreated the anchor key")
	}
}

func TestAuditDetectsMissingAnchorWithLog(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 3)
	if err := os.Remove(st.anchor.path); err != nil {
		t.Fatal(err)
	}
	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("audit reported intact when the anchor was deleted but the log kept its entries")
	}
}

// TestAnchorMigratesExistingLog covers the first-append-over-a-pre-existing-log
// path: a store that was written unanchored, later opened anchored, must have
// its anchor seeded from a full replay (not from a missing record read as
// count 0), so the count reflects every prior entry.
func TestAnchorMigratesExistingLog(t *testing.T) {
	dir := t.TempDir()
	keyDir := t.TempDir()
	clock := func() time.Time { return time.Unix(1000, 0) }

	// Write three entries with NO anchor.
	plain, err := Open(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	run := NewRunID()
	for i := 0; i < 3; i++ {
		if _, err := plain.Append(KindEvidence, run, nil, map[string]int{"seq": i}); err != nil {
			t.Fatal(err)
		}
	}

	// Reopen anchored and append once — the anchor must seed to 4, not 1.
	anchored, err := OpenAnchored(dir, clock, filepath.Join(keyDir, "anchor.json"), filepath.Join(keyDir, "anchor.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anchored.Append(KindEvidence, run, nil, map[string]int{"seq": 3}); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := anchored.anchor.read()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.Count != 4 {
		t.Fatalf("anchor did not seed from the pre-existing log: ok=%v count=%d want 4", ok, rec.Count)
	}
	res, err := anchored.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("migrated store failed audit: %+v", res)
	}
}

// rollAnchorBackOne rewrites the anchor to lag the log by one entry, exactly as
// a crash between the log fsync and the anchor rename would leave it.
func rollAnchorBackOne(t *testing.T, st *Store) {
	t.Helper()
	all, err := st.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := loadAnchorKey(st.anchor.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	prevHead := all[len(all)-2].Hash
	n := len(all) - 1
	stale := anchorRecord{Head: prevHead, Count: n, MAC: anchorMAC(key, prevHead, n)}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.anchor.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAuditNamesIncompleteAppend simulates the benign crash window — the log
// entry synced but the anchor rename didn't land, leaving the log one ahead of
// the anchor. Audit must flag it, but as an interrupted append, not a rewrite.
func TestAuditNamesIncompleteAppend(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 3)
	rollAnchorBackOne(t, st)

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("audit reported intact when the log ran ahead of the anchor")
	}
	if !strings.Contains(res.Reason, "incomplete-append") {
		t.Fatalf("interrupted append not named as such: %q", res.Reason)
	}
}

// TestAnchorSelfHealsAfterCrash pins the recovery contract: after a crash left
// the anchor one behind, the next successful append must reconcile the count
// from the real log (not blindly increment the stale record), so audit returns
// to intact. Without reconciliation a single crash would brick audit forever.
func TestAnchorSelfHealsAfterCrash(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 3)
	rollAnchorBackOne(t, st) // anchor now pins count=2 against a 3-entry log

	// One more successful append — the recovery write.
	appendN(t, st, 1)

	rec, ok, err := st.anchor.read()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.Count != 4 {
		t.Fatalf("anchor did not self-heal: ok=%v count=%d want 4", ok, rec.Count)
	}
	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("audit still failing after a recovery append: %+v", res)
	}
}

// The reseal-laundering attack: rewrite the log in place (rehashed into a
// self-consistent chain, same length — the anchor is the only witness), then
// let one legitimate append land. rebind's reconcile path must refuse to
// HMAC-bind the forged prefix as "crash recovery"; the anchor keeps pinning
// the real history and Audit keeps failing.
func TestAppendAfterRewriteRefusesReseal(t *testing.T) {
	st := openAnchored(t)
	appendN(t, st, 5)

	recBefore, ok, err := st.anchor.read()
	if err != nil || !ok {
		t.Fatalf("anchor not readable after appends: ok=%v err=%v", ok, err)
	}

	all, err := st.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	all[2].Body = json.RawMessage(`{"seq":999,"forged":true}`)
	rechain(t, all)
	lines := make([][]byte, len(all))
	for i, a := range all {
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = raw
	}
	writeLines(t, st, lines)

	// The legitimate append lands in the log (fsync'd before the anchor
	// moves) but the anchor must refuse to advance over the forged prefix.
	_, err = st.Append(KindEvidence, NewRunID(), nil, map[string]int{"seq": 5})
	if !errors.Is(err, ErrRebindRewrite) {
		t.Fatalf("append over a rewritten prefix did not refuse reseal: %v", err)
	}

	recAfter, ok, err := st.anchor.read()
	if err != nil || !ok {
		t.Fatalf("anchor not readable after refused reseal: ok=%v err=%v", ok, err)
	}
	if recAfter != recBefore {
		t.Fatalf("anchor moved despite refusal: before=%+v after=%+v", recBefore, recAfter)
	}

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("audit reported intact after a rewrite + refused reseal")
	}
}
