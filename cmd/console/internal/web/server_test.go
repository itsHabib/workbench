package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/console/internal/gatecli"
)

const testHost = "127.0.0.1:7788"

// clientReturning builds a gate client whose runner dispatches on the gate
// subcommand (args[0], since these tests use no -state) to a canned response.
func clientReturning(responses map[string]string) *gatecli.Client {
	return gatecli.New("gate", "", func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("no args")
		}
		body, ok := responses[args[0]]
		if !ok {
			return nil, fmt.Errorf("gate %s: boom", strings.Join(args, " "))
		}
		return []byte(body), nil
	})
}

func do(t *testing.T, s *Server, method, target, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestNextProxiesGateJSON(t *testing.T) {
	s := New(clientReturning(map[string]string{"next": `{"parked":[],"grants":[]}`}), testHost)
	rec := do(t, s, "GET", "/api/next", testHost)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	// nosniff must ride every response, /api/* included — gate's stdout carries
	// untrusted strings, and a sniffed JSON body would run under no CSP.
	if ns := rec.Header().Get("X-Content-Type-Options"); ns != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", ns)
	}
	var in struct {
		Parked []any `json:"parked"`
		Grants []any `json:"grants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &in); err != nil {
		t.Fatalf("not JSON: %v — %s", err, rec.Body)
	}
}

func TestRunProxiesExplain(t *testing.T) {
	s := New(clientReturning(map[string]string{"explain": `{"run":"run_9f3a41c2","artifacts":[]}`}), testHost)
	rec := do(t, s, "GET", "/api/run/run_9f3a41c2", testHost)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "run_9f3a41c2") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestRunBadIDIsGatewayError(t *testing.T) {
	s := New(clientReturning(map[string]string{"explain": `{}`}), testHost)
	rec := do(t, s, "GET", "/api/run/notarun", testHost)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("bad run id should be a 502, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("error response should carry an error field: %s", rec.Body)
	}
}

func TestAuditEndpoint(t *testing.T) {
	s := New(clientReturning(map[string]string{"audit": "chain intact\n"}), testHost)
	rec := do(t, s, "GET", "/api/audit", testHost)
	var st gatecli.AuditStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("not JSON: %v — %s", err, rec.Body)
	}
	if !st.OK {
		t.Fatalf("clean audit should report ok, got %+v", st)
	}
}

func TestHostPinRejectsForeignHost(t *testing.T) {
	s := New(clientReturning(map[string]string{"next": "{}"}), testHost)
	rec := do(t, s, "GET", "/api/next", "evil.example.com:7788")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a foreign Host must be refused (DNS-rebinding guard), got %d", rec.Code)
	}
}

func TestHostPinAcceptsLoopbackAliases(t *testing.T) {
	s := New(clientReturning(map[string]string{"next": `{"parked":[],"grants":[]}`}), testHost)
	for _, host := range []string{testHost, "localhost:7788", "[::1]:7788"} {
		if rec := do(t, s, "GET", "/api/next", host); rec.Code != 200 {
			t.Fatalf("host %q should be allowed on the same port, got %d", host, rec.Code)
		}
	}
	// Same loopback host but the WRONG port is refused.
	if rec := do(t, s, "GET", "/api/next", "127.0.0.1:9999"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong port must be refused, got %d", rec.Code)
	}
}

func TestAppServesHTMLWithCSP(t *testing.T) {
	s := New(clientReturning(nil), testHost)
	for _, path := range []string{"/", "/run/run_9f3a41c2"} {
		rec := do(t, s, "GET", path, testHost)
		if rec.Code != 200 {
			t.Fatalf("%s status %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s content-type %q", path, ct)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Fatalf("%s missing CSP: %q", path, csp)
		}
		if !strings.Contains(rec.Body.String(), "<title>gate</title>") {
			t.Fatalf("%s did not serve the app page", path)
		}
	}
}

func TestGateFailureIsGatewayError(t *testing.T) {
	// A gate that errors (no canned response) surfaces as a 502 with the message,
	// never a 200 with a broken body.
	s := New(clientReturning(map[string]string{}), testHost)
	rec := do(t, s, "GET", "/api/next", testHost)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("gate failure should be a 502, got %d", rec.Code)
	}
}

func TestRequireLoopback(t *testing.T) {
	ok := []string{"127.0.0.1:7788", "127.0.0.1:0", "localhost:1234", "[::1]:7788"}
	for _, a := range ok {
		if err := requireLoopback(a); err != nil {
			t.Errorf("requireLoopback(%q) = %v, want nil", a, err)
		}
	}
	bad := []string{"0.0.0.0:80", "192.168.1.5:80", "example.com:80", "10.0.0.1:1"}
	for _, a := range bad {
		if err := requireLoopback(a); err == nil {
			t.Errorf("requireLoopback(%q) = nil, want refusal", a)
		}
	}
}
