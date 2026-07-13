package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/contracts/execution"
)

func TestRunE2EGoldenBundle(t *testing.T) {
	dir := t.TempDir()
	repo := initGitRepo(t, dir)
	rev := gitHead(t, repo)

	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	prog := []byte(`package main
import (
	"fmt"
	"os"
)
func main() {
	fmt.Println("golden-ok")
	fmt.Println(os.Getenv("RUNWAY_WORKSPACE") != "")
	fmt.Println(os.Getenv("RUNWAY_INPUTS") != "")
	fmt.Println(os.Getenv("RUNWAY_OUT") != "")
}
`)
	if err := os.WriteFile(filepath.Join(bundleDir, "main.go"), prog, 0o600); err != nil {
		t.Fatal(err)
	}
	name := "go"
	litRun := "run"
	workSpec := execution.WorkSpec{
		SchemaVersion: execution.SchemaVersion,
		Command: execution.Command{
			Executable: execution.Executable{Name: &name},
			Args: []execution.Arg{
				{Literal: &litRun},
				{Path: &execution.PathRef{Root: execution.RootInputs, Value: "main.go"}},
			},
		},
		Cwd: execution.PathRef{Root: execution.RootWorkspace, Value: "."},
		Workspace: execution.Workspace{
			Kind:     "git",
			URL:      repo,
			Revision: rev,
		},
		Inputs: []execution.Input{{
			Name:   "prog",
			Source: "main.go",
			Target: "main.go",
			SHA256: sha256Hex(prog),
		}},
	}
	work, err := json.Marshal(workSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "work.json"), work, 0o600); err != nil {
		t.Fatal(err)
	}
	reqDoc := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_golden",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(work)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 120000, CancelGraceMS: 1000},
	}
	req, err := json.Marshal(reqDoc)
	if err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "request.json")
	if err := os.WriteFile(spec, req, 0o600); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(dir, "state")
	runID, exitCode, err := runOnce(spec, bundleDir, stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Fatalf("golden workload exit code = %d, want 0", exitCode)
	}
	runDir := filepath.Join(stateRoot, "runs", runID)
	for _, rel := range []string{
		"request.json", "work.json", "events.ndjson",
		"inputs", "logs", "artifacts", "private",
	} {
		if _, err := os.Stat(filepath.Join(runDir, rel)); err != nil {
			t.Fatalf("run dir missing %s: %v", rel, err)
		}
	}
	reqGot, err := os.ReadFile(filepath.Join(runDir, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reqGot, req) {
		t.Fatal("request.json must hold exact accepted bytes")
	}
	events, err := journal.ReadHistory(filepath.Join(runDir, "events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := execution.Reduce(events)
	if err != nil {
		t.Fatal(err)
	}
	if st.Terminal {
		t.Fatal("PR1 leaves the run open; Reduce must report Terminal=false")
	}
	// Exactly the PR 1 canonical sequence: run_accepted, placement_allocated,
	// workload_ready, workload_started, workload_exited.
	if st.LastSeq != 5 || st.Phase != execution.PhaseWorkload {
		t.Fatalf("unexpected open history: %+v events=%+v", st, events)
	}
	stdout, err := os.ReadFile(filepath.Join(runDir, "logs", "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stdout, []byte("golden-ok")) {
		t.Fatalf("workload output missing: %q", stdout)
	}
}

func TestRunRejectsNonDefaultProfile(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	work := []byte(`{
  "schema_version": "0.1.0",
  "command": {"executable": {"name": "true"}},
  "cwd": {"root": "workspace", "value": "."},
  "workspace": {"kind": "git", "url": "https://example.invalid/repo.git", "revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}`)
	if err := os.WriteFile(filepath.Join(bundleDir, "work.json"), work, 0o600); err != nil {
		t.Fatal(err)
	}
	reqDoc := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_profile",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(work)},
		Placement:     execution.Placement{Backend: "local", Profile: "custom"},
		Policy:        execution.Policy{DeadlineMS: 1000, CancelGraceMS: 0},
	}
	req, err := json.Marshal(reqDoc)
	if err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "request.json")
	if err := os.WriteFile(spec, req, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = runOnce(spec, bundleDir, filepath.Join(dir, "state"))
	if err == nil {
		t.Fatal("non-default profile must be rejected")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("placement.profile")) {
		t.Fatalf("want profile error, got %v", err)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("w"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "README")
	run("git", "commit", "-m", "init")
	return repo
}

func gitHead(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(out))
}
