// Package query implements typed column scans over projected JSONL metadata.
package query

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Field selects a dotted JSON object path, with an explicit type and query name.
type Field struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

// Column stores integers or dictionary codes, with -1/0 present/missing masks.
type Column struct {
	Field
	Values     []int64
	Valid      []int64
	Dictionary []string
	lookup     map[string]int64
}

// Source identifies the exact byte prefix consumed, including an incomplete tail.
type Source struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Table is an immutable snapshot after ingestion. Querying never touches sources.
type Table struct {
	Columns         []Column
	Sources         []Source
	Rows            int
	BuiltAt         string
	IgnoredTails    int
	dictionaryBytes int64
}

// ParseField accepts name=path:int or name=path:string.
func ParseField(s string) (Field, error) {
	name, rest, ok := strings.Cut(s, "=")
	if !ok {
		return Field{}, fmt.Errorf("column %q: want name=path:int|string", s)
	}
	path, kind, ok := strings.Cut(rest, ":")
	if !ok || path == "" || (kind != "int" && kind != "string") {
		return Field{}, fmt.Errorf("column %q: want name=path:int|string", s)
	}
	return Field{Name: name, Path: path, Type: kind}, nil
}

// New initializes a table. source and line are reserved provenance columns.
func New(fields []Field) (*Table, error) {
	if len(fields) == 0 || len(fields) > 30 {
		return nil, fmt.Errorf("select 1 to 30 columns")
	}
	t := &Table{}
	seen := map[string]bool{"source": true, "line": true}
	for _, f := range fields {
		if !identifier(f.Name) || seen[f.Name] || (f.Type != "int" && f.Type != "string") {
			return nil, fmt.Errorf("invalid or repeated column %q", f.Name)
		}
		seen[f.Name] = true
		t.Columns = append(t.Columns, Column{Field: f})
	}
	t.Columns = append(t.Columns, Column{Field: Field{Name: "source", Type: "string"}}, Column{Field: Field{Name: "line", Type: "int"}})
	return t, nil
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// Find returns a column or an actionable schema error.
func (t *Table) Find(name string) (*Column, error) {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i], nil
		}
	}
	return nil, fmt.Errorf("unknown column %q; run eventq schema INDEX", name)
}

func (c *Column) appendValue(v any) error {
	if v == nil {
		c.Values = append(c.Values, 0)
		c.Valid = append(c.Valid, 0)
		return nil
	}
	n, err := c.encode(v)
	if err != nil {
		return fmt.Errorf("column %s: %w", c.Name, err)
	}
	c.Values = append(c.Values, n)
	c.Valid = append(c.Valid, -1)
	return nil
}

func (c *Column) encode(v any) (int64, error) {
	if c.Type == "int" {
		switch n := v.(type) {
		case int64:
			return n, nil
		case json.Number:
			return strconv.ParseInt(string(n), 10, 64)
		default:
			return 0, fmt.Errorf("expected integer, got %T", v)
		}
	}
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("expected string, got %T", v)
	}
	if c.lookup == nil {
		c.lookup = make(map[string]int64)
	}
	if n, ok := c.lookup[s]; ok {
		return n, nil
	}
	n := int64(len(c.Dictionary))
	c.Dictionary = append(c.Dictionary, s)
	c.lookup[s] = n
	return n, nil
}

// Value returns a projected value or nil for missing/null fields.
func (c *Column) Value(row int) any {
	if c.Valid[row] == 0 {
		return nil
	}
	if c.Type == "string" {
		return c.Dictionary[c.Values[row]]
	}
	return c.Values[row]
}

func (t *Table) appendRow(values []any, source string, line int64) error {
	if int64(t.Rows+1)*int64(len(t.Columns))*16 > 256<<20 {
		return fmt.Errorf("column memory exceeds 256 MiB; partition the input")
	}
	values = append(values, source, line)
	for i, v := range values {
		before := len(t.Columns[i].Dictionary)
		if err := t.Columns[i].appendValue(v); err != nil {
			return err
		}
		if len(t.Columns[i].Dictionary) > before {
			t.dictionaryBytes += int64(len(t.Columns[i].Dictionary[before]))
		}
		if t.dictionaryBytes > 8<<20 {
			return fmt.Errorf("dictionary strings exceed 8 MiB; select fewer fields or partition the input")
		}
	}
	t.Rows++
	return nil
}
