// Package web serves the embedded Control Room browser application.
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const (
	csrfCookie = "controlroom_csrf"
	csp        = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

//go:embed static/index.html static/app.js static/styles.css
var assets embed.FS

// SnapshotSupplier returns the latest immutable snapshot.
type SnapshotSupplier func() model.Snapshot

// RefreshRequest identifies a requested collection trigger.
type RefreshRequest struct {
	Mode    string `json:"mode"`
	Trigger string `json:"trigger"`
}

// RefreshReceipt lets the browser wait for a generation newer than its baseline.
type RefreshReceipt struct {
	BaselineVersion uint64 `json:"baseline_version"`
	Status          string `json:"status"`
}

// RefreshFunc starts or joins snapshot collection.
type RefreshFunc func(context.Context, RefreshRequest) (RefreshReceipt, error)

// Config supplies the narrow publication seams owned by the web package.
type Config struct {
	Host        string
	Snapshot    SnapshotSupplier
	Refresh     RefreshFunc
	TokenSource func([]byte) (int, error)
}

type server struct {
	host     string
	snapshot SnapshotSupplier
	refresh  RefreshFunc
	token    string
}

// New builds a host-locked handler and generates its process-scoped CSRF token.
func New(config Config) (http.Handler, error) {
	if config.Host == "" || config.Snapshot == nil || config.Refresh == nil {
		return nil, fmt.Errorf("host, snapshot supplier, and refresh callback are required")
	}
	source := config.TokenSource
	if source == nil {
		source = rand.Read
	}
	raw := make([]byte, 32)
	n, err := source(raw)
	if err != nil || n != len(raw) {
		return nil, fmt.Errorf("generate CSRF token: %w", errors.Join(err, io.ErrUnexpectedEOF))
	}
	return &server{
		host: config.Host, snapshot: config.Snapshot, refresh: config.Refresh,
		token: base64.RawURLEncoding.EncodeToString(raw),
	}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	if r.Host != s.host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/":
		s.serveAsset(w, r, "static/index.html", "text/html; charset=utf-8", true)
	case "/static/app.js":
		s.serveAsset(w, r, "static/app.js", "text/javascript; charset=utf-8", false)
	case "/static/styles.css":
		s.serveAsset(w, r, "static/styles.css", "text/css; charset=utf-8", false)
	case "/api/v1/snapshot":
		s.serveSnapshot(w, r)
	case "/api/v1/refresh":
		s.serveRefresh(w, r)
	case "/healthz":
		s.serveHealth(w, r)
	default:
		http.NotFound(w, r)
	}
}

func setSecurityHeaders(header http.Header) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", csp)
}

func allowRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (s *server) serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string, shell bool) {
	if !allowRead(w, r) {
		return
	}
	if shell && r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	body, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if shell {
		w.Header().Set("Cache-Control", "no-store")
		http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: s.token, Path: "/", SameSite: http.SameSiteStrictMode})
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func (s *server) serveSnapshot(w http.ResponseWriter, r *http.Request) {
	if !allowRead(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	body, err := json.Marshal(s.snapshot())
	if err != nil {
		http.Error(w, "snapshot unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func (s *server) serveRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeRefresh(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	request, ok := decodeRefresh(r.Body)
	if !ok || request.Mode != "demo" || request.Trigger != "manual" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	receipt, err := s.refresh(r.Context(), request)
	if err != nil {
		http.Error(w, "refresh unavailable", http.StatusServiceUnavailable)
		return
	}
	if receipt.Status != "started" && receipt.Status != "joined" {
		http.Error(w, "refresh unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (s *server) authorizeRefresh(r *http.Request) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		return false
	}
	if r.Header.Get("Origin") != "http://"+s.host {
		return false
	}
	cookie, err := r.Cookie(csrfCookie)
	if err != nil {
		return false
	}
	header := r.Header.Get("X-Controlroom-CSRF")
	return secureEqual(cookie.Value, s.token) && secureEqual(header, s.token)
}

func secureEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func decodeRefresh(body io.Reader) (RefreshRequest, bool) {
	decoder := json.NewDecoder(io.LimitReader(body, 1025))
	decoder.DisallowUnknownFields()
	var request RefreshRequest
	if err := decoder.Decode(&request); err != nil {
		return RefreshRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RefreshRequest{}, false
	}
	return request, true
}

func (s *server) serveHealth(w http.ResponseWriter, r *http.Request) {
	if !allowRead(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		http.Error(w, "response unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

// AssetSource returns embedded source for contract tests.
func AssetSource(name string) ([]byte, error) {
	if strings.Contains(name, "..") || strings.ContainsAny(name, `\\:`) {
		return nil, fmt.Errorf("invalid asset name")
	}
	return assets.ReadFile("static/" + name)
}
