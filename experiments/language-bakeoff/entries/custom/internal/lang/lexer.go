package lang

import (
	"fmt"
	"unicode/utf8"
)

type tokenKind int

const (
	tEOF tokenKind = iota
	tNewline
	tIdent
	tString
	tDot
	tLBrace
	tRBrace
)

func (k tokenKind) String() string {
	return [...]string{"end of file", "end of line", "word", "string", `"."`, `"{"`, `"}"`}[k]
}

// Pos is a 1-based line and column (in bytes).
type Pos struct{ Line, Col int }

type token struct {
	kind tokenKind
	text string // identifier, or a string's decoded value
	pos  Pos
}

type lexer struct {
	src  []byte
	off  int
	line int
	col  int
}

func newLexer(src []byte) *lexer { return &lexer{src: src, line: 1, col: 1} }

func (l *lexer) peek() byte {
	if l.off >= len(l.src) {
		return 0
	}
	return l.src[l.off]
}

func (l *lexer) advance() {
	if l.src[l.off] == '\n' {
		l.line++
		l.col = 0
	}
	l.off++
	l.col++
}

func (l *lexer) pos() Pos { return Pos{l.line, l.col} }

// next returns the next token. Spaces, tabs and comments are skipped;
// newlines are tokens because they end attributes.
func (l *lexer) next() (token, error) {
	l.skipBlanks()
	start := l.pos()
	if l.off >= len(l.src) {
		return token{kind: tEOF, pos: start}, nil
	}
	c := l.peek()
	if k, ok := punct[c]; ok {
		l.advance()
		return token{kind: k, pos: start}, nil
	}
	if c == '"' {
		return l.str(start)
	}
	if isIdentStart(c) {
		return l.ident(start), nil
	}
	return token{}, errAt(start, "unexpected character %q", c)
}

// A semicolon ends an attribute just like a newline does.
var punct = map[byte]tokenKind{'\n': tNewline, ';': tNewline, '.': tDot, '{': tLBrace, '}': tRBrace}

func (l *lexer) skipBlanks() {
	for l.off < len(l.src) {
		c := l.peek()
		if c == '#' {
			l.skipComment()
			continue
		}
		if c != ' ' && c != '\t' && c != '\r' {
			return
		}
		l.advance()
	}
}

func (l *lexer) skipComment() {
	for l.off < len(l.src) && l.peek() != '\n' {
		l.advance()
	}
}

func (l *lexer) ident(start Pos) token {
	from := l.off
	for l.off < len(l.src) && isIdentPart(l.peek()) {
		l.advance()
	}
	return token{kind: tIdent, text: string(l.src[from:l.off]), pos: start}
}

// str lexes a double-quoted string with the escapes \n \t \" and \\.
// Strings stay on one line.
func (l *lexer) str(start Pos) (token, error) {
	l.advance() // opening quote
	var b []byte
	for {
		c := l.peek()
		if l.off >= len(l.src) || c == '\n' {
			return token{}, errAt(start, "unterminated string")
		}
		l.advance()
		if c == '"' {
			return token{kind: tString, text: string(b), pos: start}, nil
		}
		if c != '\\' {
			b = append(b, c)
			continue
		}
		e, err := l.escape()
		if err != nil {
			return token{}, err
		}
		b = append(b, e)
	}
}

func (l *lexer) escape() (byte, error) {
	at := Pos{l.line, l.col - 1}
	if l.off >= len(l.src) {
		return 0, errAt(at, "unterminated string")
	}
	c := l.peek()
	l.advance()
	switch c {
	case 'n':
		return '\n', nil
	case 't':
		return '\t', nil
	case '"', '\\':
		return c, nil
	}
	return 0, errAt(at, `unknown escape \%c (use \n, \t, \" or \\)`, c)
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c == '-' || (c >= '0' && c <= '9')
}

func errAt(p Pos, format string, args ...any) Diagnostics {
	return Diagnostics{{Pos: p, Msg: fmt.Sprintf(format, args...)}}
}

// invalidUTF8 finds the first byte that is not UTF-8. Rejecting it keeps
// every string round-trippable through JSON, so digests and saved plans
// see exactly what the source says.
func invalidUTF8(src []byte) (Pos, bool) {
	at := Pos{Line: 1, Col: 1}
	for i := 0; i < len(src); {
		r, n := utf8.DecodeRune(src[i:])
		if r == utf8.RuneError && n == 1 {
			return at, true
		}
		at.Col += n
		if r == '\n' {
			at = Pos{Line: at.Line + 1, Col: 1}
		}
		i += n
	}
	return Pos{}, false
}
