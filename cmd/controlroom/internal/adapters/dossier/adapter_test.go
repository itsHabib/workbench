package dossier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

type fakeFactory struct {
	mu      sync.Mutex
	starts  int
	fail    bool
	lines   string
	process *fakeProcess
	gate    <-chan struct{}
	started chan struct{}
	once    sync.Once
}

func (f *fakeFactory) Start(_ string, _ ...string) (process, error) {
	f.mu.Lock()
	f.starts++
	fail, lines, gate, started := f.fail, f.lines, f.gate, f.started
	f.mu.Unlock()
	if started != nil {
		f.once.Do(func() { close(started) })
	}
	if gate != nil {
		<-gate
	}
	if fail {
		return nil, errors.New("start failed")
	}
	p := newFakeProcess(lines)
	f.mu.Lock()
	f.process = p
	f.mu.Unlock()
	return p, nil
}

type recordingCloser struct {
	bytes.Buffer
	closed chan struct{}
	once   sync.Once
}

func (w *recordingCloser) Close() error {
	w.once.Do(func() { close(w.closed) })
	return nil
}

type fakeProcess struct {
	stdin  *recordingCloser
	stdout io.ReadCloser
	killed bool
}

func newFakeProcess(lines string) *fakeProcess {
	return &fakeProcess{stdin: &recordingCloser{closed: make(chan struct{})}, stdout: io.NopCloser(strings.NewReader(lines))}
}

func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *fakeProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *fakeProcess) Wait() error           { <-p.stdin.closed; return nil }
func (p *fakeProcess) Kill() error           { p.killed = true; return nil }

func TestCollectHandshakesCallsEveryOwnerReadAndReusesChild(t *testing.T) {
	factory := &fakeFactory{lines: healthyScript()}
	a := New("dossier", "corpus")
	a.factory = factory
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceOK || len(got.Tasks) != 1 {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Tasks[0].Artifacts[0].URL != "https://github.com/o/r/pull/1" {
		t.Fatalf("artifact was not normalized: %#v", got.Tasks[0])
	}
	writes := factory.process.stdin.String()
	for _, method := range []string{"initialize", "notifications/initialized", "project.list", "project.overview", "phase.list", "task.list", "task.get", "artifact.list"} {
		if !strings.Contains(writes, method) {
			t.Fatalf("missing %s in protocol writes: %s", method, writes)
		}
	}
	if factory.starts != 1 {
		t.Fatalf("starts = %d, want 1", factory.starts)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBreakerOpensAfterThreeFailureCyclesAndSuppressesAutomaticStart(t *testing.T) {
	factory := &fakeFactory{fail: true}
	a := New("dossier", "corpus")
	a.factory = factory
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if got := a.Collect(context.Background()); got.Receipt.ErrorCode != "start_failed" {
			t.Fatalf("cycle %d: %#v", i, got)
		}
	}
	got := a.Collect(context.Background())
	if got.Receipt.ErrorCode != "breaker_open" || factory.starts != 3 {
		t.Fatalf("unexpected suppression: %#v starts=%d", got, factory.starts)
	}
}

func TestManualHalfOpenSuccessResetsBreaker(t *testing.T) {
	factory := &fakeFactory{fail: true}
	a := New("dossier", "corpus")
	a.factory = factory
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		a.Collect(context.Background())
	}
	factory.fail, factory.lines = false, healthyScript()
	if got := a.CollectManual(context.Background()); got.Receipt.State != model.SourceOK {
		t.Fatalf("manual probe failed: %#v", got)
	}
	if a.failures != 0 || !a.openUntil.IsZero() {
		t.Fatalf("breaker did not reset: failures=%d open=%s", a.failures, a.openUntil)
	}
}

func TestConcurrentManualHalfOpenCallersJoinOneProbe(t *testing.T) {
	factory := &fakeFactory{fail: true}
	a := New("dossier", "corpus")
	a.factory = factory
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		a.Collect(context.Background())
	}
	gate := make(chan struct{})
	started := make(chan struct{})
	factory.mu.Lock()
	factory.fail, factory.lines, factory.gate, factory.started = false, healthyScript(), gate, started
	factory.mu.Unlock()
	results := make(chan Result, 2)
	go func() { results <- a.CollectManual(context.Background()) }()
	<-started
	go func() { results <- a.CollectManual(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		a.stateMu.Lock()
		joined := a.probe != nil && a.probe.waiters == 1
		a.stateMu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second manual caller did not join the probe")
		}
		runtime.Gosched()
	}
	close(gate)
	first, second := <-results, <-results
	if first.Receipt.State != model.SourceOK || second.Receipt.State != model.SourceOK || len(first.Tasks) != 1 || len(second.Tasks) != 1 {
		t.Fatalf("joiners differed: first=%#v second=%#v", first, second)
	}
	factory.mu.Lock()
	starts := factory.starts
	factory.mu.Unlock()
	if starts != 4 {
		t.Fatalf("starts = %d, want exactly one half-open start after three failures", starts)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
}

func healthyScript() string {
	responses := []string{
		`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`,
		mcp(2, `{"projects":[{"slug":"workbench"}]}`),
		mcp(3, `{"project":{"slug":"workbench"}}`),
		mcp(4, `{"phases":[]}`),
		mcp(5, `{"tasks":[{"id":"tsk_1"}]}`),
		mcp(6, `{"id":"tsk_1","project":"prj_1","project_slug":"workbench","phase":"ph_1","slug":"task","title":"Task","status":"in_progress","created_at":"2026-07-13T10:00:00Z","updated_at":"2026-07-13T11:00:00Z","depends_on":[]}`),
		mcp(7, `{"artifacts":[{"task":"tsk_1","kind":"pr","ref":"https://github.com/o/r/pull/1","label":"PR"}]}`),
	}
	return strings.Join(responses, "\n") + "\n"
}

func mcp(id int, payload string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"result":{"content":[{"type":"text","text":` + quote(payload) + `}],"isError":false}}`
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
