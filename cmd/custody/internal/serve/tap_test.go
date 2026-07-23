package serve

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubProber is a RulesetProber for tests.
type stubProber struct {
	ok  bool
	err error
}

func (s stubProber) InForce(_ string) (bool, error) { return s.ok, s.err }

// --- ValidateTapAddr ---

func TestValidateTapAddrWildcardRefused(t *testing.T) {
	cases := []struct {
		name string
		addr string
	}{
		{"ipv4 wildcard", "0.0.0.0:8127"},
		{"ipv6 wildcard", "[::]:8127"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTapAddr(tc.addr, "tap")
			if err == nil {
				t.Fatalf("ValidateTapAddr(%q) = nil, want error", tc.addr)
			}
			if !strings.Contains(err.Error(), "refused_wildcard_bind") {
				t.Fatalf("want refused_wildcard_bind in error, got: %v", err)
			}
		})
	}
}

func TestValidateTapAddrNonTapRefused(t *testing.T) {
	// 127.0.0.1 is on the loopback interface, not a tap interface.
	err := ValidateTapAddr("127.0.0.1:8127", "tap")
	if err == nil {
		t.Fatal("ValidateTapAddr with loopback + tap prefix = nil, want error")
	}
	if !strings.Contains(err.Error(), "refused_non_tap_bind") {
		t.Fatalf("want refused_non_tap_bind in error, got: %v", err)
	}
}

func TestValidateTapAddrOverridePrefixAccepted(t *testing.T) {
	// Find any interface that has an IP address and use its name-prefix as the
	// override. This lets the test exercise the happy path on all platforms
	// without requiring a real tap interface.
	prefix, addr := findInterfaceForTest(t)
	if err := ValidateTapAddr(addr+":8127", prefix); err != nil {
		t.Fatalf("ValidateTapAddr(%q, prefix=%q): %v", addr, prefix, err)
	}
}

func TestValidateTapAddrBadAddr(t *testing.T) {
	// Missing port makes SplitHostPort fail.
	if err := ValidateTapAddr("notanaddr", "tap"); err == nil {
		t.Fatal("want error for malformed addr, got nil")
	}
}

func TestValidateTapAddrInvalidIP(t *testing.T) {
	err := ValidateTapAddr("notanip:8127", "tap")
	if err == nil {
		t.Fatal("want error for non-IP host, got nil")
	}
	if !strings.Contains(err.Error(), "refused_bad_tap_addr") {
		t.Fatalf("want refused_bad_tap_addr in error, got: %v", err)
	}
}

// findInterfaceForTest returns a (prefix, ip) pair from the first interface
// that has a globally-routable or loopback address. The prefix is the first
// character of the interface name so ValidateTapAddr can match it. The test is
// skipped if no suitable interface is found.
func findInterfaceForTest(t *testing.T) (prefix, addr string) {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skip("cannot enumerate interfaces:", err)
	}
	for _, iface := range ifaces {
		if len(iface.Name) == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		for _, a := range addrs {
			ip := tapIfaceIP(a)
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 != nil {
				// Use first character of the interface name as prefix.
				return iface.Name[:1], ip4.String()
			}
		}
	}
	t.Skip("no suitable interface found for prefix-override test")
	return "", ""
}

// --- PreflightFirewall ---

func TestPreflightFirewallPasses(t *testing.T) {
	if err := PreflightFirewall("10.0.0.1:8127", stubProber{ok: true}); err != nil {
		t.Fatalf("PreflightFirewall with ok prober: %v", err)
	}
}

func TestPreflightFirewallRuleAbsent(t *testing.T) {
	err := PreflightFirewall("10.0.0.1:8127", stubProber{ok: false})
	if err == nil {
		t.Fatal("want error when rule is absent, got nil")
	}
	if !strings.Contains(err.Error(), "refused_preflight_no_rule") {
		t.Fatalf("want refused_preflight_no_rule in error, got: %v", err)
	}
}

func TestPreflightFirewallProberError(t *testing.T) {
	probeErr := errors.New("exec: nft not found")
	err := PreflightFirewall("10.0.0.1:8127", stubProber{ok: false, err: probeErr})
	if err == nil {
		t.Fatal("want error when prober fails, got nil")
	}
	if !strings.Contains(err.Error(), "refused_preflight_error") {
		t.Fatalf("want refused_preflight_error in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), probeErr.Error()) {
		t.Fatalf("want probe error in message, got: %v", err)
	}
}

// --- tapHandler source enforcement ---

// tapHarness extends the test harness with a bound derived grant.
type tapHarness struct {
	*harness
	tapHandler  http.Handler
	boundTok    string
	boundSource string
}

func newTapHarness(t *testing.T, upstream *httptest.Server, actions []string) *tapHarness {
	t.Helper()
	h := newHarness(t, upstream, actions)
	now := func() time.Time { return h.now }

	boundSource := "10.0.0.42"
	// Derive a child grant bound to boundSource.
	_, childTok, err := h.engine.grants.Derive(h.token, actions, time.Hour, boundSource, "test-tap", now)
	if err != nil {
		t.Fatalf("Derive bound grant: %v", err)
	}
	handler := NewTapHandler(h.engine, h.engine.grants)
	return &tapHarness{
		harness:     h,
		tapHandler:  handler,
		boundTok:    childTok,
		boundSource: boundSource,
	}
}

// doTap sends a request to the tapHandler with the given RemoteAddr.
func doTap(handler http.Handler, method, target, remoteAddr string, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1"+target, nil)
	r.RequestURI = target
	r.RemoteAddr = remoteAddr
	for k, vs := range header {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// TestTapUnboundGrantRefused checks that an unbound (root) grant is refused on
// the tap listener even when it would be valid on the localhost listener.
func TestTapUnboundGrantRefused(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	th := newTapHarness(t, up, []string{"read"})

	// Use the root (unbound) token — valid on localhost, refused on tap.
	w := doTap(th.tapHandler, "GET", "/tracker/rest/api/2/issue/PROJ-1",
		th.boundSource+":54321",
		http.Header{grantHeader: {th.token}})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unbound grant on tap: status = %d, want 401", w.Code)
	}
	body := decodeTapErr(t, w)
	if body.Code != "refused_unbound_on_tap" {
		t.Fatalf("code = %q, want refused_unbound_on_tap", body.Code)
	}
	if body.RequestID == "" {
		t.Fatal("error body missing request_id")
	}
	if cp.calls != 0 {
		t.Fatal("unbound grant must never reach upstream")
	}
}

// TestTapSourceMismatchRefused checks that a bound grant presented from the
// wrong source is refused with refused_source_mismatch.
func TestTapSourceMismatchRefused(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	th := newTapHarness(t, up, []string{"read"})

	wrongIP := "10.0.0.99"
	w := doTap(th.tapHandler, "GET", "/tracker/rest/api/2/issue/PROJ-1",
		wrongIP+":12345",
		http.Header{grantHeader: {th.boundTok}})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("source mismatch: status = %d, want 401", w.Code)
	}
	body := decodeTapErr(t, w)
	if body.Code != "refused_source_mismatch" {
		t.Fatalf("code = %q, want refused_source_mismatch", body.Code)
	}
	if body.RequestID == "" {
		t.Fatal("error body missing request_id")
	}
	if cp.calls != 0 {
		t.Fatal("source-mismatch request must never reach upstream")
	}
}

// TestTapMatchingSourceAllowed checks that a bound grant from the correct
// source is passed through to the engine and reaches upstream.
func TestTapMatchingSourceAllowed(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	th := newTapHarness(t, up, []string{"read"})

	w := doTap(th.tapHandler, "GET", "/tracker/rest/api/2/issue/PROJ-1",
		th.boundSource+":54321",
		http.Header{grantHeader: {th.boundTok}})

	if w.Code != http.StatusOK {
		t.Fatalf("matching source: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if cp.calls != 1 {
		t.Fatalf("matching source: %d upstream calls, want 1", cp.calls)
	}
}

// TestTapNoGrantPassesToEngine checks that a request with no grant is passed to
// the engine (which refuses with refused_no_grant), not silently swallowed.
func TestTapNoGrantPassesToEngine(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	th := newTapHarness(t, up, []string{"read"})

	w := doTap(th.tapHandler, "GET", "/tracker/rest/api/2/issue/PROJ-1",
		th.boundSource+":54321",
		http.Header{})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no grant: status = %d, want 401", w.Code)
	}
	body := decodeTapErr(t, w)
	if body.Code != "refused_no_grant" {
		t.Fatalf("code = %q, want refused_no_grant", body.Code)
	}
}

// TestTapRefusalIsLogged verifies that tap-layer refusals (unbound, mismatch)
// appear in the shared artifact log with verdict "refused".
func TestTapRefusalIsLogged(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	th := newTapHarness(t, up, []string{"read"})

	// Unbound refusal.
	doTap(th.tapHandler, "GET", "/tracker/rest/api/2/issue/PROJ-1",
		th.boundSource+":1",
		http.Header{grantHeader: {th.token}}) // root = unbound

	rec := lastLog(t, th.log)
	if rec.Verdict != verdictRefused {
		t.Fatalf("unbound refusal verdict = %q, want refused", rec.Verdict)
	}
	if rec.RequestID == "" {
		t.Fatal("log record missing request_id")
	}

	// Source mismatch refusal.
	doTap(th.tapHandler, "GET", "/tracker/rest/api/2/issue/PROJ-1",
		"192.168.1.1:2",
		http.Header{grantHeader: {th.boundTok}})

	rec = lastLog(t, th.log)
	if rec.Verdict != verdictRefused {
		t.Fatalf("mismatch refusal verdict = %q, want refused", rec.Verdict)
	}
}

// TestTapListenerParity verifies that the same request gets the same verdict
// whether it goes to the engine directly (localhost listener) or to the tap
// handler (after passing the source gate). Source rules aside, semantics must
// be identical — the engine is shared, not duplicated.
func TestTapListenerParity(t *testing.T) {
	// Test parity across two cases: pass and denial.
	cases := []struct {
		name   string
		method string
		target string
	}{
		{"pass", "GET", "/tracker/rest/api/2/issue/PROJ-123"},
		{"denied_no_action", "POST", "/tracker/rest/api/2/issue/PROJ-1/comment"},
		{"unknown_key", "GET", "/nokey/path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp := &capture{}
			up := upstreamOK(t, cp)
			th := newTapHarness(t, up, []string{"read"})

			// Direct engine request (localhost listener path).
			engineW := do(th.engine, tc.method, tc.target,
				http.Header{grantHeader: {th.boundTok}}, nil)

			// Reset cp for tap request.
			cp.mu.Lock()
			cp.calls = 0
			cp.mu.Unlock()

			// Tap handler request with matching source.
			tapW := doTap(th.tapHandler, tc.method, tc.target,
				th.boundSource+":9999",
				http.Header{grantHeader: {th.boundTok}})

			if engineW.Code != tapW.Code {
				t.Fatalf("parity: engine=%d tap=%d for %s %s",
					engineW.Code, tapW.Code, tc.method, tc.target)
			}
			// For non-2xx responses, the code bodies should match.
			if engineW.Code != http.StatusOK {
				var eBody, tBody errorBody
				if err := json.Unmarshal(engineW.Body.Bytes(), &eBody); err != nil {
					t.Fatalf("decode engine error body: %v", err)
				}
				if err := json.Unmarshal(tapW.Body.Bytes(), &tBody); err != nil {
					t.Fatalf("decode tap error body: %v", err)
				}
				if eBody.Code != tBody.Code {
					t.Fatalf("parity: engine code=%q tap code=%q", eBody.Code, tBody.Code)
				}
			}
		})
	}
}

// TestTapIPv4MappedIPv6Match verifies that an IPv4-mapped IPv6 client address
// (::ffff:a.b.c.d) is correctly matched against an IPv4 BoundSource.
func TestTapIPv4MappedIPv6Match(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	th := newTapHarness(t, up, []string{"read"})

	// th.boundSource is "10.0.0.42" (IPv4); connect as the mapped IPv6 form.
	mappedAddr := "[::ffff:10.0.0.42]:54321"
	w := doTap(th.tapHandler, "GET", "/tracker/rest/api/2/issue/PROJ-1",
		mappedAddr,
		http.Header{grantHeader: {th.boundTok}})

	if w.Code != http.StatusOK {
		t.Fatalf("IPv4-mapped IPv6: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func decodeTapErr(t *testing.T, w *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, w.Body.String())
	}
	return body
}

// --- ruleset matching (the preflight's source-restriction policy) ---
//
// These guard the finding that a bare port match let a drop-only or
// source-unrestricted rule pass the preflight: the matcher must require an
// accept rule carrying a source restriction on the same line.

func TestNftAllowsPort(t *testing.T) {
	cases := []struct {
		name    string
		ruleset string
		want    bool
	}{
		{"source-restricted accept", "ip saddr 10.0.100.0/24 tcp dport 8127 accept", true},
		{"ipv6 source-restricted accept", "ip6 saddr fd00:100::/64 tcp dport 8127 accept", true},
		{"drop only", "tcp dport 8127 log prefix \"x\" drop", false},
		{"accept without saddr", "tcp dport 8127 accept", false},
		{"saddr+accept on different rules", "ip saddr 10.0.100.0/24 accept\ntcp dport 8127 drop", false},
		{"wrong port", "ip saddr 10.0.100.0/24 tcp dport 9999 accept", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nftAllowsPort(tc.ruleset, "8127"); got != tc.want {
				t.Fatalf("nftAllowsPort(%q) = %v, want %v", tc.ruleset, got, tc.want)
			}
		})
	}
}

func TestIptablesAllowsPort(t *testing.T) {
	cases := []struct {
		name string
		save string
		want bool
	}{
		{"source-restricted accept", "-A INPUT -s 10.0.100.0/24 -p tcp --dport 8127 -j ACCEPT", true},
		{"drop only", "-A INPUT -p tcp --dport 8127 -j DROP", false},
		{"accept without source", "-A INPUT -p tcp --dport 8127 -j ACCEPT", false},
		{"source+accept on different rules", "-A INPUT -s 10.0.100.0/24 -j ACCEPT\n-A INPUT -p tcp --dport 8127 -j DROP", false},
		{"wrong port", "-A INPUT -s 10.0.100.0/24 -p tcp --dport 9999 -j ACCEPT", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := iptablesAllowsPort(tc.save, "8127"); got != tc.want {
				t.Fatalf("iptablesAllowsPort(%q) = %v, want %v", tc.save, got, tc.want)
			}
		})
	}
}
