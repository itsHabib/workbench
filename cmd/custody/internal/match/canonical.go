package match

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// decodeOnce percent-decodes p exactly once and refuses any byte that would make
// the canonical path ambiguous once an upstream re-normalizes it (spec §7 C): a
// stray or truncated escape, a decoded escape character (a second layer, e.g.
// %252F), an encoded separator (%2F / %5C), a control/NUL, or a non-ASCII byte.
// A literal backslash and literal controls/non-ASCII are refused too; a literal
// forward slash is the only separator and is preserved.
func decodeOnce(p string) (string, error) {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c != '%' {
			if err := checkLiteral(c); err != nil {
				return "", err
			}
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(p) {
			return "", fmt.Errorf("%w: truncated percent-escape", ErrBadTarget)
		}
		hi, ok1 := unhex(p[i+1])
		lo, ok2 := unhex(p[i+2])
		if !ok1 || !ok2 {
			return "", fmt.Errorf("%w: invalid percent-escape", ErrBadTarget)
		}
		dec := hi<<4 | lo
		if err := checkDecoded(dec); err != nil {
			return "", err
		}
		b.WriteByte(dec)
		i += 2
	}
	return b.String(), nil
}

// checkLiteral refuses raw bytes a path may not carry unencoded: a backslash
// (a separator on some upstreams), control characters, DEL, and non-ASCII (v0
// is conservative — a full-width or combining spelling refuses, never matches).
func checkLiteral(c byte) error {
	if c == '\\' {
		return fmt.Errorf("%w: literal backslash", ErrBadTarget)
	}
	if c < 0x20 || c >= 0x7f {
		return fmt.Errorf("%w: control or non-ASCII byte", ErrBadTarget)
	}
	return nil
}

// checkDecoded refuses a byte that must never appear from decoding: a second
// escape (%), an encoded separator (/ or \), controls/NUL/DEL, or non-ASCII.
func checkDecoded(c byte) error {
	if c == '%' {
		return fmt.Errorf("%w: double-encoded escape", ErrBadTarget)
	}
	if c == '/' || c == '\\' {
		return fmt.Errorf("%w: encoded path separator", ErrBadTarget)
	}
	if c < 0x20 || c >= 0x7f {
		return fmt.Errorf("%w: encoded control or non-ASCII byte", ErrBadTarget)
	}
	return nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// removeDotSegments resolves "." and ".." against a rooted, decoded path (RFC
// 3986 §5.2.4, applied over segments since encoded separators are already
// refused). A ".." never climbs above the root. The result is what both the
// matcher and the forwarder use, so no "." or ".." survives to an upstream.
func removeDotSegments(p string) string {
	segments := strings.Split(p, "/")
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		switch seg {
		case ".":
			continue
		case "..":
			if len(out) > 1 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	joined := strings.Join(out, "/")
	if joined == "" {
		return "/"
	}
	return joined
}

// splitKey separates the leading key segment from the vendor path. The vendor
// path is always rooted; "/tracker" alone yields ("tracker", "/").
func splitKey(p string) (string, string) {
	trimmed := strings.TrimPrefix(p, "/")
	key, rest, found := strings.Cut(trimmed, "/")
	if !found || rest == "" {
		return key, "/"
	}
	return key, "/" + rest
}

// parseQuery parses the raw query string with the stdlib, which decodes values,
// rejects a malformed escape, and (Go ≥1.17) rejects a ";" separator — closing
// the ?jql=a;jql=b matcher/upstream differential of spec §7 D at the door. A
// repeated param surfaces as multiple values, which the once-only predicate then
// denies. QueryKeys is returned sorted for a stable log line.
func parseQuery(raw string) (url.Values, []string, error) {
	if raw == "" {
		return url.Values{}, nil, nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: malformed query", ErrBadTarget)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return values, keys, nil
}
