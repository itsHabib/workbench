package engine

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"schedlab/clock"
	"schedlab/errs"
	"schedlab/ids"
	"schedlab/log"
	"schedlab/plan"
	"schedlab/retry"
	"schedlab/state"
	"schedlab/worker"
)

var policy = retry.Policy{Base: time.Second, Max: 4 * time.Second, Factor: 2, Jitter: 0, MaxAttempts: 3}

func config(workers ...string) Config {
	return Config{Retry: policy, LeaseTTL: 5 * time.Second, Workers: workers}
}

// etl: extract -> transform -> load, audit after extract and transform.
var etl = plan.Spec{Name: "etl", Priority: 1, Tasks: map[string][]string{
	"extract": nil, "transform": {"extract"}, "load": {"transform"}, "audit": {"extract", "transform"},
}}

type rig struct {
	f      *clock.Fake
	e      *Engine
	events bytes.Buffer
	mu     sync.Mutex
	calls  []Task
	custom map[string]func(Task) (string, error)
}

func newRig(t *testing.T, cfg Config, specs ...plan.Spec) *rig {
	t.Helper()
	r := &rig{f: clock.NewFake(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), custom: map[string]func(Task) (string, error){}}
	e, err := New(r.f, cfg, r.handle, &r.events)
	if err != nil {
		t.Fatal(err)
	}
	r.e = e
	for _, s := range specs {
		if err := e.Register(s); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *rig) handle(task Task) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, task)
	fn := r.custom[task.Name]
	r.mu.Unlock()
	if fn != nil {
		return fn(task)
	}
	return "ok:" + task.Name, nil
}

func (r *rig) tick(t *testing.T) {
	t.Helper()
	if err := r.e.Tick(); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) get(t *testing.T, id string) Run {
	t.Helper()
	run, err := r.e.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func metric(t *testing.T, e *Engine, name string) int64 {
	t.Helper()
	for _, line := range strings.Split(e.Metrics(), "\n") {
		if !strings.HasPrefix(line, name+" ") {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimPrefix(line, name+" "), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	t.Fatalf("metric %q missing in %q", name, e.Metrics())
	return 0
}

func trail(e *Engine, run, task string) []string {
	var out []string
	for _, en := range e.Log() {
		if en.Run == run && en.Task == task {
			out = append(out, fmt.Sprintf("%s>%s:%s", en.From, en.To, en.Note))
		}
	}
	return out
}

func TestHiddenNewRejects(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(0))
	ok := func(Task) (string, error) { return "", nil }
	bad := map[string]Config{
		"bad policy":  {Retry: retry.Policy{}, LeaseTTL: time.Second, Workers: []string{"w"}},
		"zero ttl":    {Retry: policy, LeaseTTL: 0, Workers: []string{"w"}},
		"sub-ms ttl":  {Retry: policy, LeaseTTL: 999 * time.Microsecond, Workers: []string{"w"}},
		"no workers":  {Retry: policy, LeaseTTL: time.Second},
		"empty name":  {Retry: policy, LeaseTTL: time.Second, Workers: []string{"w", ""}},
		"repeat name": {Retry: policy, LeaseTTL: time.Second, Workers: []string{"w", "w"}},
	}
	for name, cfg := range bad {
		if _, err := New(f, cfg, ok, nil); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := New(f, config("w"), nil, nil); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("nil handler: %v", err)
	}
	e, err := New(f, config("w"), ok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Register(plan.Spec{Name: "x", Tasks: map[string][]string{"a": {"a"}}}); !errors.Is(err, errs.ErrCycle) {
		t.Fatalf("cycle: %v", err)
	}
	if err := e.Register(etl); err != nil {
		t.Fatal(err)
	}
	if err := e.Register(etl); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("repeat: %v", err)
	}
	if _, err := e.Submit("nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("unknown workflow: %v", err)
	}
	if len(e.Runs()) != 0 || len(e.Log()) != 0 || metric(t, e, "runs_submitted") != 0 {
		t.Fatal("a refused submit changed something")
	}
	want := "leases_expired 0\nresults_stale 0\nruns_cancelled 0\nruns_failed 0\nruns_submitted 0\nruns_succeeded 0\n" +
		"task_ms_bucket{le=\"10\"} 0\ntask_ms_bucket{le=\"100\"} 0\ntask_ms_bucket{le=\"1000\"} 0\ntask_ms_bucket{le=\"10000\"} 0\n" +
		"task_ms_bucket{le=\"+Inf\"} 0\ntask_ms_sum 0\ntask_ms_count 0\n" +
		"tasks_claimed 0\ntasks_failed 0\ntasks_retried 0\ntasks_succeeded 0\n"
	if got := e.Metrics(); got != want {
		t.Fatalf("fresh metrics:\n%s", got)
	}
}

func TestHiddenSubmitThenRunToSuccess(t *testing.T) {
	r := newRig(t, config("w1", "w2"), etl)
	r.custom["transform"] = func(task Task) (string, error) {
		r.f.Advance(50 * time.Millisecond)
		return "rows=3", nil
	}
	id, err := r.e.Submit("etl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ids.Parse(id); err != nil {
		t.Fatalf("run id %q: %v", id, err)
	}
	start := r.f.Now().UnixMilli()
	run := r.get(t, id)
	want := Run{
		ID: id, Workflow: "etl", State: state.Pending,
		Tasks:    map[string]state.State{"extract": state.Pending, "transform": state.Pending, "load": state.Pending, "audit": state.Pending},
		Attempts: map[string]int{}, Outputs: map[string]string{}, CreatedMs: start, UpdatedMs: start,
	}
	if !reflect.DeepEqual(run, want) {
		t.Fatalf("%+v", run)
	}
	if got := trail(r.e, id, ""); !reflect.DeepEqual(got, []string{">PENDING:etl"}) {
		t.Fatalf("%v", got)
	}
	entries := r.e.Log()
	if len(entries) != 5 || entries[1].Task != "extract" || entries[2].Task != "transform" || entries[3].Task != "audit" || entries[4].Task != "load" {
		t.Fatalf("%+v", entries)
	}
	if metric(t, r.e, "runs_submitted") != 1 || metric(t, r.e, "tasks_claimed") != 0 {
		t.Fatal(r.e.Metrics())
	}

	r.tick(t)
	run = r.get(t, id)
	if run.State != state.Running || run.Tasks["extract"] != state.Succeeded || run.Tasks["transform"] != state.Pending || len(r.calls) != 1 {
		t.Fatalf("after tick 1: %+v calls=%d", run, len(r.calls))
	}
	if got := trail(r.e, id, "extract"); !reflect.DeepEqual(got, []string{">PENDING:", "PENDING>RUNNING:attempt 1", "RUNNING>SUCCEEDED:"}) {
		t.Fatalf("%v", got)
	}
	if c := r.calls[0]; c.Run != id || c.Workflow != "etl" || c.Name != "extract" || c.Attempt != 1 || len(c.Inputs) != 0 || c.Inputs == nil {
		t.Fatalf("%+v", c)
	}
	r.tick(t)
	run = r.get(t, id)
	if run.Tasks["transform"] != state.Succeeded || run.Outputs["transform"] != "rows=3" || len(r.calls) != 2 {
		t.Fatalf("after tick 2: %+v", run)
	}
	if got := r.calls[1].Inputs; !reflect.DeepEqual(got, map[string]string{"extract": "ok:extract"}) {
		t.Fatalf("transform inputs %v", got)
	}
	r.tick(t)
	run = r.get(t, id)
	if run.State != state.Succeeded || len(r.calls) != 4 || r.calls[2].Name != "audit" || r.calls[3].Name != "load" {
		t.Fatalf("after tick 3: %+v calls=%+v", run, r.calls)
	}
	if got := r.calls[2].Inputs; !reflect.DeepEqual(got, map[string]string{"extract": "ok:extract", "transform": "rows=3"}) {
		t.Fatalf("audit inputs %v", got)
	}
	if !reflect.DeepEqual(run.Attempts, map[string]int{"extract": 1, "transform": 1, "load": 1, "audit": 1}) {
		t.Fatalf("%v", run.Attempts)
	}
	if run.UpdatedMs != start+50 || run.CreatedMs != start {
		t.Fatalf("times %d %d", run.CreatedMs, run.UpdatedMs)
	}
	if got := trail(r.e, id, ""); !reflect.DeepEqual(got, []string{">PENDING:etl", "PENDING>RUNNING:", "RUNNING>SUCCEEDED:"}) {
		t.Fatalf("%v", got)
	}
	for name, want := range map[string]int64{"runs_succeeded": 1, "tasks_claimed": 4, "tasks_succeeded": 4, "tasks_failed": 0, "results_stale": 0} {
		if got := metric(t, r.e, name); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if m := r.e.Metrics(); !strings.Contains(m, "task_ms_bucket{le=\"10\"} 3\n") || !strings.Contains(m, "task_ms_bucket{le=\"100\"} 4\n") || !strings.Contains(m, "task_ms_sum 50\n") {
		t.Fatalf("%s", m)
	}
	r.tick(t)
	if len(r.calls) != 4 || metric(t, r.e, "tasks_claimed") != 4 {
		t.Fatal("a finished run kept running")
	}
	written, err := log.ReadAll(&r.events)
	if err != nil || !reflect.DeepEqual(written, r.e.Log()) {
		t.Fatalf("events writer: %v", err)
	}
	run.Tasks["extract"] = state.Failed
	run.Outputs["x"] = "y"
	if again := r.get(t, id); again.Tasks["extract"] != state.Succeeded || len(again.Outputs) != 4 {
		t.Fatal("Get shares maps with the engine")
	}
	if runs := r.e.Runs(); len(runs) != 1 || runs[0].ID != id {
		t.Fatalf("%+v", runs)
	}
}

func TestHiddenRetryThenSucceed(t *testing.T) {
	r := newRig(t, config("w1"), plan.Spec{Name: "one", Tasks: map[string][]string{"flaky": nil}})
	r.custom["flaky"] = func(task Task) (string, error) {
		if task.Attempt < 3 {
			return "", fmt.Errorf("boom %d", task.Attempt)
		}
		return "third time", nil
	}
	id, _ := r.e.Submit("one")
	r.tick(t)
	run := r.get(t, id)
	if run.State != state.Running || run.Tasks["flaky"] != state.Pending || run.Attempts["flaky"] != 1 {
		t.Fatalf("%+v", run)
	}
	r.tick(t)
	if len(r.calls) != 1 {
		t.Fatal("retried before its delay")
	}
	r.f.Advance(time.Second)
	r.tick(t)
	if len(r.calls) != 2 || r.calls[1].Attempt != 2 {
		t.Fatalf("calls %+v", r.calls)
	}
	r.f.Advance(time.Second)
	r.tick(t)
	if len(r.calls) != 2 {
		t.Fatal("second retry waits 2s")
	}
	r.f.Advance(time.Second)
	r.tick(t)
	run = r.get(t, id)
	if run.State != state.Succeeded || run.Attempts["flaky"] != 3 || run.Outputs["flaky"] != "third time" {
		t.Fatalf("%+v", run)
	}
	want := []string{
		">PENDING:", "PENDING>RUNNING:attempt 1", "RUNNING>PENDING:retry in 1s",
		"PENDING>RUNNING:attempt 2", "RUNNING>PENDING:retry in 2s", "PENDING>RUNNING:attempt 3", "RUNNING>SUCCEEDED:",
	}
	if got := trail(r.e, id, "flaky"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	for name, want := range map[string]int64{"tasks_claimed": 3, "tasks_failed": 2, "tasks_retried": 2, "tasks_succeeded": 1, "runs_succeeded": 1, "runs_failed": 0} {
		if got := metric(t, r.e, name); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
}

func TestHiddenFailureCancelsTheRest(t *testing.T) {
	r := newRig(t, config("w1", "w2"), plan.Spec{Name: "wf", Tasks: map[string][]string{"a": nil, "b": {"a"}, "c": {"a"}, "d": nil}})
	r.custom["a"] = func(Task) (string, error) { return "", errors.New("disk full") }
	id, _ := r.e.Submit("wf")
	r.tick(t)
	run := r.get(t, id)
	if run.Tasks["a"] != state.Pending || run.Tasks["d"] != state.Succeeded || run.Attempts["a"] != 1 {
		t.Fatalf("%+v", run)
	}
	r.f.Advance(time.Second)
	r.tick(t)
	r.f.Advance(2 * time.Second)
	r.tick(t)
	run = r.get(t, id)
	wantTasks := map[string]state.State{"a": state.Failed, "b": state.Cancelled, "c": state.Cancelled, "d": state.Succeeded}
	if run.State != state.Failed || !reflect.DeepEqual(run.Tasks, wantTasks) || run.Attempts["a"] != 3 {
		t.Fatalf("%+v", run)
	}
	if got := trail(r.e, id, "a"); got[len(got)-1] != "RUNNING>FAILED:disk full" {
		t.Fatalf("%v", got)
	}
	if got := trail(r.e, id, "b"); !reflect.DeepEqual(got, []string{">PENDING:", "PENDING>CANCELLED:run failed"}) {
		t.Fatalf("%v", got)
	}
	if got := trail(r.e, id, ""); !reflect.DeepEqual(got, []string{">PENDING:wf", "PENDING>RUNNING:", "RUNNING>FAILED:a: disk full"}) {
		t.Fatalf("%v", got)
	}
	entries := r.e.Log()
	n := len(entries)
	if entries[n-4].Task != "a" || entries[n-3].Task != "b" || entries[n-2].Task != "c" || entries[n-1].Task != "" {
		t.Fatalf("failure order: %+v", entries[n-4:])
	}
	for name, want := range map[string]int64{"tasks_failed": 3, "tasks_retried": 2, "runs_failed": 1, "runs_succeeded": 0, "tasks_succeeded": 1} {
		if got := metric(t, r.e, name); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	calls := len(r.calls)
	r.f.Advance(time.Hour)
	r.tick(t)
	if len(r.calls) != calls {
		t.Fatal("a failed run kept running")
	}
	if err := r.e.Cancel(id); !errors.Is(err, errs.ErrState) {
		t.Fatalf("cancel failed run: %v", err)
	}
}

func TestHiddenLeaseExpiryAndStaleResult(t *testing.T) {
	r := newRig(t, config("w1"), plan.Spec{Name: "wf", Tasks: map[string][]string{"slow": nil}})
	r.custom["slow"] = func(task Task) (string, error) {
		switch task.Attempt {
		case 1:
			return "", errors.New("first")
		case 2:
			r.f.Advance(6 * time.Second) // past the 5s lease
			return "late", nil
		}
		return "done", nil
	}
	id, _ := r.e.Submit("wf")
	key := id + "/slow"
	if _, ok, err := r.e.Work("w1"); ok || err != nil {
		t.Fatalf("nothing queued yet: %v %v", ok, err)
	}
	r.tick(t)
	r.f.Advance(time.Second)
	res, ok, err := r.e.Work("w1")
	if err != nil || !ok || res.ItemID != key || res.Epoch != 2 || res.Worker != "w1" || !res.OK || res.Output != "late" {
		t.Fatalf("%+v %v %v", res, ok, err)
	}
	if run := r.get(t, id); run.Tasks["slow"] != state.Running || run.Attempts["slow"] != 2 {
		t.Fatalf("%+v", run)
	}
	r.tick(t)
	run := r.get(t, id)
	if run.Tasks["slow"] != state.Pending || metric(t, r.e, "leases_expired") != 1 || metric(t, r.e, "tasks_retried") != 2 {
		t.Fatalf("%+v\n%s", run, r.e.Metrics())
	}
	if got := trail(r.e, id, "slow"); got[len(got)-1] != "RUNNING>PENDING:retry in 2s" {
		t.Fatalf("%v", got)
	}
	if err := r.e.Report(res); !errors.Is(err, errs.ErrExpired) {
		t.Fatalf("stale result: %v", err)
	}
	if run := r.get(t, id); run.Tasks["slow"] != state.Pending || run.Outputs["slow"] != "" || metric(t, r.e, "results_stale") != 1 {
		t.Fatalf("stale result changed the run: %+v", run)
	}
	r.f.Advance(2 * time.Second)
	r.tick(t)
	run = r.get(t, id)
	if run.State != state.Succeeded || run.Outputs["slow"] != "done" || run.Attempts["slow"] != 3 {
		t.Fatalf("%+v", run)
	}
	if err := r.e.Report(res); !errors.Is(err, errs.ErrExpired) || metric(t, r.e, "results_stale") != 2 {
		t.Fatalf("stale again: %v", err)
	}
	for _, bad := range []string{"", "nope", id, id + "/other", "zz/slow"} {
		if err := r.e.Report(worker.Result{ItemID: bad, Epoch: 3}); !errors.Is(err, errs.ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if metric(t, r.e, "results_stale") != 2 || metric(t, r.e, "tasks_succeeded") != 1 || metric(t, r.e, "tasks_claimed") != 3 {
		t.Fatal(r.e.Metrics())
	}
	if m := r.e.Metrics(); !strings.Contains(m, "task_ms_bucket{le=\"1000\"} 2\n") || !strings.Contains(m, "task_ms_bucket{le=\"10000\"} 3\n") || !strings.Contains(m, "task_ms_sum 6000\n") {
		t.Fatalf("%s", m)
	}
}

func TestHiddenCronFiresByPriority(t *testing.T) {
	r := newRig(t, config("w1"),
		plan.Spec{Name: "lo", Cron: "*/5 * * * *", Priority: 1, Tasks: map[string][]string{"t": nil}},
		plan.Spec{Name: "hi", Cron: "*/5 * * * *", Priority: 5, Tasks: map[string][]string{"t": nil}},
		plan.Spec{Name: "manual", Tasks: map[string][]string{"t": nil}},
	)
	r.tick(t)
	if len(r.e.Runs()) != 0 {
		t.Fatal("fired with no time elapsed")
	}
	r.f.Advance(5 * time.Minute)
	r.tick(t)
	runs := r.e.Runs()
	if len(runs) != 2 || runs[0].Workflow != "hi" || runs[1].Workflow != "lo" || runs[0].ID >= runs[1].ID {
		t.Fatalf("%+v", runs)
	}
	if runs[0].State != state.Succeeded || runs[1].State != state.Running || len(r.calls) != 1 || r.calls[0].Workflow != "hi" {
		t.Fatalf("priority: %+v", runs)
	}
	r.tick(t)
	if runs := r.e.Runs(); runs[1].State != state.Succeeded || r.calls[1].Run != runs[1].ID {
		t.Fatalf("%+v", runs)
	}
	r.f.Advance(10 * time.Minute)
	r.tick(t)
	runs = r.e.Runs()
	if len(runs) != 6 || metric(t, r.e, "runs_submitted") != 6 {
		t.Fatalf("catch-up: %d runs", len(runs))
	}
	if runs[2].Workflow != "hi" || runs[3].Workflow != "lo" || runs[4].Workflow != "hi" || runs[5].Workflow != "lo" {
		t.Fatalf("%+v", runs)
	}
	if runs[2].State != state.Succeeded || runs[3].State != state.Running || runs[4].State != state.Running {
		t.Fatalf("one claim per worker per tick: %+v", runs)
	}
	for i := 1; i < len(runs); i++ {
		if runs[i].ID <= runs[i-1].ID {
			t.Fatalf("ids not increasing: %v", runs)
		}
	}
	for i := 0; i < 3; i++ {
		r.tick(t)
	}
	for _, run := range r.e.Runs() {
		if run.State != state.Succeeded {
			t.Fatalf("%+v", run)
		}
	}
	if len(r.calls) != 6 {
		t.Fatalf("%d calls", len(r.calls))
	}
}

func TestHiddenCancel(t *testing.T) {
	r := newRig(t, config("w1"), plan.Spec{Name: "wf", Tasks: map[string][]string{"a": nil, "b": {"a"}, "c": nil}})
	if err := r.e.Cancel("nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	id, _ := r.e.Submit("wf")
	r.tick(t)
	run := r.get(t, id)
	if run.Tasks["a"] != state.Succeeded || run.Tasks["c"] != state.Pending {
		t.Fatalf("%+v", run)
	}
	res, ok, err := r.e.Work("w1")
	if err != nil || !ok || res.ItemID != id+"/c" {
		t.Fatalf("%+v %v %v", res, ok, err)
	}
	r.f.Advance(time.Second)
	if err := r.e.Cancel(id); err != nil {
		t.Fatal(err)
	}
	run = r.get(t, id)
	want := map[string]state.State{"a": state.Succeeded, "b": state.Cancelled, "c": state.Cancelled}
	if run.State != state.Cancelled || !reflect.DeepEqual(run.Tasks, want) || run.UpdatedMs != run.CreatedMs+1000 {
		t.Fatalf("%+v", run)
	}
	if got := trail(r.e, id, "c"); !reflect.DeepEqual(got, []string{">PENDING:", "PENDING>RUNNING:attempt 1", "RUNNING>CANCELLED:cancelled"}) {
		t.Fatalf("%v", got)
	}
	if got := trail(r.e, id, "b"); !reflect.DeepEqual(got, []string{">PENDING:", "PENDING>CANCELLED:cancelled"}) {
		t.Fatalf("%v", got)
	}
	if got := trail(r.e, id, ""); !reflect.DeepEqual(got, []string{">PENDING:wf", "PENDING>RUNNING:", "RUNNING>CANCELLED:cancelled"}) {
		t.Fatalf("%v", got)
	}
	entries := r.e.Log()
	if n := len(entries); entries[n-1].Task != "" || entries[n-2].Task != "c" || entries[n-3].Task != "b" {
		t.Fatalf("cancel order: %+v", entries[len(entries)-3:])
	}
	if err := r.e.Cancel(id); !errors.Is(err, errs.ErrState) {
		t.Fatalf("cancel twice: %v", err)
	}
	if err := r.e.Report(res); !errors.Is(err, errs.ErrExpired) || metric(t, r.e, "results_stale") != 1 {
		t.Fatalf("result after cancel: %v", err)
	}
	if run := r.get(t, id); run.Outputs["c"] != "" || run.Tasks["c"] != state.Cancelled {
		t.Fatalf("%+v", run)
	}
	calls := len(r.calls)
	r.f.Advance(time.Hour)
	r.tick(t)
	if len(r.calls) != calls || metric(t, r.e, "leases_expired") != 0 || metric(t, r.e, "runs_cancelled") != 1 {
		t.Fatalf("cancelled run kept going: %s", r.e.Metrics())
	}
	pending, _ := r.e.Submit("wf")
	if err := r.e.Cancel(pending); err != nil {
		t.Fatal(err)
	}
	if got := trail(r.e, pending, ""); !reflect.DeepEqual(got, []string{">PENDING:wf", "PENDING>CANCELLED:cancelled"}) {
		t.Fatalf("%v", got)
	}
	if metric(t, r.e, "runs_cancelled") != 2 {
		t.Fatal(r.e.Metrics())
	}
}
