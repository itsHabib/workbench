package provider

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The request is on disk, private and complete, before the command exists; argv names
// the file and never carries the prompt; an attempt's file is never reused or followed.
func TestCommandWritesTheRequestBeforeTheBridgeExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attempt.request.json")
	cmd, err := Command(path, map[string]any{"provider": "claude", "prompt": "secret task text"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Stdin != nil || cmd.Args[len(cmd.Args)-1] != path || slices.ContainsFunc(cmd.Args, func(a string) bool { return strings.Contains(a, "secret task text") }) {
		t.Fatalf("the bridge must get its request only through the file: stdin %v argv tail %q", cmd.Stdin, cmd.Args[len(cmd.Args)-1])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("request file mode %v", info.Mode().Perm())
	}
	var got map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &got); err != nil || got["prompt"] != "secret task text" {
		t.Fatalf("request file %s: %v", raw, err)
	}
	if _, err := Command(path, map[string]any{"provider": "claude", "prompt": "another"}); err == nil {
		t.Fatal("an existing request file was reused")
	}
	link := filepath.Join(t.TempDir(), "planted.request.json")
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := Command(link, map[string]any{"provider": "claude", "prompt": "x"}); err == nil {
		t.Fatal("a planted link was followed")
	}
}

func TestProviderProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable uses a Unix shebang")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is a provider prerequisite")
	}
	cmd := exec.Command("node", "--test", "runtime.test.mjs")
	if runtime.GOOS == "darwin" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(os.Environ(), "FLEET_TEST_OBSERVER="+exe)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provider protocol: %v\n%s", err, out)
	}
}
