package controller_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/backend/local"
	"github.com/itsHabib/workbench/cmd/runway/internal/controller"
	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

type harness struct {
	t         *testing.T
	dir       string
	bundleDir string
	stateRoot string
	repo      string
	rev       string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	repo := initGitRepo(t, dir)
	h := &harness{
		t:         t,
		dir:       dir,
		bundleDir: filepath.Join(dir, "bundle"),
		stateRoot: filepath.Join(dir, "state"),
		repo:      repo,
		rev:       gitHead(t, repo),
	}
	if err := os.MkdirAll(h.bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) writeProg(name, src string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.bundleDir, name), []byte(src), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) runWith(work execution.WorkSpec, policy execution.Policy, opts controller.Options) controller.Outcome {
	h.t.Helper()
	workBytes, err := json.Marshal(work)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.bundleDir, "work.json"), workBytes, 0o600); err != nil {
		h.t.Fatal(err)
	}
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_test",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(workBytes)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        policy,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		h.t.Fatal(err)
	}
	spec := filepath.Join(h.dir, "request.json")
	if err := os.WriteFile(spec, reqBytes, 0o600); err != nil {
		h.t.Fatal(err)
	}
	out, err := controller.Run(spec, h.bundleDir, h.stateRoot, opts)
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	assertTerminalTruth(h.t, h.stateRoot, out)
	return out
}

func goRunWork(h *harness, progFile string, outputs []execution.Output) execution.WorkSpec {
	prog, err := os.ReadFile(filepath.Join(h.bundleDir, progFile))
	if err != nil {
		h.t.Fatal(err)
	}
	name := "go"
	litRun := "run"
	return execution.WorkSpec{
		SchemaVersion: execution.SchemaVersion,
		Command: execution.Command{
			Executable: execution.Executable{Name: &name},
			Args: []execution.Arg{
				{Literal: &litRun},
				{Path: &execution.PathRef{Root: execution.RootInputs, Value: progFile}},
			},
		},
		Cwd: execution.PathRef{Root: execution.RootWorkspace, Value: "."},
		Workspace: execution.Workspace{
			Kind: "git", URL: h.repo, Revision: h.rev,
		},
		Inputs: []execution.Input{{
			Name: "prog", Source: progFile, Target: progFile, SHA256: sha256Hex(prog),
		}},
		Outputs: outputs,
	}
}

func assertTerminalTruth(t *testing.T, stateRoot string, out controller.Outcome) {
	t.Helper()
	if err := execution.ValidateResult(out.Result); err != nil {
		t.Fatalf("ValidateResult: %v", err)
	}
	run, err := state.Open(stateRoot, out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	st, err := execution.Reduce(events)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Terminal {
		t.Fatal("Reduce must report Terminal=true")
	}
	if events[len(events)-1].Kind != execution.KindRunTerminal {
		t.Fatalf("last event must be run_terminal, got %s", events[len(events)-1].Kind)
	}
	raw, err := os.ReadFile(run.ResultPath())
	if err != nil {
		t.Fatal(err)
	}
	got, err := execution.DecodeResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != out.Result.Status || got.ReasonCode != out.Result.ReasonCode {
		t.Fatalf("durable result mismatch: %+v vs %+v", got, out.Result)
	}
}

func TestFlowA_Success(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
import (
  "os"
  "path/filepath"
)
func main() {
  out := os.Getenv("RUNWAY_OUT")
  if err := os.WriteFile(filepath.Join(out, "hello.txt"), []byte("ok"), 0o600); err != nil {
    panic(err)
  }
}
`)
	work := goRunWork(h, "main.go", []execution.Output{{
		Name: "hello", Path: "hello.txt", Required: true,
	}})
	out := h.runWith(work, execution.Policy{DeadlineMS: 60000, CancelGraceMS: 1000}, controller.Options{})
	if out.Result.Status != execution.StatusSucceeded || out.Result.ReasonCode != execution.ReasonCompleted {
		t.Fatalf("want succeeded/completed, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseTerminal {
		t.Fatalf("terminal_phase=%s", out.Result.TerminalPhase)
	}
	if out.Result.WorkloadExitCode == nil || *out.Result.WorkloadExitCode != 0 {
		t.Fatalf("workload_exit_code=%v", out.Result.WorkloadExitCode)
	}
	if out.ExitCode != controller.ExitOK {
		t.Fatalf("exit=%d", out.ExitCode)
	}
	if len(out.Result.Artifacts) != 1 || out.Result.Artifacts[0].Path != "artifacts/hello.txt" {
		t.Fatalf("artifacts=%+v", out.Result.Artifacts)
	}
}

func TestFlowE_MissingRequiredOutput(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
func main() {}
`)
	work := goRunWork(h, "main.go", []execution.Output{{
		Name: "missing", Path: "nope.txt", Required: true,
	}})
	out := h.runWith(work, execution.Policy{DeadlineMS: 60000, CancelGraceMS: 1000}, controller.Options{})
	if out.Result.Status != execution.StatusFailed || out.Result.ReasonCode != execution.ReasonCollectionFailed {
		t.Fatalf("want failed/collection_failed, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseCollection {
		t.Fatalf("phase=%s", out.Result.TerminalPhase)
	}
	if out.Result.WorkloadExitCode == nil || *out.Result.WorkloadExitCode != 0 {
		t.Fatalf("must retain workload_exit_code 0, got %v", out.Result.WorkloadExitCode)
	}
	if out.ExitCode != controller.ExitFailed {
		t.Fatalf("exit=%d", out.ExitCode)
	}
}

func TestFlowC_DeadlineNoisyWorkload(t *testing.T) {
	h := newHarness(t)
	// Portable busy-loop fixture: no unix-only SIGTERM ignore. Cancel/cleanup
	// must still terminate the workload (SIGKILL / taskkill) after deadline.
	h.writeProg("main.go", `package main
import (
  "fmt"
  "os"
  "time"
)
func main() {
  for {
    fmt.Fprintln(os.Stderr, "noisy")
    time.Sleep(50 * time.Millisecond)
  }
}
`)
	work := goRunWork(h, "main.go", nil)
	start := time.Now()
	out := h.runWith(work, execution.Policy{DeadlineMS: 400, CancelGraceMS: 200}, controller.Options{})
	if out.Result.Status != execution.StatusTimedOut || out.Result.ReasonCode != execution.ReasonDeadlineExceeded {
		t.Fatalf("want timed_out/deadline_exceeded, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseWorkload {
		t.Fatalf("terminal_phase=%s want workload (interrupt capture, not cleanup)", out.Result.TerminalPhase)
	}
	if out.ExitCode != controller.ExitTimedOut {
		t.Fatalf("exit=%d", out.ExitCode)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("deadline path too slow: %s", time.Since(start))
	}
	assertNoOrphans(t, out.Result.Placement.AllocationID)
}

func TestFlowC_TimedOutMissingOutput(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
import "time"
func main() { time.Sleep(10 * time.Second) }
`)
	work := goRunWork(h, "main.go", []execution.Output{{
		Name: "missing", Path: "nope.txt", Required: true,
	}})
	out := h.runWith(work, execution.Policy{DeadlineMS: 400, CancelGraceMS: 100}, controller.Options{})
	if out.Result.Status != execution.StatusTimedOut || out.Result.ReasonCode != execution.ReasonDeadlineExceeded {
		t.Fatalf("want timed_out/deadline_exceeded, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseWorkload {
		t.Fatalf("phase=%s", out.Result.TerminalPhase)
	}
	if !hasDiagnostic(out.Result, execution.ReasonCollectionFailed) {
		t.Fatalf("want collection_failed diagnostic, got %+v", out.Result.Diagnostics)
	}
}

func TestFlowC_CleanupFailureEscalation(t *testing.T) {
	h := newHarness(t)
	// Long sleep so deadline fires first; backend Cancel/Cleanup own termination
	// (no unix-only signal.Ignore in the fixture).
	h.writeProg("main.go", `package main
import "time"
func main() {
  time.Sleep(10 * time.Second)
}
`)
	work := goRunWork(h, "main.go", nil)
	be := &failCleanup{inner: local.New()}
	// Deadline must clear fixture startup (go run compile) on every GOOS,
	// then fire during the long sleep so Cleanup runs under failCleanup.
	out := h.runWith(work, execution.Policy{DeadlineMS: 2000, CancelGraceMS: 50}, controller.Options{Backend: be})
	if out.Result.Status != execution.StatusFailed || out.Result.ReasonCode != execution.ReasonCleanupFailed {
		t.Fatalf("want failed/cleanup_failed, got %s/%s phase=%s causes=%+v", out.Result.Status, out.Result.ReasonCode, out.Result.TerminalPhase, out.Result.Causes)
	}
	if out.Result.TerminalPhase != execution.PhaseCleanup {
		t.Fatalf("phase=%s", out.Result.TerminalPhase)
	}
	if len(out.Result.Causes) != 1 || out.Result.Causes[0].ReasonCode != execution.ReasonDeadlineExceeded {
		t.Fatalf("causes=%+v", out.Result.Causes)
	}
}

func TestFlowD_CancelCleanupFailureEscalation(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
import "time"
func main() { time.Sleep(10 * time.Second) }
`)
	work := goRunWork(h, "main.go", nil)
	workBytes, _ := json.Marshal(work)
	_ = os.WriteFile(filepath.Join(h.bundleDir, "work.json"), workBytes, 0o600)
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_cancel_cleanup",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(workBytes)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 30000, CancelGraceMS: 50},
	}
	reqBytes, _ := json.Marshal(req)
	spec := filepath.Join(h.dir, "request.json")
	_ = os.WriteFile(spec, reqBytes, 0o600)

	be := &failCleanup{inner: local.New()}
	var (
		out    controller.Outcome
		runErr error
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		out, runErr = controller.Run(spec, h.bundleDir, h.stateRoot, controller.Options{Backend: be})
	}()

	runID := waitForRunID(t, h.stateRoot, 15*time.Second)
	if _, err := controller.RequestCancel(h.stateRoot, runID); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if runErr != nil {
		t.Fatal(runErr)
	}
	assertTerminalTruth(t, h.stateRoot, out)
	if out.Result.Status != execution.StatusFailed || out.Result.ReasonCode != execution.ReasonCleanupFailed {
		t.Fatalf("want failed/cleanup_failed, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseCleanup {
		t.Fatalf("phase=%s", out.Result.TerminalPhase)
	}
	if len(out.Result.Causes) != 1 || out.Result.Causes[0].ReasonCode != execution.ReasonCancelRequested {
		t.Fatalf("causes=%+v", out.Result.Causes)
	}
}

type failCleanup struct {
	inner backend.Backend
}

func (f *failCleanup) Start(ctx context.Context, prep backend.PreparedRun, emit backend.Emit) (backend.Handle, error) {
	return f.inner.Start(ctx, prep, emit)
}
func (f *failCleanup) Wait(ctx context.Context, h backend.Handle, emit backend.Emit) (backend.Exit, error) {
	return f.inner.Wait(ctx, h, emit)
}
func (f *failCleanup) Cancel(ctx context.Context, h backend.Handle) error {
	return f.inner.Cancel(ctx, h)
}
func (f *failCleanup) Collect(ctx context.Context, h backend.Handle, outDir string) ([]execution.Artifact, error) {
	return f.inner.Collect(ctx, h, outDir)
}
func (f *failCleanup) Cleanup(ctx context.Context, h backend.Handle) error {
	_ = f.inner.Cleanup(ctx, h)
	return fmt.Errorf("simulated isolation failure")
}

func TestWaitErrorProducesDurableFailedReceipt(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
func main() {}
`)
	work := goRunWork(h, "main.go", nil)
	out := h.runWith(work, execution.Policy{DeadlineMS: 60000, CancelGraceMS: 100}, controller.Options{Backend: &immediateWaitErr{}})
	if out.Result.Status != execution.StatusFailed || out.Result.ReasonCode != execution.ReasonWorkloadFailed {
		t.Fatalf("want failed/workload_failed, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseWorkload {
		t.Fatalf("phase=%s", out.Result.TerminalPhase)
	}
	if out.ExitCode != controller.ExitFailed {
		t.Fatalf("exit=%d", out.ExitCode)
	}
}

type immediateWaitErr struct{}

func (b *immediateWaitErr) Start(_ context.Context, _ backend.PreparedRun, emit backend.Emit) (backend.Handle, error) {
	if err := emit(execution.PhaseStartup, execution.KindPlacementAllocated, map[string]any{"allocation_id": "imm"}); err != nil {
		return nil, err
	}
	if err := emit(execution.PhaseStartup, execution.KindWorkloadReady, nil); err != nil {
		return nil, err
	}
	if err := emit(execution.PhaseWorkload, execution.KindWorkloadStarted, nil); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}
func (b *immediateWaitErr) Wait(_ context.Context, _ backend.Handle, _ backend.Emit) (backend.Exit, error) {
	return backend.Exit{}, fmt.Errorf("backend wait boom")
}
func (b *immediateWaitErr) Cancel(_ context.Context, _ backend.Handle) error { return nil }
func (b *immediateWaitErr) Collect(_ context.Context, _ backend.Handle, _ string) ([]execution.Artifact, error) {
	return nil, nil
}
func (b *immediateWaitErr) Cleanup(_ context.Context, _ backend.Handle) error { return nil }

// raceBackend exits and accepts cancel at the same moment so Wait's
// workload_exited emit races the controller's deadline_exceeded emit.
type raceBackend struct {
	release chan struct{}
}

func (b *raceBackend) Start(_ context.Context, _ backend.PreparedRun, emit backend.Emit) (backend.Handle, error) {
	if err := emit(execution.PhaseStartup, execution.KindPlacementAllocated, map[string]any{"allocation_id": "race"}); err != nil {
		return nil, err
	}
	if err := emit(execution.PhaseStartup, execution.KindWorkloadReady, nil); err != nil {
		return nil, err
	}
	if err := emit(execution.PhaseWorkload, execution.KindWorkloadStarted, nil); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}
func (b *raceBackend) Wait(_ context.Context, _ backend.Handle, emit backend.Emit) (backend.Exit, error) {
	<-b.release
	if err := emit(execution.PhaseWorkload, execution.KindWorkloadExited, map[string]any{"exit_code": 0}); err != nil {
		return backend.Exit{}, err
	}
	return backend.Exit{Code: 0}, nil
}
func (b *raceBackend) Cancel(_ context.Context, _ backend.Handle) error {
	select {
	case <-b.release:
	default:
		close(b.release)
	}
	return nil
}
func (b *raceBackend) Collect(_ context.Context, _ backend.Handle, _ string) ([]execution.Artifact, error) {
	return nil, nil
}
func (b *raceBackend) Cleanup(_ context.Context, _ backend.Handle) error { return nil }

func TestEmitRace_WorkloadExitAndDeadline(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
func main() {}
`)
	work := goRunWork(h, "main.go", nil)
	be := &raceBackend{release: make(chan struct{})}
	// Deadline must clear prepare (git checkout) then fire while Wait blocks,
	// so Cancel releases Wait and workload_exited races deadline_exceeded.
	out := h.runWith(work, execution.Policy{DeadlineMS: 2000, CancelGraceMS: 50}, controller.Options{Backend: be})
	if out.Result.Status != execution.StatusTimedOut {
		t.Fatalf("want timed_out, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
}

func TestFlowD_CancelRace(t *testing.T) {
	t.Run("cancel_then_exit", func(t *testing.T) {
		runCancelRace(t, true)
	})
	t.Run("exit_then_cancel", func(t *testing.T) {
		runCancelRace(t, false)
	})
}

func runCancelRace(t *testing.T, cancelFirst bool) {
	t.Helper()
	h := newHarness(t)
	h.writeProg("main.go", `package main
import "time"
func main() { time.Sleep(2 * time.Second) }
`)
	work := goRunWork(h, "main.go", nil)
	workBytes, _ := json.Marshal(work)
	_ = os.WriteFile(filepath.Join(h.bundleDir, "work.json"), workBytes, 0o600)
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_cancel",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(workBytes)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 30000, CancelGraceMS: 500},
	}
	reqBytes, _ := json.Marshal(req)
	spec := filepath.Join(h.dir, "request.json")
	_ = os.WriteFile(spec, reqBytes, 0o600)

	var (
		out    controller.Outcome
		runErr error
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		out, runErr = controller.Run(spec, h.bundleDir, h.stateRoot, controller.Options{})
	}()

	// Wait until the run directory + identity exist.
	var runID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(filepath.Join(h.stateRoot, "runs"))
		if len(entries) == 1 {
			runID = entries[0].Name()
			run, err := state.Open(h.stateRoot, runID)
			if err == nil {
				if _, err := os.Stat(run.ControllerPath()); err == nil {
					break
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if runID == "" {
		t.Fatal("run never appeared")
	}

	if cancelFirst {
		if _, err := controller.RequestCancel(h.stateRoot, runID); err != nil {
			t.Fatal(err)
		}
	} else {
		// Let natural exit approach, then cancel (may no-op if already terminal).
		time.Sleep(2100 * time.Millisecond)
		if _, err := controller.RequestCancel(h.stateRoot, runID); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if runErr != nil {
		t.Fatal(runErr)
	}
	assertTerminalTruth(t, h.stateRoot, out)

	// Exactly one terminal receipt.
	run, _ := state.Open(h.stateRoot, out.RunID)
	events, _ := journal.ReadHistory(run.EventsPath())
	terminals := 0
	for _, ev := range events {
		if ev.Kind == execution.KindRunTerminal {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("want exactly one run_terminal, got %d", terminals)
	}

	// Repeat cancel is a successful no-op.
	co, err := controller.RequestCancel(h.stateRoot, out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !co.NoOp {
		t.Fatal("repeat cancel must be no-op")
	}
	if cancelFirst {
		if out.Result.Status != execution.StatusCancelled {
			t.Fatalf("cancel-first want cancelled, got %s", out.Result.Status)
		}
		if out.ExitCode != controller.ExitCancelled {
			t.Fatalf("exit=%d", out.ExitCode)
		}
	}
}

func TestResultAtomicRename(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
import (
  "os"
  "path/filepath"
  "time"
)
func main() {
  time.Sleep(300 * time.Millisecond)
  _ = os.WriteFile(filepath.Join(os.Getenv("RUNWAY_OUT"), "a.txt"), []byte("x"), 0o600)
}
`)
	work := goRunWork(h, "main.go", []execution.Output{{Name: "a", Path: "a.txt", Required: true}})
	workBytes, _ := json.Marshal(work)
	_ = os.WriteFile(filepath.Join(h.bundleDir, "work.json"), workBytes, 0o600)
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_atomic",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(workBytes)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 60000, CancelGraceMS: 1000},
	}
	reqBytes, _ := json.Marshal(req)
	spec := filepath.Join(h.dir, "request.json")
	_ = os.WriteFile(spec, reqBytes, 0o600)

	errCh := make(chan error, 1)
	go func() {
		_, err := controller.Run(spec, h.bundleDir, h.stateRoot, controller.Options{})
		errCh <- err
	}()

	var runID string
	for i := 0; i < 200 && runID == ""; i++ {
		entries, _ := os.ReadDir(filepath.Join(h.stateRoot, "runs"))
		if len(entries) == 1 {
			runID = entries[0].Name()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if runID == "" {
		t.Fatal("no run")
	}
	resultPath := filepath.Join(h.stateRoot, "runs", runID, "result.json")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(resultPath)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if !json.Valid(data) {
			t.Fatalf("partial/invalid result.json observed: %q", data)
		}
		res, err := execution.DecodeResult(data)
		if err != nil {
			t.Fatalf("unreadable result during transition: %v", err)
		}
		if err := execution.ValidateResult(res); err != nil {
			t.Fatalf("invalid result during transition: %v", err)
		}
		break
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestWatchAfterSeq(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
func main() {}
`)
	work := goRunWork(h, "main.go", nil)
	out := h.runWith(work, execution.Policy{DeadlineMS: 60000, CancelGraceMS: 100}, controller.Options{})
	var buf bytes.Buffer
	if err := controller.Watch(h.stateRoot, out.RunID, 2, false, &buf); err != nil {
		t.Fatal(err)
	}
	var events []execution.RunEvent
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		ev, err := execution.DecodeEvent(line)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
		if ev.Seq <= 2 {
			t.Fatalf("watch --after 2 leaked seq %d", ev.Seq)
		}
	}
	if len(events) == 0 {
		t.Fatal("expected events after seq 2")
	}
}

func TestResultWaitTimeoutMandatory(t *testing.T) {
	_, err := controller.ReadResult(t.TempDir(), "run_missing", true, 0)
	if err == nil {
		t.Fatal("expected error when --wait without timeout")
	}
}

func TestResultWaitTimeoutIsDistinct(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
import "time"
func main() { time.Sleep(5 * time.Second) }
`)
	work := goRunWork(h, "main.go", nil)
	workBytes, _ := json.Marshal(work)
	_ = os.WriteFile(filepath.Join(h.bundleDir, "work.json"), workBytes, 0o600)
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_wait",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(workBytes)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 30000, CancelGraceMS: 100},
	}
	reqBytes, _ := json.Marshal(req)
	spec := filepath.Join(h.dir, "request.json")
	_ = os.WriteFile(spec, reqBytes, 0o600)

	errCh := make(chan error, 1)
	go func() {
		_, err := controller.Run(spec, h.bundleDir, h.stateRoot, controller.Options{})
		errCh <- err
	}()
	runID := waitForRunID(t, h.stateRoot, 15*time.Second)
	_, err := controller.ReadResult(h.stateRoot, runID, true, 50*time.Millisecond)
	if err == nil || !errors.Is(err, controller.ErrWaitTimeout) {
		t.Fatalf("want ErrWaitTimeout, got %v", err)
	}
	// Missing run is not a timeout.
	_, err = controller.ReadResult(h.stateRoot, "run_does_not_exist", false, 0)
	if err == nil || errors.Is(err, controller.ErrWaitTimeout) {
		t.Fatalf("missing run must not be ErrWaitTimeout, got %v", err)
	}
	_, _ = controller.RequestCancel(h.stateRoot, runID)
	_ = <-errCh
}

func TestWatchFollowResultFallback(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
func main() {}
`)
	work := goRunWork(h, "main.go", nil)
	out := h.runWith(work, execution.Policy{DeadlineMS: 60000, CancelGraceMS: 100}, controller.Options{})
	run, err := state.Open(h.stateRoot, out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate crash between result rename and run_terminal: truncate journal
	// after removing the terminal event while keeping result.json.
	events, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[len(events)-1].Kind != execution.KindRunTerminal {
		t.Fatalf("precondition: need run_terminal, got %d events", len(events))
	}
	trimmed := events[:len(events)-1]
	f, err := os.Create(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, ev := range trimmed {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := controller.Watch(h.stateRoot, out.RunID, 0, true, &buf); err != nil {
		t.Fatal(err)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		status string
		reason string
		want   int
	}{
		{execution.StatusSucceeded, execution.ReasonCompleted, controller.ExitOK},
		{execution.StatusFailed, execution.ReasonWorkloadFailed, controller.ExitFailed},
		{execution.StatusFailed, execution.ReasonPlacementUnavailable, controller.ExitPlacementUnavailable},
		{execution.StatusTimedOut, execution.ReasonDeadlineExceeded, controller.ExitTimedOut},
		{execution.StatusCancelled, execution.ReasonCancelRequested, controller.ExitCancelled},
	}
	for _, tc := range cases {
		got := controller.ExitFromResult(execution.Result{
			Status:     tc.status,
			ReasonCode: tc.reason,
		})
		if got != tc.want {
			t.Fatalf("%s/%s: exit %d want %d", tc.status, tc.reason, got, tc.want)
		}
	}
}

func assertNoOrphans(t *testing.T, allocationID string) {
	t.Helper()
	pid, ok := parseAllocPID(allocationID)
	if !ok {
		t.Fatalf("assertNoOrphans: cannot parse allocation_id %q", allocationID)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphan pid %d still alive after deadline cleanup", pid)
}

func parseAllocPID(allocationID string) (int, bool) {
	const prefix = "pid:"
	if !strings.HasPrefix(allocationID, prefix) {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimPrefix(allocationID, prefix))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func pidAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

func hasDiagnostic(res execution.Result, code string) bool {
	for _, d := range res.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func waitForRunID(t *testing.T, stateRoot string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(filepath.Join(stateRoot, "runs"))
		if len(entries) != 1 {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		runID := entries[0].Name()
		run, err := state.Open(stateRoot, runID)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if _, err := os.Stat(run.ControllerPath()); err == nil {
			return runID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run never appeared")
	return ""
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
