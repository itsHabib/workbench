package server

import (
	"encoding/json"
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

func TestToolsListExposesExactlyTheFourVerbs(t *testing.T) {
	s := New(t.TempDir())
	resp := s.dispatch(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list"})
	res := resp.Result.(map[string]any)
	tools := res["tools"].([]map[string]any)
	got := make(map[string]bool)
	for _, tl := range tools {
		got[tl["name"].(string)] = true
	}
	want := []string{"driver_record", "driver_state", "driver_runs", "driver_verify"}
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
	go s.renewLoop(done, ticks)

	// The send blocks until the loop receives it, so on return the loop is
	// committed to a renewAll. Sleep briefly to let that finish, then read once —
	// no concurrent reader collides with the lease-file rename.
	ticks <- time.Now()
	time.Sleep(50 * time.Millisecond)
	if after := readExpiry(t, dir, "dsr_r"); !after.After(before) {
		t.Fatalf("lease expiry did not advance after a tick: before=%s after=%s", before, after)
	}

	// Session exit stops renewal: after done closes, the loop returns and no
	// longer receives ticks (spec §6 — server exit stops renewal).
	close(done)
	select {
	case ticks <- time.Now():
		t.Fatal("renewLoop still receiving ticks after exit")
	case <-time.After(50 * time.Millisecond):
	}
}

// A definitive ownership loss (the run stolen out from under the session) drops
// the lease from the held set so it will be re-Claimed on the next record.
func TestRenewAllEvictsOnOwnershipLoss(t *testing.T) {
	withTTL(t, 20*time.Millisecond)
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.leaseFor("dsr_r", "session:x"); err != nil {
		t.Fatalf("leaseFor: %v", err)
	}
	time.Sleep(40 * time.Millisecond) // the session's lease expires
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
	data, err := os.ReadFile(filepath.Join(dir, run, "lease.json"))
	if err != nil {
		t.Fatalf("read lease: %v", err)
	}
	var rec struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode lease: %v", err)
	}
	return rec.ExpiresAt
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
