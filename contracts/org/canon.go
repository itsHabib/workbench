// Package org is the agent-organization contract vocabulary: durable roles,
// their append-only continuity chains, the assignments and messages that pass
// between them, and the supervision that replaces a role's incarnation when it
// dies.
//
// This file is the canonical encoder every other file in the package digests
// through. It exists as its own concern, and lands before any record type,
// because a chain's digests outlive every refactor that follows them: once a
// record is written, the bytes that produced its hash can never change.
//
// The encoder is deliberately NOT reflection over Go structs. Every one of the
// bakeoff kernels this package draws from — branchroom, mandate, obligation,
// proofline — canonicalizes with json.Marshal, which makes the byte sequence
// depend on Go *declaration order*, HTML-escapes `&`, `<`, and `>`, and
// carries no scheme version. Those digests are stable Go-to-Go and reproducible
// nowhere else, and reordering a struct field silently invalidates history.
// Here a type states its canonical shape by building a Value, so the encoding
// is a specification a second implementation can follow rather than an artifact
// of this one.
//
// It is a leaf: it imports nothing outside the standard library and carries no
// decision logic.
package org

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Scheme names the encoding these bytes were produced under. It is recorded on
// every digested payload and checked on read, so a future scheme can be
// introduced without silently reinterpreting existing chains. Bump it only for
// a change that alters bytes for some input; adding a Value kind that no
// existing record uses does not.
const Scheme = "canon/v1"

// Kind discriminates a Value. The set is closed and small on purpose: every
// kind must have exactly one byte sequence for a given logical value, and each
// addition is a new opportunity for two implementations to disagree.
type Kind uint8

const (
	KindString Kind = iota + 1
	KindInt
	KindBool
	KindNull
	KindList
	KindMap
)

// Value is the canonical form of anything this package digests. It models the
// smallest set of shapes the org contracts need — no floats, no timestamps as
// a distinct kind, no arbitrary nesting rules beyond list and map.
//
// Floats are absent deliberately. Their decimal representation is the single
// richest source of cross-language digest disagreement, and nothing in an org
// record needs one: sequences are integers, deadlines are RFC 3339 strings,
// and sizes are integers.
type Value struct {
	kind  Kind
	str   string
	num   int64
	flag  bool
	list  []Value
	pairs []Field
}

// Field is one named member of a map Value. Encoding sorts fields by name, so
// the order they are supplied in is irrelevant — a record type cannot change
// its digest by rearranging the code that builds it, which is the property
// json.Marshal fails to provide.
type Field struct {
	Name  string
	Value Value
}

// String, Int, Bool, Null, List, and Map construct Values. They are the entire
// authoring surface; a record type's canonical shape is a Map of these.
func String(s string) Value { return Value{kind: KindString, str: s} }
func Int(n int64) Value     { return Value{kind: KindInt, num: n} }
func Bool(b bool) Value     { return Value{kind: KindBool, flag: b} }
func Null() Value           { return Value{kind: KindNull} }

// List is an ordered sequence. Order is meaningful and preserved: a chain's
// records, a charter's scopes, and a checkpoint's next-actions all mean
// something different rearranged.
func List(items ...Value) Value {
	return Value{kind: KindList, list: items}
}

// Map is an unordered set of named fields, encoded in sorted-name order.
func Map(fields ...Field) Value {
	return Value{kind: KindMap, pairs: fields}
}

// F is shorthand for one field, so a record's canonical shape reads as a table.
func F(name string, v Value) Field { return Field{Name: name, Value: v} }

// Strings lifts a slice into a List of String, the shape most record fields
// need.
func Strings(ss []string) Value {
	items := make([]Value, 0, len(ss))
	for _, s := range ss {
		items = append(items, String(s))
	}
	return List(items...)
}

// Canonical is implemented by every type this package digests. A type states
// its own field order and membership here; nothing is derived by reflection.
type Canonical interface {
	Canonical() Value
}

// Encoding errors. Callers branch with errors.Is. Every one of them names a
// value the encoder refuses to guess about rather than encoding something a
// second implementation might encode differently.
var (
	ErrUnknownKind    = errors.New("org: unknown value kind")
	ErrInvalidUTF8    = errors.New("org: string is not valid UTF-8")
	ErrDuplicateField = errors.New("org: duplicate field name in map")
	ErrEmptyFieldName = errors.New("org: empty field name in map")
)

// Encode renders v as canonical bytes.
//
// The grammar is JSON-shaped so the output stays greppable in a JSONL chain,
// but it is specified here rather than delegated: no insignificant whitespace,
// map fields in sorted-name order, integers in their shortest decimal form,
// and exactly one escape rule for strings. It never HTML-escapes, so a chain
// carrying a `&` in a decision's prose does not acquire a `&`.
//
// The output does not include the scheme version; Digest does. A caller
// embedding canonical bytes inside a larger structure records the scheme once,
// at the boundary where the digest is taken.
func Encode(v Value) ([]byte, error) {
	var buf []byte
	buf, err := appendValue(buf, v)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func appendValue(dst []byte, v Value) ([]byte, error) {
	switch v.kind {
	case KindString:
		return appendString(dst, v.str)
	case KindInt:
		return strconv.AppendInt(dst, v.num, 10), nil
	case KindBool:
		return strconv.AppendBool(dst, v.flag), nil
	case KindNull:
		return append(dst, "null"...), nil
	case KindList:
		return appendList(dst, v.list)
	case KindMap:
		return appendMap(dst, v.pairs)
	}
	return nil, fmt.Errorf("%w: %d", ErrUnknownKind, v.kind)
}

func appendList(dst []byte, items []Value) ([]byte, error) {
	dst = append(dst, '[')
	for i := range items {
		if i > 0 {
			dst = append(dst, ',')
		}
		next, err := appendValue(dst, items[i])
		if err != nil {
			return nil, err
		}
		dst = next
	}
	return append(dst, ']'), nil
}

func appendMap(dst []byte, fields []Field) ([]byte, error) {
	sorted, err := sortFields(fields)
	if err != nil {
		return nil, err
	}
	dst = append(dst, '{')
	for i := range sorted {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst, err = appendString(dst, sorted[i].Name)
		if err != nil {
			return nil, err
		}
		dst = append(dst, ':')
		dst, err = appendValue(dst, sorted[i].Value)
		if err != nil {
			return nil, err
		}
	}
	return append(dst, '}'), nil
}

// sortFields returns a name-sorted copy and refuses the two shapes that would
// make a map's encoding ambiguous: an empty name, and two fields sharing one.
// A duplicate is refused rather than deduped, because "last wins" is a rule a
// second implementation would have to reproduce exactly, and refusing needs no
// agreement at all.
func sortFields(fields []Field) ([]Field, error) {
	out := make([]Field, len(fields))
	copy(out, fields)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for i := range out {
		if out[i].Name == "" {
			return nil, ErrEmptyFieldName
		}
		if i > 0 && out[i].Name == out[i-1].Name {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateField, out[i].Name)
		}
	}
	return out, nil
}

// hexDigits is the lowercase alphabet for \u escapes. Case is part of the
// encoding:  and  are different bytes.
const hexDigits = "0123456789abcdef"

// appendString writes a quoted string under exactly one escape rule: the two
// characters JSON requires (`"` and `\`), and C0 controls as \u00XX with the
// five short forms JSON defines. Everything else — including `&`, `<`, `>`,
// DEL, and every non-ASCII rune — is written literally as UTF-8.
//
// The literal-by-default rule is the important one. Go's encoding/json escapes
// `<`, `>`, and `&` by default and U+2028/U+2029 always, all of which are
// JavaScript-embedding concerns that have no business in a durable digest, and
// all of which a second implementation would have to know to reproduce.
func appendString(dst []byte, s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidUTF8, s)
	}
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			dst = append(dst, c)
			continue
		}
		dst = appendEscape(dst, c)
	}
	return append(dst, '"'), nil
}

func appendEscape(dst []byte, c byte) []byte {
	switch c {
	case '"':
		return append(dst, '\\', '"')
	case '\\':
		return append(dst, '\\', '\\')
	case '\b':
		return append(dst, '\\', 'b')
	case '\f':
		return append(dst, '\\', 'f')
	case '\n':
		return append(dst, '\\', 'n')
	case '\r':
		return append(dst, '\\', 'r')
	case '\t':
		return append(dst, '\\', 't')
	}
	return append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
}
