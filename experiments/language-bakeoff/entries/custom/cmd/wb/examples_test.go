package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the checked-in examples from current output")

// golden compares output with a checked-in example, so the examples and the
// demo transcript in the repository are always real output.
func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s is stale: run go test ./cmd/wb -update and review the diff\ngot:\n%s", path, got)
	}
}

func TestExamplesAreCurrent(t *testing.T) {
	f := newFixture(t)
	src, err := os.ReadFile("../../examples/pipeline.wb")
	f.must(err)
	f.source(string(src))

	golden(t, "../../examples/pipeline.intent.json", f.wb("intent", "pipeline.wb").stdout)
	golden(t, "../../examples/pipeline.plan.txt", f.plan().stdout)
	f.plan("-out", "plan.json").want(t, 0)
	plan, err := os.ReadFile(f.root + "/plan.json")
	f.must(err)
	golden(t, "../../examples/pipeline.plan.json", string(plan))

	f.mustApply()
	f.source(strings.Replace(string(src), "brown fox", "red fox", 1))
	golden(t, "../../examples/change.plan.txt", f.plan().stdout)

	bad, err := os.ReadFile("../../examples/invalid.wb")
	f.must(err)
	f.must(os.WriteFile(f.root+"/invalid.wb", bad, 0o644))
	r := f.wb("check", "invalid.wb")
	r.want(t, 2)
	golden(t, "../../examples/invalid.errors.txt", r.stderr)
}

func TestDemoTranscriptIsCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the whole demo")
	}
	out, err := exec.Command("sh", "../../demo.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("demo failed: %v\n%s", err, out)
	}
	golden(t, "../../examples/demo.transcript.txt", string(out))
}
