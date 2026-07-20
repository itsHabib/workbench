package driverstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// buildUnknownKindLine constructs a well-formed event with an unknown kind,
// maintaining the hash chain (Prev = prevHash, Hash = SHA-256(canonical)).
// The returned bytes are the encoded event WITHOUT a trailing newline.
func buildUnknownKindLine(t *testing.T, id, run, prevHash string) []byte {
	t.Helper()
	e := Event{
		ID:    id,
		Run:   run,
		V:     dsc.Version,
		Kind:  "unknown_future_kind_v99",
		Time:  baseTime.Add(10 * time.Second),
		Actor: "session:a",
		Body:  json.RawMessage(`{}`),
		Prev:  prevHash,
	}
	e.Hash = dsc.ComputeHash(e)
	return dsc.EncodeEvent(e)
}

// ---- Reduce tests --------------------------------------------------------

// TestReduceReferenceVector reduces a ledger built from the P1 reference-vector
// event and checks the resulting RunState matches the event's manifest data.
// This is acceptance criterion 1: Reduce over the P1 reference-vector ledger
// yields the pinned RunState.
func TestReduceReferenceVector(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_01JQRUN0000000000000000RUN0"
	stream := "dss_01JQSTREAM0000000000000001"
	l, err := Claim(dir, run, "session:demo-01")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	body := json.RawMessage(`{"repo":"itsHabib/workbench","source":"docs/driver/driver.md","manifest":{"driver_version":1,"repo":"workbench"},"streams":[{"stream":"dss_01JQSTREAM0000000000000001","doc_path":"docs/features/driver-state/spec.md"}],"ship_run_ref":"drv_01JQSHIP000000000000000001"}`)
	mustAppend(t, dir, l, Event{
		ID:     "evt_01JQEVENT00000000000000IMP0",
		V:      dsc.Version,
		Kind:   dsc.KindRunImported,
		Time:   time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Actor:  "session:demo-01",
		ExtRef: "drv_01JQSHIP000000000000000001",
		Body:   body,
	})

	state, err := Reduce(dir, run)
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	if state.Run.Repo != "itsHabib/workbench" {
		t.Errorf("repo = %q, want %q", state.Run.Repo, "itsHabib/workbench")
	}
	if state.Run.Source != "docs/driver/driver.md" {
		t.Errorf("source = %q, want %q", state.Run.Source, "docs/driver/driver.md")
	}
	if state.Run.Status != RunStatusOpen {
		t.Errorf("run status = %q, want %q", state.Run.Status, RunStatusOpen)
	}
	if !state.Run.ImportedAt.Equal(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("imported_at = %v, want 2026-07-16T12:00:00Z", state.Run.ImportedAt)
	}
	sr, ok := state.Streams[stream]
	if !ok {
		t.Fatalf("stream %q not in RunState.Streams", stream)
	}
	if sr.Status != dsc.StatusPending {
		t.Errorf("stream status = %q, want %q", sr.Status, dsc.StatusPending)
	}
}

// TestReduceManifestSeededPendingStreams covers spec §7 F3: a resuming session
// sees the full stream set from the manifest even when some streams have no
// subsequent events.
func TestReduceManifestSeededPendingStreams(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo:     "r",
		Source:   "s",
		Manifest: json.RawMessage(`{}`),
		Streams: []dsc.StreamSpec{
			{Stream: "dss_a", DocPath: "a.md"},
			{Stream: "dss_b", DocPath: "b.md"},
			{Stream: "dss_c", DocPath: "c.md"},
		},
	}))
	// Only dss_a receives a dispatch; dss_b and dss_c remain manifest-seeded pending.
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))

	state, err := Reduce(dir, run)
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	if len(state.Streams) != 3 {
		t.Fatalf("want 3 streams (manifest-seeded), got %d", len(state.Streams))
	}
	if state.Streams["dss_a"].Status != dsc.StatusDispatched {
		t.Errorf("dss_a = %q, want dispatched", state.Streams["dss_a"].Status)
	}
	if state.Streams["dss_b"].Status != dsc.StatusPending {
		t.Errorf("dss_b = %q, want pending", state.Streams["dss_b"].Status)
	}
	if state.Streams["dss_c"].Status != dsc.StatusPending {
		t.Errorf("dss_c = %q, want pending", state.Streams["dss_c"].Status)
	}
}

// TestReduceStatusOverlayPerKind runs a full happy-path lifecycle and checks
// that every status-changing kind is reflected correctly in the RunState.
func TestReduceStatusOverlayPerKind(t *testing.T) {
	dir, l := happyLifecycle(t)
	stream := "dss_a"

	state, err := Reduce(dir, l.Run())
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	sr, ok := state.Streams[stream]
	if !ok {
		t.Fatalf("stream %q missing from RunState", stream)
	}
	if sr.Status != dsc.StatusMerged {
		t.Errorf("stream status = %q, want merged", sr.Status)
	}
	if sr.PR != 7 {
		t.Errorf("PR = %d, want 7", sr.PR)
	}
	if sr.URL != "https://x/7" {
		t.Errorf("URL = %q, want https://x/7", sr.URL)
	}
	if sr.MergeCommit != "def" {
		t.Errorf("merge_commit = %q, want def", sr.MergeCommit)
	}
	if len(sr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(sr.Attempts))
	}
	if sr.Attempts[0].Seq != 1 || !sr.Attempts[0].Terminal {
		t.Errorf("attempt[0] = %+v, want {Seq:1 Terminal:true}", sr.Attempts[0])
	}
	if state.Run.Status != RunStatusFinished {
		t.Errorf("run status = %q, want finished", state.Run.Status)
	}
}

// TestReduceFoldsResumeLocators is the F3 resume acceptance: Reduce on a
// ledger that carries stream_dispatched{branch,worktree} and
// stream_attempt{commit} yields a RunState with all three locators, so
// state --json exposes them with no CLI change.
func TestReduceFoldsResumeLocators(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	stream := "dss_a"
	l, err := Claim(dir, run, "session:a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`),
		Streams: []dsc.StreamSpec{{Stream: stream, DocPath: "d.md"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, stream, "session:a", baseTime.Add(time.Second), dsc.StreamDispatchedBody{
		Branch:   "feat/resume-locators",
		Worktree: "/tmp/wt-resume",
	}))
	mustAppend(t, dir, l, ev("evt_3", dsc.KindStreamAttempt, stream, "session:a", baseTime.Add(2*time.Second), dsc.StreamAttemptBody{
		Seq: 1, DocPath: "d.md", Terminal: true, Commit: "deadbeef01",
	}))

	state, err := Reduce(dir, run)
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	sr, ok := state.Streams[stream]
	if !ok {
		t.Fatalf("stream %q missing", stream)
	}
	if sr.Branch != "feat/resume-locators" {
		t.Errorf("branch = %q, want feat/resume-locators", sr.Branch)
	}
	if sr.Worktree != "/tmp/wt-resume" {
		t.Errorf("worktree = %q, want /tmp/wt-resume", sr.Worktree)
	}
	if len(sr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(sr.Attempts))
	}
	if sr.Attempts[0].Commit != "deadbeef01" {
		t.Errorf("attempt commit = %q, want deadbeef01", sr.Attempts[0].Commit)
	}
	if sr.Status != dsc.StatusLanded {
		t.Errorf("status = %q, want landed", sr.Status)
	}
}

// TestReduceAbsentLocatorsTolerated covers old ledgers: a null/empty
// stream_dispatched body and a stream_attempt without commit still fold,
// leaving Branch/Worktree/Commit empty.
func TestReduceAbsentLocatorsTolerated(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	stream := "dss_a"
	l, _ := Claim(dir, run, "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`),
		Streams: []dsc.StreamSpec{{Stream: stream, DocPath: "d.md"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, stream, "session:a", baseTime.Add(time.Second), nil))
	mustAppend(t, dir, l, ev("evt_3", dsc.KindStreamAttempt, stream, "session:a", baseTime.Add(2*time.Second), dsc.StreamAttemptBody{
		Seq: 1, DocPath: "d.md", Terminal: true,
	}))

	state, err := Reduce(dir, run)
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	sr := state.Streams[stream]
	if sr.Branch != "" || sr.Worktree != "" {
		t.Fatalf("absent dispatch locators must stay empty, got branch=%q worktree=%q", sr.Branch, sr.Worktree)
	}
	if len(sr.Attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(sr.Attempts))
	}
	if sr.Attempts[0].Commit != "" {
		t.Fatalf("absent attempt commit must stay empty, got %q", sr.Attempts[0].Commit)
	}
	if sr.Status != dsc.StatusLanded {
		t.Errorf("status = %q, want landed", sr.Status)
	}
}

// TestReduceUnknownKindSkipped verifies that an unknown kind in the ledger is
// skipped in the fold (no error, stream status unchanged) but the chain is
// still verified across it.
func TestReduceUnknownKindSkipped(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	first := mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))

	// Append a well-formed unknown-kind event maintaining the hash chain.
	ledger := filepath.Join(dir, run, "events.jsonl")
	f, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	line := buildUnknownKindLine(t, "evt_uk", run, first.Hash)
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	f.Close()

	state, err := Reduce(dir, run)
	if err != nil {
		t.Fatalf("reduce over unknown kind: %v", err)
	}
	// Unknown kind must not change stream status.
	if state.Streams["dss_a"].Status != dsc.StatusPending {
		t.Errorf("stream status = %q after unknown kind, want pending", state.Streams["dss_a"].Status)
	}
	// Run must still list one stream (the manifest seed was retained).
	if len(state.Streams) != 1 {
		t.Errorf("want 1 stream, got %d", len(state.Streams))
	}
}

// TestReduceMidChainBreak verifies that a corrupt hash mid-ledger returns
// ErrChainBroken — never silent truncation (spec §8).
func TestReduceMidChainBreak(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))

	corruptFirstHash(t, filepath.Join(dir, run, "events.jsonl"))

	_, err := Reduce(dir, run)
	if !errors.Is(err, ErrChainBroken) {
		t.Fatalf("want ErrChainBroken for mid-chain break, got %v", err)
	}
}

// TestReduceTornFinalLineDiscard verifies that a partial line left by a crash
// (no trailing newline) is discarded silently; the prior complete events fold
// without error.
func TestReduceTornFinalLineDiscard(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))

	// Simulate a crash mid-append: partial line with no trailing newline.
	ledger := filepath.Join(dir, run, "events.jsonl")
	f, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"id":"evt_torn","v":"driver-state-v0.1.0","kind":"stream_dispa`)
	f.Close()

	state, err := Reduce(dir, run)
	if err != nil {
		t.Fatalf("reduce with torn tail: %v", err)
	}
	if len(state.Streams) != 1 {
		t.Fatalf("want 1 stream (from manifest), got %d", len(state.Streams))
	}
	if state.Streams["dss_a"].Status != dsc.StatusPending {
		t.Errorf("stream status = %q after torn-tail discard, want pending", state.Streams["dss_a"].Status)
	}
}

// ---- Runs tests ----------------------------------------------------------

// TestRunsToleratesCorruptRun covers spec §7 F5: a broken-chain run is listed
// as RunStatusCorrupt while all other runs in the directory list normally.
func TestRunsToleratesCorruptRun(t *testing.T) {
	dir := t.TempDir()

	// Good run: one healthy import.
	lGood, _ := Claim(dir, "dsr_good", "session:a")
	mustAppend(t, dir, lGood, ev("evt_g1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))

	// Bad run: corrupt chain.
	lBad, _ := Claim(dir, "dsr_bad", "session:b")
	mustAppend(t, dir, lBad, ev("evt_b1", dsc.KindRunImported, "", "session:b", baseTime, dsc.RunImportedBody{
		Repo: "r2", Source: "s2", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_b", DocPath: "d"}},
	}))
	corruptFirstHash(t, filepath.Join(dir, "dsr_bad", "events.jsonl"))

	summaries, err := Runs(dir)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("want 2 summaries (good + corrupt), got %d", len(summaries))
	}

	byRun := make(map[string]RunSummary, len(summaries))
	for _, s := range summaries {
		byRun[s.Run] = s
	}

	good, ok := byRun["dsr_good"]
	if !ok {
		t.Fatal("dsr_good missing from listing")
	}
	if good.Status != RunStatusOpen {
		t.Errorf("good run status = %q, want %q", good.Status, RunStatusOpen)
	}
	if good.Repo != "r" {
		t.Errorf("good run repo = %q, want r", good.Repo)
	}

	bad, ok := byRun["dsr_bad"]
	if !ok {
		t.Fatal("dsr_bad missing from listing")
	}
	if bad.Status != RunStatusCorrupt {
		t.Errorf("bad run status = %q, want %q", bad.Status, RunStatusCorrupt)
	}
}

// TestRunsSkipsUnstartedDir verifies that a directory without events.jsonl
// (a run that was claimed but never appended to) is excluded from the listing.
func TestRunsSkipsUnstartedDir(t *testing.T) {
	dir := t.TempDir()

	// Claim a run but never append — creates the dir, no ledger file.
	if _, err := Claim(dir, "dsr_empty", "session:a"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// A started run for contrast.
	lStarted, _ := Claim(dir, "dsr_started", "session:b")
	mustAppend(t, dir, lStarted, ev("evt_1", dsc.KindRunImported, "", "session:b", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))

	summaries, err := Runs(dir)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("want 1 summary (unstarted dir excluded), got %d", len(summaries))
	}
	if summaries[0].Run != "dsr_started" {
		t.Errorf("listed run = %q, want dsr_started", summaries[0].Run)
	}
}

// TestRunsEmptyDir verifies that an empty directory returns an empty list
// without error.
func TestRunsEmptyDir(t *testing.T) {
	summaries, err := Runs(t.TempDir())
	if err != nil {
		t.Fatalf("runs on empty dir: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("want 0 summaries, got %d", len(summaries))
	}
}

// TestRunsAbsentDir verifies that a non-existent directory returns nil, nil.
func TestRunsAbsentDir(t *testing.T) {
	summaries, err := Runs(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("runs on absent dir: %v", err)
	}
	if summaries != nil {
		t.Fatalf("want nil, got %v", summaries)
	}
}

// ---- Verify tests --------------------------------------------------------

// TestVerifyCleanChain verifies that a well-formed ledger passes verification.
func TestVerifyCleanChain(t *testing.T) {
	dir, l := happyLifecycle(t)
	if err := Verify(dir, l.Run()); err != nil {
		t.Fatalf("verify clean chain: %v", err)
	}
}

// TestVerifyBrokenChain verifies that a corrupted hash returns ErrChainBroken
// with detail that names the offending event.
func TestVerifyBrokenChain(t *testing.T) {
	dir := t.TempDir()
	run := "dsr_run1"
	l, _ := Claim(dir, run, "session:a")
	mustAppend(t, dir, l, ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
		Repo: "r", Source: "s", Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "d"}},
	}))
	mustAppend(t, dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_a", "session:a", baseTime.Add(time.Second), nil))

	corruptFirstHash(t, filepath.Join(dir, run, "events.jsonl"))

	err := Verify(dir, run)
	if !errors.Is(err, ErrChainBroken) {
		t.Fatalf("want ErrChainBroken, got %v", err)
	}
	// The error should be a wrapped ErrChainBroken with detail, not the bare
	// sentinel — a bare sentinel would name no event.
	if err != nil && err.Error() == ErrChainBroken.Error() {
		t.Errorf("want ErrChainBroken with detail, got bare sentinel: %v", err)
	}
}

// TestVerifyAbsentRun verifies that Verify on a non-existent run returns an
// error (not ErrChainBroken; the run simply does not exist).
func TestVerifyAbsentRun(t *testing.T) {
	err := Verify(t.TempDir(), "dsr_absent")
	if err == nil {
		t.Fatal("want error for absent run, got nil")
	}
	if errors.Is(err, ErrChainBroken) {
		t.Errorf("absent run should not return ErrChainBroken, got: %v", err)
	}
}

func TestReduceRejectsTraversalRunID(t *testing.T) {
	if _, err := Reduce(t.TempDir(), "../escape"); err == nil {
		t.Fatal("want run-id validation error, got nil")
	}
}

func TestVerifyRejectsTraversalRunID(t *testing.T) {
	if err := Verify(t.TempDir(), "../escape"); err == nil {
		t.Fatal("want run-id validation error, got nil")
	}
}
