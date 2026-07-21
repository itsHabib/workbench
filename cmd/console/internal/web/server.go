// Package web is the console's loopback HTTP surface: it serves one embedded,
// self-contained UI page and a few JSON endpoints that proxy gate's own
// projections. It has NO mutating routes — judging and minting stay in the CLI
// in this version, so the console can only read and render, never decide.
package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/itsHabib/workbench/cmd/console/internal/gatecli"
)

//go:embed static/app.html
var appPage []byte

// Server routes the read-only console. Construct it with New; it is an
// http.Handler that pins the Host header before dispatching.
type Server struct {
	gate *gatecli.Client
	host string
	mux  *http.ServeMux
}

// New builds a server serving gate's data on the given host:port (used for the
// Host-header allowlist).
func New(gate *gatecli.Client, host string) *Server {
	s := &Server{gate: gate, host: host, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/next", s.handleNext)
	s.mux.HandleFunc("GET /api/run/{id}", s.handleRun)
	s.mux.HandleFunc("GET /api/audit", s.handleAudit)
	// The single page is served for the root and for any /run/<id> deep link;
	// its own script picks the view from the path.
	s.mux.HandleFunc("GET /{$}", s.handleApp)
	s.mux.HandleFunc("GET /run/{id}", s.handleApp)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// nosniff on every response — the app page and the /api/* JSON alike. gate's
	// stdout carries untrusted strings (PR titles, escalation questions), and a
	// browser that MIME-sniffed a directly-navigated /api response as HTML would
	// run it with no CSP. Setting it here, before dispatch, covers every route.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Loopback hardening: a page on any origin can still POST/GET to 127.0.0.1,
	// so pin the Host header to the address we serve on. This refuses a
	// DNS-rebinding attacker — a remote name re-resolved to 127.0.0.1 arrives
	// with its own Host and is turned away before reaching a handler.
	if !s.hostAllowed(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// hostAllowed accepts the exact serve address and the loopback aliases for the
// same port, and nothing else.
func (s *Server) hostAllowed(h string) bool {
	if h == s.host {
		return true
	}
	_, port, err := net.SplitHostPort(s.host)
	if err != nil {
		return false
	}
	host, hp, err := net.SplitHostPort(h)
	if err != nil || hp != port {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	raw, err := s.gate.Next(r.Context())
	if err != nil {
		s.gateError(w, err)
		return
	}
	writeJSON(w, raw)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	raw, err := s.gate.Explain(r.Context(), r.PathValue("id"))
	if err != nil {
		s.gateError(w, err)
		return
	}
	writeJSON(w, raw)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	st, err := s.gate.Audit(r.Context())
	if err != nil {
		s.gateError(w, err)
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		s.gateError(w, err)
		return
	}
	writeJSON(w, b)
}

func (s *Server) handleApp(w http.ResponseWriter, _ *http.Request) {
	// Everything is same-origin and inlined; forbid any external fetch so the
	// page can neither pull nor exfiltrate. There are no dependencies to pull —
	// the header makes that a rule rather than a hope.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(appPage)
}

func writeJSON(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

// gateError reports a failed gate invocation as a 502: the console is a gateway
// to gate, and a gate that errored is an upstream failure, not the console's.
func (s *Server) gateError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// Serve binds a loopback listener and serves until ctx is cancelled. It refuses
// any non-loopback address: the console has no authentication, so it must never
// be reachable off the machine. Returns the bound address via addrFn (called
// once the listener is up) so a caller can print the real port for addr ":0".
func Serve(ctx context.Context, addr string, gate *gatecli.Client, addrFn func(string)) error {
	if err := requireLoopback(addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", addr, err)
	}
	if addrFn != nil {
		addrFn(ln.Addr().String())
	}
	srv := &http.Server{Handler: New(gate, ln.Addr().String()), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sh)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// requireLoopback rejects any bind address that is not localhost or a loopback
// IP — the one invariant that keeps an unauthenticated console on the machine.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("web: bad addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("web: refusing non-loopback bind %q — the console has no auth and must stay on this machine", addr)
	}
	return nil
}
