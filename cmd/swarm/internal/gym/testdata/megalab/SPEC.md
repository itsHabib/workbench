# megalab

Build one Go module named `megalab` (go 1.22, standard library only) holding three subsystems and
one package that ties them together. Each subsystem is a complete spec of its own, reproduced below,
and lives in its own directory: `kv/` (a key-value engine), `shop/` (an order-processing backend) and
`sched/` (a job scheduler). Import paths are `megalab/kv/<pkg>`, `megalab/shop/<pkg>` and
`megalab/sched/<pkg>`. Hidden tests exercise every package of every subsystem and the bridge, so
names and behaviour are exact.

Thirty-five packages in all. This is more than one team can hold in its head at once, so cut it up.
The subsystems do not import each other; only `bridge` imports across them.

## bridge (imports megalab/kv/store, megalab/shop/app, megalab/sched/clock, megalab/sched/engine, megalab/sched/plan, megalab/sched/retry, megalab/sched/state)

An order that is paid in the shop becomes a fulfilment run in the scheduler, and every command the
bridge executes is remembered in the key-value store.

- `func New(c clock.Clock, events io.Writer) (*Bridge, error)`. Owns one `app.New(c.Now, events)`, one
  `store.New(c.Now)` and one scheduler engine `engine.New(c, cfg, handler, nil)` with
  `cfg = engine.Config{Retry: retry.Policy{Base: time.Second, Max: time.Minute, Factor: 2, Jitter: 0, MaxAttempts: 3}, LeaseTTL: time.Minute, Workers: []string{"w1", "w2"}, Rand: func() float64 { return 0 }}`.
  Registers one workflow `plan.Spec{Name: "fulfil", Tasks: map[string][]string{"pick": nil, "pack": {"pick"}, "ship": {"pack"}}}`.
  Errors from the engine pass through. `events` may be nil.
- `(*Bridge) Exec(line string) (string, error)`. Runs `app.Exec(line)` and returns exactly its reply and
  error. When the reply is not an error: the bridge's command count goes up by one and the store gets
  `cmd:<count>` = the line as given and `reply:<count>` = the reply (ttl 0). When the command's first
  word is `PAY` (case-insensitive) and it succeeded, a run of `fulfil` is submitted to the engine and
  the store gets `fulfil:<order>` = the run id, where `<order>` is the second word of the line. A
  failed command changes nothing anywhere. Serialized; safe for concurrent use.
- `(*Bridge) Tick() error`. `engine.Tick()`. The handler returns the task name as its output and never
  fails. After a run reaches `state.Succeeded` the store gets `fulfil:<order>` = `done` for the order
  that submitted it. Repeated ticks are how a run advances; three ticks complete a fresh run.
- `(*Bridge) Fulfilment(order string) (state.State, bool)`. The state of the run for that order via
  `engine.Get`, false when the order has no run.
- `(*Bridge) Count() int`. Successful commands so far.
- `(*Bridge) Store() *store.Store`. The store itself, for reading.

Done means: `go build ./... && go vet ./... && go test ./...` pass on `main` at `origin`, and each package has tests of its own.
