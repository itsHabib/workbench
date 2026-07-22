package match

import "strings"

// pathMatches reports whether the anchored path glob matches the whole canonical
// path. Both are split on "/" (encoded separators are already refused, so this is
// exact). A "**" glob segment matches zero or more path segments; any other glob
// segment matches exactly one path segment, where "*" ranges only over the
// constrained alphabet (spec §7 C) so PROJ-* cannot admit reserved routing
// punctuation. The match is anchored at both ends.
func pathMatches(glob, path string) bool {
	return matchSegments(strings.Split(glob, "/"), strings.Split(path, "/"))
}

func matchSegments(glob, path []string) bool {
	for len(glob) > 0 {
		if glob[0] == "**" {
			return matchDoubleStar(glob[1:], path)
		}
		if len(path) == 0 {
			return false
		}
		if !matchSegment(glob[0], path[0]) {
			return false
		}
		glob, path = glob[1:], path[1:]
	}
	return len(path) == 0
}

// matchDoubleStar tries to satisfy the rest of the glob after a "**" by letting
// the "**" absorb 0..len(path) leading segments.
func matchDoubleStar(glob, path []string) bool {
	for i := 0; i <= len(path); i++ {
		if matchSegments(glob, path[i:]) {
			return true
		}
	}
	return false
}

// matchSegment matches one glob segment (which may contain "*") against one path
// segment. A literal character must match exactly; a "*" matches a run of zero or
// more characters, but only from the constrained alphabet — the moment it would
// have to absorb a reserved/routing character, the match fails. This is the
// standard linear backtracking glob, with the constraint enforced at each byte a
// "*" absorbs.
func matchSegment(glob, seg string) bool {
	gi, si := 0, 0
	star, mark := -1, 0
	for si < len(seg) {
		switch {
		case gi < len(glob) && glob[gi] == '*':
			star, mark, gi = gi, si, gi+1
		case gi < len(glob) && glob[gi] == seg[si]:
			gi, si = gi+1, si+1
		case star >= 0 && isConstrained(seg[mark]):
			gi, mark = star+1, mark+1
			si = mark
		default:
			return false
		}
	}
	for gi < len(glob) && glob[gi] == '*' {
		gi++
	}
	return gi == len(glob)
}

// isConstrained reports whether c is in the segment-glob alphabet a "*" may
// absorb: the RFC 3986 unreserved set (letters, digits, "-._~"). Everything else
// — reserved gen/sub-delims, path parameters, vendor syntax — stops a "*".
func isConstrained(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("-._~", c) >= 0
}
