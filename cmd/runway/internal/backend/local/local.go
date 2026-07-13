// Package local is the process-group backend: one non-shell argv, explicit
// cwd/env, and redacting capture of stdout/stderr into logs/ (D11).
package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/contracts/execution"
)

// Backend starts one local process group per run.
type Backend struct{}

// New returns the local process-group backend.
func New() *Backend { return &Backend{} }

type handle struct {
	cmd    *exec.Cmd
	pgid   int
	stdout *os.File
	stderr *os.File
	wg     sync.WaitGroup
	mu     sync.Mutex
	done   bool
	exit   backend.Exit
	err    error
}

// Start launches ONE process group with the fully expanded argv. Never a
// shell, never string interpolation. Secrets live only in process memory
// and are redacted from captured logs (FR12/D8).
func (b *Backend) Start(ctx context.Context, prep backend.PreparedRun, emit backend.Emit) (backend.Handle, error) {
	if len(prep.Argv) == 0 {
		return nil, fmt.Errorf("local: argv is empty")
	}
	stdout, err := os.OpenFile(prep.StdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("local: open stdout log: %w", err)
	}
	stderr, err := os.OpenFile(prep.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		stdout.Close()
		return nil, fmt.Errorf("local: open stderr log: %w", err)
	}

	cmd := exec.CommandContext(ctx, prep.Argv[0], prep.Argv[1:]...)
	cmd.Dir = prep.Cwd
	cmd.Env = prep.Env
	setProcessGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		closeAll(stdout, stderr)
		return nil, fmt.Errorf("local: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		closeAll(stdout, stderr)
		return nil, fmt.Errorf("local: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		closeAll(stdout, stderr)
		return nil, fmt.Errorf("local: start: %w", err)
	}
	pgid, err := processGroupID(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		closeAll(stdout, stderr)
		return nil, fmt.Errorf("local: process group: %w", err)
	}

	h := &handle{cmd: cmd, pgid: pgid, stdout: stdout, stderr: stderr}
	h.wg.Add(2)
	go capture(stdoutPipe, stdout, prep.Secrets, &h.wg)
	go capture(stderrPipe, stderr, prep.Secrets, &h.wg)

	allocID := fmt.Sprintf("pid:%d", cmd.Process.Pid)
	if err := emit(execution.PhaseStartup, execution.KindPlacementAllocated, map[string]any{
		"backend":       "local",
		"allocation_id": allocID,
		"pid":           cmd.Process.Pid,
	}); err != nil {
		_ = b.Cleanup(ctx, h)
		return nil, err
	}
	if err := emit(execution.PhaseWorkload, execution.KindWorkloadStarted, map[string]any{
		"pid": cmd.Process.Pid,
	}); err != nil {
		_ = b.Cleanup(ctx, h)
		return nil, err
	}
	return h, nil
}

// Wait blocks until the process exits, then emits workload_exited.
func (b *Backend) Wait(_ context.Context, bh backend.Handle, emit backend.Emit) (backend.Exit, error) {
	h, err := asHandle(bh)
	if err != nil {
		return backend.Exit{}, err
	}
	waitErr := h.cmd.Wait()
	h.wg.Wait()
	closeAll(h.stdout, h.stderr)

	code := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
			waitErr = nil
		}
	}
	h.mu.Lock()
	h.done = true
	h.exit = backend.Exit{Code: code}
	h.err = waitErr
	h.mu.Unlock()

	if waitErr != nil {
		return backend.Exit{}, fmt.Errorf("local: wait: %w", waitErr)
	}
	if err := emit(execution.PhaseWorkload, execution.KindWorkloadExited, map[string]any{
		"exit_code": code,
	}); err != nil {
		return backend.Exit{Code: code}, err
	}
	return backend.Exit{Code: code}, nil
}

// Cancel signals the process group. PR 2 owns cancel policy; this is the
// mechanism hook.
func (b *Backend) Cancel(_ context.Context, bh backend.Handle) error {
	h, err := asHandle(bh)
	if err != nil {
		return err
	}
	return signalGroup(h.pgid)
}

// Collect is a no-op in PR 1 — collection ordering is PR 2 lifecycle policy.
func (b *Backend) Collect(_ context.Context, bh backend.Handle, _ string) ([]execution.Artifact, error) {
	if _, err := asHandle(bh); err != nil {
		return nil, err
	}
	return nil, nil
}

// Cleanup kills any remaining process-group members so a run leaves no
// orphans after the controller exits.
func (b *Backend) Cleanup(_ context.Context, bh backend.Handle) error {
	h, err := asHandle(bh)
	if err != nil {
		return err
	}
	return killGroup(h.pgid)
}

func asHandle(bh backend.Handle) (*handle, error) {
	h, ok := bh.(*handle)
	if !ok || h == nil {
		return nil, fmt.Errorf("local: invalid handle")
	}
	return h, nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}
}

func capture(r io.Reader, w io.Writer, secrets [][]byte, wg *sync.WaitGroup) {
	defer wg.Done()
	redacted := newRedactor(w, secrets)
	_, _ = io.Copy(redacted, r)
	_ = redacted.Close()
}
