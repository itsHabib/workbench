package driverstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	withTTL(t, 20*time.Millisecond)
	dir := t.TempDir()
	l, _ := Claim(dir, "dsr_run1", "session:a")
	time.Sleep(40 * time.Millisecond) // lease expires
	_, err := Append(dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	if !errors.Is(err, errLeaseExpired) {
		t.Fatalf("want errLeaseExpired, got %v", err)
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
