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
	cmd        *exec.Cmd
	pgid       int
	stdout     *os.File
	stderr     *os.File
	wg         sync.WaitGroup
	mu         sync.Mutex
	done       bool
	exit       backend.Exit
	err        error
	captureErr error
}

// Start launches ONE process group with the fully expanded argv. Never a
// shell, never string interpolation. Secrets live only in process memory
// and are redacted from captured logs (FR12/D8).
func (b *Backend) Start(_ context.Context, prep backend.PreparedRun, emit backend.Emit) (backend.Handle, error) {
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

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdout, stderr)
		return nil, fmt.Errorf("local: stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdoutR, stdoutW, stdout, stderr)
		return nil, fmt.Errorf("local: stderr pipe: %w", err)
	}

	cmd := exec.Command(prep.Argv[0], prep.Argv[1:]...)
	cmd.Dir = prep.Cwd
	cmd.Env = prep.Env
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		closeAll(stdoutR, stdoutW, stderrR, stderrW, stdout, stderr)
		return nil, fmt.Errorf("local: start: %w", err)
	}
	// Close write ends in the parent so EOF arrives when all holders exit.
	closeAll(stdoutW, stderrW)

	pgid, err := processGroupID(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		closeAll(stdoutR, stderrR, stdout, stderr)
		return nil, fmt.Errorf("local: process group: %w", err)
	}

	h := &handle{cmd: cmd, pgid: pgid, stdout: stdout, stderr: stderr}
	h.wg.Add(2)
	go capture(stdoutR, stdout, prep.Secrets, h)
	go capture(stderrR, stderr, prep.Secrets, h)

	if err := writeAllocation(prep.PrivateDir, cmd.Process.Pid, pgid); err != nil {
		_ = b.abortStart(h)
		return nil, err
	}

	allocID := fmt.Sprintf("pid:%d", cmd.Process.Pid)
	if err := emit(execution.PhaseStartup, execution.KindPlacementAllocated, map[string]any{
		"backend":       "local",
		"allocation_id": allocID,
		"pid":           cmd.Process.Pid,
	}); err != nil {
		_ = b.abortStart(h)
		return nil, err
	}
	// Local process has no separate guest-readiness signal; emit immediately.
	if err := emit(execution.PhaseStartup, execution.KindWorkloadReady, map[string]any{
		"pid": cmd.Process.Pid,
	}); err != nil {
		_ = b.abortStart(h)
		return nil, err
	}
	if err := emit(execution.PhaseWorkload, execution.KindWorkloadStarted, map[string]any{
		"pid": cmd.Process.Pid,
	}); err != nil {
		_ = b.abortStart(h)
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
	// Bound EOF against grandchildren that inherited the pipe write ends.
	_ = killGroup(h.pgid)
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
	capErr := h.captureErr
	h.mu.Unlock()

	if waitErr != nil {
		return backend.Exit{}, fmt.Errorf("local: wait: %w", waitErr)
	}
	if err := emit(execution.PhaseWorkload, execution.KindWorkloadExited, map[string]any{
		"exit_code": code,
	}); err != nil {
		return backend.Exit{Code: code}, err
	}
	if capErr != nil {
		return backend.Exit{Code: code}, fmt.Errorf("local: capture: %w", capErr)
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

// abortStart tears down a partially-started handle after an emit failure:
// kill group, drain capture, reap the process, close log files.
func (b *Backend) abortStart(h *handle) error {
	_ = killGroup(h.pgid)
	h.wg.Wait()
	_ = h.cmd.Wait()
	closeAll(h.stdout, h.stderr)
	return nil
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

// capture owns its pipe read end and closes it when the stream drains, so
// repeated runs cannot accumulate descriptors waiting on GC finalizers.
func capture(r *os.File, w io.Writer, secrets [][]byte, h *handle) {
	defer h.wg.Done()
	defer r.Close()
	redacted := newRedactor(w, secrets)
	_, copyErr := io.Copy(redacted, r)
	closeErr := redacted.Close()
	err := copyErr
	if err == nil {
		err = closeErr
	}
	if err == nil {
		return
	}
	h.mu.Lock()
	if h.captureErr == nil {
		h.captureErr = err
	}
	h.mu.Unlock()
}
