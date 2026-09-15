package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Case 6: the third adapter (dir) joins the graph through a reference and
// participates in ordering, ownership, drift and early cutoff, with no
// engine changes (see TestEngineNeverImportsBuiltinAdapters).
func TestCase6ThirdAdapter(t *testing.T) {
	w := applied(t, "v4.wb.hcl")
	w.use("v5.wb.hcl")

	p := w.mustWB("plan").stdout
	contains(t, p,
		"= file.input  unchanged",
		"+ dir.build   create     build: directory, mode 0750",
		`> exec.shout  run        config stdout: "output.txt" -> "build/output.txt"`,
		"upstream dir.build changes",
		"note: output.txt is no longer declared; left in place, no longer owned",
		"Plan: 1 to create, 0 to update, 1 to run, 2 unchanged.")
	if strings.Index(p, "dir.build") > strings.Index(p, "exec.shout") {
		t.Errorf("dir.build must be planned before exec.shout:\n%s", p)
	}

	w.mustWB("apply")
	info, err := os.Stat(filepath.Join(w.dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "build is a directory", info.IsDir(), true)
	equal(t, "build mode", info.Mode().Perm(), os.FileMode(0o750))
	equal(t, "build/output.txt", w.read("build/output.txt"), v4Output)
	equal(t, "old output.txt left in place", w.read("output.txt"), v4Output)
	equal(t, "tr runs", w.runs(), 2)
	contains(t, w.mustWB("apply").stdout, "Nothing to apply.")

	// The adapter observes, it does not remember: a hand chmod is drift.
	if err := os.Chmod(filepath.Join(w.dir, "build"), 0o700); err != nil {
		t.Fatal(err)
	}
	contains(t, w.mustWB("plan").stdout, "~ dir.build   update     build: directory, mode 0700 -> directory, mode 0750")
	contains(t, w.mustWB("apply").stdout, "exec.shout  unchanged  fresh once upstream applied")
	equal(t, "tr runs", w.runs(), 2)
}

// Writing inside the directory without referencing its block does not
// compile: the generic ownership check catches it, naming the reference.
func TestCase6WriteInsideDirRequiresReference(t *testing.T) {
	w := newWorkspace(t)
	w.use("v5.wb.hcl")
	w.write("main.wb.hcl", strings.Replace(w.read("main.wb.hcl"), `"${dir.build.path}/output.txt"`, `"build/output.txt"`, 1))

	r := w.wb("plan")
	equal(t, "exit code", r.code, 2)
	contains(t, r.stderr, "Error: Missing reference",
		"exec.shout writes build/output.txt, which dir.build owns, but does not reference dir.build. Use dir.build.path")
}

// The dir adapter refuses to take over a path it did not create.
func TestCase6DirRefusesToReplaceAFile(t *testing.T) {
	w := newWorkspace(t)
	w.use("v5.wb.hcl")
	w.write("build", "not a directory\n")

	r := w.wb("plan")
	equal(t, "exit code", r.code, 1)
	contains(t, r.stderr, "dir.build: build exists and is not a directory; refusing to replace it")
	equal(t, "build untouched", w.read("build"), "not a directory\n")
}
