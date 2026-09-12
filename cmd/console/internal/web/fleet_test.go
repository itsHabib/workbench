package web

import (
	"context"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/console/internal/fleetcli"
)

func TestFleetRoutesReadOnlyAndEscapingContract(t *testing.T) {
	called := 0
	c := fleetcli.New("fleet", "lens", "", func(_ context.Context, _ string, _ string, _ []byte, _ ...string) ([]byte, error) {
		called++
		return []byte(`{"workers":[]}`), nil
	})
	s := New(clientReturning(nil), testHost, c)
	if rec := do(t, s, "GET", "/api/fleet/status", testHost); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := do(t, s, "POST", "/api/fleet/status", testHost); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	if rec := do(t, s, "GET", "/api/fleet/status", "evil.example"); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if called != 1 {
		t.Fatal(called)
	}
	rec := do(t, s, "GET", "/fleet", testHost)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal(rec)
	}
	if !strings.Contains(rec.Body.String(), "esc(m.body)") || !strings.Contains(rec.Body.String(), "prose(h.conclusion)") {
		t.Fatal("untrusted prose must be escaped")
	}
}
