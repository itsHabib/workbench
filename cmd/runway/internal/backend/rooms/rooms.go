// Package rooms is Runway's Rooms CLI adapter. It imports no Rooms code: one
// placed attempt is one argv invocation plus the rooms-native lifecycle file.
package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
	"github.com/itsHabib/workbench/contracts/execution"
)

const (
	profileAgentCursor = "agent-cursor"
	backendFile        = "backend.json"
	lifecycleFile      = "rooms-lifecycle.ndjson"
)

var allowedSecrets = map[string]struct{}{
	"ANTHROPIC_API_KEY": {},
	"CURSOR_API_KEY":    {},
}

// Backend runs the resolved agent-cursor profile through the Rooms CLI.
type Backend struct{ config Config }

// New returns a Rooms adapter with an already-resolved profile.
func New(config Config) *Backend { return &Backend{config: config} }

// NewFromEnvironment resolves the installed agent-cursor profile.
func NewFromEnvironment() (*Backend, error) {
	config, err := ConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	return New(config), nil
}

// Admit validates the portable work against the resolved profile before the
// controller creates durable run state.
func (b *Backend) Admit(work execution.WorkSpec) error {
	if err := b.config.validate(); err != nil {
		return err
	}
	if err := validateSecrets(work); err != nil {
		return err
	}
	if _, err := taskInput(work); err != nil {
		return err
	}
	if _, err := os.Stat(b.config.Image); err != nil {
		return fmt.Errorf("rooms: profile image: %w", err)
	}
	return nil
}

type handle struct {
	config Config
	cmd    *exec.Cmd
	prep   backend.PreparedRun

	events      <-chan lifecycleItem
	processDone chan struct{}
	stdout      *os.File
	stderr      *os.File
	captureWG   sync.WaitGroup

	mu          sync.Mutex
	processErr  error
	processCode int
	captureErr  error
	pending     *lifecycleRecord
	receipt     execution.PlacementReceipt
	roomID      string
	poolFull    bool
}

// Start validates the profile, launches Rooms, and maps lifecycle transitions
// through workload_started. Wait/Collect/Cleanup consume their later segments;
// the supplied context keeps controller deadline and cancellation policy live
// during allocation and guest startup.
func (b *Backend) Start(ctx context.Context, prep backend.PreparedRun, emit backend.Emit) (backend.Handle, error) {
	if err := b.config.validate(); err != nil {
		return nil, err
	}
	if err := validateSecrets(prep.Work); err != nil {
		return nil, err
	}
	task, err := taskPath(prep)
	if err != nil {
		return nil, err
	}
	imageSHA, err := fileSHA256(b.config.Image)
	if err != nil {
		return nil, fmt.Errorf("rooms: hash profile image: %w", err)
	}
	if prep.Out == "" || prep.PrivateDir == "" {
		return nil, fmt.Errorf("rooms: prepared run is missing out/private roots")
	}
	lifecyclePath := filepath.Join(prep.PrivateDir, lifecycleFile)
	args := b.runArgs(prep, task, lifecyclePath)

	stdout, stderr, stdoutR, stdoutW, stderrR, stderrW, err := openCapture(prep)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(b.config.Launcher, args...)
	cmd.Env = roomsEnv(prep)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		closeAll(stdoutR, stdoutW, stderrR, stderrW, stdout, stderr)
		return nil, fmt.Errorf("rooms: start: %w", err)
	}
	closeAll(stdoutW, stderrW)

	h := &handle{
		config:      b.config,
		cmd:         cmd,
		prep:        prep,
		processDone: make(chan struct{}),
		stdout:      stdout,
		stderr:      stderr,
		receipt: execution.PlacementReceipt{
			Backend:        "rooms",
			Profile:        profileAgentCursor,
			AllocationID:   "none",
			ImageSHA256:    imageSHA,
			StreamDelivery: execution.StreamDeliveryTerminalReplay,
			Enforced: map[string]any{
				"cpu":              1,
				"memory_mib":       256,
				"network":          "egress",
				"rootfs":           "readonly_overlay",
				"secret_transport": "ssh_sendenv",
				"secret_names":     []string{"ANTHROPIC_API_KEY", "CURSOR_API_KEY"},
			},
			Details: map[string]any{"runner": "cursor", "model": b.config.Model},
		},
	}
	h.captureWG.Add(2)
	go capture(stdoutR, stdout, prep.Secrets, h)
	go capture(stderrR, stderr, prep.Secrets, h)
	if err := h.writeDurable(); err != nil {
		_ = killProcessGroup(cmd)
		_ = cmd.Wait()
		h.captureWG.Wait()
		closeAll(stdout, stderr)
		return nil, err
	}
	go h.waitProcess()
	h.events = tailLifecycle(lifecyclePath, h.processDone, b.config.pollInterval())

	if err := emit(execution.PhaseStartup, "placement_profile_resolved", map[string]any{
		"receipt": h.receipt,
	}); err != nil {
		_ = b.Cancel(context.Background(), h)
		_ = b.Cleanup(context.Background(), h)
		return nil, err
	}
	if err := h.awaitStartup(ctx, emit); err != nil {
		if !backend.IsPlacementUnavailable(err) {
			_ = signalProcessGroup(h.cmd)
		}
		_ = b.Cleanup(context.Background(), h)
		return nil, err
	}
	return h, nil
}

// Wait maps Rooms allocation/readiness/workload events and returns at the
// workload boundary. Rooms may still be collecting and cleaning; those
// lifecycle segments are consumed by Collect and Cleanup respectively.
func (b *Backend) Wait(ctx context.Context, bh backend.Handle, emit backend.Emit) (backend.Exit, error) {
	h, err := asHandle(bh)
	if err != nil {
		return backend.Exit{}, err
	}
	for {
		record, err := h.next(ctx)
		if err != nil {
			return backend.Exit{}, err
		}
		switch record.Event {
		case "workload_exited":
			if err := emit(execution.PhaseWorkload, execution.KindWorkloadExited, map[string]any{
				"exit_code": record.ExitCode,
				"status":    record.Status,
			}); err != nil {
				return backend.Exit{Code: record.ExitCode}, err
			}
			return backend.Exit{Code: record.ExitCode}, nil
		case "boot_failed", "guest_unreachable", "workload_failed":
			return backend.Exit{}, fmt.Errorf("rooms: %s: %s", record.Event, record.Error)
		default:
			h.pushBack(record)
			return backend.Exit{}, fmt.Errorf("rooms: workload exited event missing before %s", record.Event)
		}
	}
}

func (h *handle) awaitStartup(ctx context.Context, emit backend.Emit) error {
	for {
		record, err := h.next(ctx)
		if err != nil {
			return err
		}
		switch record.Event {
		case "slot_allocated":
			if err := h.allocated(record, emit); err != nil {
				return err
			}
		case "pool_full":
			h.poolFull = true
			return &backend.PlacementUnavailable{Backend: "rooms", Cap: record.Cap}
		case "vmm_started", "guest_ready":
			if err := emit(execution.PhaseStartup, record.Event, roomsDetails(record)); err != nil {
				return err
			}
		case "ssh_ready":
			if err := emit(execution.PhaseStartup, execution.KindWorkloadReady, roomsDetails(record)); err != nil {
				return err
			}
		case "workload_started":
			return emit(execution.PhaseWorkload, execution.KindWorkloadStarted, roomsDetails(record))
		case "boot_failed", "guest_unreachable", "workload_failed":
			return fmt.Errorf("rooms: %s: %s", record.Event, record.Error)
		default:
			return fmt.Errorf("rooms: workload started event missing before %s", record.Event)
		}
	}
}

// Collect waits for Rooms' host-side `--out` copy. The controller subsequently
// validates and hashes the declared Runway outputs from that same directory.
func (b *Backend) Collect(ctx context.Context, bh backend.Handle, _ string) ([]execution.Artifact, error) {
	h, err := asHandle(bh)
	if err != nil {
		return nil, err
	}
	for {
		record, err := h.next(ctx)
		if err != nil {
			return nil, err
		}
		switch record.Event {
		case "collection_started":
			continue
		case "collection_done":
			return nil, nil
		case "collection_failed":
			return nil, fmt.Errorf("rooms: collection failed: %s", record.Error)
		case "cleanup_done", "cleanup_failed":
			h.pushBack(record)
			return nil, fmt.Errorf("rooms: collection completion event missing")
		case "workload_failed":
			return nil, fmt.Errorf("rooms: workload failed after exit: %s", record.Error)
		}
	}
}

// Cancel asks the Rooms supervisor to terminate. Rooms owns Firecracker/TAP/
// slot teardown and records the verified outcome on the lifecycle stream.
func (b *Backend) Cancel(_ context.Context, bh backend.Handle) error {
	h, err := asHandle(bh)
	if err != nil {
		return err
	}
	return signalProcessGroup(h.cmd)
}

// Cleanup consumes Rooms' verified teardown outcome and joins the supervisor.
func (b *Backend) Cleanup(ctx context.Context, bh backend.Handle) error {
	h, err := asHandle(bh)
	if err != nil {
		return err
	}
	if h.poolFull {
		return h.join(ctx)
	}
	for {
		record, nextErr := h.next(ctx)
		if nextErr != nil {
			_ = killProcessGroup(h.cmd)
			_ = h.join(context.Background())
			return nextErr
		}
		switch record.Event {
		case "cleanup_done":
			return h.join(ctx)
		case "cleanup_failed":
			_ = h.join(ctx)
			return fmt.Errorf("rooms: cleanup failed: %s", record.Error)
		}
	}
}

func (b *Backend) runArgs(prep backend.PreparedRun, task, lifecycle string) []string {
	args := append([]string(nil), b.config.Prefix...)
	return append(args,
		"run",
		"--runner", "cursor",
		"--image", b.config.Image,
		"--repo", prep.Work.Workspace.URL,
		"--base-sha", prep.Work.Workspace.Revision,
		"--task", task,
		"--model", b.config.Model,
		"--out", prep.Out,
		"--lifecycle", lifecycle,
	)
}

func (h *handle) allocated(record lifecycleRecord, emit backend.Emit) error {
	h.roomID = record.RoomID
	h.receipt.AllocationID = record.RoomID
	h.receipt.Details = map[string]any{
		"model":  h.config.Model,
		"runner": "cursor",
		"slot":   record.Slot,
		"tap":    record.Tap,
	}
	if err := h.writeDurable(); err != nil {
		return err
	}
	return emit(execution.PhaseStartup, execution.KindPlacementAllocated, map[string]any{
		"allocation_id": record.RoomID,
		"receipt":       h.receipt,
	})
}

func (h *handle) next(ctx context.Context) (lifecycleRecord, error) {
	h.mu.Lock()
	if h.pending != nil {
		record := *h.pending
		h.pending = nil
		h.mu.Unlock()
		return record, nil
	}
	h.mu.Unlock()
	select {
	case <-ctx.Done():
		return lifecycleRecord{}, ctx.Err()
	case item, ok := <-h.events:
		if !ok {
			h.mu.Lock()
			err := h.processErr
			code := h.processCode
			h.mu.Unlock()
			if err != nil {
				return lifecycleRecord{}, fmt.Errorf("rooms: lifecycle ended with process exit %d: %w", code, err)
			}
			return lifecycleRecord{}, fmt.Errorf("rooms: lifecycle ended before expected transition")
		}
		if item.err != nil {
			return lifecycleRecord{}, item.err
		}
		return item.record, nil
	}
}

func (h *handle) pushBack(record lifecycleRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending = &record
}

func (h *handle) waitProcess() {
	err := h.cmd.Wait()
	h.captureWG.Wait()
	closeAll(h.stdout, h.stderr)
	code := 0
	if err != nil {
		code = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	h.mu.Lock()
	h.processErr = err
	h.processCode = code
	h.mu.Unlock()
	close(h.processDone)
}

func (h *handle) join(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.processDone:
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.captureErr != nil {
		return fmt.Errorf("rooms: capture: %w", h.captureErr)
	}
	return nil
}

func taskPath(prep backend.PreparedRun) (string, error) {
	input, err := taskInput(prep.Work)
	if err != nil {
		return "", err
	}
	path := filepath.Join(prep.Inputs, filepath.FromSlash(input.Target))
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("rooms: task input %q: %w", input.Target, err)
	}
	return path, nil
}

func taskInput(work execution.WorkSpec) (execution.Input, error) {
	for _, input := range work.Inputs {
		if input.Name == "task" {
			return input, nil
		}
	}
	return execution.Input{}, fmt.Errorf("rooms: agent-cursor profile requires an input named %q", "task")
}

func validateSecrets(work execution.WorkSpec) error {
	for _, secret := range work.Secrets {
		if _, ok := allowedSecrets[secret.Name]; ok {
			continue
		}
		return fmt.Errorf("rooms: agent-cursor secret %q is not in the SSH SendEnv allowlist", secret.Name)
	}
	return nil
}

func roomsEnv(prep backend.PreparedRun) []string {
	values := envMap(prep.Env)
	keep := []string{"HOME", "LOGNAME", "PATH", "ROOMS_MAX_POOL", "RUST_LOG", "TEMP", "TMP", "TMPDIR", "USER"}
	for _, secret := range prep.Work.Secrets {
		keep = append(keep, secret.Name)
	}
	out := make([]string, 0, len(keep))
	seen := map[string]struct{}{}
	for _, name := range keep {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if value, ok := values[name]; ok {
			out = append(out, name+"="+value)
		}
	}
	return out
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			out[name] = value
		}
	}
	return out
}

func roomsDetails(record lifecycleRecord) map[string]any {
	details := map[string]any{"room_id": record.RoomID, "rooms_seq": record.Seq}
	if record.PID != nil {
		details["pid"] = *record.PID
	}
	if len(record.Command) > 0 {
		details["command"] = record.Command
	}
	return details
}

func fileSHA256(path string) (string, error) {
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

func openCapture(prep backend.PreparedRun) (*os.File, *os.File, *os.File, *os.File, *os.File, *os.File, error) {
	stdout, err := os.OpenFile(prep.StdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("rooms: open stdout log: %w", err)
	}
	stderr, err := os.OpenFile(prep.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		closeAll(stdout)
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("rooms: open stderr log: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdout, stderr)
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("rooms: stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdoutR, stdoutW, stdout, stderr)
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("rooms: stderr pipe: %w", err)
	}
	return stdout, stderr, stdoutR, stdoutW, stderrR, stderrW, nil
}

func capture(r *os.File, w io.Writer, secrets [][]byte, h *handle) {
	defer h.captureWG.Done()
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

func asHandle(bh backend.Handle) (*handle, error) {
	h, ok := bh.(*handle)
	if !ok || h == nil {
		return nil, fmt.Errorf("rooms: invalid handle")
	}
	return h, nil
}

func closeAll(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}

// Allocation is the durable Rooms cleanup handle. It contains no argv or env.
type Allocation struct {
	Backend    string                     `json:"backend"`
	PID        int                        `json:"pid"`
	PGID       int                        `json:"pgid"`
	StartTicks uint64                     `json:"start_ticks"`
	RoomID     string                     `json:"room_id,omitempty"`
	Receipt    execution.PlacementReceipt `json:"receipt"`
}

func (h *handle) writeDurable() error {
	pgid, err := processGroupID(h.cmd)
	if err != nil {
		return fmt.Errorf("rooms: process group: %w", err)
	}
	ticks, err := claim.StartTicks(h.cmd.Process.Pid)
	if err != nil {
		ticks = 0
	}
	allocation := Allocation{
		Backend:    "rooms",
		PID:        h.cmd.Process.Pid,
		PGID:       pgid,
		StartTicks: ticks,
		RoomID:     h.roomID,
		Receipt:    h.receipt,
	}
	data, err := json.Marshal(allocation)
	if err != nil {
		return fmt.Errorf("rooms: encode backend.json: %w", err)
	}
	path := filepath.Join(h.prep.PrivateDir, backendFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("rooms: write backend.json temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rooms: publish backend.json: %w", err)
	}
	return nil
}
