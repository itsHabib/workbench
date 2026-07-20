package rooms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/contracts/execution"
)

func TestLifecycleMapsAcrossBackendMethods(t *testing.T) {
	be, prep := helperBackend(t, "success")
	var phases []string
	var kinds []string
	var receipt execution.PlacementReceipt
	emit := func(phase, kind string, details map[string]any) error {
		phases = append(phases, phase)
		kinds = append(kinds, kind)
		if got, ok := details["receipt"].(execution.PlacementReceipt); ok {
			receipt = got
		}
		return nil
	}

	h, err := be.Start(context.Background(), prep, emit)
	if err != nil {
		t.Fatal(err)
	}
	exit, err := be.Wait(context.Background(), h, emit)
	if err != nil {
		t.Fatal(err)
	}
	if exit.Code != 0 {
		t.Fatalf("exit=%d", exit.Code)
	}
	if _, err := be.Collect(context.Background(), h, prep.Out); err != nil {
		t.Fatal(err)
	}
	if err := be.Cleanup(context.Background(), h); err != nil {
		t.Fatal(err)
	}

	assertMappedEvents(t, phases, kinds)
	assertReceiptAndRedaction(t, prep, receipt)
}

func assertMappedEvents(t *testing.T, phases, kinds []string) {
	t.Helper()
	want := []string{
		"placement_profile_resolved",
		execution.KindPlacementAllocated,
		"vmm_started",
		"guest_ready",
		execution.KindWorkloadReady,
		execution.KindWorkloadStarted,
		execution.KindWorkloadExited,
	}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("kinds=%v want=%v", kinds, want)
	}
	for i, phase := range phases {
		wantPhase := execution.PhaseStartup
		if i >= 5 {
			wantPhase = execution.PhaseWorkload
		}
		if phase != wantPhase {
			t.Fatalf("phases[%d]=%s want %s (all=%v)", i, phase, wantPhase, phases)
		}
	}
}

func assertReceiptAndRedaction(t *testing.T, prep backend.PreparedRun, receipt execution.PlacementReceipt) {
	t.Helper()
	if receipt.AllocationID != "room-test" || receipt.ImageSHA256 == "" {
		t.Fatalf("receipt=%+v", receipt)
	}
	if receipt.StreamDelivery != execution.StreamDeliveryTerminalReplay {
		t.Fatalf("stream_delivery=%q", receipt.StreamDelivery)
	}

	stdout, err := os.ReadFile(prep.StdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stdout, []byte("fixture-secret")) {
		t.Fatalf("secret leaked into stdout: %q", stdout)
	}
	if !bytes.Contains(stdout, []byte(redactToken)) {
		t.Fatalf("redaction marker missing: %q", stdout)
	}
	durable, err := os.ReadFile(filepath.Join(prep.PrivateDir, backendFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(durable, []byte("fixture-secret")) {
		t.Fatal("secret leaked into backend.json")
	}
	var allocation Allocation
	if err := json.Unmarshal(durable, &allocation); err != nil {
		t.Fatal(err)
	}
	if allocation.RoomID != "room-test" || allocation.Receipt.AllocationID != "room-test" {
		t.Fatalf("allocation=%+v", allocation)
	}
}

func TestPoolFullIsStructuredPlacementUnavailable(t *testing.T) {
	be, prep := helperBackend(t, "pool_full")
	emit := func(_, _ string, _ map[string]any) error { return nil }
	_, err := be.Start(context.Background(), prep, emit)
	if !backend.IsPlacementUnavailable(err) {
		t.Fatalf("want placement unavailable, got %v", err)
	}
	var unavailable *backend.PlacementUnavailable
	if !errors.As(err, &unavailable) || unavailable.Cap != 8 {
		t.Fatalf("unavailable=%+v err=%v", unavailable, err)
	}
}

func TestCollectionAndCleanupFailuresRemainDistinct(t *testing.T) {
	for _, scenario := range []string{"collection_failed", "cleanup_failed"} {
		t.Run(scenario, func(t *testing.T) {
			be, prep := helperBackend(t, scenario)
			emit := func(_, _ string, _ map[string]any) error { return nil }
			h, err := be.Start(context.Background(), prep, emit)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := be.Wait(context.Background(), h, emit); err != nil {
				t.Fatal(err)
			}
			_, collectErr := be.Collect(context.Background(), h, prep.Out)
			cleanupErr := be.Cleanup(context.Background(), h)
			if scenario == "collection_failed" && collectErr == nil {
				t.Fatal("collection failure was lost")
			}
			if scenario == "cleanup_failed" && cleanupErr == nil {
				t.Fatal("cleanup failure was lost")
			}
		})
	}
}

func TestUnexpectedLifecycleEventsFailTheirBoundary(t *testing.T) {
	for _, scenario := range []string{"unexpected_collection", "unexpected_cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			be, prep := helperBackend(t, scenario)
			emit := func(_, _ string, _ map[string]any) error { return nil }
			h, err := be.Start(context.Background(), prep, emit)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := be.Wait(context.Background(), h, emit); err != nil {
				t.Fatal(err)
			}
			_, collectErr := be.Collect(context.Background(), h, prep.Out)
			if scenario == "unexpected_collection" && collectErr == nil {
				t.Fatal("unexpected collection event was silently dropped")
			}
			if scenario == "unexpected_cleanup" && collectErr != nil {
				t.Fatalf("collection failed before cleanup: %v", collectErr)
			}
			if scenario == "unexpected_cleanup" && be.Cleanup(context.Background(), h) == nil {
				t.Fatal("unexpected cleanup event was silently dropped")
			}
		})
	}
}

func TestLifecycleRecordRequiresRoomID(t *testing.T) {
	out := make(chan lifecycleItem, 1)
	line := []byte(`{"seq":1,"ts":"2026-07-19T00:00:00Z","room_id":"","event":"pool_full","cap":8}` + "\n")
	_, _, _, err := emitCompleteLines(line, 1, "", out)
	if err == nil || !strings.Contains(err.Error(), "missing room_id") {
		t.Fatalf("want missing room_id error, got %v", err)
	}
}

func TestRejectsSecretOutsideSendEnvAllowlistBeforeSpawn(t *testing.T) {
	be, prep := helperBackend(t, "success")
	prep.Work.Secrets = []execution.Secret{{Name: "GH_TOKEN", Ref: "env:GH_TOKEN"}}
	_, err := be.Start(context.Background(), prep, func(_, _ string, _ map[string]any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "SendEnv allowlist") {
		t.Fatalf("want allowlist error, got %v", err)
	}
}

func TestRoomsArgvContainsNoResolvedSecret(t *testing.T) {
	be, prep := helperBackend(t, "success")
	task, err := taskPath(prep)
	if err != nil {
		t.Fatal(err)
	}
	args := be.runArgs(prep, task, filepath.Join(prep.PrivateDir, lifecycleFile))
	if strings.Contains(strings.Join(args, " "), "fixture-secret") {
		t.Fatalf("secret leaked into argv: %v", args)
	}
	if got := flagValue(args, "--runner"); got != "cursor" {
		t.Fatalf("runner=%q", got)
	}
	if got := flagValue(args, "--task"); got != task {
		t.Fatalf("task=%q want %q", got, task)
	}
}

func TestStartHonorsContextDuringGuestStartup(t *testing.T) {
	be, prep := helperBackend(t, "slow_start")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := be.Start(ctx, prep, func(_, _ string, _ map[string]any) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context deadline, got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("startup cancellation took %s", time.Since(started))
	}
}

func TestImageHashHonorsStartupContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.ext4")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 256*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fileSHA256(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context cancellation, got %v", err)
	}
}

func TestDurableCleanupFailsClosedWithoutRoomIdentity(t *testing.T) {
	private := t.TempDir()
	data, err := json.Marshal(Allocation{Backend: "rooms", PID: os.Getpid(), PGID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, backendFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CleanupDurable(private)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Uncertain || result.AllocationID == "" {
		t.Fatalf("cleanup=%+v", result)
	}
}

func TestRoomIDFromLifecycleAcceptsLargeRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), lifecycleFile)
	record := lifecycleRecord{
		Seq:     1,
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
		RoomID:  "room-large-record",
		Event:   "workload_started",
		Command: []string{strings.Repeat("x", 70*1024)},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	roomID, err := roomIDFromLifecycle(path)
	if err != nil {
		t.Fatal(err)
	}
	if roomID != "room-large-record" {
		t.Fatalf("room id=%q", roomID)
	}
}

func TestRoomPresentRejectsUnreadableAndFindsID(t *testing.T) {
	if !roomPresent([]byte("not-json"), "room-a") {
		t.Fatal("unreadable residue report must fail closed")
	}
	if !roomPresent([]byte(`{"schema_version":2,"rooms":[]}`), "room-a") {
		t.Fatal("unknown residue schema must fail closed")
	}
	if !roomPresent([]byte(`{"schema_version":1,"rooms":[{"id":"room-a"}]}`), "room-a") {
		t.Fatal("room-a should be present")
	}
	if roomPresent([]byte(`{"schema_version":1,"rooms":[]}`), "room-a") {
		t.Fatal("empty report should be clean")
	}
}

func helperBackend(t *testing.T, scenario string) (*Backend, backend.PreparedRun) {
	t.Helper()
	dir := t.TempDir()
	inputs := filepath.Join(dir, "inputs")
	out := filepath.Join(dir, "artifacts")
	private := filepath.Join(dir, "private")
	for _, path := range []string{inputs, out, private} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(inputs, "task.md"), []byte("do the work"), 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(dir, "agent.ext4")
	if err := os.WriteFile(image, []byte("image-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "true"
	prep := backend.PreparedRun{
		RunID: "run-test",
		Work: execution.WorkSpec{
			SchemaVersion: execution.SchemaVersion,
			Command:       execution.Command{Executable: execution.Executable{Name: &name}},
			Workspace: execution.Workspace{
				Kind:     execution.WorkspaceKindGit,
				URL:      "https://example.invalid/repo.git",
				Revision: strings.Repeat("a", 40),
			},
			Inputs:  []execution.Input{{Name: "task", Target: "task.md"}},
			Secrets: []execution.Secret{{Name: "CURSOR_API_KEY", Ref: "env:CURSOR_API_KEY"}},
		},
		Env:        append(os.Environ(), "CURSOR_API_KEY=fixture-secret"),
		Inputs:     inputs,
		Out:        out,
		StdoutPath: filepath.Join(dir, "stdout.log"),
		StderrPath: filepath.Join(dir, "stderr.log"),
		Secrets:    [][]byte{[]byte("fixture-secret")},
		PrivateDir: private,
	}
	config := Config{
		Launcher: os.Args[0],
		Prefix:   []string{"-test.run=TestRoomsHelperProcess", "--", "--rooms-helper"},
		Image:    image,
		Model:    scenario,
		Poll:     time.Millisecond,
	}
	return New(config), prep
}

// TestRoomsHelperProcess is a hermetic rooms CLI double. The parent invokes
// this test binary with `-test.run` and the real adapter argv; no Rooms binary,
// KVM, shell, or host setup is required.
func TestRoomsHelperProcess(_ *testing.T) {
	if !containsArg(os.Args, "--rooms-helper") {
		return
	}
	lifecycle := flagValue(os.Args, "--lifecycle")
	out := flagValue(os.Args, "--out")
	scenario := flagValue(os.Args, "--model")
	if scenario == "slow_start" {
		time.Sleep(2 * time.Second)
	}
	secret := os.Getenv("CURSOR_API_KEY")
	if len(secret) > 1 {
		_, _ = os.Stdout.Write([]byte(secret[:len(secret)/2]))
		_, _ = os.Stdout.Write([]byte(secret[len(secret)/2:] + "\n"))
	}
	seq := int64(0)
	emit := func(event string, fields map[string]any) {
		seq++
		record := map[string]any{
			"seq":     seq,
			"ts":      time.Now().UTC().Format(time.RFC3339Nano),
			"room_id": "room-test",
			"event":   event,
		}
		for key, value := range fields {
			record[key] = value
		}
		data, err := json.Marshal(record)
		if err != nil {
			os.Exit(90)
		}
		file, err := os.OpenFile(lifecycle, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(91)
		}
		_, _ = file.Write(append(data, '\n'))
		_ = file.Sync()
		_ = file.Close()
		time.Sleep(3 * time.Millisecond)
	}
	if scenario == "pool_full" {
		emit("pool_full", map[string]any{"cap": 8})
		os.Exit(4)
	}
	emit("slot_allocated", map[string]any{"slot": 2, "tap": "tap-fc2"})
	emit("vmm_started", map[string]any{"pid": 42})
	emit("guest_ready", nil)
	emit("ssh_ready", nil)
	emit("workload_started", map[string]any{"command": []string{"cursor-runner"}})
	emit("workload_exited", map[string]any{"exit_code": 0, "status": "succeeded"})
	emit("collection_started", nil)
	if err := os.MkdirAll(out, 0o700); err != nil {
		os.Exit(92)
	}
	if err := os.WriteFile(filepath.Join(out, "result.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		os.Exit(93)
	}
	if scenario == "collection_failed" {
		emit("collection_failed", map[string]any{"error": "copy failed"})
	}
	if scenario == "unexpected_collection" {
		emit("collection_progress", nil)
	}
	if scenario != "collection_failed" {
		emit("collection_done", nil)
	}
	if scenario == "unexpected_cleanup" {
		emit("cleanup_progress", nil)
	}
	if scenario == "cleanup_failed" {
		emit("cleanup_failed", map[string]any{"error": "tap remained"})
		return
	}
	emit("cleanup_done", nil)
}

func flagValue(args []string, name string) string {
	for i := range args {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
