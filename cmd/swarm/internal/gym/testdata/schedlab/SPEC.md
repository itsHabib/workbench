# schedlab

Build a small job scheduler / workflow engine as a Go module named `schedlab` (go 1.22, standard library only).
Fourteen packages in layers; a package imports only the packages its section names. Hidden tests exercise
each package and the whole engine, so the names and behaviour below are exact.

Layers, lowest first: `errs`, `clock` / `ids`, `cron`, `dag`, `retry`, `state`, `metrics` / `lease`, `queue`,
`log` / `worker`, `plan` / `engine`.

## Shared decisions
- Every error this spec names a sentinel for satisfies `errors.Is(err, thatSentinel)`. Wrapping with more text is fine. Errors passed up from a lower package keep their sentinel.
- Nothing reads the wall clock except `clock.System`. Every other package takes a `clock.Clock`. Timestamps stored in records are `clock.Millis(c)` and are called `...Ms`. A thing with a ttl or delay is measured in whole milliseconds: it is live (or not yet ready) while `clock.Millis(c)` is strictly less than its `ExpiresMs` (or `ReadyMs`).
- Randomness is injected as `rnd func() float64` returning values in `[0, 1)`. A nil `rnd` behaves as if it always returned 0.5.
- Run ids are `ids.ID`. A task's queue item id and lease key are both `<runID>/<taskName>`.
- The lease epoch rule, used by `worker` and `engine`: every claim of a key acquires its lease, so the key's epoch counts the claims. A result carries the epoch its worker held; it is applied only when that epoch equals `lease.Epoch(key)` at the time it is applied, otherwise it is stale and changes nothing.
- The state transition table lives in `state.CanTransition` and is enforced by both `log.Append` and `engine`.
- When an operation returns an error, it changed nothing, unless this spec says otherwise.
- "Safe for concurrent use" means correct under `go test -race`.

## errs
- Sentinels, all distinct, made with `errors.New`: `ErrInvalid`, `ErrNotFound`, `ErrConflict`, `ErrState`, `ErrExpired`, `ErrCycle`.
- `func Code(err error) string`. nil gives `OK`. Otherwise the first sentinel, in the order listed above, that `errors.Is` matches gives `INVALID`, `NOT_FOUND`, `CONFLICT`, `STATE`, `EXPIRED`, `CYCLE`. Anything else gives `INTERNAL`.

## clock (imports nothing from the module)
- `type Clock interface { Now() time.Time }`.
- `func System() Clock`. Reads the wall clock.
- `func Millis(c Clock) int64`. `c.Now().UnixMilli()`.
- `func NewFake(start time.Time) *Fake`. `(*Fake) Now() time.Time` returns start until it is moved. `(*Fake) Advance(d time.Duration) time.Time` moves the fake forward by d and returns the new time; a negative d panics. `(*Fake) Set(t time.Time)` moves it to t, forwards or backwards. Safe for concurrent use.

## ids (imports errs, clock)
- `type ID string`. Exactly 19 characters: 12 lower-case hex digits of a unix millisecond, `-`, 6 lower-case hex digits of a sequence number. `(ID) String() string`.
- `func Parse(s string) (ID, error)`. Checks that shape exactly (length, positions, lower-case hex); otherwise `ErrInvalid`.
- `(ID) Time() (time.Time, error)`. `time.UnixMilli(ms).UTC()` for a well-formed id, else `ErrInvalid`.
- `func Sort(ids []ID)`. Ascending in place. Ids from one generator sort in the order they were made.
- `func New(c clock.Clock) *Gen`. `(*Gen) Next() ID`. Let t be the larger of `clock.Millis(c)` (a negative value counts as 0) and the millisecond of the previous id. When t equals the previous id's millisecond the sequence is the previous sequence plus one, otherwise 0. A sequence that would pass `ffffff` instead moves t forward by one millisecond and restarts at 0. So ids from one generator are strictly increasing even when the clock stands still or goes backwards; the first id at millisecond 1000 is `0000000003e8-000000`. Safe for concurrent use: concurrent Next calls never return the same id.

## cron (imports errs)
- Five fields, separated by one or more spaces or tabs, surrounding white space ignored: minute 0-59, hour 0-23, day of month 1-31, month 1-12, day of week 0-6 with 0 Sunday. A field is a comma-separated list of items; an item is `*`, `*/n`, `a`, `a-b` or `a-b/n`. Numbers are decimal digits only (leading zeros allowed). `*/n` means the field's minimum, plus n, plus 2n, ... up to the maximum; `a-b/n` means a, a+n, ... up to b. `ErrInvalid` for any other shape (`a/n`, names, `?`, `7` as a weekday, a sign, an empty item, five fields not present), a number out of range, a > b, or n < 1.
- `type Expr struct { Minute, Hour, Dom, Month, Dow []int }`. Each slice holds the allowed values sorted ascending without duplicates. The zero Expr matches nothing.
- `func Parse(s string) (Expr, error)`.
- `(Expr) String() string`. The five fields joined by single spaces; a field that allows every value is `*`, otherwise its values joined by commas: `*/15 * * * *` gives `0,15,30,45 * * * *`.
- `(Expr) Matches(t time.Time) bool`. True when t's minute, hour, day of month, month and weekday are all allowed; seconds and smaller are ignored. Unlike Vixie cron, day of month and day of week must both match.
- `(Expr) Next(after time.Time) (time.Time, error)`. The earliest whole minute (seconds and nanoseconds 0) strictly after `after`, in `after`'s Location, that Matches. `ErrNotFound` when there is none within the five years after `after` (`0 0 30 2 *`), so `0 0 29 2 *` always finds a leap day.

## dag (imports errs)
- `func New() *Graph`. Not safe for concurrent use.
- `(*Graph) AddNode(id string) error`. `ErrInvalid` for an empty id, `ErrConflict` when it exists.
- `(*Graph) AddEdge(from, to string) error`. to depends on from. `ErrNotFound` when either node is missing (checked first), `ErrInvalid` when from == to, `ErrConflict` when the edge exists, `ErrCycle` when the edge would make a cycle; a refused edge changes nothing.
- `(*Graph) Nodes() []string`. Sorted. `(*Graph) Len() int`.
- `(*Graph) Deps(id string) ([]string, error)`. Sorted direct predecessors. `(*Graph) Dependents(id string) ([]string, error)`. Sorted direct successors. Both `ErrNotFound` for an unknown id and non-nil for a known one.
- `(*Graph) Order() []string`. A topological order: repeatedly take the smallest (by string order) node whose predecessors have all been taken. Deterministic, and total because cycles cannot be added.
- `(*Graph) Ready(done map[string]bool) []string`. Sorted nodes with `done[id]` false whose every predecessor has `done[pred]` true. A nil map is fine.

## retry (imports errs)
- `type Policy struct { Base, Max time.Duration; Factor, Jitter float64; MaxAttempts int }`.
- `(Policy) Validate() error`. `ErrInvalid` when Base <= 0, Max < Base, Factor < 1, Jitter outside [0, 1] or MaxAttempts < 1.
- `(Policy) Retry(attempt int) bool`. True when 1 <= attempt < MaxAttempts: attempt number `attempt` has failed and another one is allowed.
- `(Policy) Delay(attempt int, rnd func() float64) (time.Duration, error)`. The wait after failed attempt number `attempt`. `ErrInvalid` when Validate fails or attempt < 1 (checked first), `ErrState` when Retry(attempt) is false. Otherwise raw = min(Max, Base * Factor^(attempt-1)) as float nanoseconds, u = rnd() (0.5 for nil), and the result is `raw * (1 + Jitter*(2u-1))` truncated to whole nanoseconds; it is not capped again. Base 1s, Factor 2, Max 5s, Jitter 0.5, MaxAttempts 5: attempt 1 with u 0.5 is 1s, attempt 3 with u 0 is 2s, attempt 4 with u 1 is 7.5s.
- `var Default = Policy{Base: time.Second, Max: time.Minute, Factor: 2, Jitter: 0, MaxAttempts: 3}`.

## state (imports errs)
- `type State string`: `None = ""`, `Pending = "PENDING"`, `Running = "RUNNING"`, `Succeeded = "SUCCEEDED"`, `Failed = "FAILED"`, `Cancelled = "CANCELLED"`.
- `func All() []State`. The five named states in that order, None left out.
- `func Parse(s string) (State, error)`. Case-insensitive match on the five named states; anything else, including "", is `ErrInvalid`.
- `func CanTransition(from, to State) bool`. True only for None to Pending, Pending to Running, Pending to Cancelled, Running to Succeeded, Running to Failed, Running to Cancelled, Running to Pending.
- `func Terminal(s State) bool`. True for Succeeded, Failed, Cancelled.

## metrics (imports errs)
- Names match `^[a-z_][a-z0-9_]*$`; anything else is `ErrInvalid`.
- `func New() *Registry`. Safe for concurrent use, as are counters and histograms.
- `(*Registry) Counter(name string) (*Counter, error)`. Creates or returns the counter; `ErrConflict` when the name is a histogram. `(*Counter) Add(n int64)`, a negative n panics. `(*Counter) Inc()`. `(*Counter) Value() int64`.
- `(*Registry) Histogram(name string, bounds []float64) (*Histogram, error)`. bounds must be non-empty and strictly increasing, else `ErrInvalid`; the slice is copied. Returns the existing histogram when the name exists with equal bounds, `ErrConflict` when it exists with other bounds or as a counter.
- `(*Histogram) Observe(v float64)`. `(*Histogram) Count() int64`. `(*Histogram) Sum() float64`. `(*Histogram) Counts() []int64`. One entry per bound holding how many observations were <= that bound (cumulative), then a last entry equal to Count.
- `(*Registry) Render() string`. Entries sorted by name, lines ended by `\n`, empty string for an empty registry. A counter is `name value`. A histogram is one `name_bucket{le="B"} n` line per bound in order, then `name_bucket{le="+Inf"} count`, `name_sum s`, `name_count count`. Floats (bounds, sums) use `strconv.FormatFloat(v, 'g', -1, 64)`.

## lease (imports errs, clock)
- `type Lease struct { Key, Holder string; Epoch int64; ExpiresMs int64 }`.
- `func New(c clock.Clock) *Store`. Safe for concurrent use: concurrent Acquire calls on one key with different holders give exactly one winner.
- `(*Store) Acquire(key, holder string, ttl time.Duration) (Lease, error)`. `ErrInvalid` for an empty key or holder, or ttl under one millisecond. `ErrConflict` when the key has a live lease with another holder. When the key's live lease is holder's own, it is renewed: same Epoch, ExpiresMs = `Millis + ttl` in milliseconds. Otherwise a new lease is made with Epoch = `Epoch(key) + 1`; epochs start at 1 and never go backwards, whatever expired or was released in between.
- `(*Store) Check(key, holder string, epoch int64) error`. nil when the key's live lease has that holder and epoch; `ErrExpired` when there is no live lease; `ErrState` when there is one with another holder or epoch.
- `(*Store) Release(key, holder string, epoch int64) error`. Check, then drop the lease.
- `(*Store) Get(key string) (Lease, bool)`. The live lease, if any. `(*Store) Epoch(key string) int64`. The last epoch issued for key, 0 for never.

## queue (imports errs, clock)
- `type Item struct { ID string; Priority int; ReadyMs int64; Payload string }`.
- `func New(c clock.Clock) *Queue`. Safe for concurrent use: concurrent Pop calls never return the same item.
- `(*Queue) Push(id string, priority int, delay time.Duration, payload string) error`. `ErrInvalid` for an empty id or negative delay, `ErrConflict` when id is in the queue. ReadyMs = `Millis + delay` in milliseconds.
- `(*Queue) Pop() (Item, bool)`. Removes and returns the best ready item: highest Priority first, then smallest ReadyMs, then earliest Push. False when no item is ready. `(*Queue) Peek() (Item, bool)`. The same without removing.
- `(*Queue) Remove(id string) bool`. True when it was queued. `(*Queue) Has(id string) bool`.
- `(*Queue) Len() int`. All queued items. `(*Queue) Ready() int`. Those ready now.

## log (imports errs, clock, state)
- `type Entry struct { Seq, AtMs int64; Run, Task string; From, To state.State; Note string }`. Task "" means the run itself.
- `func Encode(e Entry) string`. One line, no newline in the output, whatever valid UTF-8 the strings hold (spaces, tabs, newlines, quotes, unicode). Deterministic.
- `func Decode(line string) (Entry, error)`. `Decode(Encode(e)) == e` for every valid Entry. `ErrInvalid` for garbage, Seq < 1, an empty Run, a From that is neither None nor a named state, a To that is not a named state, or a From-To pair `state.CanTransition` refuses.
- `func ReadAll(r io.Reader) ([]Entry, error)`. Decodes each line in order, skips blank lines. A bad line, or a Seq not greater than the previous entry's, stops with `ErrInvalid` whose text contains `line N` (1-based, counting blank lines).
- `func New(c clock.Clock, w io.Writer) *Log`. `w` may be nil.
- `func Load(c clock.Clock, r io.Reader, w io.Writer) (*Log, error)`. A Log holding `ReadAll(r)`; later Appends continue from the last Seq. Loading writes nothing to `w`.
- `(*Log) Append(run, task string, from, to state.State, note string) (Entry, error)`. `ErrInvalid` for an empty run or a pair CanTransition refuses. Seq starts at 1 and rises by 1, AtMs is `clock.Millis(c)`. Writes `Encode(entry) + "\n"` to `w`; lines reach `w` in Seq order even under concurrent Appends.
- `(*Log) Entries() []Entry`. All, in order. `(*Log) ForRun(run string) []Entry`. Those with that Run. `(*Log) Last(run, task string) (state.State, bool)`. The To of the latest entry for that run and task. Copies; safe for concurrent use.

## worker (imports errs, clock, lease, queue)
- `type Result struct { ItemID, Worker string; Epoch int64; OK bool; Output, Msg string }`. Msg is the handler error's text, "" on success.
- `type Handler func(item queue.Item) (string, error)`.
- `func New(c clock.Clock, q *queue.Queue, ls *lease.Store, ttl time.Duration, h Handler) (*Pool, error)`. `ErrInvalid` when q, ls or h is nil or ttl is under one millisecond.
- `(*Pool) Run(name string) (Result, bool, error)`. Worker `name` (empty is `ErrInvalid`) pops one item. No ready item: false. Otherwise it acquires the lease on `item.ID` with holder `name` for ttl; if that fails the item is pushed back (same id, priority and payload, delay 0) and the error is returned with false. Then it calls the handler and returns `{ItemID, name, Epoch, err == nil, output, msg}`, true, nil. The lease is not released: it stays for whoever applies the result, or expires.
- `(*Pool) Drain(name string, max int) ([]Result, error)`. Runs until there is no ready item, an error, or max results (max <= 0 means no limit); returns the results so far and the error, if any.
- Safe for concurrent use with different names.

## plan (imports errs, clock, cron, dag)
- `type Spec struct { Name, Cron string; Priority int; Tasks map[string][]string }`. Tasks maps a task name to the names it depends on. Cron "" means the workflow only runs when submitted by hand.
- `type Workflow struct { Name string; Priority int; Scheduled bool; Expr cron.Expr; Graph *dag.Graph }`.
- `func Compile(s Spec) (*Workflow, error)`. `ErrInvalid` for an empty Name, no tasks, a task name that is empty or holds `/` or white space, a dependency that is not a task, or a Cron that `cron.Parse` refuses; `ErrCycle` when the dependencies form a cycle, a task depending on itself included. Scheduled is `Cron != ""`.
- `(*Workflow) Order() []string`. `Graph.Order()`.
- `type Fire struct { Workflow string; At time.Time }`.
- `func Window(ws []*Workflow, from, to time.Time) ([]Fire, error)`. Every fire of every scheduled workflow with `from < At <= to`, sorted by At then Workflow. `ErrInvalid` when to is before from. Unscheduled workflows and `ErrNotFound` from `cron.Next` are skipped. Nil when there are none.
- `func Next(ws []*Workflow, after time.Time) []Fire`. The earliest fire after `after` of each scheduled workflow, keeping only those at the minimum At, sorted by Workflow. Nil when there are none.
- `func New(c clock.Clock, ws ...*Workflow) (*Planner, error)`. `ErrConflict` on a repeated Name. The planner remembers `c.Now()` as its mark. Safe for concurrent use.
- `(*Planner) Add(w *Workflow) error`. `ErrConflict` on a known Name. `(*Planner) Get(name string) (*Workflow, bool)`. `(*Planner) Names() []string`. Sorted.
- `(*Planner) Due() []Fire`. `Window(all, mark, now)`, then the mark becomes now. When now is before the mark nothing is returned and the mark stays.

## engine (imports everything it needs)
- `type Config struct { Retry retry.Policy; LeaseTTL time.Duration; Workers []string; Rand func() float64 }`.
- `type Task struct { Run, Workflow, Name string; Attempt int; Inputs map[string]string }`. Inputs maps each direct dependency to its output. `type Handler func(t Task) (string, error)`. A handler must not call the engine; it may move the clock.
- `type Run struct { ID, Workflow string; State state.State; Tasks map[string]state.State; Attempts map[string]int; Outputs map[string]string; CreatedMs, UpdatedMs int64 }`. Attempts counts claims per task; Outputs holds the outputs of succeeded tasks.
- `func New(c clock.Clock, cfg Config, h Handler, events io.Writer) (*Engine, error)`. `ErrInvalid` when `cfg.Retry.Validate()` fails, LeaseTTL is under one millisecond, Workers is empty or holds an empty or repeated name, or h is nil. The engine owns one `ids.Gen`, `lease.Store`, `queue.Queue`, `worker.Pool` (ttl LeaseTTL), `plan.Planner`, `log.Log` (writing to `events`, which may be nil) and `metrics.Registry`, all on `c`. The registry starts with counters `runs_submitted`, `runs_succeeded`, `runs_failed`, `runs_cancelled`, `tasks_claimed`, `tasks_succeeded`, `tasks_failed`, `tasks_retried`, `leases_expired`, `results_stale` and the histogram `task_ms` with bounds 10, 100, 1000, 10000. Every method is serialized; safe for concurrent use.
- `(*Engine) Register(s plan.Spec) error`. `plan.Compile` then `Planner.Add`; their errors pass through.
- `(*Engine) Submit(workflow string) (string, error)`. `ErrNotFound` for an unknown name. Makes a run with the next id, State Pending, every task Pending, Attempts and Outputs empty non-nil maps, CreatedMs and UpdatedMs now. Logs `None -> Pending` for the run with Note = workflow name, then `None -> Pending` for each task in `Order()` with Note "". `runs_submitted` +1. Returns the id.
- `(*Engine) Tick() error`. In this order:
  1. Fire: `Planner.Due()`; each fire is submitted as by Submit, in the order returned.
  2. Expire: for each run in submission order and each task in Order() that is Running whose lease is not live, `leases_expired` +1 and the attempt fails with message `lease expired` (see failure below).
  3. Enqueue: for each run in Pending or Running and each task from `Graph.Ready(succeeded tasks)` that is Pending and whose item id `Queue.Has` not: push `<run>/<task>` with the workflow's Priority, delay 0, Payload = task name. The first push of a Pending run logs the run `Pending -> Running` (Note "").
  4. Work: for each name in `cfg.Workers` in order, one `Work(name)`; a result is passed to `Report`, whose `ErrExpired` is swallowed.
- `(*Engine) Work(name string) (worker.Result, bool, error)`. `Pool.Run(name)`. Inside the claim, before the handler runs: the task's Attempts +1, `Pending -> Running` logged with Note `attempt N`, `tasks_claimed` +1; the handler gets Task with that Attempt and Inputs copied from Outputs; after it returns `task_ms` observes the clock's milliseconds spent in the handler. The result is not applied.
- `(*Engine) Report(r worker.Result) error`. `ErrNotFound` when r.ItemID is not `<run>/<task>` of a known run and task. Stale (`results_stale` +1, `ErrExpired`, nothing else changes) when the task is not Running or `r.Epoch != lease.Epoch(r.ItemID)`. Otherwise the lease is released (its error ignored) and:
  - success: `Running -> Succeeded` Note "", Outputs[task] = r.Output, `tasks_succeeded` +1; when every task of the run is Succeeded the run logs `Running -> Succeeded` Note "" and `runs_succeeded` +1.
  - failure, with a = Attempts[task], `tasks_failed` +1: when `Retry(a)`, d = `Delay(a, cfg.Rand)`, the task logs `Running -> Pending` Note `retry in <d>` (`d.String()`), the item is pushed with delay d, `tasks_retried` +1. Otherwise the task logs `Running -> Failed` Note r.Msg, every other Pending or Running task of the run logs `-> Cancelled` Note `run failed` in Order() and leaves the queue, the run logs `Running -> Failed` Note `<task>: <msg>`, `runs_failed` +1.
- `(*Engine) Cancel(runID string) error`. `ErrNotFound`, `ErrState` when the run is terminal. Every Pending or Running task logs `-> Cancelled` Note `cancelled` in Order() and leaves the queue; the run logs `-> Cancelled` Note `cancelled`; `runs_cancelled` +1. A claimed task's lease is left alone, so its result comes back stale.
- Every logged change sets the run's UpdatedMs to now.
- `(*Engine) Get(id string) (Run, error)`. `(*Engine) Runs() []Run`. In submission order. Copies that share no maps with the engine.
- `(*Engine) Log() []log.Entry`. `(*Engine) Metrics() string`. `Registry.Render()`.

Done means: `go build ./... && go vet ./... && go test ./...` pass on `main` at `origin`, and each package has tests of its own.
