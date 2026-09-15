// Package lang is the wb language front end: a lexer, a parser and a
// checker that turns source into a normalized intent.Intent.
//
// The grammar is deliberately small:
//
//	file  = { decl }
//	decl  = lifecycle kind name "{" { attr } "}"
//	attr  = name value { value } (newline | ";" | "}")
//	value = string | name "." name
//
// A lifecycle is keep (a resource wb converges) or run (work wb replays when
// its inputs change). Kinds come from adapters, not from the grammar.
package lang

import (
	"fmt"
	"sort"
	"strings"
)

// File is a parsed source file.
type File struct {
	Decls []*Block
}

// Block is one declaration.
type Block struct {
	Lifecycle Word
	Kind      Word
	Name      Word
	Attrs     []*Attr
}

// Word is an identifier with its position.
type Word struct {
	Text string
	Pos  Pos
}

// Attr is one attribute line.
type Attr struct {
	Name   Word
	Values []Value
}

// Value is a string literal or a reference to another declaration.
type Value struct {
	Pos Pos
	Lit string
	Ref *Ref
}

// Ref is a named reference: decl.attr.
type Ref struct {
	Decl, Attr string
}

func (r Ref) String() string { return r.Decl + "." + r.Attr }

// Diagnostic is one error at a source position.
type Diagnostic struct {
	Pos Pos
	Msg string
}

// Diagnostics is every error found in a source file, in position order.
type Diagnostics []Diagnostic

func (ds Diagnostics) Error() string { return ds.Format("") }

// Format renders diagnostics as "file:line:col: message" lines.
func (ds Diagnostics) Format(file string) string {
	var b strings.Builder
	for i, d := range ds {
		if i > 0 {
			b.WriteByte('\n')
		}
		if file != "" {
			b.WriteString(file + ":")
		}
		fmt.Fprintf(&b, "%d:%d: %s", d.Pos.Line, d.Pos.Col, d.Msg)
	}
	return b.String()
}

func (ds Diagnostics) sorted() Diagnostics {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].Pos.Line != ds[j].Pos.Line {
			return ds[i].Pos.Line < ds[j].Pos.Line
		}
		return ds[i].Pos.Col < ds[j].Pos.Col
	})
	return ds
}

type parser struct {
	lx  *lexer
	tok token
}

// Parse parses source. Syntax errors stop the parse at the first one, since
// later errors would mostly be echoes of it.
func Parse(src []byte) (*File, error) {
	if at, bad := invalidUTF8(src); bad {
		return nil, errAt(at, "source is not valid UTF-8")
	}
	p := &parser{lx: newLexer(src)}
	if err := p.advance(); err != nil {
		return nil, err
	}
	f := &File{}
	for {
		if err := p.skipNewlines(); err != nil {
			return nil, err
		}
		if p.tok.kind == tEOF {
			return f, nil
		}
		b, err := p.block()
		if err != nil {
			return nil, err
		}
		f.Decls = append(f.Decls, b)
	}
}

func (p *parser) advance() error {
	t, err := p.lx.next()
	if err != nil {
		return err
	}
	p.tok = t
	return nil
}

func (p *parser) skipNewlines() error {
	for p.tok.kind == tNewline {
		if err := p.advance(); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) word(what string) (Word, error) {
	if p.tok.kind != tIdent {
		return Word{}, errAt(p.tok.pos, "expected %s, found %s", what, p.describe())
	}
	w := Word{Text: p.tok.text, Pos: p.tok.pos}
	return w, p.advance()
}

func (p *parser) describe() string {
	if p.tok.kind == tIdent {
		return fmt.Sprintf("%q", p.tok.text)
	}
	return p.tok.kind.String()
}

func (p *parser) block() (*Block, error) {
	b := &Block{}
	var err error
	if b.Lifecycle, err = p.word("a declaration starting with keep or run"); err != nil {
		return nil, err
	}
	if b.Kind, err = p.word("a kind after " + b.Lifecycle.Text); err != nil {
		return nil, err
	}
	if b.Name, err = p.word("a name after " + b.Lifecycle.Text + " " + b.Kind.Text); err != nil {
		return nil, err
	}
	if p.tok.kind != tLBrace {
		return nil, errAt(p.tok.pos, "expected \"{\" to open %s, found %s", b.Name.Text, p.describe())
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	return b, p.body(b)
}

func (p *parser) body(b *Block) error {
	for {
		if err := p.skipNewlines(); err != nil {
			return err
		}
		if p.tok.kind == tRBrace {
			return p.advance()
		}
		if p.tok.kind == tEOF {
			return errAt(b.Name.Pos, "%s is missing its closing \"}\"", b.Name.Text)
		}
		a, err := p.attr()
		if err != nil {
			return err
		}
		b.Attrs = append(b.Attrs, a)
	}
}

// attr parses one attribute: a name, then values up to the end of the line
// or the block's closing brace.
func (p *parser) attr() (*Attr, error) {
	name, err := p.word("an attribute name")
	if err != nil {
		return nil, err
	}
	a := &Attr{Name: name}
	for p.tok.kind != tNewline && p.tok.kind != tRBrace && p.tok.kind != tEOF {
		v, err := p.value(name.Text, len(a.Values))
		if err != nil {
			return nil, err
		}
		a.Values = append(a.Values, v)
	}
	if len(a.Values) == 0 {
		return nil, errAt(name.Pos, "attribute %s needs a value", name.Text)
	}
	return a, nil
}

func (p *parser) value(attr string, prior int) (Value, error) {
	at := p.tok.pos
	if p.tok.kind == tString {
		v := Value{Pos: at, Lit: p.tok.text}
		return v, p.advance()
	}
	if p.tok.kind != tIdent {
		return Value{}, errAt(at, "expected a value for %s, found %s", attr, p.describe())
	}
	decl := p.tok.text
	if err := p.advance(); err != nil {
		return Value{}, err
	}
	if p.tok.kind != tDot && prior > 0 {
		return Value{}, errAt(at, "bare word %q after %s's value: start each attribute on its own line or after \";\"", decl, attr)
	}
	if p.tok.kind != tDot {
		return Value{}, errAt(at, "bare word %q: quote a string (\"%s\") or reference an attribute (%s.path)", decl, decl, decl)
	}
	if err := p.advance(); err != nil {
		return Value{}, err
	}
	field, err := p.word("an attribute name after " + decl + ".")
	if err != nil {
		return Value{}, err
	}
	return Value{Pos: at, Ref: &Ref{Decl: decl, Attr: field.Text}}, nil
}
