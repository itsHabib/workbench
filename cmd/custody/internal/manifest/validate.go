package manifest

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

// decode reads exactly one JSON manifest, rejecting unknown fields at every
// level and any trailing data. Unknown fields map to ErrUnknownField so a typo
// or a removed field fails loud at load rather than being silently dropped.
func decode(r io.Reader) (*Manifest, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return nil, fmt.Errorf("%w: %v", ErrUnknownField, err)
		}
		return nil, fmt.Errorf("manifest: parse: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("manifest: parse: trailing data after manifest")
	}
	return &m, nil
}

// validMethods is the set of HTTP methods a rule may name, plus the "*"
// wildcard. TRACE and CONNECT are absent on purpose: they are denied
// unconditionally at the proxy (spec §7 C), so naming one in a rule is dead,
// misleading config and is refused at load.
var validMethods = map[string]bool{
	"*": true, "GET": true, "HEAD": true, "POST": true,
	"PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true,
}

func validateMethods(methods []string) error {
	if len(methods) == 0 {
		return fmt.Errorf("%w: methods", ErrMissingField)
	}
	for _, m := range methods {
		if !validMethods[m] {
			return fmt.Errorf("%w: %q", ErrBadMethod, m)
		}
	}
	return nil
}

// validatePath checks the anchored path glob is well-formed. It validates
// STRUCTURE only — actual matching is the proxy's job. A path must be rooted,
// carry no control/whitespace/reserved routing characters that would let a glob
// admit a separator it never meant to (spec §7 C), and be a valid glob pattern.
func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: path", ErrMissingField)
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: %q must start with /", ErrBadPath, p)
	}
	if bad, r := firstDisallowedPathRune(p); bad {
		return fmt.Errorf("%w: %q carries disallowed character %q", ErrBadPath, p, r)
	}
	if _, err := path.Match(p, ""); err != nil {
		return fmt.Errorf("%w: %q: %v", ErrBadPath, p, err)
	}
	return nil
}

// firstDisallowedPathRune reports the first character a path glob may not carry.
// The allowed alphabet is the unreserved URL path set plus the glob metacharacter
// "*" — deliberately excluding query/fragment separators, backslashes, control
// characters, and reserved routing punctuation.
func firstDisallowedPathRune(p string) (bool, rune) {
	const allowedPunct = "/-._~*"
	for _, r := range p {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if strings.ContainsRune(allowedPunct, r) {
			continue
		}
		return true, r
	}
	return false, 0
}

func validateInject(inject []Injection) error {
	if len(inject) == 0 {
		return fmt.Errorf("%w: inject", ErrMissingField)
	}
	if len(inject) != 1 {
		return fmt.Errorf("%w: v0 accepts exactly one inject entry, got %d", ErrBadInject, len(inject))
	}
	in := inject[0]
	if in.Kind != "header" {
		return fmt.Errorf("%w: v0 inject kind must be \"header\", got %q", ErrBadInject, in.Kind)
	}
	if err := validateHeaderName(in.Name); err != nil {
		return err
	}
	return validateTemplate(in.Template)
}

// deniedHeaders are header names an injection may never target: Host (forced
// from the manifest authority), hop-by-hop headers, forwarding headers, and —
// by prefix — custody's own X-Custody-* namespace (spec §5, §7 C).
var deniedHeaders = map[string]bool{
	"host":                true,
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"forwarded":           true,
	"x-forwarded-for":     true,
	"x-forwarded-host":    true,
	"x-forwarded-proto":   true,
	"x-forwarded-port":    true,
	"x-real-ip":           true,
}

func validateHeaderName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: inject name", ErrMissingField)
	}
	if !isHeaderToken(name) {
		return fmt.Errorf("%w: %q is not a valid header name", ErrBadHeaderName, name)
	}
	lower := strings.ToLower(name)
	if deniedHeaders[lower] {
		return fmt.Errorf("%w: %q may not be injected", ErrBadHeaderName, name)
	}
	if strings.HasPrefix(lower, "x-custody-") {
		return fmt.Errorf("%w: %q is in custody's reserved namespace", ErrBadHeaderName, name)
	}
	return nil
}

// isHeaderToken reports whether name is a valid RFC 7230 field-name (a token of
// tchar characters).
func isHeaderToken(name string) bool {
	const tchar = "!#$%&'*+-.^_`|~"
	for _, r := range name {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if strings.ContainsRune(tchar, r) {
			continue
		}
		return false
	}
	return true
}

// validateTemplate enforces the single-placeholder, no-CRLF injection template
// (spec §8.2): exactly one "{secret}" placeholder and no carriage return or
// newline that could split the header.
func validateTemplate(tmpl string) error {
	if tmpl == "" {
		return fmt.Errorf("%w: inject template", ErrMissingField)
	}
	if strings.ContainsAny(tmpl, "\r\n") {
		return fmt.Errorf("%w: template carries CR/LF", ErrBadTemplate)
	}
	if n := strings.Count(tmpl, "{secret}"); n != 1 {
		return fmt.Errorf("%w: template must contain exactly one {secret}, got %d", ErrBadTemplate, n)
	}
	return nil
}
