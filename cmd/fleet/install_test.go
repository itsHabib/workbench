package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual --apply rewrite block in isolation: no build, live home,
// lane installation or migration is needed to verify the config transformation.
func TestInstallerRemovesOnlyFleetShadowHooks(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	raw, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	marker := "python3 - \"$f\" \"$bin\" <<'PY'\n"
	_, body, ok := strings.Cut(string(raw), marker)
	if !ok {
		t.Fatal("rewrite block missing")
	}
	body, _, ok = strings.Cut(body, "\nPY")
	if !ok {
		t.Fatal("rewrite block unterminated")
	}
	bin := "/tmp/fleet/bin/fleet"
	commands := []string{"python3 /tmp/fleet/hook.py", bin + " hook claude --shadow", bin + " hook codex --shadow", "echo --shadow", "/other/fleet hook claude --shadow", bin + " hook claude"}
	hooks := []map[string]string{}
	for _, c := range commands {
		hooks = append(hooks, map[string]string{"type": "command", "command": c})
	}
	config := map[string]any{"extra": "preserved", "hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "*", "hooks": hooks}}}}
	data, _ := json.Marshal(config)
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		cmd := exec.Command(python, "-", p, bin)
		cmd.Stdin = strings.NewReader(body)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := run()
	second := run()
	if first != second {
		t.Fatal("apply is not idempotent")
	}
	for _, want := range []string{"echo --shadow", "/other/fleet hook claude --shadow", "preserved", "matcher", bin + " hook claude"} {
		if !strings.Contains(first, want) {
			t.Fatalf("lost %q: %s", want, first)
		}
	}
	if strings.Contains(first, bin+" hook claude --shadow") || strings.Contains(first, bin+" hook codex --shadow") || strings.Contains(first, "hook.py") {
		t.Fatal(first)
	}
}

func TestInstallerRollbackIgnoresUnrelatedBackup(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "settings.json")
	for name, body := range map[string]string{p: "current", p + ".bak-20260908-120000": "expected", p + ".bak-env": "unrelated"} {
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := exec.Command(bash, "install.sh", "--rollback")
	c.Env = append(os.Environ(), "HOME="+root, "FLEET_HOME="+filepath.Join(root, "fleet"))
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("rollback: %v %s", err, out)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "expected" {
		t.Fatalf("restored=%q err=%v", got, err)
	}
}
