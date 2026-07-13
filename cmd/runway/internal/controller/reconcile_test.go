package controller_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend/local"
	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
	"github.com/itsHabib/workbench/cmd/runway/internal/controller"
	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

func TestReconcileControllerLostReceipt(t *testing.T) {
	h := newHarness(t)
	runID, run := seedDeadControllerRun(t, h, true)

	// watch must not invent liveness — follow=false returns without error.
	var buf bytes.Buffer
	if err := controller.Watch(h.stateRoot, runID, 0, false, &buf); err != nil {
		t.Fatal(err)
	}

	out, err := controller.Reconcile(h.stateRoot, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Mutated || out.Result == nil {
		t.Fatalf("want mutated controller_lost, got %+v", out)
	}
	if out.Result.Status != execution.StatusFailed || out.Result.ReasonCode != execution.ReasonControllerLost {
		t.Fatalf("want failed/controller_lost, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseTerminal {
		t.Fatalf("phase=%s", out.Result.TerminalPhase)
	}
	if err := execution.ValidateResult(*out.Result); err != nil {
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
		t.Fatal("Reduce must be terminal")
	}
	// Prior flushed events remain intact (run_accepted at seq 1).
	if events[0].Kind != execution.KindRunAccepted {
		t.Fatalf("first event=%s", events[0].Kind)
	}
	assertNoSecrets(t, run.Root)
}

func TestReconcileAppendsOnlyMissingTerminal(t *testing.T) {
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
	// Crash between result rename and run_terminal.
	events, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	trimmed := events[:len(events)-1]
	rewriteJournal(t, run.EventsPath(), trimmed)
	before := len(trimmed)

	// Kill the claim holder identity so reconcile can take over: rewrite
	// controller.json + claim to a dead owner.
	markControllerDead(t, run)

	rec, err := controller.Reconcile(h.stateRoot, out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Mutated {
		t.Fatal("want mutated append of run_terminal")
	}
	if rec.Result.ReasonCode != out.Result.ReasonCode {
		t.Fatalf("must not rewrite result: got %s want %s", rec.Result.ReasonCode, out.Result.ReasonCode)
	}
	after, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != before+1 {
		t.Fatalf("want exactly one appended event, before=%d after=%d", before, len(after))
	}
	if after[len(after)-1].Kind != execution.KindRunTerminal {
		t.Fatalf("last=%s", after[len(after)-1].Kind)
	}
	raw, err := os.ReadFile(run.ResultPath())
	if err != nil {
		t.Fatal(err)
	}
	var durable execution.Result
	if err := json.Unmarshal(raw, &durable); err != nil {
		t.Fatal(err)
	}
	if durable.ReasonCode != out.Result.ReasonCode {
		t.Fatalf("result rewritten: %s", durable.ReasonCode)
	}
}

func TestReconcileRace_OneWinner(t *testing.T) {
	h := newHarness(t)
	runID, run := seedDeadControllerRun(t, h, true)
	beforeEvents, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	beforeClaim, err := claim.Read(run.PrivateDir())
	if err != nil {
		t.Fatal(err)
	}

	const n = 6
	var (
		wg      sync.WaitGroup
		mutated atomic.Int64
		noop    atomic.Int64
		mu      sync.Mutex
		results []controller.ReconcileOutcome
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			out, err := controller.Reconcile(h.stateRoot, runID)
			if err != nil {
				t.Errorf("reconcile: %v", err)
				return
			}
			mu.Lock()
			results = append(results, out)
			mu.Unlock()
			if out.Mutated {
				mutated.Add(1)
				return
			}
			noop.Add(1)
		}()
	}
	wg.Wait()
	if mutated.Load() != 1 {
		t.Fatalf("want exactly one mutator, got %d (noop=%d)", mutated.Load(), noop.Load())
	}
	afterEvents, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEvents) <= len(beforeEvents) {
		t.Fatalf("journal must grow once: before=%d after=%d", len(beforeEvents), len(afterEvents))
	}
	afterClaim, err := claim.Read(run.PrivateDir())
	if err != nil {
		t.Fatal(err)
	}
	if afterClaim.Generation != beforeClaim.Generation+1 {
		t.Fatalf("claim generation %d → %d", beforeClaim.Generation, afterClaim.Generation)
	}
	res, err := controller.ReadResult(h.stateRoot, runID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.ReasonCode != execution.ReasonControllerLost {
		t.Fatalf("reason=%s", res.ReasonCode)
	}
}

func TestReconcileFailsClosedOnUncertainBackend(t *testing.T) {
	t.Run("live_leader_unkillable", func(t *testing.T) {
		h := newHarness(t)
		runID, run := seedDeadControllerRun(t, h, false)

		ticks, err := claim.StartTicks(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		if ticks == 0 {
			t.Skip("process-start identity unsupported on this GOOS")
		}
		// Live PID + matching start ticks, but pgid 0 so killGroup is a no-op —
		// allocation remains live → uncertain.
		alloc := local.Allocation{PID: os.Getpid(), PGID: 0, StartTicks: ticks}
		data, _ := json.Marshal(alloc)
		if err := os.WriteFile(run.BackendPath(), data, 0o600); err != nil {
			t.Fatal(err)
		}

		out, err := controller.Reconcile(h.stateRoot, runID)
		if err != nil {
			t.Fatal(err)
		}
		assertCleanupFailedDiagnostic(t, out, os.Getpid())
	})

	t.Run("leader_dead_descendants_unknown", func(t *testing.T) {
		h := newHarness(t)
		runID, run := seedDeadControllerRun(t, h, false)

		pgid := deadLeaderProbePGID(t)
		// Dead leader PID with a still-reachable process group — must not
		// report clean (unix: kill(-pgid,0) succeeds ⇒ Uncertain; windows:
		// no group enumeration ⇒ Uncertain).
		alloc := local.Allocation{PID: 999999, PGID: pgid, StartTicks: 1}
		data, _ := json.Marshal(alloc)
		if err := os.WriteFile(run.BackendPath(), data, 0o600); err != nil {
			t.Fatal(err)
		}

		out, err := controller.Reconcile(h.stateRoot, runID)
		if err != nil {
			t.Fatal(err)
		}
		assertCleanupFailedDiagnostic(t, out, 999999)
	})
}

func TestReconcileTerminalJournalWithoutResult(t *testing.T) {
	h := newHarness(t)
	runID, run := seedDeadControllerRun(t, h, true)

	j, err := journal.OpenAppend(run.EventsPath(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(execution.PhaseTerminal, execution.KindRunTerminal, map[string]any{
		"status": "failed",
	}); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
	before, err := os.ReadFile(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}

	_, err = controller.Reconcile(h.stateRoot, runID)
	if err == nil {
		t.Fatal("terminal journal without result must error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("terminal journal without result")) {
		t.Fatalf("want corrupt-state error, got %v", err)
	}
	after, err := os.ReadFile(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reconcile must not mutate journal on corrupt terminal-without-result")
	}
	if _, err := os.Stat(run.ResultPath()); !os.IsNotExist(err) {
		t.Fatal("reconcile must not invent result.json")
	}
}

func assertCleanupFailedDiagnostic(t *testing.T, out controller.ReconcileOutcome, pid int) {
	t.Helper()
	if out.Result == nil || out.Result.ReasonCode != execution.ReasonControllerLost {
		t.Fatalf("want controller_lost, got %+v", out.Result)
	}
	if out.Result.Status != execution.StatusFailed {
		t.Fatalf("must stay failed, got %s", out.Result.Status)
	}
	found := false
	for _, d := range out.Result.Diagnostics {
		if d.Code != execution.ReasonCleanupFailed {
			continue
		}
		found = true
		if d.Details["allocation_id"] != "pid:"+strconv.Itoa(pid) {
			t.Fatalf("diagnostics details=%v", d.Details)
		}
	}
	if !found {
		t.Fatalf("want cleanup_failed diagnostic naming allocation, got %+v", out.Result.Diagnostics)
	}
}

func TestReconcileRefusesLiveController(t *testing.T) {
	h := newHarness(t)
	runID, run := seedDeadControllerRun(t, h, true)
	// Rewrite controller identity to this live process.
	ticks, err := claim.StartTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if ticks == 0 {
		t.Skip("process-start identity unsupported on this GOOS")
	}
	id, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "start_ticks": ticks})
	if err := os.WriteFile(run.ControllerPath(), id, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = controller.Reconcile(h.stateRoot, runID)
	if err == nil {
		t.Fatal("live controller must refuse reconcile")
	}
}

func TestGateB_ControllerLossE2E(t *testing.T) {
	h := newHarness(t)
	h.writeProg("main.go", `package main
import "time"
func main() { time.Sleep(30 * time.Second) }
`)
	work := goRunWork(h, "main.go", nil)
	workBytes, _ := json.Marshal(work)
	_ = os.WriteFile(filepath.Join(h.bundleDir, "work.json"), workBytes, 0o600)
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_gateb_loss",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(workBytes)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 60000, CancelGraceMS: 100},
	}
	reqBytes, _ := json.Marshal(req)
	spec := filepath.Join(h.dir, "request.json")
	_ = os.WriteFile(spec, reqBytes, 0o600)

	bin := buildRunway(t)
	cmd := exec.Command(bin, "run", "--spec", spec, "--bundle", h.bundleDir, "--state", h.stateRoot)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	runID := waitForRunID(t, h.stateRoot, 20*time.Second)
	waitForEventKind(t, h.stateRoot, runID, execution.KindWorkloadStarted, 30*time.Second)

	run, err := state.Open(h.stateRoot, runID)
	if err != nil {
		t.Fatal(err)
	}
	flushed, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(flushed) < 2 {
		t.Fatalf("want flushed pre-kill events, got %d", len(flushed))
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()

	// watch makes no liveness claim — returns the durable prefix only.
	var watchBuf bytes.Buffer
	if err := controller.Watch(h.stateRoot, runID, 0, false, &watchBuf); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ReadResult(h.stateRoot, runID, false, 0); err == nil {
		t.Fatal("result must be absent before reconcile")
	}

	rec, err := controller.Reconcile(h.stateRoot, runID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Result == nil || rec.Result.ReasonCode != execution.ReasonControllerLost {
		t.Fatalf("want controller_lost, got %+v", rec.Result)
	}
	after, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	for i := range flushed {
		if after[i].Seq != flushed[i].Seq || after[i].Kind != flushed[i].Kind {
			t.Fatalf("prior event %d mutated", i)
		}
	}
	if after[len(after)-1].Kind != execution.KindRunTerminal {
		t.Fatalf("last=%s", after[len(after)-1].Kind)
	}
	if rec.Result.Placement.AllocationID != "none" {
		assertNoOrphans(t, rec.Result.Placement.AllocationID)
	}
	assertNoSecrets(t, run.Root)
}

func seedDeadControllerRun(t *testing.T, h *harness, withClaim bool) (string, state.RunDir) {
	t.Helper()
	runID := "run_" + sha256Hex([]byte(t.Name()))[:16]
	run, err := state.Create(h.stateRoot, runID)
	if err != nil {
		t.Fatal(err)
	}
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_dead",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex([]byte(`{}`))},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 1000, CancelGraceMS: 0},
	}
	reqBytes, _ := json.Marshal(req)
	_ = os.WriteFile(run.RequestPath(), reqBytes, 0o600)
	_ = os.WriteFile(run.WorkPath(), []byte(`{}`), 0o600)
	ctrl := []byte(`{"pid":999999,"start_ticks":1}`)
	_ = os.WriteFile(run.ControllerPath(), ctrl, 0o600)
	if withClaim {
		writeClaim(t, run, claim.Owner{PID: 999999, StartTicks: 1, Generation: 1})
	}
	j, err := journal.Create(run.EventsPath(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(execution.PhaseAdmission, execution.KindRunAccepted, map[string]any{"request_id": "req_dead"}); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
	return runID, run
}

func markControllerDead(t *testing.T, run state.RunDir) {
	t.Helper()
	_ = os.WriteFile(run.ControllerPath(), []byte(`{"pid":999999,"start_ticks":1}`), 0o600)
	writeClaim(t, run, claim.Owner{PID: 999999, StartTicks: 1, Generation: 1})
}

func writeClaim(t *testing.T, run state.RunDir, o claim.Owner) {
	t.Helper()
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(run.ClaimPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func rewriteJournal(t *testing.T, path string, events []execution.RunEvent) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func buildRunway(t *testing.T) string {
	t.Helper()
	name := "runway"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	modRoot := string(bytes.TrimSpace(out))
	cmd := exec.Command("go", "build", "-o", bin, "github.com/itsHabib/workbench/cmd/runway")
	cmd.Dir = modRoot
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build runway: %v: %s", err, o)
	}
	return bin
}

func assertNoSecrets(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("SUPER_SECRET_VALUE")) {
			t.Errorf("secret leaked in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
