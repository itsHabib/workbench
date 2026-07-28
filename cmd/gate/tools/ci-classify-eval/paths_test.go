package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testSourceDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

func writeGoMod(t *testing.T, dir, moduleLine string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(moduleLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWorkbenchModule_acceptsRepoRoot(t *testing.T) {
	root, err := findModuleRoot(testSourceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkbenchModule(root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWorkbenchModule_rejectsForeignModule(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/not-workbench")
	err := validateWorkbenchModule(dir)
	if err == nil {
		t.Fatal("expected error for foreign module")
	}
	if !strings.Contains(err.Error(), "ci-classify-eval:") {
		t.Fatalf("error %q missing ci-classify-eval prefix", err)
	}
	if !strings.Contains(err.Error(), "not the workbench module") {
		t.Fatalf("error %q should explain foreign module rejection", err)
	}
}

func TestFindModuleRoot_fromNestedSource(t *testing.T) {
	root := t.TempDir()
	writeGoMod(t, root, "module "+workbenchModule)
	nested := filepath.Join(root, "cmd", "gate", "tools", "ci-classify-eval")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findModuleRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("findModuleRoot(%q) = %q, want %q", nested, got, root)
	}
}

func TestResolvePaths_defaultEvalDirUnderRepoRoot(t *testing.T) {
	source := testSourceDir(t)
	paths, err := resolvePaths(pathOptions{sourceDir: source})
	if err != nil {
		t.Fatal(err)
	}

	wantEval := filepath.Join(paths.repoRoot, defaultEvalRel)
	if paths.evalDir != wantEval {
		t.Fatalf("evalDir = %q, want %q", paths.evalDir, wantEval)
	}
	if _, err := os.Stat(filepath.Join(paths.evalDir, "ci-lines-v2.jsonl")); err != nil {
		t.Fatalf("default eval bundle missing fixtures: %v", err)
	}
}

func TestResolvePaths_explicitEvalDirPassedThrough(t *testing.T) {
	source := testSourceDir(t)
	root, err := findModuleRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("cmd", "gate", "docs", "features", "ci-classify", "eval")
	paths, err := resolvePaths(pathOptions{
		sourceDir:       source,
		evalDirFlag:     rel,
		evalDirExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if paths.evalDir != rel {
		t.Fatalf("evalDir = %q, want explicit relative %q", paths.evalDir, rel)
	}
	if paths.repoRoot != root {
		t.Fatalf("repoRoot = %q, want %q", paths.repoRoot, root)
	}
}

func TestResolvePaths_repoRootOverride(t *testing.T) {
	source := testSourceDir(t)
	root, err := findModuleRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := resolvePaths(pathOptions{
		sourceDir:    source,
		repoRootFlag: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if paths.repoRoot != root {
		t.Fatalf("repoRoot = %q, want %q", paths.repoRoot, root)
	}
}

func TestResolvePaths_fromNonRootWorkingDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}

	paths, err := resolvePaths(pathOptions{sourceDir: testSourceDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.evalDir, "ci-classify.prompt.txt")); err != nil {
		t.Fatalf("default eval bundle not resolved from non-root cwd: %v", err)
	}
}

func TestResolvePaths_invalidRepoRootOverride(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/other")
	_, err := resolvePaths(pathOptions{
		sourceDir:    testSourceDir(t),
		repoRootFlag: dir,
	})
	if err == nil {
		t.Fatal("expected error for invalid repo-root override")
	}
	if !strings.Contains(err.Error(), "ci-classify-eval:") {
		t.Fatalf("error %q missing ci-classify-eval prefix", err)
	}
}
