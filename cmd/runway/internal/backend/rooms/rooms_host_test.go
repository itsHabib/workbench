//go:build rooms_host

package rooms

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/contracts/execution"
)

// TestRoomsHostAgentCursor is an opt-in adapter smoke, intentionally separate
// from the downstream Gate C equivalence/adversarial run. Invoke on rooms-host:
//
//	RUNWAY_ROOMS_HOST_TEST=1 RUNWAY_ROOMS_HOST_REPO=<url> \
//	RUNWAY_ROOMS_HOST_REVISION=<sha> go test -tags rooms_host \
//	./cmd/runway/internal/backend/rooms -run TestRoomsHostAgentCursor
func TestRoomsHostAgentCursor(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("RUNWAY_ROOMS_HOST_TEST") != "1" {
		t.Skip("requires an explicitly enabled Linux rooms-host")
	}
	repo := os.Getenv("RUNWAY_ROOMS_HOST_REPO")
	revision := os.Getenv("RUNWAY_ROOMS_HOST_REVISION")
	secret := os.Getenv("CURSOR_API_KEY")
	if repo == "" || revision == "" || secret == "" {
		t.Fatal("RUNWAY_ROOMS_HOST_REPO, RUNWAY_ROOMS_HOST_REVISION, and CURSOR_API_KEY are required")
	}
	dir := t.TempDir()
	inputs := filepath.Join(dir, "inputs")
	out := filepath.Join(dir, "out")
	private := filepath.Join(dir, "private")
	for _, path := range []string{inputs, out, private} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(inputs, "task.md"), []byte("Inspect the repository and make no changes. Return a one-sentence summary."), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "cursor-agent"
	work := execution.WorkSpec{
		SchemaVersion: execution.SchemaVersion,
		Command:       execution.Command{Executable: execution.Executable{Name: &name}},
		Workspace:     execution.Workspace{Kind: execution.WorkspaceKindGit, URL: repo, Revision: revision},
		Inputs:        []execution.Input{{Name: "task", Target: "task.md"}},
		Secrets:       []execution.Secret{{Name: "CURSOR_API_KEY", Ref: "env:CURSOR_API_KEY"}},
	}
	adapter, err := NewFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Admit(work); err != nil {
		t.Fatal(err)
	}
	prep := backend.PreparedRun{
		RunID:      "host-smoke",
		Work:       work,
		Env:        os.Environ(),
		Inputs:     inputs,
		Out:        out,
		StdoutPath: filepath.Join(dir, "stdout.log"),
		StderrPath: filepath.Join(dir, "stderr.log"),
		Secrets:    [][]byte{[]byte(secret)},
		PrivateDir: private,
	}
	emit := func(_, _ string, _ map[string]any) error { return nil }
	h, err := adapter.Start(context.Background(), prep, emit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Wait(context.Background(), h, emit); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Collect(context.Background(), h, out); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Cleanup(context.Background(), h); err != nil {
		t.Fatal(err)
	}
}
