package state_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/state"
)

func TestCreateRunDirectoryLayout(t *testing.T) {
	root := t.TempDir()
	run, err := state.Create(root, "run_layout")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"inputs", "logs", "artifacts", "workspace", "private",
	} {
		p := filepath.Join(run.Root, rel)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
		if !fi.IsDir() {
			t.Fatalf("%s is not a directory", rel)
		}
	}
	fi, err := os.Stat(run.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.WritePrivate("backend.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	pfi, err := os.Stat(filepath.Join(run.PrivateDir(), "backend.json"))
	if err != nil {
		t.Fatal(err)
	}
	// POSIX permission bits are meaningless on Windows (Go reports 0777 for
	// dirs; mode bits do not model ACLs). Windows restrictiveness is inherited
	// from the user-profile ACL of the default state root. Keep production
	// os.MkdirAll(0700)/0600 as-is; assert strictly on non-Windows only.
	if runtime.GOOS == "windows" {
		return
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("run dir must be restrictive, got %v", fi.Mode().Perm())
	}
	if pfi.Mode().Perm() != 0o600 {
		t.Fatalf("private file mode %v want 0600", pfi.Mode().Perm())
	}
}

func TestCreateRejectsExistingRunDir(t *testing.T) {
	root := t.TempDir()
	if _, err := state.Create(root, "run_dup"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Create(root, "run_dup"); err == nil {
		t.Fatal("reusing an existing run dir must fail")
	}
}

func TestDefaultRootUsesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(state.EnvState, dir)
	if got := state.DefaultRoot(); got != dir {
		t.Fatalf("DefaultRoot=%q want %q", got, dir)
	}
}
