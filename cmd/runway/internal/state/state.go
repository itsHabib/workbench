// Package state owns the run-directory layout and creation. Every path a
// runway controller needs is derived here and nowhere else (TDD §5).
package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvState is the one documented environment value that defaults the state
// root when --state is omitted.
const EnvState = "RUNWAY_STATE"

// RunDir is one run's durable directory under <state>/runs/<run-id>/.
type RunDir struct {
	Root string
}

// DefaultRoot returns the state root: $RUNWAY_STATE when set, otherwise
// ~/.runway. Callers that pass --state never need this.
func DefaultRoot() string {
	if v := os.Getenv(EnvState); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".runway"
	}
	return filepath.Join(home, ".runway")
}

// Create allocates <state>/runs/<run-id>/ with restrictive permissions and
// the TDD §5 subdirectories. private/ is mode 0700 so files written into it
// can be mode 0600 without widening the parent.
func Create(stateRoot, runID string) (RunDir, error) {
	if runID == "" {
		return RunDir{}, fmt.Errorf("state: run id is empty")
	}
	runsRoot := filepath.Join(stateRoot, "runs")
	if err := os.MkdirAll(runsRoot, 0o700); err != nil {
		return RunDir{}, fmt.Errorf("state: create runs root: %w", err)
	}
	root := filepath.Join(runsRoot, runID)
	// Fail if the run dir already exists — run IDs are random, so a
	// collision is always suspicious (pre-created / permissive dir).
	if err := os.Mkdir(root, 0o700); err != nil {
		return RunDir{}, fmt.Errorf("state: create run dir: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return RunDir{}, fmt.Errorf("state: chmod run dir: %w", err)
	}
	for _, sub := range []string{"inputs", "logs", "artifacts", "workspace"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			return RunDir{}, fmt.Errorf("state: create %s: %w", sub, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "private"), 0o700); err != nil {
		return RunDir{}, fmt.Errorf("state: create private: %w", err)
	}
	return RunDir{Root: root}, nil
}

// RequestPath is request.json — the exact accepted request bytes.
func (r RunDir) RequestPath() string { return filepath.Join(r.Root, "request.json") }

// WorkPath is work.json — the exact verified portable work manifest.
func (r RunDir) WorkPath() string { return filepath.Join(r.Root, "work.json") }

// EventsPath is the append-only canonical event journal.
func (r RunDir) EventsPath() string { return filepath.Join(r.Root, "events.ndjson") }

// InputsDir holds exact verified declared bundle inputs.
func (r RunDir) InputsDir() string { return filepath.Join(r.Root, "inputs") }

// LogsDir holds controller and workload byte streams.
func (r RunDir) LogsDir() string { return filepath.Join(r.Root, "logs") }

// ArtifactsDir is the out root for declared workload products.
func (r RunDir) ArtifactsDir() string { return filepath.Join(r.Root, "artifacts") }

// WorkspaceDir is the materialized immutable workspace checkout.
func (r RunDir) WorkspaceDir() string { return filepath.Join(r.Root, "workspace") }

// PrivateDir is host-local backend/controller state (mode 0700).
func (r RunDir) PrivateDir() string { return filepath.Join(r.Root, "private") }

// StdoutLog is the redacted workload stdout stream.
func (r RunDir) StdoutLog() string { return filepath.Join(r.LogsDir(), "stdout.log") }

// StderrLog is the redacted workload stderr stream.
func (r RunDir) StderrLog() string { return filepath.Join(r.LogsDir(), "stderr.log") }

// WritePrivate writes a private/ file at mode 0600.
func (r RunDir) WritePrivate(name string, data []byte) error {
	path := filepath.Join(r.PrivateDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("state: write private %s: %w", name, err)
	}
	return nil
}

// ResultPath is the atomic terminal receipt.
func (r RunDir) ResultPath() string { return filepath.Join(r.Root, "result.json") }

// ControllerPath is private/controller.json — PID + process-start identity.
func (r RunDir) ControllerPath() string {
	return filepath.Join(r.PrivateDir(), "controller.json")
}

// CancelRequestPath is the cancel-request marker written by `runway cancel`.
func (r RunDir) CancelRequestPath() string {
	return filepath.Join(r.PrivateDir(), "cancel.request")
}

// ClaimPath is private/writer.claim — the atomic exclusive writer claim.
func (r RunDir) ClaimPath() string {
	return filepath.Join(r.PrivateDir(), "writer.claim")
}

// BackendPath is private/backend.json — opaque durable backend allocation.
func (r RunDir) BackendPath() string {
	return filepath.Join(r.PrivateDir(), "backend.json")
}

// Open resolves an existing run directory under the state root. It does not
// create anything — missing or non-directory paths fail.
func Open(stateRoot, runID string) (RunDir, error) {
	if runID == "" {
		return RunDir{}, fmt.Errorf("state: run id is empty")
	}
	root := filepath.Join(stateRoot, "runs", runID)
	fi, err := os.Stat(root)
	if err != nil {
		return RunDir{}, fmt.Errorf("state: open run: %w", err)
	}
	if !fi.IsDir() {
		return RunDir{}, fmt.Errorf("state: open run: %s is not a directory", root)
	}
	return RunDir{Root: root}, nil
}
