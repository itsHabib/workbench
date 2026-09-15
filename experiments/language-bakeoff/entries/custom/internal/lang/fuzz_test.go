package lang

import (
	"errors"
	"testing"
)

// FuzzCompile checks that no input makes the front end panic, and that
// every rejection is a positioned diagnostic rather than a bare error.
func FuzzCompile(f *testing.F) {
	for _, seed := range []string{
		"keep file notes {\n  path \"notes.txt\"\n  text \"hi\\n\"\n}\n",
		"run command c { stdin notes.path; argv \"tr\" \"a-z\" \"A-Z\"; stdout \"o\" }",
		"run command a { stdin b.stdout; argv \"x\"; stdout \"a\" }\nrun command b { stdin a.stdout; argv \"x\"; stdout \"b\" }",
		"keep dir d { path \"d\" } # comment",
		"keep file a { path \"a",
		"}{..\"\\",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		in, err := Compile("fuzz.wb", []byte(src), schemas)
		if err == nil {
			if in == nil {
				t.Fatal("nil intent without an error")
			}
			return
		}
		var ds Diagnostics
		if !errors.As(err, &ds) || len(ds) == 0 {
			t.Fatalf("rejection is not a diagnostic: %v", err)
		}
		for _, d := range ds {
			if d.Pos.Line < 1 || d.Pos.Col < 1 {
				t.Fatalf("unpositioned diagnostic %+v", d)
			}
		}
	})
}
