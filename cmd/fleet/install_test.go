package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Exercise the real installer, example assets, role projections and both hook faces
// with no prior harness configuration or private lanes in a disposable home.
func TestPublicInstall(t *testing.T) {
	// The installer needs a POSIX shell, not a POSIX OS: Git Bash on Windows runs it.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH; the installer needs a POSIX shell (Git Bash on Windows)")
	}
	home := t.TempDir()
	// Keep Go's build cache, never the user's Fleet/harness configuration.
	cache, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	modCache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GOCACHE=" + strings.TrimSpace(string(cache)), "GOMODCACHE=" + strings.TrimSpace(string(modCache)), "FLEET_WATCH=off", "GIT_CONFIG_NOSYSTEM=1"}
	exe := ""
	if runtime.GOOS == "windows" {
		// Go, git and the harness resolve the home and temp dirs from these on Windows.
		exe = ".exe"
		env = append(env, "USERPROFILE="+home)
		for _, key := range []string{"SYSTEMROOT", "COMSPEC", "PATHEXT", "TEMP", "TMP", "LOCALAPPDATA"} {
			if v := os.Getenv(key); v != "" {
				env = append(env, key+"="+v)
			}
		}
	}
	run := func(input, name string, args ...string) string {
		t.Helper()
		c := exec.Command(name, args...)
		c.Env = env
		c.Stdin = strings.NewReader(input)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	run("", "bash", "install.sh")
	if _, err := os.Stat(filepath.Join(home, ".fleet")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote installation")
	}
	run("", "bash", "install.sh", "--apply")
	run("", "bash", "install.sh", "--apply")
	bin := filepath.Join(home, ".fleet", "bin", "fleet"+exe)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("installer did not emit the platform binary name: %v", err)
	}
	for _, kind := range []string{"author", "verifier", "supervisor"} {
		checkout := filepath.Join(home, kind)
		run("", "git", "init", "-q", checkout)
		run("", bin, "role", checkout, kind+":demo", "--tenant", "demo")
		run("", bin, "role", checkout, kind+":demo", "--tenant", "demo")
		for _, p := range []string{"CLAUDE.local.md", ".codex/config.toml", ".codex/rules/fleet-role.rules"} {
			if _, err := os.Stat(filepath.Join(checkout, p)); err != nil {
				t.Fatal(err)
			}
		}
		assertLifecycle(t, filepath.Join(checkout, ".claude/settings.local.json"), "hook claude")
		assertAllowIsList(t, filepath.Join(checkout, ".claude/settings.local.json"))
		assertLifecycle(t, filepath.Join(home, ".codex/hooks.json"), "hook codex")
		for _, harness := range []string{"claude", "codex"} {
			event := map[string]any{"hook_event_name": "SessionStart", "session_id": kind + "-" + harness, "cwd": checkout}
			data, _ := json.Marshal(event)
			out := run(string(data), bin, "hook", harness)
			if !strings.Contains(out, kind+":demo") {
				t.Fatalf("missing role context: %s", out)
			}
		}
	}
	repo := filepath.Join(home, "repo")
	run("", "git", "init", "-q", "-b", "main", repo)
	run("", "git", "-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "--allow-empty", "-m", "fixture")
	run("", bin, "pool", repo, "author", "1", "--tenant", "demo")
	run("", bin, "pool", repo, "verifier", "1", "--tenant", "demo")
	run("", bin, "status", "--all")
	// Edited cards must survive an accidental reinstall.
	card := filepath.Join(home, ".fleet/lanes/author/card.md")
	if err := os.WriteFile(card, []byte("custom card"), 0600); err != nil {
		t.Fatal(err)
	}
	run("", "bash", "install.sh") // An edited card is advisory in a dry run.
	c := exec.Command("bash", "install.sh", "--apply")
	c.Env = env
	if out, err := c.CombinedOutput(); err == nil || !strings.Contains(string(out), "refusing to replace") {
		t.Fatalf("edited card not protected: %v %s", err, out)
	}
}

func assertLifecycle(t *testing.T, path, command string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"} {
		groups := config.Hooks[event]
		if len(groups) == 1 && (event == "PreToolUse" || event == "PostToolUse") {
			matcher := regexp.MustCompile(groups[0].Matcher)
			if groups[0].Matcher == "" || matcher.MatchString("Read") || !matcher.MatchString("Bash") || !matcher.MatchString("Write") {
				t.Errorf("%s: wrong %s matcher: %q", path, event, groups[0].Matcher)
			}
		}
		if len(groups) != 1 || len(groups[0].Hooks) != 1 || !strings.Contains(groups[0].Hooks[0].Command, command) {
			t.Errorf("%s: wrong %s registration: %v", path, event, groups)
		}
	}
}

// Claude Code types permissions.allow as an array and rejects the whole file on null.
func assertAllowIsList(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Permissions struct {
			Allow json.RawMessage `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if allow := strings.TrimSpace(string(config.Permissions.Allow)); allow != "" && !strings.HasPrefix(allow, "[") {
		t.Fatalf("%s: permissions.allow must be a list, got %s", path, allow)
	}
}
