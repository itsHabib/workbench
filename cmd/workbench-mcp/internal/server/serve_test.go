package server

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestServeRoundTripFramedAndReleasesOnEOF drives the full stdio loop: an
// initialize, a notification (no response), a driver_record over the wire, then
// EOF. It asserts newline-framed JSON-RPC responses come back in order, the
// notification is silent, and the session lease is released on EOF.
func TestServeRoundTripFramedAndReleasesOnEOF(t *testing.T) {
	withTTL(t, 2*time.Second)
	dir := t.TempDir()
	s := New(dir)

	recordArgs, _ := json.Marshal(toolCallParams{Name: "driver_record", Arguments: importEvent("dss_a", "session:x")})
	in := strings.Join([]string{
		mustLine(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize"}),
		mustLine(rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}),
		mustLine(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("2"), Method: "tools/call", Params: recordArgs}),
	}, "\n") + "\n"

	var out strings.Builder
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// Two responses (initialize id=1, tools/call id=2) — the notification is silent.
	lines := nonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("want 2 framed responses, got %d: %q", len(lines), out.String())
	}
	var initResp, callResp rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if string(initResp.ID) != "1" {
		t.Fatalf("first response id = %s, want 1", initResp.ID)
	}
	if err := json.Unmarshal([]byte(lines[1]), &callResp); err != nil {
		t.Fatalf("decode call response: %v", err)
	}
	if string(callResp.ID) != "2" {
		t.Fatalf("second response id = %s, want 2", callResp.ID)
	}

	// EOF ended the session: no lease is still held, so a fresh writer can claim.
	s.mu.Lock()
	held := len(s.leases)
	s.mu.Unlock()
	if held != 0 {
		t.Fatalf("session leases not released on EOF: %d held", held)
	}
}

func mustLine(req rpcRequest) string {
	b, _ := json.Marshal(req)
	return string(b)
}

func nonEmptyLines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			out = append(out, sc.Text())
		}
	}
	return out
}
