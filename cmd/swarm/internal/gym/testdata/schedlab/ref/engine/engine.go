package engine

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"schedlab/clock"
	"schedlab/errs"
	"schedlab/ids"
	"schedlab/lease"
	"schedlab/log"
	"schedlab/metrics"
	"schedlab/plan"
	"schedlab/queue"
	"schedlab/retry"
	"schedlab/state"
	"schedlab/worker"
)

type Config struct {
	Retry    retry.Policy
	LeaseTTL time.Duration
	Workers  []string
	Rand     func() float64
}

type Task struct {
	Run, Workflow, Name string
	Attempt             int
	Inputs              map[string]string
}

type Handler func(t Task) (string, error)

type Run struct {
	ID, Workflow         string
	State                state.State
	Tasks                map[string]state.State
	Attempts             map[string]int
	Outputs              map[string]string
	CreatedMs, UpdatedMs int64
}

var counterNames = []string{
	"runs_submitted", "runs_succeeded", "runs_failed", "runs_cancelled",
	"tasks_claimed", "tasks_succeeded", "tasks_failed", "tasks_retried",
	"leases_expired", "results_stale",
}

type run struct {
	id, workflow string
	wf           *plan.Workflow
	state        state.State
	tasks        map[string]state.State
	attempts     map[string]int
	outputs      map[string]string
	createdMs    int64
	updatedMs    int64
}

type Engine struct {
	mu       sync.Mutex
	c        clock.Clock
	cfg      Config
	h        Handler
	gen      *ids.Gen
	ls       *lease.Store
	q        *queue.Queue
	pool     *worker.Pool
	planner  *plan.Planner
	lg       *log.Log
	reg      *metrics.Registry
	counters map[string]*metrics.Counter
	taskMs   *metrics.Histogram
	runs     map[string]*run
	order    []string
}

func New(c clock.Clock, cfg Config, h Handler, events io.Writer) (*Engine, error) {
	if err := cfg.Retry.Validate(); err != nil {
		return nil, err
	}
	if cfg.LeaseTTL < time.Millisecond || len(cfg.Workers) == 0 || h == nil {
		return nil, fmt.Errorf("engine: bad config: %w", errs.ErrInvalid)
	}
	seen := map[string]bool{}
	for _, w := range cfg.Workers {
		if w == "" || seen[w] {
			return nil, fmt.Errorf("engine: worker %q: %w", w, errs.ErrInvalid)
		}
		seen[w] = true
	}
	planner, _ := plan.New(c)
	e := &Engine{
		c: c, cfg: cfg, h: h, gen: ids.New(c), ls: lease.New(c), q: queue.New(c), planner: planner,
		lg: log.New(c, events), reg: metrics.New(), counters: map[string]*metrics.Counter{}, runs: map[string]*run{},
	}
	for _, n := range counterNames {
		e.counters[n], _ = e.reg.Counter(n)
	}
	e.taskMs, _ = e.reg.Histogram("task_ms", []float64{10, 100, 1000, 10000})
	e.pool, _ = worker.New(c, e.q, e.ls, cfg.LeaseTTL, e.claim)
	return e, nil
}

func (e *Engine) count(name string) { e.counters[name].Inc() }

func (e *Engine) Register(s plan.Spec) error {
	w, err := plan.Compile(s)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.planner.Add(w)
}

// transition moves a run (task "") or one of its tasks and logs it.
func (e *Engine) transition(r *run, task string, to state.State, note string) error {
	from := r.state
	if task != "" {
		from = r.tasks[task]
	}
	if _, err := e.lg.Append(r.id, task, from, to, note); err != nil {
		return err
	}
	if task == "" {
		r.state = to
	} else {
		r.tasks[task] = to
	}
	r.updatedMs = clock.Millis(e.c)
	return nil
}

func (e *Engine) submit(workflow string) (string, error) {
	wf, ok := e.planner.Get(workflow)
	if !ok {
		return "", fmt.Errorf("engine: workflow %q: %w", workflow, errs.ErrNotFound)
	}
	now := clock.Millis(e.c)
	r := &run{
		id: string(e.gen.Next()), workflow: workflow, wf: wf, tasks: map[string]state.State{},
		attempts: map[string]int{}, outputs: map[string]string{}, createdMs: now, updatedMs: now,
	}
	e.runs[r.id] = r
	e.order = append(e.order, r.id)
	if err := e.transition(r, "", state.Pending, workflow); err != nil {
		return "", err
	}
	for _, t := range wf.Order() {
		if err := e.transition(r, t, state.Pending, ""); err != nil {
			return "", err
		}
	}
	e.count("runs_submitted")
	return r.id, nil
}

func (e *Engine) Submit(workflow string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.submit(workflow)
}

func key(r *run, task string) string { return r.id + "/" + task }

func (e *Engine) Tick() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range e.planner.Due() {
		if _, err := e.submit(f.Workflow); err != nil {
			return err
		}
	}
	for _, id := range e.order {
		r := e.runs[id]
		if state.Terminal(r.state) {
			continue
		}
		for _, t := range r.wf.Order() {
			if r.tasks[t] != state.Running {
				continue
			}
			if _, live := e.ls.Get(key(r, t)); live {
				continue
			}
			e.count("leases_expired")
			if err := e.fail(r, t, "lease expired"); err != nil {
				return err
			}
		}
	}
	for _, id := range e.order {
		r := e.runs[id]
		if r.state != state.Pending && r.state != state.Running {
			continue
		}
		done := map[string]bool{}
		for t, st := range r.tasks {
			done[t] = st == state.Succeeded
		}
		for _, t := range r.wf.Graph.Ready(done) {
			if r.tasks[t] != state.Pending || e.q.Has(key(r, t)) {
				continue
			}
			if err := e.q.Push(key(r, t), r.wf.Priority, 0, t); err != nil {
				return err
			}
			if r.state == state.Pending {
				if err := e.transition(r, "", state.Running, ""); err != nil {
					return err
				}
			}
		}
	}
	for _, name := range e.cfg.Workers {
		res, ok, err := e.pool.Run(name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := e.report(res); err != nil && !errors.Is(err, errs.ErrExpired) {
			return err
		}
	}
	return nil
}

func (e *Engine) lookup(itemID string) (*run, string, error) {
	runID, task, ok := strings.Cut(itemID, "/")
	r := e.runs[runID]
	if !ok || r == nil {
		return nil, "", fmt.Errorf("engine: item %q: %w", itemID, errs.ErrNotFound)
	}
	if _, ok := r.tasks[task]; !ok {
		return nil, "", fmt.Errorf("engine: item %q: %w", itemID, errs.ErrNotFound)
	}
	return r, task, nil
}

// claim runs inside Pool.Run while the engine lock is held by Work.
func (e *Engine) claim(item queue.Item) (string, error) {
	r, task, err := e.lookup(item.ID)
	if err != nil {
		return "", err
	}
	r.attempts[task]++
	if err := e.transition(r, task, state.Running, fmt.Sprintf("attempt %d", r.attempts[task])); err != nil {
		return "", err
	}
	e.count("tasks_claimed")
	inputs := map[string]string{}
	deps, _ := r.wf.Graph.Deps(task)
	for _, d := range deps {
		inputs[d] = r.outputs[d]
	}
	start := clock.Millis(e.c)
	out, herr := e.h(Task{Run: r.id, Workflow: r.workflow, Name: task, Attempt: r.attempts[task], Inputs: inputs})
	e.taskMs.Observe(float64(clock.Millis(e.c) - start))
	return out, herr
}

func (e *Engine) Work(name string) (worker.Result, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pool.Run(name)
}

func (e *Engine) report(res worker.Result) error {
	r, task, err := e.lookup(res.ItemID)
	if err != nil {
		return err
	}
	if r.tasks[task] != state.Running || res.Epoch != e.ls.Epoch(res.ItemID) {
		e.count("results_stale")
		return fmt.Errorf("engine: result for %q epoch %d: %w", res.ItemID, res.Epoch, errs.ErrExpired)
	}
	_ = e.ls.Release(res.ItemID, res.Worker, res.Epoch)
	if !res.OK {
		return e.fail(r, task, res.Msg)
	}
	if err := e.transition(r, task, state.Succeeded, ""); err != nil {
		return err
	}
	r.outputs[task] = res.Output
	e.count("tasks_succeeded")
	for _, st := range r.tasks {
		if st != state.Succeeded {
			return nil
		}
	}
	if err := e.transition(r, "", state.Succeeded, ""); err != nil {
		return err
	}
	e.count("runs_succeeded")
	return nil
}

func (e *Engine) Report(res worker.Result) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.report(res)
}

func (e *Engine) fail(r *run, task, msg string) error {
	e.count("tasks_failed")
	a := r.attempts[task]
	if e.cfg.Retry.Retry(a) {
		d, err := e.cfg.Retry.Delay(a, e.cfg.Rand)
		if err != nil {
			return err
		}
		if err := e.transition(r, task, state.Pending, "retry in "+d.String()); err != nil {
			return err
		}
		if err := e.q.Push(key(r, task), r.wf.Priority, d, task); err != nil {
			return err
		}
		e.count("tasks_retried")
		return nil
	}
	if err := e.transition(r, task, state.Failed, msg); err != nil {
		return err
	}
	if err := e.cancelTasks(r, "run failed"); err != nil {
		return err
	}
	if err := e.transition(r, "", state.Failed, task+": "+msg); err != nil {
		return err
	}
	e.count("runs_failed")
	return nil
}

func (e *Engine) cancelTasks(r *run, note string) error {
	for _, t := range r.wf.Order() {
		if st := r.tasks[t]; st != state.Pending && st != state.Running {
			continue
		}
		if err := e.transition(r, t, state.Cancelled, note); err != nil {
			return err
		}
		e.q.Remove(key(r, t))
	}
	return nil
}

func (e *Engine) Cancel(runID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.runs[runID]
	if !ok {
		return fmt.Errorf("engine: run %q: %w", runID, errs.ErrNotFound)
	}
	if state.Terminal(r.state) {
		return fmt.Errorf("engine: run %q is %s: %w", runID, r.state, errs.ErrState)
	}
	if err := e.cancelTasks(r, "cancelled"); err != nil {
		return err
	}
	if err := e.transition(r, "", state.Cancelled, "cancelled"); err != nil {
		return err
	}
	e.count("runs_cancelled")
	return nil
}

func (r *run) snapshot() Run {
	out := Run{
		ID: r.id, Workflow: r.workflow, State: r.state, Tasks: make(map[string]state.State, len(r.tasks)),
		Attempts: make(map[string]int, len(r.attempts)), Outputs: make(map[string]string, len(r.outputs)),
		CreatedMs: r.createdMs, UpdatedMs: r.updatedMs,
	}
	for k, v := range r.tasks {
		out.Tasks[k] = v
	}
	for k, v := range r.attempts {
		out.Attempts[k] = v
	}
	for k, v := range r.outputs {
		out.Outputs[k] = v
	}
	return out
}

func (e *Engine) Get(id string) (Run, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.runs[id]
	if !ok {
		return Run{}, fmt.Errorf("engine: run %q: %w", id, errs.ErrNotFound)
	}
	return r.snapshot(), nil
}

func (e *Engine) Runs() []Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Run, 0, len(e.order))
	for _, id := range e.order {
		out = append(out, e.runs[id].snapshot())
	}
	return out
}

func (e *Engine) Log() []log.Entry { return e.lg.Entries() }

func (e *Engine) Metrics() string { return e.reg.Render() }
