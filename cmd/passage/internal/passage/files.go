package passage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxRecord = 16 << 20
const maxEvidence = 256 << 10

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes: %s", limit, path)
	}
	return b, nil
}

func decode(path string, into any) error {
	b, err := readLimited(path, maxRecord)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing data in %s", path)
	}
	return nil
}

func snapshot(root string, p Phase) (Snapshot, error) {
	s := Snapshot{Files: map[string]string{}}
	for _, input := range p.Inputs {
		path, err := localPath(root, input)
		if err != nil {
			return s, err
		}
		b, err := readLimited(path, maxRecord)
		if err != nil {
			return s, err
		}
		s.Files[input] = digest(b)
	}
	if !p.Git {
		return s, nil
	}
	status, err := git(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	if status != "" {
		return s, fmt.Errorf("git worktree is dirty; commit inputs and keep evidence/state outside it")
	}
	s.Head, err = git(root, "rev-parse", "--verify", "HEAD")
	return s, err
}

func localPath(root, input string) (string, error) {
	if !filepath.IsLocal(input) {
		return "", fmt.Errorf("input must be relative to root: %s", input)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, input))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("input escapes root: %s", input)
	}
	return path, nil
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	// Git identity must come from the work root, never the caller's index or gitdir.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git: %w: %s", err, b)
	}
	return strings.TrimSpace(string(b)), nil
}

func publish(path string, r Record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if len(b)+1 > maxRecord {
		return fmt.Errorf("record exceeds %d bytes", maxRecord)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".passage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func locked(path string, fn func() error) error {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("lock work record (another writer or interrupted write): %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	defer os.Remove(path + ".lock")
	return fn()
}
