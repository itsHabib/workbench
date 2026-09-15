package intent_test

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapters"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/intent"
)

func compile(t *testing.T, src string) (*intent.Intent, hcl.Diagnostics) {
	t.Helper()
	reg, err := adapter.NewRegistry(adapters.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	in, _, diags := intent.Compile([]intent.Source{{Name: "main.wb.hcl", Bytes: []byte(src)}}, reg)
	return in, diags
}

const input = `resource "file" "input" {
  path    = "input.txt"
  content = "hi\n"
}
`

// Every unsupported construct fails with a specific message at the line
// that caused it.
func TestUnsupportedConstructsFailUsefully(t *testing.T) {
	cases := []struct {
		name, src, summary, detail string
		line                       int
	}{
		{"terraform variable block", `variable "x" {}`, "Unsupported block type", "there are no input variables", 1},
		{"module block", `module "m" {}`, "Unsupported block type", "modules are out of scope", 1},
		{"top-level argument", `x = 1`, "Unsupported top-level argument", `"x" cannot be set at the top level`, 1},
		{"count meta-argument", "resource \"file\" \"a\" {\n  path = \"a\"\n  count = 2\n  content = \"\"\n}", "Unsupported meta-argument", "repetition is out of scope", 3},
		{"depends_on", "resource \"file\" \"a\" {\n  path = \"a\"\n  content = \"\"\n  depends_on = []\n}", "Unsupported meta-argument", "dependencies come from references", 4},
		{"nested lifecycle block", "resource \"file\" \"a\" {\n  path = \"a\"\n  content = \"\"\n  lifecycle {}\n}", "Unsupported meta-argument", "never deletes or replaces", 4},
		{"function call", "resource \"file\" \"a\" {\n  path = \"a\"\n  content = upper(\"x\")\n}", "Function calls are not supported", "upper() could read the clock", 3},
		{"unknown argument", "resource \"file\" \"a\" {\n  path = \"a\"\n  content = \"\"\n  mode = \"0644\"\n}", "Unsupported argument", "Valid arguments: path, content", 4},
		{"missing argument", "resource \"file\" \"a\" {\n  path = \"a\"\n}", "Missing required argument", `file.a needs "content"`, 1},
		{"unknown type", `resource "s3_bucket" "b" {}`, "Unknown resource type", `No adapter is registered for "s3_bucket"`, 1},
		{"wrong kind", "task \"file\" \"a\" {\n  path = \"a\"\n  content = \"\"\n}", "Wrong block kind", `declare it as resource "file" "a"`, 1},
		{"duplicate", input + input, "Duplicate block", "file.input is already declared at main.wb.hcl:1", 5},
		{"variable reference", input + "task \"exec\" \"t\" {\n  command = [\"cat\"]\n  stdin = var.path\n  stdout = \"o\"\n}", "Unsupported reference", "there are no variables", 7},
		{"undeclared reference", input + "task \"exec\" \"t\" {\n  command = [\"cat\"]\n  stdin = file.missing.path\n  stdout = \"o\"\n}", "Reference to undeclared block", "file.missing is not declared", 7},
		{"unknown attribute", input + "task \"exec\" \"t\" {\n  command = [\"cat\"]\n  stdin = file.input.nope\n  stdout = \"o\"\n}", "Unsupported attribute", `"nope"`, 7},
		{"wrong value type", "task \"exec\" \"t\" {\n  command = \"cat\"\n  stdout = \"o\"\n}", "Incorrect attribute value type", "command must be list(string)", 2},
		{"path escape", "resource \"file\" \"a\" {\n  path = \"../outside\"\n  content = \"\"\n}", "Invalid block configuration", "escapes the workspace", 2},
		{"state directory", "resource \"file\" \"a\" {\n  path = \".wb/journal.jsonl\"\n  content = \"\"\n}", "Invalid block configuration", "inside the engine's .wb directory", 2},
		{"owned twice", input + "resource \"file\" \"again\" {\n  path = \"input.txt\"\n  content = \"\"\n}", "Path owned twice", "file.input and file.again both own input.txt", 5},
		{"source file", "resource \"file\" \"a\" {\n  path = \"main.wb.hcl\"\n  content = \"\"\n}", "Path is a source file", "wb source", 1},
		{"missing reference", input + "task \"exec\" \"t\" {\n  command = [\"cat\"]\n  stdin = \"input.txt\"\n  stdout = \"o\"\n}", "Missing reference",
			"exec.t reads input.txt, which file.input owns, but does not reference file.input. Use file.input.path", 5},
		{"state directory, other case (F2)", "resource \"file\" \"a\" {\n  path = \".WB/journal.jsonl\"\n  content = \"\"\n}", "Invalid block configuration", "inside the engine's .wb directory", 2},
		{"source file, other case (F2)", "resource \"file\" \"a\" {\n  path = \"Main.wb.hcl\"\n  content = \"\"\n}", "Path is a source file", "wb source", 1},
		{"owned twice, other case (F2)", "resource \"file\" \"a\" {\n  path = \"notes.txt\"\n  content = \"\"\n}\nresource \"file\" \"b\" {\n  path = \"NOTES.txt\"\n  content = \"\"\n}", "Path owned twice", "file.a and file.b both own NOTES.txt", 5},
		{"would become source (F3)", "resource \"file\" \"a\" {\n  path = \"extra.wb.hcl\"\n  content = \"\"\n}", "Path is a source file", "would become wb source", 1},
		{"reads its own output (F4)", "task \"exec\" \"t\" {\n  command = [\"cat\"]\n  stdin = \"data.txt\"\n  stdout = \"data.txt\"\n}", "Block reads its own output", "exec.t reads data.txt, which it also writes", 1},
		{"self reference", "task \"exec\" \"t\" {\n  command = [\"cat\"]\n  stdout = \"o\"\n  stdin = exec.t.stdout\n}", "Self reference", "exec.t cannot reference its own attributes", 4},
		{"cycle", "task \"exec\" \"a\" {\n  command = [\"cat\"]\n  stdin = exec.b.stdout\n  stdout = \"a\"\n}\ntask \"exec\" \"b\" {\n  command = [\"cat\"]\n  stdin = exec.a.stdout\n  stdout = \"b\"\n}",
			"Dependency cycle", "exec.a -> exec.b -> exec.a", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, diags := compile(t, tc.src)
			d := find(diags, tc.summary)
			if d == nil {
				t.Fatalf("no %q diagnostic; got: %v", tc.summary, diags)
			}
			if !strings.Contains(d.Detail, tc.detail) {
				t.Errorf("detail %q lacks %q", d.Detail, tc.detail)
			}
			if d.Subject == nil || d.Subject.Start.Line != tc.line {
				t.Errorf("subject %v, want line %d", d.Subject, tc.line)
			}
		})
	}
}

func find(diags hcl.Diagnostics, summary string) *hcl.Diagnostic {
	for _, d := range diags {
		if d.Summary == summary {
			return d
		}
	}
	return nil
}

// References become dependency edges and interpolated values; HCL's pure
// expressions (templates, conditionals) evaluate; order follows references.
func TestReferencesOrderAndEvaluate(t *testing.T) {
	in, diags := compile(t, `
task "exec" "second" {
  command = ["cat"]
  stdin   = exec.first.stdout
  stdout  = "second-${exec.first.stdout}"
}

task "exec" "first" {
  command = ["tr", "a-z", "A-Z"]
  stdin   = file.input.path
  stdout  = true ? "first.txt" : "never.txt"
}
`+input)
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	var order []string
	for _, n := range in.Nodes {
		order = append(order, n.Address)
	}
	if strings.Join(order, " ") != "file.input exec.first exec.second" {
		t.Fatalf("order %v", order)
	}
	second := in.Node("exec.second")
	if got := second.Attrs.Str("stdout"); got != "second-first.txt" {
		t.Errorf("interpolated stdout = %q", got)
	}
	if strings.Join(second.Deps, ",") != "exec.first" || strings.Join(second.Reads, ",") != "first.txt" {
		t.Errorf("deps %v reads %v", second.Deps, second.Reads)
	}
	if got := in.Node("file.input").Attrs.Str("sha256"); got != adapter.DigestBytes([]byte("hi\n")) {
		t.Errorf("derived sha256 = %q", got)
	}
}

// null on an optional field means unset, so `cond ? path : null` works; on
// a required field it is an error (F11).
func TestNullOnOptionalFieldIsUnset(t *testing.T) {
	in, diags := compile(t, "task \"exec\" \"t\" {\n  command = [\"true\"]\n  stdin   = false ? \"x\" : null\n  stdout  = \"o\"\n}\n")
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	if _, set := in.Node("exec.t").Attrs["stdin"]; set {
		t.Errorf("stdin should be unset: %v", in.Node("exec.t").Attrs)
	}
	_, diags = compile(t, "task \"exec\" \"t\" {\n  command = [\"true\"]\n  stdout  = null\n}\n")
	if d := find(diags, "Invalid attribute value"); d == nil || !strings.Contains(d.Detail, "stdout must be a non-null string") {
		t.Errorf("required null: %v", diags)
	}
}

// The source digest changes with any byte of source, so a saved plan can
// detect edits.
func TestSourceDigestTracksEdits(t *testing.T) {
	a, _ := compile(t, input)
	b, _ := compile(t, strings.Replace(input, "hi", "ho", 1))
	if a.SourceDigest == b.SourceDigest {
		t.Fatal("digest did not change")
	}
}
