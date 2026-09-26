package query

import (
	"fmt"
	"strconv"
	"strings"
	"text/scanner"
	"unicode"
)

type predicate struct {
	col   *Column
	op    string
	value int64
}

// Plan binds fields and literals once; hot loops see only columns and integers.
type Plan struct {
	predicates []predicate
	table      *Table
}

// Compile accepts comparisons joined by AND or &&. Missing/null never compares
// true, even for !=. IS NULL and IS NOT NULL test presence explicitly.
func Compile(t *Table, expression string) (*Plan, error) {
	var s scanner.Scanner
	s.Init(strings.NewReader(expression))
	s.Mode = scanner.ScanIdents | scanner.ScanInts | scanner.ScanStrings
	var scanErr error
	s.Error = func(_ *scanner.Scanner, msg string) { scanErr = fmt.Errorf("query syntax: %s", msg) }
	p := &Plan{table: t}
	for token := s.Scan(); token != scanner.EOF; token = s.Scan() {
		if token != scanner.Ident {
			return nil, fmt.Errorf("expected column name, got %q", s.TokenText())
		}
		c, err := t.Find(s.TokenText())
		if err != nil {
			return nil, err
		}
		pred, err := parsePredicate(&s, c)
		if err != nil {
			return nil, err
		}
		p.predicates = append(p.predicates, pred)
		if len(p.predicates) > 64 {
			return nil, fmt.Errorf("at most 64 predicates")
		}
		if err := conjunction(&s); err != nil {
			return nil, err
		}
	}
	if scanErr != nil {
		return nil, scanErr
	}
	return p, nil
}

func conjunction(s *scanner.Scanner) error {
	token := s.Scan()
	if token == scanner.EOF {
		return nil
	}
	if strings.EqualFold(s.TokenText(), "AND") || token == '&' && s.Scan() == '&' {
		for unicode.IsSpace(s.Peek()) {
			s.Next()
		}
		if s.Peek() == scanner.EOF {
			return fmt.Errorf("missing predicate after AND")
		}
		return nil
	}
	return fmt.Errorf("expected AND or &&, got %q", s.TokenText())
}

func parsePredicate(s *scanner.Scanner, c *Column) (predicate, error) {
	op := s.Scan()
	if strings.EqualFold(s.TokenText(), "IS") {
		return parsePresence(s, c)
	}
	operator := string(op)
	if s.Peek() == '=' {
		s.Next()
		operator += "="
	}
	switch operator {
	case "==", "!=", ">", ">=", "<", "<=":
	default:
		return predicate{}, fmt.Errorf("unsupported operator %q", operator)
	}
	token := s.Scan()
	literal := s.TokenText()
	if token == '-' {
		token = s.Scan()
		literal = "-" + s.TokenText()
	}
	if c.Type == "string" {
		return stringPredicate(c, operator, token, literal)
	}
	if token != scanner.Int {
		return predicate{}, fmt.Errorf("column %s requires a signed integer", c.Name)
	}
	v, err := strconv.ParseInt(literal, 10, 64)
	if err != nil {
		return predicate{}, fmt.Errorf("invalid integer %q", literal)
	}
	return predicate{c, operator, v}, nil
}

func parsePresence(s *scanner.Scanner, c *Column) (predicate, error) {
	s.Scan()
	op := "null"
	if strings.EqualFold(s.TokenText(), "NOT") {
		op = "present"
		s.Scan()
	}
	if !strings.EqualFold(s.TokenText(), "NULL") {
		return predicate{}, fmt.Errorf("expected NULL or NOT NULL after IS")
	}
	return predicate{c, op, 0}, nil
}

func stringPredicate(c *Column, op string, token rune, literal string) (predicate, error) {
	if token != scanner.String {
		return predicate{}, fmt.Errorf("column %s requires a double-quoted string", c.Name)
	}
	if op != "==" && op != "!=" {
		return predicate{}, fmt.Errorf("string columns support == and !=; select a numeric time field for ranges")
	}
	v, err := strconv.Unquote(literal)
	if err != nil {
		return predicate{}, err
	}
	for i, s := range c.Dictionary {
		if s == v {
			return predicate{c, op, int64(i)}, nil
		}
	}
	return predicate{c, op, -1}, nil
}

// Match returns -1 for matching rows, 0 otherwise. scalar forces the reference
// kernel even in a binary built with the experimental SIMD backend.
func (p *Plan) Match(scalar bool) []int64 {
	mask := make([]int64, p.table.Rows)
	for i := range mask {
		mask[i] = -1
	}
	for _, pred := range p.predicates {
		if scalar {
			scalarFilter(mask, pred.col.Values, pred.col.Valid, pred.op, pred.value)
			continue
		}
		filter(mask, pred.col.Values, pred.col.Valid, pred.op, pred.value)
	}
	return mask
}

func scalarFilter(mask, values, valid []int64, op string, value int64) {
	// Dispatch is outside the row loop; compilation removes interpretation cost.
	switch op {
	case "==":
		for i, v := range values {
			mask[i] &= truth(v == value) & valid[i]
		}
	case "!=":
		for i, v := range values {
			mask[i] &= truth(v != value) & valid[i]
		}
	case ">":
		for i, v := range values {
			mask[i] &= truth(v > value) & valid[i]
		}
	case ">=":
		for i, v := range values {
			mask[i] &= truth(v >= value) & valid[i]
		}
	case "<":
		for i, v := range values {
			mask[i] &= truth(v < value) & valid[i]
		}
	case "<=":
		for i, v := range values {
			mask[i] &= truth(v <= value) & valid[i]
		}
	case "null":
		for i, v := range valid {
			mask[i] &= ^v
		}
	case "present":
		for i, v := range valid {
			mask[i] &= v
		}
	}
}

func truth(b bool) int64 {
	if b {
		return -1
	}
	return 0
}
