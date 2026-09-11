package watch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestTailUsesObservedTranscriptWithoutAWatcher(t *testing.T) {
	home, _ := deliverEnv(t)
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	data := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"Checking the failing test"},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"result","subtype":"error_max_turns","num_turns":60,"total_cost_usd":3.25,"is_error":true}
{"type":"assistant","message":`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(fleet.Path("sessions", "worker.json"), fleet.Rec{"session": "worker", "launch_dir": home, "last_event_at": fleet.Now(), "transcript_path": path}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Tail(context.Background(), &out, "hub:lead", 20, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Checking the failing test", "tool: Bash", "go test ./...", "error_max_turns", "60", "$3.2500"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, &out)
		}
	}
	if strings.Contains(out.String(), "private reasoning") {
		t.Fatal("reasoning block exposed")
	}
	if Heartbeat() != nil {
		t.Fatal("tail ticked the watcher")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := Tail(ctx, &out, "hub:lead", 1, true); err != nil || time.Since(start) > time.Second {
		t.Fatalf("follow did not cancel: %v", err)
	}
	if _, err := tailSource("hub:unknown"); err == nil {
		t.Fatal("guessed unknown address")
	}
}

func TestTraceKeepsPartialLineAndCodexTools(t *testing.T) {
	raw := []byte("{\"type\":\"response_item\",\"payload\":{\"type\":\"function_call\",\"name\":\"exec_command\",\"arguments\":\"go test\"}}\n{\"type\":")
	consumed, lines := traceLines(raw, false)
	if consumed != bytes.IndexByte(raw, '\n')+1 || len(lines) != 1 || !strings.Contains(lines[0], "exec_command") {
		t.Fatalf("%d %v", consumed, lines)
	}
	if _, lines := traceLines(raw, true); len(lines) != 0 {
		t.Fatal("printed truncated first record", lines)
	}
}

func TestEachLaunchHasItsOwnOutputAndResult(t *testing.T) {
	home, sink := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home}
	putStoreMail(t, "hub:lead", "first-result", fleet.Now()-60, nil)
	deliver(fleet.Now())
	launched(t, sink)
	waitExit(t, target)
	first, err := readLaunch(target)
	if err != nil {
		t.Fatal(err)
	}
	result := `{"type":"result","subtype":"error_max_turns","is_error":true}`
	if err := os.WriteFile(fleet.S(first, "output"), []byte(result), 0600); err != nil {
		t.Fatal(err)
	}
	if got := fleet.M(runtimeRow(target, nil), "result"); fleet.S(got, "subtype") != "error_max_turns" {
		t.Fatal(got)
	}
	putStoreMail(t, "hub:lead", "second-result", fleet.Now()-60, nil)
	deliver(fleet.Now())
	waitExit(t, target)
	second, err := readLaunch(target)
	if err != nil {
		t.Fatal(err)
	}
	if fleet.S(first, "output") == fleet.S(second, "output") || fleet.M(runtimeRow(target, nil), "result") != nil {
		t.Fatal("old result leaked into replacement", second)
	}
}

func TestTailAndStatusShowFinalAnswerWithoutHooks(t *testing.T) {
	home, sink := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home}
	putStoreMail(t, "hub:lead", "final-answer", fleet.Now()-60, nil)
	deliver(fleet.Now())
	launched(t, sink)
	waitExit(t, target)
	launch, err := readLaunch(target)
	if err != nil {
		t.Fatal(err)
	}
	result := fleet.Rec{"type": "result", "subtype": "success", "result": "The regression is fixed.", "is_error": false}
	if err := os.WriteFile(fleet.S(launch, "output"), append(fleet.DumpJSON(result), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Tail(context.Background(), &out, "hub:lead", 20, false); err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{out.String(), RuntimeText()} {
		if !strings.Contains(got, "assistant: The regression is fixed.") || !strings.Contains(got, "result: success") {
			t.Fatalf("missing final answer or result: %s", got)
		}
	}
	if Heartbeat() != nil {
		t.Fatal("inspection ticked the watcher")
	}
}

func TestRuntimeReportsRejectedDeliveryEntries(t *testing.T) {
	deliverEnv(t)
	cfg := fleet.ReadJSON(fleet.Path("deliver.json"))
	cfg["invalid"] = fleet.Rec{"cwd": "/unused"}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
		t.Fatal(err)
	}
	got := RuntimeStatus()
	if len(got["workers"].([]fleet.Rec)) != 1 || !strings.Contains(fleet.S(got, "configuration_error"), "1 entries omitted") {
		t.Fatal(got)
	}
	if err := os.WriteFile(fleet.Path("deliver.json"), []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	got = RuntimeStatus()
	if len(got["workers"].([]fleet.Rec)) != 0 || fleet.S(got, "configuration_error") != "deliver.json must be an object" {
		t.Fatal(got)
	}
}

func TestTailFallsBackWhenRecordedTranscriptIsUnavailable(t *testing.T) {
	home, sink := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home}
	putStoreMail(t, "hub:lead", "fallback", fleet.Now()-60, nil)
	deliver(fleet.Now())
	launched(t, sink)
	waitExit(t, target)
	launch, err := readLaunch(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, "removed.jsonl"), home} {
		if err := fleet.WriteJSON(fleet.Path("sessions", "worker.json"), fleet.Rec{"session": "worker", "launch_dir": home, "last_event_at": fleet.Now(), "transcript_path": path}); err != nil {
			t.Fatal(err)
		}
		got, err := tailSource("hub:lead")
		if err != nil || got != fleet.S(launch, "output") {
			t.Fatalf("did not fall back from %s: %s %v", path, got, err)
		}
	}
}

func TestFollowWaitsForFirstObservedTrace(t *testing.T) {
	home, _ := deliverEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- Tail(ctx, &out, "hub:lead", 20, true) }()
	select {
	case err := <-done:
		t.Fatalf("follow ended before a source arrived: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	path := filepath.Join(home, "arrived.jsonl")
	if err := os.WriteFile(path, []byte("worker has started\n"), 0600); err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(fleet.Path("sessions", "worker.json"), fleet.Rec{"session": "worker", "launch_dir": home, "last_event_at": fleet.Now(), "transcript_path": path}); err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	if err := <-done; err != nil || !strings.Contains(out.String(), "worker has started") {
		t.Fatalf("follow missed arriving source: %v %s", err, &out)
	}
	if err := Tail(ctx, &out, "hub:unknown", 20, true); err == nil {
		t.Fatal("follow hid an invalid address")
	}
}

func TestTailSkipsPartialFirstLineInLargeFile(t *testing.T) {
	home, _ := deliverEnv(t)
	path := filepath.Join(home, "large.jsonl")
	data := strings.Repeat("x", traceWindow+100) + "\n" + `{"type":"assistant","message":{"content":"Recent answer"}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(fleet.Path("sessions", "worker.json"), fleet.Rec{"session": "worker", "launch_dir": home, "last_event_at": fleet.Now(), "transcript_path": path}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Tail(context.Background(), &out, "hub:lead", 20, false); err != nil || !strings.Contains(out.String(), "Recent answer") {
		t.Fatalf("large trace lost complete final record: %v %s", err, &out)
	}
}
