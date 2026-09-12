package fleet

import (
	"encoding/json"
	"runtime"
	"sort"
	"strings"
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

// backslashIsSeparator: on Windows an unquoted backslash in a cd operand is a path
// separator the agent meant, not a shell escape. Lexing it as an escape turned
// `C:\Users\seat` into the drive-relative `C:Usersseat`, which matched nothing bound,
// and the directory guard failed open (#320). Only cd operands read it this way; the
// other guards keep POSIX lexing so their deny matching does not move.
var backslashIsSeparator = runtime.GOOS == "windows"

// cdWords lexes a cd operand the way this platform's paths are written.
func cdWords(text string) []string { return lexWords(text, backslashIsSeparator) }

// shellWords is the tokens of one simple command: a small POSIX lexer handling
// double quotes, single quotes and backslashes. Inside double quotes a backslash
// escapes only $ ` " \ and newline, as the shell does, so a quoted Windows path keeps
// its separators everywhere. If the fragment is not lexable — an unbalanced quote —
// it falls back to whitespace splitting so a switch is still seen.
func shellWords(text string) []string { return lexWords(text, false) }

func lexWords(text string, backslashSeparator bool) []string {
	var out []string
	var cur []rune
	inWord := false
	quote := rune(0)
	esc := false
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
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
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(rs) && strings.ContainsRune("$`\"\\\n", rs[i+1]):
				esc = true
			default:
				cur = append(cur, r)
			}
		case r == '\\' && !backslashSeparator:
			esc = true
			inWord = true
		case isQuote(r):
			quote = r
			inWord = true
		case isSpace(r):
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

func isQuote(r rune) bool { return r == '\'' || r == '"' }
func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

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
