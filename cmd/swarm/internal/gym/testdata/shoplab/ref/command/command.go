package command

import (
	"fmt"
	"sort"
	"strings"

	"shoplab/errs"
)

type Cmd struct {
	Op      string
	Args    []string
	IdemKey string
}

var arity = map[string][2]int{
	"PRODUCT": {5, 5}, "STOCK": {2, 2}, "AVAIL": {1, 1}, "COUPON": {3, 4}, "ADD": {3, 3},
	"REMOVE": {2, 2}, "SHOW": {1, 1}, "CHECKOUT": {2, 3}, "PAY": {2, 2}, "SHIP": {1, 1},
	"DELIVER": {1, 1}, "CANCEL": {1, 1}, "ORDER": {1, 1}, "BALANCE": {1, 1}, "REPORT": {0, 0},
}

func Ops() []string {
	out := make([]string, 0, len(arity))
	for op := range arity {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

type token struct {
	text   string
	quoted bool
}

func bad(format string, a ...any) error {
	return fmt.Errorf("command: "+format+": %w", append(a, errs.ErrInvalid)...)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }

func lex(s string) ([]token, error) {
	var out []token
	for i := 0; i < len(s); {
		if isSpace(s[i]) {
			i++
			continue
		}
		if s[i] != '"' {
			j := i
			for j < len(s) && !isSpace(s[j]) {
				if s[j] == '"' {
					return nil, bad("quote inside a token")
				}
				j++
			}
			out = append(out, token{text: s[i:j]})
			i = j
			continue
		}
		var b strings.Builder
		i++
		closed := false
		for i < len(s) && !closed {
			switch c := s[i]; {
			case c == '"':
				closed = true
			case c == '\\':
				if i+1 >= len(s) || s[i+1] != '"' && s[i+1] != '\\' {
					return nil, bad("bad escape")
				}
				b.WriteByte(s[i+1])
				i++
			default:
				b.WriteByte(c)
			}
			i++
		}
		if !closed {
			return nil, bad("unterminated quote")
		}
		if i < len(s) && !isSpace(s[i]) {
			return nil, bad("text after a closing quote")
		}
		out = append(out, token{text: b.String(), quoted: true})
	}
	return out, nil
}

func Parse(line string) (Cmd, error) {
	toks, err := lex(line)
	if err != nil {
		return Cmd{}, err
	}
	cmd := Cmd{Args: []string{}}
	var rest []string
	haveKey := false
	for _, t := range toks {
		if t.quoted || !strings.HasPrefix(t.text, "@") {
			rest = append(rest, t.text)
			continue
		}
		if haveKey || t.text == "@" {
			return Cmd{}, bad("idempotency key %q", t.text)
		}
		haveKey = true
		cmd.IdemKey = t.text[1:]
	}
	if len(rest) == 0 {
		return Cmd{}, bad("empty line")
	}
	cmd.Op = strings.ToUpper(rest[0])
	cmd.Args = append(cmd.Args, rest[1:]...)
	n, ok := arity[cmd.Op]
	if !ok {
		return Cmd{}, bad("unknown op %q", cmd.Op)
	}
	if len(cmd.Args) < n[0] || len(cmd.Args) > n[1] {
		return Cmd{}, bad("%s takes %d to %d arguments", cmd.Op, n[0], n[1])
	}
	return cmd, nil
}
