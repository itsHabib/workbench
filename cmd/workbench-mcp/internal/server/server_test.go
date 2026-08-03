package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/driverstate"
)

func TestInitializeHandshake(t *testing.T) {
	s := New(t.TempDir())
	req := rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize"}
	resp := s.dispatch(req)
	if resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
}

func TestToolsListExposesExactlyTheDriverVerbs(t *testing.T) {
	s := New(t.TempDir())
	resp := s.dispatch(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list"})
	res := resp.Result.(map[string]any)
	tools := res["tools"].([]map[string]any)
	got := make(map[string]bool)
	for _, tl := range tools {
		got[tl["name"].(string)] = true
	}
	want := []string{"driver_record", "driver_transition", "driver_state", "driver_runs", "driver_verify", "driver_rollup"}
	if len(got) != len(want) {
		t.Fatalf("want %d verbs, got %d: %v", len(want), len(got), got)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing verb %q", w)
		}
	}
	// No capability-mutating verb is registered (excluded by construction).
	if got["gate_grant"] || got["driver_renew"] {
		t.Fatalf("a forbidden verb is exposed: %v", got)
	}
}

func TestUnknownVerbIsMethodNotFound(t *testing.T) {
	s := New(t.TempDir())
	params, _ := json.Marshal(toolCallParams{Name: "driver_frobnicate"})
	req := rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params}
	resp := s.dispatch(req)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("want MethodNotFound, got %+v (result %+v)", resp.Error, resp.Result)
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	s := New(t.TempDir())
	resp := s.dispatch(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "nope"})
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("want MethodNotFound, got %+v", resp.Error)
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	s := New(t.TempDir())
	line, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
	_, respond := s.handleMessage(line)
	if respond {
		t.Fatal("a notification must not get a response")
	}
}

func TestRenewIntervalIsHalfTTL(t *testing.T) {
	withTTL(t, 90*time.Second)
	s := New(t.TempDir())
	if got := s.renewInterval(); got != 45*time.Second {
		t.Fatalf("renewInterval = %v, want 45s (TTL/2)", got)
	}
}

func TestRenewLoopRenewsEachTickThenStopsOnExit(t *testing.T) {
	withTTL(t, 2*time.Second)
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.leaseFor("dsr_r", "session:x"); err != nil {
		t.Fatalf("leaseFor: %v", err)
	}
	before := readExpiry(t, dir, "dsr_r") // single read, no renew in flight yet

	ticks := make(chan time.Time)
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		s.renewLoop(done, ticks)
		close(exited)
	}()

	// Wait on the renew actually landing, not on the clock: a sweep can exhaust
	// its bounded lock-retry budget under a loaded -race runner, and production's
	// answer to that is the next tick (renewAll keeps the lease and retries). The
	// test drives the same recovery instead of asserting one sweep won a race.
	waitForRenew(t, ticks, dir, "dsr_r", before)

	// Session exit stops renewal: after done closes, the loop returns and no
	// longer receives ticks (spec §6 — server exit stops renewal). Assert the
	// return first — racing a send against the close would just be reading
	// select's coin flip, and the wait also keeps a late sweep from writing into
	// an already-cleaned TempDir.
	close(done)
	select {
	case <-exited:
	case <-time.After(renewWaitTimeout):
		t.Fatal("renewLoop did not return after exit")
	}
	select {
	case ticks <- time.Now():
		t.Fatal("renewLoop still receiving ticks after exit")
	default:
	}
}

// A definitive ownership loss (the run stolen out from under the session) drops
// the lease from the held set so it will be re-Claimed on the next record.
func TestRenewAllEvictsOnOwnershipLoss(t *testing.T) {
	withTTL(t, 5*time.Second)
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.leaseFor("dsr_r", "session:x"); err != nil {
		t.Fatalf("leaseFor: %v", err)
	}
	// Force the session's lease expired on disk, then let another writer steal it
	// — deterministic, no wall-clock race against the TTL.
	if err := driverstate.ExpireLeaseForTest(dir, "dsr_r"); err != nil {
		t.Fatalf("force expire: %v", err)
	}
	other, err := driverstate.Claim(dir, "dsr_r", "session:other")
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	defer other.Release()

	s.renewAll()
	if _, ok := s.leases["dsr_r"]; ok {
		t.Fatal("a definitively lost lease should be evicted from the held set")
	}
}

// A TRANSIENT renew failure (here: the lease lock is held, so Renew can't
// acquire it) must NOT evict — the lease is still live on disk, so dropping it
// would make the next Claim return ErrLocked against this very session. It is
// kept and retried next tick.
func TestRenewAllKeepsLeaseOnTransientError(t *testing.T) {
	withTTL(t, 2*time.Second) // long TTL so the fresh lock never stale-breaks
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.leaseFor("dsr_r", "session:x"); err != nil {
		t.Fatalf("leaseFor: %v", err)
	}
	// Hold the run's lease lock with a fresh mtime: Renew's acquire contends and
	// exhausts its budget (a transient error), not an ownership loss.
	lock := filepath.Join(dir, "dsr_r", "lease.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lock)

	s.renewAll()
	if _, ok := s.leases["dsr_r"]; !ok {
		t.Fatal("a transient renew failure must keep the lease for a retry")
	}
}

// A run_imported retry (omitted run, same import key) resolves to the original
// run via Append's dedupe; the speculatively minted run must be cleaned up, so
// exactly one run dir and one held lease remain — no orphan.
func TestImportRetryDedupesNoOrphan(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	imp := importEventKeyed("dss_a", "session:x", "2026-07-16T00:00:00Z")

	first := callRecord(t, s, "", imp)
	if first.IsError {
		t.Fatalf("first import errored: %s", resultText(t, first))
	}
	var e1 driverstate.Event
	if err := json.Unmarshal([]byte(resultText(t, first)), &e1); err != nil {
		t.Fatalf("decode first: %v", err)
	}

	second := callRecord(t, s, "", imp) // the lost-response retry
	if second.IsError {
		t.Fatalf("retry import errored: %s", resultText(t, second))
	}
	var e2 driverstate.Event
	if err := json.Unmarshal([]byte(resultText(t, second)), &e2); err != nil {
		t.Fatalf("decode second: %v", err)
	}

	if e2.Run != e1.Run || e2.Hash != e1.Hash {
		t.Fatalf("retry should return the original run/event: e1=%s/%s e2=%s/%s", e1.Run, e1.Hash, e2.Run, e2.Hash)
	}
	if n := countRunDirs(t, dir); n != 1 {
		t.Fatalf("want exactly one run dir (orphan cleaned), got %d", n)
	}
	if len(s.leases) != 1 {
		t.Fatalf("want exactly one held lease (minted orphan discarded), got %d", len(s.leases))
	}
}

// A cached lease that loses ownership mid-session (here: expires after a
// suspend) must be evicted the moment a driver_record's Append reports the loss,
// so the next record re-Claims immediately instead of failing on the dead lease
// until the renew tick.
func TestRecordEvictsCachedLeaseOnAppendOwnershipLoss(t *testing.T) {
	withTTL(t, 5*time.Second) // comfortable — expiry is forced on disk, not slept
	dir := t.TempDir()
	s := New(dir)
	run := mustRun(t, callRecord(t, s, "", importEvent("dss_a", "session:x")))
	if _, ok := s.leases[run]; !ok {
		t.Fatal("import should have cached the run lease")
	}

	// The cached lease loses ownership mid-session (a suspend): force it expired
	// on disk rather than sleeping past a short TTL under a loaded/-race runner.
	if err := driverstate.ExpireLeaseForTest(dir, run); err != nil {
		t.Fatalf("force expire: %v", err)
	}

	bad := callRecord(t, s, run, event(dsc.KindStreamDispatched, "dss_a", "session:x", struct{}{}))
	if !bad.IsError {
		t.Fatalf("expected an ownership-loss error, got %s", resultText(t, bad))
	}
	if _, ok := s.leases[run]; ok {
		t.Fatal("the dead lease should be evicted after append-time ownership loss")
	}
	// The next record re-Claims the (now expired) run and succeeds.
	ok := callRecord(t, s, run, event(dsc.KindStreamDispatched, "dss_a", "session:x", struct{}{}))
	if ok.IsError {
		t.Fatalf("retry after eviction should re-claim and succeed, got %s", resultText(t, ok))
	}
}

func countRunDirs(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// --- test helpers ---

// withTTL tunes the lease TTL for a test so renew cadence and expiry paths do
// not need the production 90s window.
func withTTL(t *testing.T, ttl time.Duration) {
	t.Helper()
	prev := driverstate.DefaultLeaseTTL
	driverstate.DefaultLeaseTTL = ttl
	t.Cleanup(func() { driverstate.DefaultLeaseTTL = prev })
}

func readExpiry(t *testing.T, dir, run string) time.Time {
	t.Helper()
	exp, err := leaseExpiry(dir, run)
	if err != nil {
		t.Fatalf("read lease: %v", err)
	}
	return exp
}

func leaseExpiry(dir, run string) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(dir, run, "lease.json"))
	if err != nil {
		return time.Time{}, fmt.Errorf("read lease: %w", err)
	}
	var rec struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return time.Time{}, fmt.Errorf("decode lease: %w", err)
	}
	return rec.ExpiresAt, nil
}

// renewWaitTimeout bounds waitForRenew. Generous on purpose: it is a failure
// deadline, not a cadence — a healthy run satisfies it on the first tick.
const renewWaitTimeout = 10 * time.Second

// waitForRenew ticks renewLoop until run's on-disk lease expiry moves past prev,
// and fails the test if it never does. Each send blocks until the loop receives
// it, so a return means the loop committed to a renewAll — but not that the
// sweep succeeded, so a stalled expiry just earns another tick. A read that
// loses a race with the lease file's atomic rename counts as "not yet" too.
func waitForRenew(t *testing.T, ticks chan<- time.Time, dir, run string, prev time.Time) {
	t.Helper()
	deadline := time.Now().Add(renewWaitTimeout)
	for time.Now().Before(deadline) {
		ticks <- time.Now()
		exp, err := leaseExpiry(dir, run)
		if err == nil && exp.After(prev) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("lease expiry did not advance within %s of ticks: before=%s", renewWaitTimeout, prev)
}

// corruptLedger appends a well-formed but chain-breaking event line to a run's
// ledger — a complete line (trailing newline, so it is not a torn tail) whose
// prev does not link to the head. Reads must flag ErrChainBroken.
func corruptLedger(t *testing.T, dir, run string) {
	t.Helper()
	path := filepath.Join(dir, run, "events.jsonl")
	bad := map[string]any{
		"id":    "evt_bad",
		"run":   run,
		"v":     dsc.Version,
		"kind":  string(dsc.KindRunFinished),
		"time":  "2026-07-16T09:00:00Z",
		"actor": "session:x",
		"body":  nil,
		"prev":  "not-the-head-hash",
		"hash":  "0000",
	}
	line, _ := json.Marshal(bad)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatalf("write corrupt line: %v", err)
	}
}
