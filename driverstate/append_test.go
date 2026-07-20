package driverstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

var baseTime = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

func ev(id string, kind dsc.Kind, stream, actor string, tm time.Time, body any) Event {
	var raw json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		raw = b
	}
	return Event{
		ID:     id,
		V:      dsc.Version,
		Kind:   kind,
		Stream: stream,
		Time:   tm,
		Actor:  actor,
		Body:   raw,
	}
}

func mustAppend(t *testing.T, dir string, l Lease, e Event) Event {
	t.Helper()
	out, err := Append(dir, l, e)
	if err != nil {
		t.Fatalf("append %s: %v", e.Kind, err)
	}
	return out
}

// happyLifecycle records a full single-stream run and returns dir/run.
func happyLifecycle(t *testing.T) (string, Lease) {
	t.Helper()
	dir := t.TempDir()
	run := "dsr_run1"
	stream := "dss_a"
	l, err := Claim(dir, run, "session:a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo:     "itsHabib/workbench",
		Source:   "docs/driver.md",
		Manifest: json.RawMessage(`{"v":1}`),
		Streams:  []dsc.StreamSpec{{Stream: stream, DocPath: "docs/x.md"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, stream, "session:a", baseTime.Add(time.Second), nil))
	mustAppend(t, dir, l, ev("evt_3", dsc.KindStreamAttempt, stream, "session:a", baseTime.Add(2*time.Second), dsc.StreamAttemptBody{Seq: 1, DocPath: "docs/x.md", Terminal: true}))
	mustAppend(t, dir, l, ev("evt_4", dsc.KindStreamPROpened, stream, "session:a", baseTime.Add(3*time.Second), dsc.StreamPROpenedBody{PR: 7, URL: "https://x/7", HeadSHA: "abc"}))
	mustAppend(t, dir, l, ev("evt_5", dsc.KindReviewCycle, stream, "session:a", baseTime.Add(4*time.Second), dsc.ReviewCycleBody{Cycle: 1, PanelSettled: true, Findings: 0}))
	mustAppend(t, dir, l, ev("evt_6", dsc.KindStreamMerged, stream, "session:a", baseTime.Add(5*time.Second), dsc.StreamMergedBody{PR: 7, MergeCommit: "def", MergedAt: "2026-07-16T12:05:00Z"}))
	mustAppend(t, dir, l, ev("evt_7", dsc.KindRunFinished, "", "session:a", baseTime.Add(6*time.Second), nil))
	return dir, l
}

func readLedger(t *testing.T, dir, run string) []Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, run, "events.jsonl"))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	events, err := decodeLedger(data)
	if err != nil {
		t.Fatalf("decode ledger: %v", err)
	}
	return events
}

func TestHappyLifecycleChain(t *testing.T) {
	dir, l := happyLifecycle(t)
	events := readLedger(t, dir, l.Run())
	if len(events) != 7 {
		t.Fatalf("want 7 events, got %d", len(events))
	}
	prevHash := ""
	for i, e := range events {
		if e.Prev != prevHash {
			t.Fatalf("event %d prev = %q, want %q", i, e.Prev, prevHash)
		}
		if got := dsc.ComputeHash(e); got != e.Hash {
			t.Fatalf("event %d hash mismatch: stored %q recomputed %q", i, e.Hash, got)
		}
		prevHash = e.Hash
	}
}

func TestReferenceVector(t *testing.T) {
	// The pinned P1 canonical-encoding reference vector: appending the exact
	// vector event must reproduce its pinned hash byte-for-byte.
	vecPath := filepath.Join("..", "contracts", "driverstate", "testdata", "canonical-vector.json")
	data, err := os.ReadFile(vecPath)
	if err != nil {
		t.Fatalf("read vector: %v", err)
	}
	var vec struct {
		Canonical string `json:"canonical"`
		Hash      string `json:"hash"`
	}
	if err := json.Unmarshal(data, &vec); err != nil {
		t.Fatalf("decode vector: %v", err)
	}
	run := "dsr_01JQRUN0000000000000000RUN0"
	body := json.RawMessage(`{"repo":"itsHabib/workbench","source":"docs/driver/driver.md","manifest":{"driver_version":1,"repo":"workbench"},"streams":[{"stream":"dss_01JQSTREAM0000000000000001","doc_path":"docs/features/driver-state/spec.md"}],"ship_run_ref":"drv_01JQSHIP000000000000000001"}`)
	e := Event{
		ID:     "evt_01JQEVENT00000000000000IMP0",
		V:      dsc.Version,
		Kind:   dsc.KindRunImported,
		Time:   time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Actor:  "session:demo-01",
		ExtRef: "drv_01JQSHIP000000000000000001",
		Body:   body,
	}
	dir := t.TempDir()
	l, err := Claim(dir, run, "session:demo-01")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	out, err := Append(dir, l, e)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if out.Hash != vec.Hash {
		t.Fatalf("appended hash %q != pinned vector hash %q", out.Hash, vec.Hash)
	}
	if got := string(dsc.Canonical(out)); got != vec.Canonical {
		t.Fatalf("canonical bytes drift:\n got %s\nwant %s", got, vec.Canonical)
	}
}

func TestIdempotentReappend(t *testing.T) {
	dir := t.TempDir()
	l, _ := Claim(dir, "dsr_run1", "session:a")
	first := mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	again, err := Append(dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime.Add(time.Hour), dsc.RunImportedBody{
		Repo: "different", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	if err != nil {
		t.Fatalf("re-append: %v", err)
	}
	if again.Hash != first.Hash {
		t.Fatalf("idempotent re-append should return original: %q vs %q", again.Hash, first.Hash)
	}
	if events := readLedger(t, dir, "dsr_run1"); len(events) != 1 {
		t.Fatalf("ledger should hold one event, got %d", len(events))
	}
}

func TestMonotonicityRejected(t *testing.T) {
	dir := t.TempDir()
	l, _ := Claim(dir, "dsr_run1", "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	_, err := Append(dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(-time.Hour), nil))
	if err == nil || !strings.Contains(err.Error(), "monotonicity") {
		t.Fatalf("want monotonicity rejection, got %v", err)
	}
}

func TestIllegalTransitionRejected(t *testing.T) {
	dir := t.TempDir()
	l, _ := Claim(dir, "dsr_run1", "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))
	// stream is `dispatched` with no pr_opened: merge is illegal (spec §7 F2).
	_, err := Append(dir, l, ev("evt_3", dsc.KindStreamMerged, "dss_a", "session:a", baseTime.Add(2*time.Second), dsc.StreamMergedBody{PR: 1, MergeCommit: "c", MergedAt: "t"}))
	var illegal ErrIllegalTransition
	if !errors.As(err, &illegal) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
	if illegal.From != dsc.StatusDispatched || illegal.Event != string(dsc.KindStreamMerged) {
		t.Fatalf("wrong transition detail: %+v", illegal)
	}
}

func TestSeqMustIncrease(t *testing.T) {
	dir := t.TempDir()
	l, _ := Claim(dir, "dsr_run1", "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))
	mustAppend(t, dir, l, ev("evt_3", dsc.KindStreamAttempt, "dss_a", "session:a", baseTime.Add(2*time.Second), dsc.StreamAttemptBody{Seq: 2, DocPath: "d", Terminal: false}))
	// seq 2 already seen; a non-increasing seq is rejected.
	_, err := Append(dir, l, ev("evt_4", dsc.KindStreamAttempt, "dss_a", "session:a", baseTime.Add(3*time.Second), dsc.StreamAttemptBody{Seq: 2, DocPath: "d", Terminal: true}))
	if err == nil || !strings.Contains(err.Error(), "seq") {
		t.Fatalf("want seq rejection, got %v", err)
	}
}

// TestAppendEventIDPrefix rejects ids that lack the evt_ prefix while keeping
// short fixture ids (evt_1) and NewEventID-shaped hex ids accepted.
func TestAppendEventIDPrefix(t *testing.T) {
	importBody := dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`),
		Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}
	cases := []struct {
		name    string
		id      string
		wantErr string
	}{
		{name: "short fixture id ok", id: "evt_1"},
		{name: "hex-shaped id ok", id: "evt_0123456789abcdef0123456789abcdef"},
		{name: "missing prefix rejected", id: "bad_1", wantErr: "must start with evt_"},
		{name: "bare ulid rejected", id: "01JQEVENT00000000000000IMP0", wantErr: "must start with evt_"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			l, err := Claim(dir, "dsr_run1", "session:a")
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			_, err = Append(dir, l, ev(c.id, dsc.KindRunImported, "", "session:a", baseTime, importBody))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestTornTailHealed(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	first := mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	// Simulate a crash mid-write: a torn partial line with no trailing newline.
	ledger := filepath.Join(dir, run, "events.jsonl")
	f, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"evt_torn","v":"driver-state-v0.1.0","kind":"stream_dispat`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// The next append must heal the torn tail, then commit cleanly.
	second := mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))
	events := readLedger(t, dir, run)
	if len(events) != 2 {
		t.Fatalf("want 2 healed events, got %d", len(events))
	}
	if events[0].Hash != first.Hash || events[1].Hash != second.Hash {
		t.Fatalf("healed chain mismatch")
	}
	if events[1].Prev != first.Hash {
		t.Fatalf("healed event should chain to first head")
	}
}

func TestAppendWithoutLiveLease(t *testing.T) {
	withTTL(t, 5*time.Second)
	dir := t.TempDir()
	l, _ := Claim(dir, "dsr_run1", "session:a")
	if err := ExpireLeaseForTest(dir, "dsr_run1"); err != nil { // lease expires
		t.Fatalf("force expire: %v", err)
	}
	_, err := Append(dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("want ErrLeaseExpired, got %v", err)
	}
}

func importBody(repo, source, generatedAt, stream string) dsc.RunImportedBody {
	return dsc.RunImportedBody{
		Repo:        repo,
		Source:      source,
		GeneratedAt: generatedAt,
		Manifest:    json.RawMessage(`{}`),
		Streams:     []dsc.StreamSpec{{Stream: stream, DocPath: "d"}},
	}
}

// Cluster 1 — the append is bound to the lease's own state dir: a mismatched dir
// must be rejected before any write, never validated in one root and written to
// another.
func TestAppendRejectsMismatchedDir(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	l, err := Claim(dirA, "dsr_run1", "session:a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = Append(dirB, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("r", "s", "", "dss_a")))
	if err == nil || !strings.Contains(err.Error(), "does not match lease dir") {
		t.Fatalf("want dir-mismatch rejection, got %v", err)
	}
}

// Cluster 1 — the lease is revalidated INSIDE the append lock: a holder whose
// lease was stolen (generation bumped) must not be able to write, even though it
// passed a Claim earlier.
func TestAppendRevalidatesStolenLease(t *testing.T) {
	withTTL(t, 5*time.Second)
	dir := t.TempDir()
	first, err := Claim(dir, "dsr_run1", "session:a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := ExpireLeaseForTest(dir, "dsr_run1"); err != nil { // first's lease expires
		t.Fatalf("force expire: %v", err)
	}
	if _, err := Claim(dir, "dsr_run1", "session:b"); err != nil {
		t.Fatalf("steal claim: %v", err)
	}
	// first lost the lease to session:b — its append must fail with ErrLocked,
	// not slip through on the stale generation.
	_, err = Append(dir, first, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("r", "s", "", "dss_a")))
	if !errors.As(err, new(ErrLocked)) {
		t.Fatalf("want ErrLocked from stolen-lease append, got %v", err)
	}
}

// Cluster 2 — a retried import reusing (repo, source, generated_at) with a fresh
// run/event id returns the original committed event and mints no second run.
func TestImportDedupeAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	l1, _ := Claim(dir, "dsr_run1", "session:a")
	orig := mustAppend(t, dir, l1, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("itsHabib/wb", "docs/driver.md", "2026-07-16T00:00:00Z", "dss_a")))
	if err := l1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	l2, _ := Claim(dir, "dsr_run2", "session:b")
	dup, err := Append(dir, l2, ev("evt_99", dsc.KindRunImported, "", "session:b", baseTime.Add(time.Hour), importBody("itsHabib/wb", "docs/driver.md", "2026-07-16T00:00:00Z", "dss_a")))
	if err != nil {
		t.Fatalf("dedupe append: %v", err)
	}
	if dup.ID != orig.ID || dup.Hash != orig.Hash {
		t.Fatalf("retried import should return the original event, got id=%q hash=%q", dup.ID, dup.Hash)
	}
	// run2's ledger must be empty — no second run was minted.
	if _, err := os.Stat(filepath.Join(dir, "dsr_run2", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dedupe should not have written a second run's ledger, stat = %v", err)
	}
}

// Cluster 2 — a different generated_at is a genuinely new import and DOES mint a
// second run (the key discriminates, it does not blanket-suppress).
func TestImportDifferentGeneratedAtMintsRun(t *testing.T) {
	dir := t.TempDir()
	l1, _ := Claim(dir, "dsr_run1", "session:a")
	mustAppend(t, dir, l1, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("r", "s", "2026-07-16T00:00:00Z", "dss_a")))
	_ = l1.Release()

	l2, _ := Claim(dir, "dsr_run2", "session:b")
	out := mustAppend(t, dir, l2, ev("evt_2", dsc.KindRunImported, "", "session:b", baseTime.Add(time.Hour), importBody("r", "s", "2026-07-17T00:00:00Z", "dss_a")))
	if out.ID != "evt_2" {
		t.Fatalf("distinct generated_at should mint a new run, got id=%q", out.ID)
	}
	if events := readLedger(t, dir, "dsr_run2"); len(events) != 1 {
		t.Fatalf("run2 should hold its own import, got %d events", len(events))
	}
}

// Cycle 2, cluster 2 — two concurrent imports of the SAME (repo,source,
// generated_at) into DIFFERENT run dirs must serialize under the state-root
// import lock: exactly one run's ledger is written, and both Append calls return
// that one committed event.
func TestConcurrentImportDedupe(t *testing.T) {
	dir := t.TempDir()
	body := importBody("itsHabib/wb", "docs/driver.md", "2026-07-16T00:00:00Z", "dss_a")

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]Event, 2)
	errs := make([]error, 2)
	runs := []string{"dsr_run1", "dsr_run2"}
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := Claim(dir, runs[i], "session:a")
			if err != nil {
				errs[i] = err
				return
			}
			<-start
			results[i], errs[i] = Append(dir, l, ev("evt_"+runs[i], dsc.KindRunImported, "", "session:a", baseTime, body))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	if results[0].Hash != results[1].Hash {
		t.Fatalf("both imports should resolve to one committed event: %q vs %q", results[0].Hash, results[1].Hash)
	}
	ledgers := 0
	for _, run := range runs {
		if _, err := os.Stat(filepath.Join(dir, run, "events.jsonl")); err == nil {
			ledgers++
		}
	}
	if ledgers != 1 {
		t.Fatalf("exactly one run should have minted a ledger, got %d", ledgers)
	}
}

// Cycle 2, cluster 3 — a sibling run mid-append (a committed import followed by a
// crash-torn partial line with no newline) must NOT hide its committed import
// from the dedupe scan: the retried import still resolves to the original.
func TestTornTailSiblingStillDedupes(t *testing.T) {
	dir := t.TempDir()
	l1, _ := Claim(dir, "dsr_run1", "session:a")
	orig := mustAppend(t, dir, l1, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("r", "s", "2026-07-16T00:00:00Z", "dss_a")))
	if err := l1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Simulate run1 crash-torn mid-append: a partial next line, no newline.
	ledger := filepath.Join(dir, "dsr_run1", "events.jsonl")
	f, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"evt_torn","v":"driver-state-v0.1.0","kind":"stream_dispa`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	l2, _ := Claim(dir, "dsr_run2", "session:b")
	dup, err := Append(dir, l2, ev("evt_2", dsc.KindRunImported, "", "session:b", baseTime.Add(time.Hour), importBody("r", "s", "2026-07-16T00:00:00Z", "dss_a")))
	if err != nil {
		t.Fatalf("dedupe over torn sibling: %v", err)
	}
	if dup.Hash != orig.Hash {
		t.Fatalf("retried import should resolve to run1's original despite its torn tail, got %q want %q", dup.Hash, orig.Hash)
	}
	if _, err := os.Stat(filepath.Join(dir, "dsr_run2", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("run2 should not have minted a ledger, stat = %v", err)
	}
}

// Cluster 3 — a mid-chain corruption is caught on read: Append refuses to seal a
// new event onto an unverified head, returning ErrChainBroken.
func TestCorruptChainRejectedOnAppend(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("r", "s", "", "dss_a")))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))

	corruptFirstHash(t, filepath.Join(dir, run, "events.jsonl"))

	_, err := Append(dir, l, ev("evt_3", dsc.KindStreamAttempt, "dss_a", "session:a", baseTime.Add(2*time.Second), dsc.StreamAttemptBody{Seq: 1, DocPath: "d", Terminal: false}))
	if !errors.Is(err, ErrChainBroken) {
		t.Fatalf("want ErrChainBroken appending onto a corrupt chain, got %v", err)
	}
}

// corruptFirstHash flips one hex digit in the first ledger line's sealed hash,
// keeping the line valid JSON so it decodes but fails chain verification.
func corruptFirstHash(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitN(data, []byte("\n"), 2)
	marker := []byte(`"hash":"`)
	idx := bytes.LastIndex(lines[0], marker)
	if idx < 0 {
		t.Fatal("no hash field in first line")
	}
	pos := idx + len(marker)
	if lines[0][pos] == '0' {
		lines[0][pos] = '1'
	} else {
		lines[0][pos] = '0'
	}
	if err := os.WriteFile(path, bytes.Join(lines, []byte("\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Cluster 6 — when the retry budget is exhausted on a held lock, the caller sees
// a real contention error, never the internal errRetry marker.
func TestAppendLockContentionError(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	// A FRESH append.lock (mtime now) is a live writer's lock: it is not stale,
	// so the retry budget is exhausted rather than broken.
	lock := filepath.Join(dir, run, "append.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lock)
	_, err := Append(dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("r", "s", "", "dss_a")))
	if errors.Is(err, errRetry) {
		t.Fatalf("internal errRetry marker leaked to caller: %v", err)
	}
	if !errors.Is(err, errLockContended) {
		t.Fatalf("want errLockContended, got %v", err)
	}
}

// Cluster 7 — once run_finished is on the ledger, no further event is legal: not
// a reopening stream transition, not a second run_finished.
func TestNoTransitionsAfterRunFinished(t *testing.T) {
	dir, l := happyLifecycle(t)
	stream := "dss_a"

	_, err := Append(dir, l, ev("evt_reopen", dsc.KindStreamDispatched, stream, "session:a", baseTime.Add(10*time.Second), nil))
	var illegal ErrIllegalTransition
	if !errors.As(err, &illegal) || illegal.From != "run_finished" {
		t.Fatalf("want ErrIllegalTransition from run_finished, got %v", err)
	}

	_, err = Append(dir, l, ev("evt_finish2", dsc.KindRunFinished, "", "session:a", baseTime.Add(11*time.Second), nil))
	if !errors.As(err, new(ErrIllegalTransition)) {
		t.Fatalf("want a second run_finished rejected, got %v", err)
	}
}

// Cluster 9 — an append.lock orphaned by a crashed writer (mtime older than the
// lease TTL) self-clears: the next append breaks it and commits.
func TestStaleAppendLockRecovered(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	lock := filepath.Join(dir, run, "append.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Age the lock well past the lease TTL — a crashed writer's orphan.
	old := time.Now().Add(-2 * DefaultLeaseTTL)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	out := mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, importBody("r", "s", "", "dss_a")))
	if out.ID != "evt_1" {
		t.Fatalf("append over stale lock should succeed, got id=%q", out.ID)
	}
}

func TestRunFinishedRequiresTerminalStreams(t *testing.T) {
	dir := t.TempDir()
	l, _ := Claim(dir, "dsr_run1", "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))
	// dss_a is dispatched (non-terminal): run_finished must be rejected.
	_, err := Append(dir, l, ev("evt_3", dsc.KindRunFinished, "", "session:a", baseTime.Add(2*time.Second), nil))
	if !errors.As(err, new(ErrIllegalTransition)) {
		t.Fatalf("want ErrIllegalTransition for premature run_finished, got %v", err)
	}
}
