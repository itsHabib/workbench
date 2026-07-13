package bundle_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/bundle"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

func TestBundleRejectsDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	work := minimalWork(t, "https://example.invalid/repo.git", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	workPath := filepath.Join(bundleDir, "work.json")
	if err := os.WriteFile(workPath, work, 0o600); err != nil {
		t.Fatal(err)
	}
	// Wrong digest in the request — admission must fail before a run starts.
	req := []byte(`{
  "schema_version": "0.1.0",
  "request_id": "req_mismatch",
  "work": {"manifest": "work.json", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
  "placement": {"backend": "local", "profile": "default"},
  "policy": {"deadline_ms": 1000, "cancel_grace_ms": 0}
}`)
	spec := filepath.Join(dir, "request.json")
	if err := os.WriteFile(spec, req, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Admit(spec, bundleDir); err == nil {
		t.Fatal("digest mismatch must fail admission")
	}
}

func TestBundleRejectsInputDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "in.txt"), []byte("real-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := []byte(`{
  "schema_version": "0.1.0",
  "command": {"executable": {"name": "true"}},
  "cwd": {"root": "workspace", "value": "."},
  "workspace": {"kind": "git", "url": "https://example.invalid/repo.git", "revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "inputs": [{"name": "in", "source": "in.txt", "target": "in.txt", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"}]
}`)
	if err := os.WriteFile(filepath.Join(bundleDir, "work.json"), work, 0o600); err != nil {
		t.Fatal(err)
	}
	req := placedRequest(t, work)
	spec := filepath.Join(dir, "request.json")
	if err := os.WriteFile(spec, req, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Admit(spec, bundleDir); err == nil {
		t.Fatal("input digest mismatch must fail admission before a workload starts")
	}
}

func TestBundleRejectsSourceEscape(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Place a secret file outside the bundle and try to reach it via ..
	outside := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex([]byte("nope"))
	work := []byte(`{
  "schema_version": "0.1.0",
  "command": {"executable": {"name": "true"}},
  "cwd": {"root": "workspace", "value": "."},
  "workspace": {"kind": "git", "url": "https://example.invalid/repo.git", "revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "inputs": [{"name": "x", "source": "../secret.txt", "target": "x.txt", "sha256": "` + sum + `"}]
}`)
	// Validators reject traversal before I/O — either layer failing is correct.
	workPath := filepath.Join(bundleDir, "work.json")
	if err := os.WriteFile(workPath, work, 0o600); err != nil {
		t.Fatal(err)
	}
	req := placedRequest(t, work)
	spec := filepath.Join(dir, "request.json")
	if err := os.WriteFile(spec, req, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Admit(spec, bundleDir); err == nil {
		t.Fatal("source escape must be rejected")
	}
}

func TestBundleMaterializeCopiesExactBytes(t *testing.T) {
	dir := t.TempDir()
	repo := initGitRepo(t, dir)
	rev := gitHead(t, repo)

	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello-input")
	if err := os.WriteFile(filepath.Join(bundleDir, "in.txt"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	name := "true"
	workSpec := execution.WorkSpec{
		SchemaVersion: execution.SchemaVersion,
		Command:       execution.Command{Executable: execution.Executable{Name: &name}},
		Cwd:           execution.PathRef{Root: execution.RootWorkspace, Value: "."},
		Workspace: execution.Workspace{
			Kind:     "git",
			URL:      repo,
			Revision: rev,
		},
		Inputs: []execution.Input{{
			Name:   "in",
			Source: "in.txt",
			Target: "in.txt",
			SHA256: sha256Hex(payload),
		}},
	}
	work, err := json.Marshal(workSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "work.json"), work, 0o600); err != nil {
		t.Fatal(err)
	}
	req := placedRequest(t, work)
	spec := filepath.Join(dir, "request.json")
	if err := os.WriteFile(spec, req, 0o600); err != nil {
		t.Fatal(err)
	}
	adm, err := bundle.Admit(spec, bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	run, err := state.Create(filepath.Join(dir, "state"), "run_mat")
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Materialize(adm, run); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(run.InputsDir(), "in.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("input bytes changed: %q", got)
	}
	workGot, err := os.ReadFile(run.WorkPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(workGot) != string(work) {
		t.Fatal("work.json bytes must be exact")
	}
}

func minimalWork(t *testing.T, url, rev string) []byte {
	t.Helper()
	name := "true"
	work, err := json.Marshal(execution.WorkSpec{
		SchemaVersion: execution.SchemaVersion,
		Command:       execution.Command{Executable: execution.Executable{Name: &name}},
		Cwd:           execution.PathRef{Root: execution.RootWorkspace, Value: "."},
		Workspace:     execution.Workspace{Kind: "git", URL: url, Revision: rev},
	})
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func placedRequest(t *testing.T, work []byte) []byte {
	t.Helper()
	req, err := json.Marshal(execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_ok",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(work)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 60000, CancelGraceMS: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	return req
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
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
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
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out[:len(out)-1]) // trim newline
}
