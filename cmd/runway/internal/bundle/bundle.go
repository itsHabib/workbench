// Package bundle admits a placed request against a work bundle and
// materializes verified bytes into a run directory. Digests are checked
// before admission completes; path escapes are rejected at the I/O layer
// even though validators already reject traversal (defense in depth).
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

// Admitted is a schema-valid, digest-verified request+work pair ready to
// persist. Secret values are never resolved here.
type Admitted struct {
	Request      execution.Request
	RequestBytes []byte
	Work         execution.WorkSpec
	WorkBytes    []byte
	BundleDir    string
}

// Admit decodes and validates the request and work manifest, resolves every
// path strictly beneath bundleDir, and verifies each declared sha256 against
// exact file bytes. A digest mismatch fails before a run is created.
func Admit(specPath, bundleDir string) (Admitted, error) {
	reqBytes, err := os.ReadFile(specPath)
	if err != nil {
		return Admitted{}, fmt.Errorf("bundle: read request: %w", err)
	}
	req, err := execution.DecodeRequest(reqBytes)
	if err != nil {
		return Admitted{}, err
	}
	if err := execution.ValidateRequest(req); err != nil {
		return Admitted{}, err
	}
	bundleAbs, err := filepath.Abs(bundleDir)
	if err != nil {
		return Admitted{}, fmt.Errorf("bundle: abs bundle: %w", err)
	}
	workPath, err := resolveUnder(bundleAbs, req.Work.Manifest)
	if err != nil {
		return Admitted{}, fmt.Errorf("bundle: work.manifest: %w", err)
	}
	workBytes, err := os.ReadFile(workPath)
	if err != nil {
		return Admitted{}, fmt.Errorf("bundle: read work manifest: %w", err)
	}
	if sum := sha256Hex(workBytes); sum != req.Work.SHA256 {
		return Admitted{}, fmt.Errorf("bundle: work.json digest mismatch: got %s want %s", sum, req.Work.SHA256)
	}
	work, err := execution.DecodeWorkSpec(workBytes)
	if err != nil {
		return Admitted{}, err
	}
	if err := execution.ValidateWorkSpec(work); err != nil {
		return Admitted{}, err
	}
	for i, in := range work.Inputs {
		src, err := resolveUnder(bundleAbs, in.Source)
		if err != nil {
			return Admitted{}, fmt.Errorf("bundle: inputs[%d].source: %w", i, err)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return Admitted{}, fmt.Errorf("bundle: read inputs[%d]: %w", i, err)
		}
		if sum := sha256Hex(data); sum != in.SHA256 {
			return Admitted{}, fmt.Errorf("bundle: inputs[%d] digest mismatch: got %s want %s", i, sum, in.SHA256)
		}
	}
	return Admitted{
		Request:      req,
		RequestBytes: reqBytes,
		Work:         work,
		WorkBytes:    workBytes,
		BundleDir:    bundleAbs,
	}, nil
}

// Materialize copies exact verified work.json and input bytes into the run
// directory and checks out the immutable workspace revision via git.
func Materialize(adm Admitted, run state.RunDir) error {
	if err := os.WriteFile(run.WorkPath(), adm.WorkBytes, 0o600); err != nil {
		return fmt.Errorf("bundle: write work.json: %w", err)
	}
	for i, in := range adm.Work.Inputs {
		if err := materializeInput(adm.BundleDir, run.InputsDir(), i, in); err != nil {
			return err
		}
	}
	return checkoutWorkspace(adm.Work.Workspace, run.WorkspaceDir())
}

func materializeInput(bundleDir, inputsDir string, i int, in execution.Input) error {
	src, err := resolveUnder(bundleDir, in.Source)
	if err != nil {
		return fmt.Errorf("bundle: inputs[%d].source: %w", i, err)
	}
	dst, err := beneath(inputsDir, in.Target)
	if err != nil {
		return fmt.Errorf("bundle: inputs[%d].target: %w", i, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("bundle: mkdir input target: %w", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("bundle: read input for copy: %w", err)
	}
	if sum := sha256Hex(data); sum != in.SHA256 {
		return fmt.Errorf("bundle: inputs[%d] digest changed since admission", i)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("bundle: write input: %w", err)
	}
	return nil
}

func checkoutWorkspace(ws execution.Workspace, dest string) error {
	// git clone into a temp sibling, then rename — keeps a failed clone from
	// leaving a half-populated workspace dir the controller might trust.
	parent := filepath.Dir(dest)
	tmp, err := os.MkdirTemp(parent, "workspace-clone-*")
	if err != nil {
		return fmt.Errorf("bundle: workspace temp: %w", err)
	}
	defer func() {
		if tmp != "" {
			_ = os.RemoveAll(tmp)
		}
	}()

	clone := exec.Command("git", "clone", "--quiet", ws.URL, tmp)
	if out, err := clone.CombinedOutput(); err != nil {
		return fmt.Errorf("bundle: git clone: %w: %s", err, strings.TrimSpace(string(out)))
	}
	checkout := exec.Command("git", "-C", tmp, "checkout", "--quiet", ws.Revision)
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("bundle: git checkout %s: %w: %s", ws.Revision, err, strings.TrimSpace(string(out)))
	}
	head := exec.Command("git", "-C", tmp, "rev-parse", "HEAD")
	out, err := head.Output()
	if err != nil {
		return fmt.Errorf("bundle: git rev-parse: %w", err)
	}
	got := strings.TrimSpace(string(out))
	if got != ws.Revision {
		return fmt.Errorf("bundle: workspace revision mismatch: got %s want %s", got, ws.Revision)
	}
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("bundle: clear workspace: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("bundle: move workspace: %w", err)
	}
	tmp = ""
	return nil
}

// resolveUnder resolves rel strictly under root, then EvalSymlinks and
// re-checks the resolved path still lies under the (symlink-resolved) root.
// Rejects a symlink that escapes the bundle before any ReadFile.
func resolveUnder(root, rel string) (string, error) {
	path, err := beneath(root, rel)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	sep := string(filepath.Separator)
	if resolved != rootResolved && !strings.HasPrefix(resolved, rootResolved+sep) {
		return "", fmt.Errorf("path %q escapes bundle root", rel)
	}
	return resolved, nil
}

// beneath resolves rel strictly under root. Absolute paths, drive prefixes,
// and ".." escapes reject even when validators already passed.
func beneath(root, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q escapes bundle root", rel)
	}
	cleaned := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/")))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes bundle root", rel)
	}
	joined := filepath.Join(root, cleaned)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	sep := string(filepath.Separator)
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+sep) {
		return "", fmt.Errorf("path %q escapes bundle root", rel)
	}
	return abs, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256File returns the hex sha256 of a file's exact bytes — test helper
// surface shared with golden fixtures.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
