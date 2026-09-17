package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Cmd struct {
	Op, Key, Val string
	TTL          time.Duration
}

func tokens(s string) ([]string, error) {
	var out []string
	i := 0
	for i < len(s) {
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		if s[i] != '"' {
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' {
				j++
			}
			out = append(out, s[i:j])
			i = j
			continue
		}
		var b strings.Builder
		i++
		closed := false
		for i < len(s) {
			if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			if s[i] == '"' {
				closed = true
				i++
				break
			}
			b.WriteByte(s[i])
			i++
		}
		if !closed {
			return nil, errors.New("query: unterminated quote")
		}
		out = append(out, b.String())
	}
	return out, nil
}

func Parse(s string) (Cmd, error) {
	t, err := tokens(s)
	if err != nil {
		return Cmd{}, err
	}
	if len(t) == 0 {
		return Cmd{}, errors.New("query: empty")
	}
	op := strings.ToUpper(t[0])
	args := t[1:]
	switch op {
	case "KEYS", "COUNT":
		if len(args) != 0 {
			return Cmd{}, fmt.Errorf("query: %s takes no arguments", op)
		}
		return Cmd{Op: op}, nil
	case "GET", "DEL":
		if len(args) != 1 {
			return Cmd{}, fmt.Errorf("query: %s takes one key", op)
		}
		return Cmd{Op: op, Key: args[0]}, nil
	case "SET":
		if len(args) == 2 {
			return Cmd{Op: op, Key: args[0], Val: args[1]}, nil
		}
		if len(args) == 4 && strings.EqualFold(args[2], "EX") {
			n, err := strconv.Atoi(args[3])
			if err != nil || n < 0 {
				return Cmd{}, errors.New("query: bad EX")
			}
			return Cmd{Op: op, Key: args[0], Val: args[1], TTL: time.Duration(n) * time.Second}, nil
		}
		return Cmd{}, errors.New("query: SET key value [EX seconds]")
	}
	return Cmd{}, fmt.Errorf("query: unknown op %q", t[0])
}
