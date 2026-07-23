// Package controller is the Runway lifecycle POLICY layer: phase transitions,
// absolute deadline, idempotent cancellation, collection/cleanup ordering
// (D7), and the atomic terminal receipt. Backends stay dumb mechanisms.
package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/backend/install"
	"github.com/itsHabib/workbench/cmd/runway/internal/bundle"
	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
	"github.com/itsHabib/workbench/cmd/runway/internal/expand"
	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

// Outcome is one finished foreground run: the validated receipt and §6 exit.
type Outcome struct {
	RunID    string
	Result   execution.Result
	ExitCode int
}

// Options configures a foreground controller run. Zero value uses the local
// backend. Tests inject Backend to force cleanup isolation failures.
type Options struct {
	Backend backend.Backend
}

// Run executes one admitted request to a single terminal receipt (Flows A–F).
// Admission failures before a run directory exists return ExitUsage with no
// Outcome. The controller acquires the per-run writer claim before any
// durable mutation beyond the empty run directory.
func Run(specPath, bundleDir, stateRoot string, opts Options) (Outcome, error) {
	adm, err := bundle.Admit(specPath, bundleDir)
	if err != nil {
		return Outcome{}, usageErr(err)
	}
	be := opts.Backend
	if be == nil {
		be, err = install.Resolve(adm.Request.Placement)
		if err != nil {
			return Outcome{}, usageErr(err)
		}
	}
	if admitter, ok := be.(backend.Admitter); ok {
		if err := admitter.Admit(adm.Work); err != nil {
			return Outcome{}, usageErr(err)
		}
	}
	if err := requireCustodySupport(be, adm.Work.Secrets); err != nil {
		return Outcome{}, usageErr(err)
	}
	runID, err := mintRunID()
	if err != nil {
		return Outcome{}, err
	}
	run, err := state.Create(stateRoot, runID)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := claim.Acquire(run.PrivateDir()); err != nil {
		// Another writer already owns this run id — refuse without further
		// mutation (request/identity/journal stay unwritten).
		return Outcome{}, usageErr(fmt.Errorf("controller: acquire writer claim: %w", err))
	}
	if err := os.WriteFile(run.RequestPath(), adm.RequestBytes, 0o600); err != nil {
		return Outcome{}, fmt.Errorf("controller: write request.json: %w", err)
	}
	if _, err := writeIdentity(run); err != nil {
		return Outcome{}, err
	}

	j, err := journal.Create(run.EventsPath(), runID)
	if err != nil {
		return Outcome{}, err
	}
	defer j.Close()

	c := &ctrl{
		run:      run,
		runID:    runID,
		adm:      adm,
		j:        j,
		started:  time.Now().UTC(),
		deadline: time.Now().Add(time.Duration(adm.Request.Policy.DeadlineMS) * time.Millisecond),
		grace:    time.Duration(adm.Request.Policy.CancelGraceMS) * time.Millisecond,
		be:       be,
		gate:     &terminalGate{},
	}
	return c.execute()
}

type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usageErr(err error) error { return usageError{err: err} }

// IsUsage reports whether err is an admission/CLI usage failure (exit 2).
func IsUsage(err error) bool {
	var u usageError
	return errors.As(err, &u)
}

type ctrl struct {
	run      state.RunDir
	runID    string
	adm      bundle.Admitted
	j        *journal.Writer
	started  time.Time
	deadline time.Time
	grace    time.Duration
	be       backend.Backend
	gate     *terminalGate

	allocID string
	receipt execution.PlacementReceipt

	// authorityRecords holds the backend's opaque custody derive records between
	// placement and collection so the room-authority receipt can be assembled at
	// finalize (grant-materialized rooms §5). Nil for env:-only runs.
	authorityRecords any

	emitMu sync.Mutex
	phase  string // last canonical phase progressed into (emitMu)
}

// emit serializes every journal write and phase advance. Backend Wait and the
// controller interrupt path may call this concurrently; the lock is the sole
// funnel so seq/NDJSON stay contiguous and phase reads are coherent.
func (c *ctrl) emit(phase, kind string, details map[string]any) error {
	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	if receipt, ok := details["receipt"].(execution.PlacementReceipt); ok {
		c.receipt = receipt
	}
	if kind == execution.KindPlacementAllocated {
		if id, ok := details["allocation_id"].(string); ok {
			c.allocID = id
		}
	}
	c.phase = phase
	_, err := c.j.Append(phase, kind, details)
	return err
}

func (c *ctrl) currentPhase() string {
	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	return c.phase
}

func (c *ctrl) execute() (Outcome, error) {
	stopSignals := ignoreCancelSignal()
	defer stopSignals()

	if err := c.emit(execution.PhaseAdmission, execution.KindRunAccepted, map[string]any{
		"request_id": c.adm.Request.RequestID,
	}); err != nil {
		return Outcome{}, err
	}

	// Absolute deadline is armed before preparation (Flow C / FR9).
	if out, done, err := c.prepare(); done {
		return out, err
	}
	if c.deadlineExceeded() {
		return c.failEarly(execution.PhasePreparation, execution.ReasonDeadlineExceeded, fmt.Errorf("deadline exceeded during preparation"))
	}

	h, out, done, err := c.startUnderDeadline()
	if done {
		return out, err
	}
	return c.runWorkload(h)
}

type startResult struct {
	h   backend.Handle
	err error
}

// startUnderDeadline runs backend startup under the absolute deadline and
// cancel marker, mirroring prepare(): a hung placement/boot cannot keep the
// run open past policy.deadline_ms, and a cancel written during expansion or
// backend start is honored before the workload phase. On interrupt it joins
// the Start goroutine and cleans up any handle it produced.
func (c *ctrl) startUnderDeadline() (backend.Handle, Outcome, bool, error) {
	watchDone := make(chan struct{})
	defer close(watchDone)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startCh := make(chan startResult, 1)
	go func() {
		h, err := c.startBackend(ctx)
		startCh <- startResult{h: h, err: err}
	}()

	deadlineCh := time.After(time.Until(c.deadline))
	cancelCh := c.watchCancel(watchDone)
	select {
	case sr := <-startCh:
		if sr.err != nil {
			if backend.IsPlacementUnavailable(sr.err) {
				out, err := c.failEarly(execution.PhaseStartup, execution.ReasonPlacementUnavailable, sr.err)
				return nil, out, true, err
			}
			if u, ok := backend.AsAuthorityUnresolved(sr.err); ok {
				out, err := c.failAuthorityUnresolved(u)
				return nil, out, true, err
			}
			out, err := c.failEarly(execution.PhaseStartup, execution.ReasonStartupFailed, sr.err)
			return nil, out, true, err
		}
		return sr.h, Outcome{}, false, nil
	case <-deadlineCh:
		cancel()
		return c.interruptedStart(startCh, execution.ReasonDeadlineExceeded)
	case <-cancelCh:
		cancel()
		return c.interruptedStart(startCh, execution.ReasonCancelRequested)
	}
}

// interruptedStart joins an interrupted Start. If Start failed, the interrupt
// reason wins the receipt (still in the startup phase). If Start succeeded —
// it may already have journaled workload_started — the handle flows into the
// normal workload path, whose await loop observes the already-fired deadline
// or cancel marker immediately; emitting a startup-phase failure here would
// regress the journal's phase order. The join is prompt for the local
// backend's bounded spawn; a future placement backend must keep Start
// interruptible for this to hold.
func (c *ctrl) interruptedStart(startCh <-chan startResult, reason string) (backend.Handle, Outcome, bool, error) {
	sr := <-startCh
	if sr.err != nil {
		out, err := c.failEarly(execution.PhaseStartup, reason, fmt.Errorf("%s during startup: start also failed: %w", reason, sr.err))
		return nil, out, true, err
	}
	return sr.h, Outcome{}, false, nil
}

// prepare materializes the bundle under the absolute deadline and cancel
// marker. A hung git clone/checkout cannot run past policy.deadline_ms.
// On interrupt, prepare cancels Materialize's context (killing in-flight
// git via CommandContext) and joins the goroutine before failEarly so no
// writer remains in the run dir (DESIGN.md).
func (c *ctrl) prepare() (Outcome, bool, error) {
	done := make(chan struct{})
	defer close(done)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- bundle.Materialize(ctx, c.adm, c.run)
	}()

	deadlineCh := time.After(time.Until(c.deadline))
	cancelCh := c.watchCancel(done)
	select {
	case err := <-errCh:
		if err != nil {
			out, e := c.failEarly(execution.PhasePreparation, execution.ReasonPreparationFailed, err)
			return out, true, e
		}
		if err := c.emit(execution.PhasePreparation, "inputs_materialized", map[string]any{
			"inputs": len(c.adm.Work.Inputs),
		}); err != nil {
			return Outcome{}, true, err
		}
		return Outcome{}, false, nil
	case <-deadlineCh:
		cancel()
		<-errCh // join: git dies under CommandContext; no orphan writer
		out, err := c.failEarly(execution.PhasePreparation, execution.ReasonDeadlineExceeded, fmt.Errorf("deadline exceeded during preparation"))
		return out, true, err
	case <-cancelCh:
		cancel()
		<-errCh // join: git dies under CommandContext; no orphan writer
		out, err := c.failEarly(execution.PhasePreparation, execution.ReasonCancelRequested, fmt.Errorf("cancel requested during preparation"))
		return out, true, err
	}
}

func (c *ctrl) startBackend(ctx context.Context) (backend.Handle, error) {
	roots := expand.NewRoots(c.run.WorkspaceDir(), c.run.InputsDir(), c.run.ArtifactsDir())
	prep, err := expand.Command(roots, c.adm.Work)
	if err != nil {
		return nil, err
	}
	secrets, secretBytes, custody, err := resolveSecrets(c.adm.Work.Secrets)
	if err != nil {
		return nil, err
	}
	if len(custody) > 0 {
		resolution, err := c.resolveCustody(ctx, custody)
		if err != nil {
			return nil, err
		}
		for k, v := range resolution.Env {
			secrets[k] = v
		}
		secretBytes = append(secretBytes, resolution.Redact...)
		c.authorityRecords = resolution.Records
	}
	childEnv := mergeEnv(os.Environ(), prep.Env, secrets)
	return c.be.Start(ctx, backend.PreparedRun{
		RunID:      c.runID,
		Work:       c.adm.Work,
		Cwd:        prep.Cwd,
		Argv:       prep.Argv,
		Env:        childEnv,
		Workspace:  roots.Workspace,
		Inputs:     roots.Inputs,
		Out:        roots.Out,
		StdoutPath: c.run.StdoutLog(),
		StderrPath: c.run.StderrLog(),
		Secrets:    secretBytes,
		PrivateDir: c.run.PrivateDir(),
	}, c.emit)
}

// resolveCustody hands the run's custody: refs to the backend's resolver. The
// admission gate (requireCustodySupport) guarantees the capability is present;
// a backend that lost it between admission and start is still handled closed.
func (c *ctrl) resolveCustody(ctx context.Context, custody []execution.Secret) (backend.CustodyResolution, error) {
	resolver, ok := c.be.(backend.CustodyResolver)
	if !ok {
		return backend.CustodyResolution{}, fmt.Errorf("controller: placement backend has no custody resolver (code: authority_unsupported)")
	}
	return resolver.ResolveCustody(ctx, backend.CustodyRequest{
		Secrets:    custody,
		Deadline:   c.deadline,
		Grace:      c.grace,
		Now:        time.Now(),
		PrivateDir: c.run.PrivateDir(),
	})
}

type waitOutcome struct {
	exit          backend.Exit
	err           error
	intent        string // "", deadline_exceeded, cancel_requested
	terminalPhase string // canonical phase captured at interrupt intent
}

func (c *ctrl) runWorkload(h backend.Handle) (Outcome, error) {
	waitCh := make(chan waitOutcome, 1)
	go func() {
		exit, err := c.be.Wait(context.Background(), h, c.emit)
		waitCh <- waitOutcome{exit: exit, err: err}
	}()

	wo := c.awaitWorkload(h, waitCh)
	if backend.IsPlacementUnavailable(wo.err) {
		_ = c.be.Cleanup(context.Background(), h)
		return c.failEarly(execution.PhaseStartup, execution.ReasonPlacementUnavailable, wo.err)
	}
	if wo.err != nil && wo.intent == "" {
		_ = c.be.Cleanup(context.Background(), h)
		phase := c.currentPhase()
		if phase == "" || phase == execution.PhaseStartup {
			return c.failEarly(execution.PhaseStartup, execution.ReasonStartupFailed, wo.err)
		}
		return c.failEarly(execution.PhaseWorkload, execution.ReasonWorkloadFailed, wo.err)
	}
	return c.finalize(h, wo)
}

func (c *ctrl) awaitWorkload(h backend.Handle, waitCh <-chan waitOutcome) waitOutcome {
	done := make(chan struct{})
	defer close(done)
	deadlineCh := time.After(time.Until(c.deadline))
	cancelCh := c.watchCancel(done)
	for {
		select {
		case wo := <-waitCh:
			return wo
		case <-deadlineCh:
			return c.interrupt(h, waitCh, execution.ReasonDeadlineExceeded)
		case <-cancelCh:
			return c.interrupt(h, waitCh, execution.ReasonCancelRequested)
		}
	}
}

func (c *ctrl) interrupt(h backend.Handle, waitCh <-chan waitOutcome, reason string) waitOutcome {
	_ = c.emit(execution.PhaseWorkload, reason, map[string]any{"reason_code": reason})
	phase := c.currentPhase()
	_ = c.be.Cancel(context.Background(), h)
	timer := time.NewTimer(c.grace)
	defer timer.Stop()
	select {
	case wo := <-waitCh:
		wo.intent = reason
		wo.terminalPhase = phase
		return wo
	case <-timer.C:
		_ = c.be.Cleanup(context.Background(), h)
		wo := <-waitCh
		wo.intent = reason
		wo.terminalPhase = phase
		return wo
	}
}

// cancelPollInterval is how often the controller re-checks the cancel-request
// marker. The marker is authoritative; SIGUSR1 (unix) is only a best-effort
// wake. On Windows there is no wake signal, so observed cancel latency is
// bounded by this interval.
const cancelPollInterval = 50 * time.Millisecond

// watchCancel polls the cancel-request marker until it appears or done closes.
func (c *ctrl) watchCancel(done <-chan struct{}) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(cancelPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if !cancelRequested(c.run) {
					continue
				}
				ch <- struct{}{}
				return
			}
		}
	}()
	return ch
}

func (c *ctrl) finalize(h backend.Handle, wo waitOutcome) (Outcome, error) {
	exitCode := wo.exit.Code
	backendArts, backendCollectErr := c.be.Collect(context.Background(), h, c.run.ArtifactsDir())
	outputArts, outputErr := collectOutputs(c.run.ArtifactsDir(), c.adm.Work.Outputs)
	arts := append(backendArts, outputArts...)
	collectErr := errors.Join(backendCollectErr, outputErr)
	if collectErr == nil {
		for _, a := range arts {
			_ = c.emit(execution.PhaseCollection, execution.KindArtifactCollected, map[string]any{
				"name": a.Name, "path": a.Path, "sha256": a.SHA256, "size": a.Size,
			})
		}
	}
	if collectErr != nil {
		_ = c.emit(execution.PhaseCollection, execution.ReasonCollectionFailed, map[string]any{
			"error": collectErr.Error(),
		})
	}

	cleanupErr := c.be.Cleanup(context.Background(), h)
	if cleanupErr == nil {
		_ = c.emit(execution.PhaseCleanup, execution.KindCleanupCompleted, nil)
	}

	arts = c.appendAuthorityReceipt(arts, cleanupErr)
	res := c.buildResult(exitCode, arts, wo, collectErr, cleanupErr)
	committed, _, err := c.gate.commit(c.run, c.j, res)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RunID: c.runID, Result: committed, ExitCode: ExitFromResult(committed)}, nil
}

// appendAuthorityReceipt assembles and names the room-authority receipt when the
// run carried custody: refs and the backend can author one. Teardown status
// follows the cleanup outcome (destroyed on success, failed otherwise, §5). A
// receipt failure is journaled but never masks the workload's own terminal
// truth — the receipt is evidence, not a gate.
func (c *ctrl) appendAuthorityReceipt(arts []execution.Artifact, cleanupErr error) []execution.Artifact {
	if c.authorityRecords == nil {
		return arts
	}
	receipter, ok := c.be.(backend.AuthorityReceipter)
	if !ok {
		return arts
	}
	art, err := receipter.AssembleAuthorityReceipt(c.authorityRecords, backend.AuthorityReceiptInputs{
		RunID:        c.runID,
		AllocationID: c.allocID,
		ArtifactsDir: c.run.ArtifactsDir(),
		Artifacts:    arts,
		TeardownOK:   cleanupErr == nil,
		// TeardownAt is the wall clock at this single finalize call. Assembly is a
		// pure function of it, so ONE run writes one stable line — but a re-run of
		// collection (reconcile, §7 F) would pick a new "now" and break byte
		// idempotency. When reconcile re-assembly lands, this must be sourced from
		// durable run state (persisted at teardown, read back), not time.Now.
		TeardownAt: time.Now().UTC(),
	})
	if err != nil {
		// Emit at the cleanup phase, not collection: cleanup_completed already
		// advanced the journal to cleanup, and a collection-phase event here would
		// regress phase order (execution terminal law). The error is surfaced, not
		// swallowed — it just must not rewind the phase.
		_ = c.emit(execution.PhaseCleanup, "authority_receipt_failed", map[string]any{"error": err.Error()})
		return arts
	}
	return append(arts, art)
}

func (c *ctrl) buildResult(exitCode int, arts []execution.Artifact, wo waitOutcome, collectErr, cleanupErr error) execution.Result {
	ended := time.Now().UTC()
	code := int64(exitCode)
	res := execution.Result{
		SchemaVersion:    execution.SchemaVersion,
		RunID:            c.runID,
		RequestID:        c.adm.Request.RequestID,
		RequestSHA256:    sha256Hex(c.adm.RequestBytes),
		WorkSHA256:       sha256Hex(c.adm.WorkBytes),
		StartedAt:        c.started.Format(time.RFC3339Nano),
		EndedAt:          ended.Format(time.RFC3339Nano),
		WorkloadExitCode: &code,
		Placement:        c.placementReceipt(),
		Causes:           []execution.Cause{},
		Diagnostics:      []execution.Diagnostic{},
		Artifacts:        arts,
	}
	if arts == nil {
		res.Artifacts = []execution.Artifact{}
	}
	phase := wo.terminalPhase
	if phase == "" {
		phase = c.currentPhase()
	}
	applyTerminalTruth(&res, exitCode, wo.intent, collectErr, cleanupErr, phase)
	return res
}

func applyTerminalTruth(res *execution.Result, exitCode int, intent string, collectErr, cleanupErr error, phase string) {
	attachCollectDiag := func() {
		if collectErr == nil {
			return
		}
		res.Diagnostics = append(res.Diagnostics, execution.Diagnostic{
			Code:    execution.ReasonCollectionFailed,
			Message: collectErr.Error(),
		})
	}
	// The escalation branches carry WHY cleanup failed in the receipt, not
	// only in the journal — collectErr gets the same treatment above.
	attachCleanupDiag := func() {
		if cleanupErr == nil {
			return
		}
		res.Diagnostics = append(res.Diagnostics, execution.Diagnostic{
			Code:    execution.ReasonCleanupFailed,
			Message: cleanupErr.Error(),
		})
	}

	// Isolation truth is global (TDD §8): cleanup failure always escalates,
	// including after cancel — cancelled+cleanupErr becomes failed/cleanup.
	if intent == execution.ReasonDeadlineExceeded && cleanupErr != nil {
		res.Status = execution.StatusFailed
		res.TerminalPhase = execution.PhaseCleanup
		res.ReasonCode = execution.ReasonCleanupFailed
		res.Causes = []execution.Cause{{
			Phase:      execution.PhaseWorkload,
			ReasonCode: execution.ReasonDeadlineExceeded,
		}}
		attachCollectDiag()
		attachCleanupDiag()
		return
	}
	if intent == execution.ReasonCancelRequested && cleanupErr != nil {
		res.Status = execution.StatusFailed
		res.TerminalPhase = execution.PhaseCleanup
		res.ReasonCode = execution.ReasonCleanupFailed
		res.Causes = []execution.Cause{{
			Phase:      execution.PhaseWorkload,
			ReasonCode: execution.ReasonCancelRequested,
		}}
		attachCollectDiag()
		attachCleanupDiag()
		return
	}
	if intent == execution.ReasonDeadlineExceeded {
		res.Status = execution.StatusTimedOut
		res.TerminalPhase = phaseOr(phase, execution.PhaseWorkload)
		res.ReasonCode = execution.ReasonDeadlineExceeded
		attachCollectDiag()
		return
	}
	if intent == execution.ReasonCancelRequested {
		res.Status = execution.StatusCancelled
		res.TerminalPhase = phaseOr(phase, execution.PhaseWorkload)
		res.ReasonCode = execution.ReasonCancelRequested
		attachCollectDiag()
		return
	}
	if collectErr != nil {
		res.Status = execution.StatusFailed
		res.TerminalPhase = execution.PhaseCollection
		res.ReasonCode = execution.ReasonCollectionFailed
		return
	}
	if cleanupErr != nil {
		res.Status = execution.StatusFailed
		res.TerminalPhase = execution.PhaseCleanup
		res.ReasonCode = execution.ReasonCleanupFailed
		return
	}
	if exitCode != 0 {
		res.Status = execution.StatusFailed
		res.TerminalPhase = execution.PhaseWorkload
		res.ReasonCode = execution.ReasonWorkloadFailed
		return
	}
	res.Status = execution.StatusSucceeded
	res.TerminalPhase = execution.PhaseTerminal
	res.ReasonCode = execution.ReasonCompleted
}

func phaseOr(phase, fallback string) string {
	if phase == "" {
		return fallback
	}
	return phase
}

func (c *ctrl) placementReceipt() execution.PlacementReceipt {
	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	if c.receipt.Backend != "" {
		receipt := c.receipt
		receipt.Backend = c.adm.Request.Placement.Backend
		receipt.Profile = c.adm.Request.Placement.Profile
		if c.allocID != "" {
			receipt.AllocationID = c.allocID
		}
		return receipt
	}
	alloc := c.allocID
	if alloc == "" {
		alloc = "none"
	}
	return execution.PlacementReceipt{
		Backend:        c.adm.Request.Placement.Backend,
		Profile:        c.adm.Request.Placement.Profile,
		AllocationID:   alloc,
		StreamDelivery: execution.StreamDeliveryNone,
	}
}

func (c *ctrl) failEarly(phase, reason string, cause error) (Outcome, error) {
	_ = c.emit(phase, reason, map[string]any{"error": cause.Error()})
	ended := time.Now().UTC()
	res := execution.Result{
		SchemaVersion: execution.SchemaVersion,
		RunID:         c.runID,
		RequestID:     c.adm.Request.RequestID,
		RequestSHA256: sha256Hex(c.adm.RequestBytes),
		WorkSHA256:    sha256Hex(c.adm.WorkBytes),
		Status:        execution.StatusFailed,
		TerminalPhase: phase,
		ReasonCode:    reason,
		StartedAt:     c.started.Format(time.RFC3339Nano),
		EndedAt:       ended.Format(time.RFC3339Nano),
		Placement:     c.placementReceipt(),
		Causes:        []execution.Cause{},
		Diagnostics: []execution.Diagnostic{{
			Code:    reason,
			Message: cause.Error(),
		}},
		Artifacts: []execution.Artifact{},
	}
	if reason == execution.ReasonDeadlineExceeded {
		res.Status = execution.StatusTimedOut
		res.TerminalPhase = phase
	}
	if reason == execution.ReasonCancelRequested {
		res.Status = execution.StatusCancelled
		res.TerminalPhase = phase
	}
	committed, _, err := c.gate.commit(c.run, c.j, res)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RunID: c.runID, Result: committed, ExitCode: ExitFromResult(committed)}, nil
}

// failAuthorityUnresolved commits the placement refusal for an unresolvable
// custody: ref (grant-materialized rooms §7 B): a startup-phase failure carrying
// reason_code authority_unresolved and the exact `custody grant` remedy in the
// diagnostics, so the driver can park the stream for a human to mint.
func (c *ctrl) failAuthorityUnresolved(u *backend.AuthorityUnresolved) (Outcome, error) {
	_ = c.emit(execution.PhaseStartup, execution.ReasonAuthorityUnresolved, map[string]any{
		"error":  u.Error(),
		"ref":    u.Ref,
		"remedy": u.Remedy,
	})
	ended := time.Now().UTC()
	res := execution.Result{
		SchemaVersion: execution.SchemaVersion,
		RunID:         c.runID,
		RequestID:     c.adm.Request.RequestID,
		RequestSHA256: sha256Hex(c.adm.RequestBytes),
		WorkSHA256:    sha256Hex(c.adm.WorkBytes),
		Status:        execution.StatusFailed,
		TerminalPhase: execution.PhaseStartup,
		ReasonCode:    execution.ReasonAuthorityUnresolved,
		StartedAt:     c.started.Format(time.RFC3339Nano),
		EndedAt:       ended.Format(time.RFC3339Nano),
		Placement:     c.placementReceipt(),
		Causes:        []execution.Cause{},
		Diagnostics: []execution.Diagnostic{{
			Code:    execution.ReasonAuthorityUnresolved,
			Message: u.Reason,
			Details: map[string]any{"ref": u.Ref, "remedy": u.Remedy},
		}},
		Artifacts: []execution.Artifact{},
	}
	committed, _, err := c.gate.commit(c.run, c.j, res)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RunID: c.runID, Result: committed, ExitCode: ExitFromResult(committed)}, nil
}

func (c *ctrl) deadlineExceeded() bool {
	return !time.Now().Before(c.deadline)
}

func mintRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("controller: mint run id: %w", err)
	}
	return "run_" + hex.EncodeToString(b[:]), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// resolveSecrets expands env: refs into the child environment and passes
// custody: refs THROUGH to the backend untouched — no value expansion at the
// controller (grant-materialized rooms §4). The returned custody slice is the
// backend's to resolve via its CustodyResolver capability. Any other scheme is
// refused. env: behavior is unchanged.
func resolveSecrets(secrets []execution.Secret) (map[string]string, [][]byte, []execution.Secret, error) {
	out := make(map[string]string, len(secrets))
	vals := make([][]byte, 0, len(secrets))
	var custody []execution.Secret
	for i, s := range secrets {
		if strings.HasPrefix(s.Ref, "custody:") {
			custody = append(custody, s)
			continue
		}
		name, ok := strings.CutPrefix(s.Ref, "env:")
		if !ok {
			return nil, nil, nil, fmt.Errorf("controller: secrets[%d].ref %q is not env:NAME or custody:<key>/<actions>", i, s.Ref)
		}
		v, ok := os.LookupEnv(name)
		if !ok {
			return nil, nil, nil, fmt.Errorf("controller: secret env %q is unset", name)
		}
		out[s.Name] = v
		vals = append(vals, []byte(v))
	}
	return out, vals, custody, nil
}

// hasCustodySecret reports whether any ref is a custody: ref.
func hasCustodySecret(secrets []execution.Secret) bool {
	for _, s := range secrets {
		if strings.HasPrefix(s.Ref, "custody:") {
			return true
		}
	}
	return false
}

// requireCustodySupport refuses at admission when a custody: ref is placed onto
// a backend with no custody resolver: the ref cannot be honored and must not
// fall back to a raw secret (coded authority_unsupported).
func requireCustodySupport(be backend.Backend, secrets []execution.Secret) error {
	if !hasCustodySecret(secrets) {
		return nil
	}
	if _, ok := be.(backend.CustodyResolver); ok {
		return nil
	}
	return fmt.Errorf("controller: placement backend has no custody resolver (code: authority_unsupported); custody: secret refs cannot be honored here")
}

func mergeEnv(base []string, roots map[string]string, secrets map[string]string) []string {
	index := map[string]int{}
	out := make([]string, 0, len(base)+len(roots)+len(secrets))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		index[k] = len(out)
		out = append(out, kv)
	}
	set := func(k, v string) {
		entry := k + "=" + v
		if i, ok := index[k]; ok {
			out[i] = entry
			return
		}
		index[k] = len(out)
		out = append(out, entry)
	}
	for k, v := range roots {
		set(k, v)
	}
	for k, v := range secrets {
		set(k, v)
	}
	return out
}

// AbsStateRoot canonicalizes the state root so RUNWAY_* env and expanded
// paths remain absolute while the child runs with cwd=workspace.
func AbsStateRoot(stateDir string) (string, error) {
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return "", fmt.Errorf("controller: resolve state root: %w", err)
	}
	return abs, nil
}
