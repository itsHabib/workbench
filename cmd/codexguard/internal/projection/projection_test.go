package projection

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testAssets() []Asset {
	return []Asset{
		{Path: "hooks.json", Data: []byte("{\"hooks\":{}}\n")},
		{Path: "rules/workbench-codexguard.rules", Data: []byte("prefix_rule(pattern=[\"gate\", \"grant\"], decision=\"forbidden\")\n")},
	}
}

func TestInspectIsNonMutatingAndStable(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	first, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("digest changed: %s != %s", first.Digest, second.Digest)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status mutated home: %v", err)
	}
	for _, entry := range first.Entries {
		if entry.State != "missing" {
			t.Fatalf("%s state = %s, want missing", entry.Path, entry.State)
		}
	}
}

func TestApplyIsHashBoundAndIdempotent(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	plan, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(home, plan.Digest, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range applied.Entries {
		if entry.State != "current" {
			t.Fatalf("%s state = %s, want current", entry.Path, entry.State)
		}
	}
	reapplied, err := Apply(home, applied.Digest, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	if reapplied.Digest != applied.Digest {
		t.Fatalf("idempotent digest changed: %s != %s", reapplied.Digest, applied.Digest)
	}
}

func TestApplyRefusesStalePlan(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	plan, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte("user owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(home, plan.Digest, testAssets()); err == nil {
		t.Fatal("stale plan applied")
	}
	data, err := os.ReadFile(filepath.Join(home, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user owned\n" {
		t.Fatalf("user file changed: %q", data)
	}
}

func TestApplyRefusesDivergentTarget(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "rules", "workbench-codexguard.rules")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("personal rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(home, plan.Digest, testAssets()); !errors.Is(err, ErrDivergent) {
		t.Fatalf("error = %v, want ErrDivergent", err)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("apply partially created hooks.json: %v", err)
	}
}

func TestApplyRefusesCollisionAfterStageAndRollsBack(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	plan, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	_, err = apply(home, plan.Digest, testAssets(), applyHooks{
		beforeCommit: func() {
			target := filepath.Join(home, "rules", "workbench-codexguard.rules")
			if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o700); mkdirErr != nil {
				t.Fatal(mkdirErr)
			}
			if writeErr := os.WriteFile(target, []byte("racer\n"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if err == nil {
		t.Fatal("racing target applied")
	}
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("error = %v, want ErrPlanChanged", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "hooks.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rollback left hooks.json: %v", statErr)
	}
	data, readErr := os.ReadFile(filepath.Join(home, "rules", "workbench-codexguard.rules"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "racer\n" {
		t.Fatalf("racer file changed: %q", data)
	}
}

func TestApplyRefusesCurrentFileChangedDuringApply(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	plan, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(home, plan.Digest, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	_, err = apply(home, applied.Digest, testAssets(), applyHooks{
		beforeCommit: func() {
			if writeErr := os.WriteFile(filepath.Join(home, "hooks.json"), []byte("changed\n"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("error = %v, want ErrPlanChanged", err)
	}
}

func TestApplyRefusesParentSwap(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "codex")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	plan, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	swapped := false
	_, err = apply(home, plan.Digest, testAssets(), applyHooks{
		beforeCommit: func() {
			rules := filepath.Join(home, "rules")
			if renameErr := os.Rename(rules, rules+"-original"); renameErr != nil {
				return
			}
			if symlinkErr := os.Symlink(outside, rules); symlinkErr != nil {
				_ = os.Rename(rules+"-original", rules)
				return
			}
			swapped = true
		},
	})
	if !swapped {
		t.Skip("platform did not permit the parent-swap fixture")
	}
	if err == nil {
		t.Fatal("parent swap applied")
	}
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("error = %v, want ErrPlanChanged", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "workbench-codexguard.rules")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("projection escaped through swapped parent: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "hooks.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rollback left hooks.json: %v", statErr)
	}
}

func TestRollbackPreservesConcurrentReplacement(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	plan, err := Inspect(home, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte("user replacement\n")
	_, err = apply(home, plan.Digest, testAssets(), applyHooks{
		afterPublish: func(index int, published stagedFile) {
			if index != 0 {
				return
			}
			if removeErr := os.Remove(published.target); removeErr != nil {
				t.Fatal(removeErr)
			}
			if writeErr := os.WriteFile(published.target, replacement, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			collision := filepath.Join(home, "rules", "workbench-codexguard.rules")
			if writeErr := os.WriteFile(collision, []byte("collision\n"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if err == nil {
		t.Fatal("collision applied")
	}
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("error = %v, want ErrPlanChanged", err)
	}
	data, readErr := os.ReadFile(filepath.Join(home, "hooks.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(data, replacement) {
		t.Fatalf("rollback removed or changed replacement: %q", data)
	}
}

func TestInspectRejectsEscapingAndDuplicatePaths(t *testing.T) {
	cases := [][]Asset{
		{{Path: "../outside", Data: []byte("x")}},
		{{Path: "rules/a", Data: []byte("x")}, {Path: "rules/../rules/a", Data: []byte("y")}},
	}
	for _, assets := range cases {
		if _, err := Inspect(t.TempDir(), assets); err == nil {
			t.Fatalf("Inspect accepted %#v", assets)
		}
	}
}

func TestInspectRejectsSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "codex")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "rules")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Inspect(home, testAssets()); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("error = %v, want ErrUnsafePath", err)
	}
}

func TestInspectRejectsLinkedAncestorAboveHome(t *testing.T) {
	base := t.TempDir()
	realParent := filepath.Join(base, "real")
	linkedParent := filepath.Join(base, "linked")
	if err := os.MkdirAll(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	home := filepath.Join(linkedParent, "codex")
	if _, err := Inspect(home, testAssets()); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("error = %v, want ErrUnsafePath", err)
	}
}
