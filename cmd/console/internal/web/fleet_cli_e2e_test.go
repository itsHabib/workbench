package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/console/internal/fleetcli"
)

// Exercise real Fleet -> Console -> TraceLens without a provider, live home or Gate grant.
func TestFleetCLIIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("builds CLI binaries")
	}
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	home := t.TempDir()
	state := filepath.Join(home, "fleet")
	org := filepath.Join(home, "org")
	repo := filepath.Join(home, "repo")
	for _, p := range []string{state, org, repo, filepath.Join(state, "sessions")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FLEET_STATE", state)
	t.Setenv("ORG_STATE", org)
	t.Setenv("ORG_TENANT", "test")
	t.Setenv("FLEET_GITHUB", "off")
	if err := os.WriteFile(filepath.Join(org, "roles.map"), []byte(filepath.ToSlash(repo)+" test author:demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(home, "trace.jsonl")
	var lines strings.Builder
	for i := 0; i < 6; i++ {
		lines.WriteString(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call` + string(rune('a'+i)) + `","name":"Bash","input":{"command":"same check"}}]}}` + "\n")
		lines.WriteString(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call` + string(rune('a'+i)) + `","content":"same observed result"}]}}` + "\n")
	}
	if err := os.WriteFile(trace, []byte(lines.String()), 0600); err != nil {
		t.Fatal(err)
	}
	session, _ := json.Marshal(map[string]any{"session": "fixture", "cwd": repo, "launch_dir": repo, "last_event_at": 1000000000, "last_event": "SessionEnd", "transcript_path": trace})
	if err := os.WriteFile(filepath.Join(state, "sessions", "fixture.json"), session, 0600); err != nil {
		t.Fatal(err)
	}
	build := func(name string) string {
		t.Helper()
		p := filepath.Join(home, name+"-test")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", p, "./cmd/"+name)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
		return p
	}
	c := fleetcli.New(build("fleet"), build("tracelens"), state, nil)
	s := New(clientReturning(nil), testHost, c)
	for _, view := range []string{"status", "inspect", "diagnostics"} {
		response := do(t, s, "GET", "/api/fleet/"+view+"?address=author:demo", testHost)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", view, response.Code, response.Body)
		}
		if view == "diagnostics" && !strings.Contains(response.Body.String(), "loop") {
			t.Fatal("repeated confirmed outcomes not diagnosed", response.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "watch", "heartbeat.json")); !os.IsNotExist(err) {
		t.Fatal("read API created watcher state", err)
	}
	// An unknown address must not be interpreted as a filesystem path.
	if _, err := c.Read(context.Background(), "trace", trace); err == nil {
		t.Fatal("filesystem address accepted")
	}
}
