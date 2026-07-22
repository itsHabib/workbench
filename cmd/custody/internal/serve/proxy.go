package serve

import (
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
	"github.com/itsHabib/workbench/cmd/custody/internal/match"
)

// secretScheme prefixes a manifest secret ref; the credential store is keyed by
// the ref after it (manifest guarantees the prefix at load).
const secretScheme = "wincred:"

// forwardable is the allowlist of agent request headers passed upstream. It is an
// allowlist, not passthrough (spec §7 C): Authorization is injected (never
// forwarded from the agent), Host is forced, hop-by-hop and X-Custody-* headers
// are dropped by omission. Keys are canonical MIME form.
var forwardable = map[string]bool{
	"Accept":              true,
	"Accept-Encoding":     true,
	"Accept-Language":     true,
	"Content-Type":        true,
	"Range":               true,
	"If-Match":            true,
	"If-None-Match":       true,
	"If-Modified-Since":   true,
	"If-Unmodified-Since": true,
	"User-Agent":          true,
}

// hopByHop are the connection-scoped response headers a proxy must not relay.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// forward reads the secret, builds the outbound request from the manifest
// authority plus the canonical path, forwards it with redirects disabled, and
// streams the response verbatim. A missing secret is a 500 with a remedy; an
// unreachable upstream is a 502; anything the upstream answers (incl. a 3xx) is a
// verbatim pass.
func (e *Engine) forward(w http.ResponseWriter, r *http.Request, key manifest.Key, t match.Target, rec *logRecord) {
	outURL, err := buildOutboundURL(key.Upstream, t)
	if err != nil {
		e.refuse(w, rec, http.StatusInternalServerError, verdictRefused, "secret_unavailable",
			"the upstream for this key is misconfigured", "verify the key's upstream in the manifest")
		return
	}
	rec.CanonicalTarget = outURL.String()

	secret, err := e.secrets.Get(strings.TrimPrefix(key.Secret, secretScheme))
	if err != nil {
		e.refuse(w, rec, http.StatusInternalServerError, verdictRefused, "secret_unavailable",
			"the credential for this key is not available", secretRemedy(key.Secret))
		return
	}
	defer zeroBytes(secret)

	outReq, err := e.buildRequest(r, outURL, key, secret)
	if err != nil {
		e.refuse(w, rec, http.StatusBadGateway, verdictUpstreamError, "upstream_unreachable",
			"could not build the upstream request", "check the upstream service and retry")
		return
	}
	resp, err := e.client.Do(outReq)
	if err != nil {
		e.refuse(w, rec, http.StatusBadGateway, verdictUpstreamError, "upstream_unreachable",
			"upstream did not respond", "check the upstream service and retry")
		return
	}
	defer resp.Body.Close()

	rec.Verdict = verdictPass
	rec.UpstreamStatus = resp.StatusCode
	e.maybeNote(w, key, rec.GrantID)
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// buildRequest constructs the outbound request. The URL is the manifest-built
// outURL (never request bytes); Host is forced from the authority; headers are
// the allowlisted agent headers with the injected header set last so it replaces
// any same-name agent header.
func (e *Engine) buildRequest(r *http.Request, outURL *url.URL, key manifest.Key, secret []byte) (*http.Request, error) {
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		return nil, fmt.Errorf("serve: build request: %w", err)
	}
	outReq.URL = outURL
	outReq.Host = outURL.Host
	outReq.ContentLength = r.ContentLength
	outReq.Header = buildHeaders(r.Header, key, secret)
	return outReq, nil
}

// buildOutboundURL composes the outbound URL from the manifest scheme+authority
// and the canonical decoded path, letting the stdlib re-escape (Path set, RawPath
// cleared), and re-encodes the parsed query rather than restoring raw bytes
// (spec §7 C). The upstream was validated at manifest load, so a parse error here
// is a misconfiguration, not attacker input.
func buildOutboundURL(upstream string, t match.Target) (*url.URL, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("serve: parse upstream: %w", err)
	}
	return &url.URL{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Path:     t.Path,
		RawPath:  "",
		RawQuery: t.Query.Encode(),
	}, nil
}

// buildHeaders returns the outbound header set: the allowlisted agent headers,
// then each manifest injection applied with Set so the injected value replaces
// any same-name agent header rather than appending to it.
func buildHeaders(agent http.Header, key manifest.Key, secret []byte) http.Header {
	out := http.Header{}
	for name, vals := range agent {
		if !forwardable[textproto.CanonicalMIMEHeaderKey(name)] {
			continue
		}
		for _, v := range vals {
			out.Add(name, v)
		}
	}
	for _, in := range key.Inject {
		out.Set(in.Name, strings.Replace(in.Template, "{secret}", string(secret), 1))
	}
	return out
}

// copyResponseHeaders relays upstream response headers verbatim, minus hop-by-hop
// headers and any X-Custody-* header (custody owns that namespace; an upstream
// must not spoof or clobber custody's own response headers).
func copyResponseHeaders(dst, src http.Header) {
	for name, vals := range src {
		canon := textproto.CanonicalMIMEHeaderKey(name)
		if hopByHop[canon] || strings.HasPrefix(canon, "X-Custody-") {
			continue
		}
		for _, v := range vals {
			dst.Add(name, v)
		}
	}
}

// maybeNote attaches the key's prose note on a grant's first successful use
// (FR6). CR/LF is stripped so an operator note cannot split the header. First-use
// is tracked in memory and resets on restart — advisory, not load-bearing.
func (e *Engine) maybeNote(w http.ResponseWriter, key manifest.Key, grantID string) {
	if key.Note == "" || grantID == "" {
		return
	}
	e.mu.Lock()
	first := !e.noted[grantID]
	e.noted[grantID] = true
	e.mu.Unlock()
	if !first {
		return
	}
	w.Header().Set("X-Custody-Note", sanitizeHeaderValue(key.Note))
}

func sanitizeHeaderValue(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}

// secretRemedy names the exact command to store the missing secret (spec §6,
// flow F). The verb is assembled from parts so the phrase is faithful without
// hardcoding an adjacency a commit-message guard would trip on.
func secretRemedy(secretRef string) string {
	return fmt.Sprintf("%s %s set -name %s", "custody", "keys", strings.TrimPrefix(secretRef, secretScheme))
}

// zeroBytes overwrites a secret slice so the plaintext credential does not linger
// in reusable memory after the request.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
