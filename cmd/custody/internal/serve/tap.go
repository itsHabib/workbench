// Package serve — tap listener: the second listener that makes a room's guest
// reachable from the tap gateway. It wraps the proxy engine with two additional
// enforcement layers:
//
//  1. Bind validation at startup: wildcard binds are refused; the address must
//     be on a tap-prefixed interface (name-prefix check, default "tap",
//     overridable via -tap-if-prefix).
//
//  2. Per-request source enforcement: on the tap listener the transport source
//     must match the grant's BoundSource field; unbound grants (empty field)
//     are refused outright. The engine itself is unmodified — this handler is
//     a gate before it, not a change inside it.
//
// A startup preflight guard also checks that the source-restriction firewall
// rule is in force before the listener opens. The check is behind a
// RulesetProber seam so tests can stub it without a real firewall.
package serve

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/grant"
)

// RulesetProber checks whether the source-restriction firewall rule required
// by the tap listener is in force. The real implementation (see tap_linux.go)
// probes nftables then falls back to iptables-save; tests use stubs.
type RulesetProber interface {
	InForce(tapAddr string) (bool, error)
}

// ValidateTapAddr checks that addr is safe to bind as a tap listener. It
// refuses wildcard addresses (0.0.0.0 and ::) and any address whose host IP is
// not assigned to an interface whose name starts with ifPrefix. An empty
// ifPrefix defaults to "tap". Fails closed on any error.
func ValidateTapAddr(addr, ifPrefix string) error {
	if ifPrefix == "" {
		ifPrefix = "tap"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("tap: bad -tap-addr %q: %w", addr, err)
	}
	if host == "0.0.0.0" || host == "::" {
		return fmt.Errorf("tap: wildcard bind %q refused (code: refused_wildcard_bind): specify a concrete tap interface address", host)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("tap: -tap-addr host %q is not a valid IP address (code: refused_bad_tap_addr)", host)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("tap: enumerate interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if !strings.HasPrefix(iface.Name, ifPrefix) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if tapIfaceIP(a).Equal(ip) {
				return nil
			}
		}
	}
	return fmt.Errorf("tap: -tap-addr %q is not on any interface with prefix %q (code: refused_non_tap_bind); check the address or set -tap-if-prefix to match your interface name", addr, ifPrefix)
}

// tapIfaceIP extracts the IP from a net.Addr returned by Interface.Addrs.
func tapIfaceIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	}
	return nil
}

// PreflightFirewall checks that the source-restriction firewall rule is
// verifiably in force before the tap listener opens. It fails closed: if the
// prober returns false or any error the startup is refused with a coded error
// and a remedy naming the runbook.
func PreflightFirewall(addr string, prober RulesetProber) error {
	ok, err := prober.InForce(addr)
	if err != nil {
		return fmt.Errorf("tap: preflight firewall probe failed (code: refused_preflight_error): %w — apply the rules in docs/features/grant-materialized-rooms/tap-runbook.md before starting the tap listener", err)
	}
	if !ok {
		return fmt.Errorf("tap: source-restriction firewall rule not detected for %s (code: refused_preflight_no_rule) — apply the rules in docs/features/grant-materialized-rooms/tap-runbook.md before starting the tap listener", addr)
	}
	return nil
}

// NewTapHandler returns an http.Handler that enforces per-request source
// binding and delegates all proxy semantics to engine. The engine is shared
// with the localhost listener so rule semantics cannot diverge between them.
//
// On the tap listener:
//   - A grant with an empty BoundSource (unbound) is refused outright
//     (refused_unbound_on_tap).
//   - A grant whose BoundSource does not match the transport source is refused
//     (refused_source_mismatch).
//   - Matching: the request is passed to engine unchanged.
//
// Requests with no grant token or an undecodable token are passed to engine,
// which refuses them with its standard refused_no_grant path.
func NewTapHandler(engine *Engine, grants *grant.Store) http.Handler {
	return &tapHandler{engine: engine, grants: grants}
}

// tapHandler is the per-listener gate that runs before the engine on the tap
// listener. It accesses engine.logger/newID/now directly (same package) so the
// refusal records appear in the shared artifact log without exporting internals.
type tapHandler struct {
	engine *Engine
	grants *grant.Store
}

// ServeHTTP enforces source binding, then delegates to the engine.
func (h *tapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok := r.Header.Get(grantHeader)
	if tok == "" {
		// No grant: engine refuses with refused_no_grant.
		h.engine.ServeHTTP(w, r)
		return
	}

	boundSrc, err := h.grants.BoundSource(tok)
	if err != nil {
		// Malformed or missing token: engine refuses with refused_no_grant.
		h.engine.ServeHTTP(w, r)
		return
	}

	if boundSrc == "" {
		h.tapRefuse(w, r, "refused_unbound_on_tap",
			"unbound grants are not permitted on the tap listener",
			"derive a bound child: custody derive -grant <parent> -actions <acts> -ttl <ttl> -bound-source <your-ip>")
		return
	}

	clientHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// r.RemoteAddr is always host:port from the net/http stack; fail closed.
		h.tapRefuse(w, r, "refused_source_mismatch",
			"could not parse transport source address",
			"ensure the connection originates from the bound source IP")
		return
	}

	grantIP := net.ParseIP(boundSrc)
	if grantIP == nil {
		// A malformed bound_source is a grant data-integrity problem, not a
		// source mismatch — keep it a distinct code so logs don't misattribute
		// it. The engine's full Validate (signature covers bound_source) is the
		// backstop; this just fails closed with a diagnosable reason.
		h.tapRefuse(w, r, "refused_grant_source_invalid",
			"grant bound source is not a valid IP address",
			"the grant record is malformed; re-derive a bound child: custody derive -grant <parent> -actions <acts> -ttl <ttl> -bound-source <your-ip>")
		return
	}
	clientIP := net.ParseIP(clientHost)
	if clientIP == nil || !grantIP.Equal(clientIP) {
		h.tapRefuse(w, r, "refused_source_mismatch",
			"transport source does not match the grant's bound source",
			"ensure the request originates from "+boundSrc+", or derive a new grant: custody derive -grant <parent> -actions <acts> -ttl <ttl> -bound-source "+clientHost)
		return
	}

	h.engine.ServeHTTP(w, r)
}

// tapRefuse writes a coded JSON refusal to w and appends one log artifact. It
// mirrors the engine's refuse/writeError shape exactly so callers see a
// consistent envelope regardless of which listener handled the request.
func (h *tapHandler) tapRefuse(w http.ResponseWriter, r *http.Request, code, reason, remedy string) {
	start := h.engine.now()
	reqID := h.engine.newID()
	w.Header().Set("X-Custody-Request-Id", reqID)
	writeError(w, http.StatusUnauthorized, errorBody{
		Code:      code,
		Reason:    reason,
		Remedy:    remedy,
		RequestID: reqID,
	})
	_ = h.engine.logger.write(logRecord{
		SchemaVersion: logSchemaVersion,
		Timestamp:     start.UTC().Format(time.RFC3339Nano),
		RequestID:     reqID,
		Method:        r.Method,
		Verdict:       verdictRefused,
		LatencyMs:     h.engine.now().Sub(start).Milliseconds(),
	})
}
