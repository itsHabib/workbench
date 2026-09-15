package lang

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

// fakeSchemas stands in for the adapter registry, so the language is tested
// without importing a single adapter.
type fakeSchemas map[string]struct {
	schema kind.Schema
	life   kind.Lifecycle
}

func (f fakeSchemas) Lookup(k string) (kind.Schema, kind.Lifecycle, bool) {
	v, ok := f[k]
	return v.schema, v.life, ok
}

func (f fakeSchemas) Kinds() []string {
	var names []string
	for k := range f {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

var schemas = fakeSchemas{
	"file": {kind.Schema{Kind: "file", Fields: []kind.Field{
		{Name: "path", Type: kind.OwnedPath, Required: true},
		{Name: "text", Type: kind.Text, Required: true},
	}, Exports: []string{"path"}}, kind.Keep},
	"command": {kind.Schema{Kind: "command", Fields: []kind.Field{
		{Name: "argv", Type: kind.List, Required: true},
		{Name: "stdin", Type: kind.Path},
		{Name: "cwd", Type: kind.Path},
		{Name: "stdout", Type: kind.OwnedPath, Required: true},
	}, Exports: []string{"stdout"}}, kind.Run},
	"dir": {kind.Schema{Kind: "dir", Fields: []kind.Field{
		{Name: "path", Type: kind.OwnedPath, Required: true},
	}, Exports: []string{"path"}}, kind.Keep},
}

func TestCompileResolvesReferencesAndOrdersByDependency(t *testing.T) {
	// Declared downstream-first on purpose: the intent is in dependency
	// order regardless of source order.
	src := `
run command tally {
  stdin  shout.stdout
  argv   "wc" "-w"
  stdout "tally.txt"
}

run command shout {
  stdin  notes.path   # a reference is a dependency edge
  argv   "tr" "a-z" "A-Z"
  stdout "upper.txt"
}

keep file notes {
  path "notes.txt"
  text "a \"quoted\"\tline\n"
}
`
	in, err := Compile("dir/pipeline.wb", []byte(src), schemas)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range in.Decls {
		ids = append(ids, d.ID)
	}
	equal(t, "order", ids, []string{"notes", "shout", "tally"})
	notes, shout, tally := in.Decls[0], in.Decls[1], in.Decls[2]
	equal(t, "source", in.Source, "pipeline.wb") // base name only: plans never carry private paths
	equal(t, "decoded escapes", notes.One("text"), "a \"quoted\"\tline\n")
	equal(t, "shout.stdin", shout.One("stdin"), "notes.txt")
	equal(t, "shout.refs", shout.Refs, map[string]string{"stdin": "notes.path"})
	equal(t, "tally.needs", tally.Needs, []string{"shout"})
	equal(t, "tally.stdin", tally.One("stdin"), "upper.txt")
	equal(t, "shout.owns", shout.Owns, []string{"upper.txt"})
	equal(t, "lifecycles", []string{notes.Lifecycle, shout.Lifecycle}, []string{"keep", "run"})
	equal(t, "digest is stable", shout.Digest(), shout.Digest())
	if shout.Digest() == tally.Digest() {
		t.Error("digests must distinguish definitions")
	}
}

func equal[T any](t *testing.T, what string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestCompileReportsExplicitErrors(t *testing.T) {
	notes := "keep file notes {\n  path \"notes.txt\"\n  text \"x\"\n}\n"
	tests := []struct {
		name, src, want string
	}{
		{"not a lifecycle", `make file a { path "a"; text "x" }`,
			`1:1: "make" is not a lifecycle: start with keep (a resource wb converges) or run (work wb replays when its inputs change)`},
		{"lifecycle does not match kind", `keep command c { argv "true"; stdout "o" }`,
			`1:1: command is one-shot work that wb replays when stale: write "run command c"`},
		{"unknown kind", `keep fil a { path "a" }`,
			`1:6: unknown kind "fil" (registered kinds: command, dir, file)`},
		{"unknown and missing attributes", "keep file a {\n  path \"a\"\n  txt \"x\"\n}",
			"1:11: file a is missing required attribute text\n3:3: file has no attribute \"txt\" (attributes: path, text)"},
		{"duplicate name", notes + `keep file notes { path "b"; text "y" }`,
			`5:11: notes is already declared on line 1`},
		{"duplicate attribute", `keep file a { path "a"; path "b"; text "x" }`,
			`1:25: path is already set on line 1`},
		{"one value expected", `keep file a { path "a" "b"; text "x" }`,
			`1:24: path takes one value, found 2`},
		{"reference in a list", notes + `run command c { argv notes.path; stdout "o" }`,
			`5:22: argv takes literal strings, not references`},
		{"reference as an owned path", notes + `run command c { argv "x"; stdout notes.path }`,
			`5:34: stdout names a path c owns, so it must be a literal path, not a reference`},
		{"escapes the workspace", `keep file a { path "../x"; text "" }`,
			`1:20: path "../x" must stay inside the workspace: relative, without ..`},
		{"absolute path", `keep file a { path "/etc/passwd"; text "" }`,
			`1:20: path "/etc/passwd" must stay inside the workspace: relative, without ..`},
		{"non-canonical path", `keep file a { path "./a"; text "" }`,
			`1:20: write path "./a" as "a"`},
		{"reserved path", `keep file a { path ".wb/journal.jsonl"; text "" }`,
			`1:20: path ".wb/journal.jsonl" is inside .wb, which wb reserves for its journal`},
		{"owning the root", `keep dir all { path "." }`,
			`1:21: path cannot own the whole workspace`},
		{"unknown reference", notes + `run command c { stdin nots.path; argv "x"; stdout "o" }`,
			`5:23: c refers to nots.path, but nothing is named nots`},
		{"unexported attribute", notes + `run command c { stdin notes.text; argv "x"; stdout "o" }`,
			`5:23: notes does not export text (exports: path)`},
		{"self reference", `run command c { stdin c.stdout; argv "x"; stdout "o" }`,
			`1:23: c refers to itself`},
		{"shared ownership", notes + `run command c { argv "x"; stdout "notes.txt" }`,
			`5:34: c claims "notes.txt", which overlaps "notes.txt" owned by notes (line 2)`},
		{"nested ownership", `keep dir d { path "d" }` + "\n" + `keep file f { path "d/f.txt"; text "" }`,
			`2:20: f claims "d/f.txt", which overlaps "d" owned by d (line 1)`},
		{"literal read of an owned path", notes + `run command c { stdin "notes.txt"; argv "x"; stdout "o" }`,
			`5:23: "notes.txt" belongs to notes; reference notes.path instead so wb settles notes first and tracks it as an input`},
		{"reads its own output", `run command c { stdin "o"; argv "x"; stdout "o" }`,
			`1:23: c uses "o", which it owns itself`},
		{"cycle", `run command a { stdin b.stdout; argv "x"; stdout "a" }` + "\n" + `run command b { stdin a.stdout; argv "x"; stdout "b" }`,
			`1:13: dependency cycle: a -> b -> a`},
		{"unterminated string", `keep file a { path "a`, `1:20: unterminated string`},
		{"unknown escape", `keep file a { path "a"; text "\q" }`, `1:31: unknown escape \q (use \n, \t, \" or \\)`},
		{"bare word", notes + `run command c { stdin notes; argv "x"; stdout "o" }`,
			`5:23: bare word "notes": quote a string ("notes") or reference an attribute (notes.path)`},
		{"missing closing brace", "keep file a {\n  path \"a\"\n", `1:11: a is missing its closing "}"`},
		{"attribute without a value", "keep file a {\n  path\n}", `2:3: attribute path needs a value`},
		{"stray character", `keep file a { path = "a" }`, `1:20: unexpected character '='`},
		{"two attributes on one line", `keep file a { path "a" text "x" }`,
			`1:24: bare word "text" after path's value: start each attribute on its own line or after ";"`},
		{"missing name", `keep file { }`, `1:11: expected a name after keep file, found "{"`},
		// Paths are compared without regard to case, and are ASCII, so no two
		// spellings can alias one file on a case-insensitive filesystem.
		{"reserved path in another case", `keep file a { path ".WB/journal.jsonl"; text "" }`,
			`1:20: path ".WB/journal.jsonl" is inside .wb, which wb reserves for its journal`},
		{"ownership differing only in case", `keep file a { path "Notes.txt"; text "" }` + "\n" + `keep file b { path "notes.txt"; text "" }`,
			`2:20: b claims "notes.txt", which overlaps "Notes.txt" owned by a (line 1)`},
		{"non-ASCII path", `keep file a { path "café.txt"; text "" }`,
			`1:20: path "café.txt" may use only printable ASCII, without backslashes`},
		{"temporary-file suffix", `keep file a { path "x.wb-partial"; text "" }`,
			`1:20: path "x.wb-partial" uses .wb-partial, which wb reserves for temporary files`},
		{"temporary-file suffix in a directory", `keep file a { path "x.WB-partial/y"; text "" }`,
			`1:20: path "x.WB-partial/y" uses .wb-partial, which wb reserves for temporary files`},
		{"invalid UTF-8", "keep file a {\n  text \"A\xffB\"\n}", `2:10: source is not valid UTF-8`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compile("t.wb", []byte(tt.src), schemas)
			var ds Diagnostics
			if !errors.As(err, &ds) {
				t.Fatalf("err = %v, want diagnostics", err)
			}
			if got := ds.Format(""); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestDiagnosticsNameTheFile(t *testing.T) {
	_, err := Compile("t.wb", []byte(`keep file a { path "/x"; text "" }`), schemas)
	var ds Diagnostics
	if !errors.As(err, &ds) || !strings.HasPrefix(ds.Format("examples/t.wb"), "examples/t.wb:1:20: ") {
		t.Fatalf("want file:line:col prefix, got %v", err)
	}
}
