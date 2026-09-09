package verbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func poolFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	old := fleet.OrgState
	fleet.OrgState = root
	t.Cleanup(func() { fleet.OrgState = old })
	t.Setenv("ORG_TENANT", "")
	repo := filepath.Join(root, "Mono")
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
	}
	run("init", repo)
	run("-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	a, b := filepath.Join(root, "seat-a"), filepath.Join(root, "seat-b")
	run("-C", repo, "worktree", "add", "--detach", a)
	run("-C", repo, "worktree", "add", "--detach", b)
	return repo, a, b
}

func poolMap(t *testing.T, lines ...string) {
	t.Helper()
	if err := os.WriteFile(fleet.RolesMap(), []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPoolTenantRejectsAmbiguousSiblings(t *testing.T) {
	repo, a, b := poolFixture(t)
	poolMap(t, a+" first worker:mono", b+" second worker:mono")
	tenant, err := poolTenant("", repo, "Mono")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("wanted ambiguous tenant refusal; tenant=%q err=%v", tenant, err)
	}
	if got, err := poolTenant("second", repo, "Mono"); err != nil || got != "second" {
		t.Fatalf("explicit tenant=%q err=%v", got, err)
	}
}

func TestPoolLabelStaysWithinTenant(t *testing.T) {
	repo, a, b := poolFixture(t)
	poolMap(t, a+" first worker:foreign", b+" second worker:mono")
	label, err := poolLabel(repo, "Mono", "second")
	if err != nil || label != "mono" {
		t.Fatalf("label=%q err=%v", label, err)
	}
}

func TestPoolLabelRejectsAmbiguousSiblings(t *testing.T) {
	repo, a, b := poolFixture(t)
	poolMap(t, a+" work worker:Mono", b+" work worker:mono")
	_, err := poolLabel(repo, "Mono", "work")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("wanted ambiguous label refusal: %v", err)
	}
}

func TestPoolInheritsUnanimousLabelWithoutRolingMain(t *testing.T) {
	repo, a, b := poolFixture(t)
	poolMap(t, a+" work worker:mono", b+" work checker:mono")
	tenant, err := poolTenant("", repo, "Mono")
	if err != nil || tenant != "work" {
		t.Fatalf("tenant=%q err=%v", tenant, err)
	}
	label, err := poolLabel(repo, "Mono", tenant)
	if err != nil || label != "mono" {
		t.Fatalf("label=%q err=%v", label, err)
	}
	if fleet.RoleOf(repo) != "" {
		t.Fatal("main gained a role")
	}
}

func TestPoolExactBindingWinsOverSiblingLabel(t *testing.T) {
	repo, a, _ := poolFixture(t)
	poolMap(t, repo+" work lead:preferred", a+" work worker:old")
	label, err := poolLabel(repo, "Mono", "work")
	if err != nil || label != "preferred" {
		t.Fatalf("label=%q err=%v", label, err)
	}
}

func TestWriteDeniesReportsAndPreservesExtras(t *testing.T) {
	existing := map[string]any{"permissions": map[string]any{"deny": []any{"Bash(old)", "Bash(shared)"}}, "unrelated": true}
	manifest := map[string]any{"denies": []any{"Bash(shared)", "Bash(new)"}}
	target := filepath.Join(t.TempDir(), "settings.json")
	deny, extra, err := writeDenies(existing, manifest, target)
	if err != nil || strings.Join(extra, ",") != "Bash(old)" || len(deny) != 3 {
		t.Fatalf("deny=%v extra=%v err=%v", deny, extra, err)
	}
	got := fleet.ReadJSON(target)
	if !fleet.B(got, "unrelated") || len(fleet.Strs(fleet.M(got, "permissions"), "deny")) != 3 {
		t.Fatalf("written settings=%v", got)
	}
}

func TestPoolAmbiguityRefusesBeforeCreatingSeats(t *testing.T) {
	repo, a, b := poolFixture(t)
	oldState := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = oldState })
	lanes, err := filepath.Abs("../../testdata/lanes")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_LANES", lanes)
	poolMap(t, a+" work worker:Mono", b+" work worker:mono")
	before, err := os.ReadFile(fleet.RolesMap())
	if err != nil {
		t.Fatal(err)
	}
	err = cmdPool(repo, "author", "1", false, "work")
	if err == nil || !strings.Contains(err.Error(), "ambiguous labels") {
		t.Fatalf("expected label ambiguity, got %v", err)
	}
	after, err := os.ReadFile(fleet.RolesMap())
	if err != nil || string(before) != string(after) {
		t.Fatalf("roles changed on refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(repo), "Mono-author-1")); !os.IsNotExist(err) {
		t.Fatalf("seat created on refusal: %v", err)
	}
}

func TestAssignReportsHoldingWorktree(t *testing.T) {
	repo, a, _ := poolFixture(t)
	branch := fleet.BranchOf(repo)
	err := assignCheckout("seat-a", a, branch)
	if err == nil || !strings.Contains(strings.ReplaceAll(err.Error(), "\\", "/"), strings.ReplaceAll(repo, "\\", "/")) {
		t.Fatalf("missing holding checkout %s: %v", repo, err)
	}
	if fleet.BranchOf(a) != "" {
		t.Fatal("refused assignment changed seat branch")
	}
}

func TestPoolAncestorDoesNotHideSiblingAmbiguity(t *testing.T) {
	repo, a, b := poolFixture(t)
	poolMap(t, filepath.Dir(repo)+" inherited lead:parent", a+" first worker:mono", b+" second worker:mono")
	_, err := poolTenant("", repo, "Mono")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ancestor hid tenant ambiguity: %v", err)
	}
}
