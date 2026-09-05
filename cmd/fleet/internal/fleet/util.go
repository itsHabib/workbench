package fleet

import (
	"encoding/json"
	"sort"
)

// ReadJSONBytes decodes one JSON object from bytes, or nil.
func ReadJSONBytes(b []byte) Rec {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

func sortFloats(xs []float64) { sort.Float64s(xs) }

func stableSort[T any](xs []T, less func(a, b T) bool) {
	sort.SliceStable(xs, func(i, j int) bool { return less(xs[i], xs[j]) })
}

// shellWords is the tokens of one simple command: a small POSIX lexer handling
// double quotes, single quotes and backslashes. If the fragment is not lexable — an
// unbalanced quote — it falls back to whitespace splitting so a switch is still seen.
func shellWords(text string) []string {
	var out []string
	var cur []rune
	inWord := false
	quote := rune(0)
	esc := false
	for _, r := range text {
		switch {
		case esc:
			cur = append(cur, r)
			esc = false
			inWord = true
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur = append(cur, r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				esc = true
			default:
				cur = append(cur, r)
			}
		case r == '\\':
			esc = true
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				out = append(out, string(cur))
				cur, inWord = nil, false
			}
		default:
			cur = append(cur, r)
			inWord = true
		}
	}
	if quote != 0 || esc {
		return fieldsFallback(text)
	}
	if inWord {
		out = append(out, string(cur))
	}
	return out
}

func fieldsFallback(text string) []string {
	var out []string
	cur := ""
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// ShellWords is shellWords, exported for the verbs.
func ShellWords(text string) []string { return shellWords(text) }

// IsWindows reports the platform, for the one place a verb picks a shell.
func IsWindows() bool { return isWindows }

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
