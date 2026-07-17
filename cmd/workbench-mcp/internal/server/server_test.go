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
