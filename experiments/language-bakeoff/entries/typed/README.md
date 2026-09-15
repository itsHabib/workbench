# Typed Go API entry

Workbench language bakeoff entry. **Bet:** an ordinary typed programming
language (here Go) gives references, composition and extension without a
new configuration language.

A workflow is a Go function that declares nodes through typed
constructors. Evaluating it yields a **normalized intent**: plain JSON.
The engine plans that intent against what is actually on disk plus the
evidence of earlier runs, prints a readable plan, and applies it through
adapters. The rest is ordinary Go: no parser, grammar or formatter.

```
./demo.sh        # the six bakeoff cases plus crash recovery, in a fresh temp dir (seconds)
go test ./...    # deterministic assertions for every case
```

Requirements: Go 1.25+ (for `os.Root`), bash, and the POSIX tools `tr`,
`awk`, `sort`, `sed`, `wc`. Tested on macOS (arm64) only.

## The flow: source, intent, plan, apply, observed outcome

**Source** ([workflows/textpipe/textpipe.go](workflows/textpipe/textpipe.go)):

```go
input := file.New(g, "input", file.Spec{
    Path:    "input.txt",
    Content: p.String("text", DefaultText),
})
upper := transform.New(g, "upper", transform.Spec{
    Command: []string{"tr", "[:lower:]", "[:upper:]"},
    Stdin:   input, // a wb.Ref: the compiler accepts only a declared node here
    Output:  wb.Rel("upper.txt"),
})
```

**Normalized intent** (`textpipe intent`, full file in
[docs/examples/textpipe.intent.json](docs/examples/textpipe.intent.json)).
The reference survives as data, and dependencies are derived from it:

```json
{ "address": "transform.upper", "kind": "transform", "class": "task",
  "owns": "upper.txt", "deps": ["file.input"],
  "spec": { "command": ["tr", "[:lower:]", "[:upper:]"],
            "stdin": { "$ref": "file.input", "path": "input.txt" },
            "output": { "path": "upper.txt" } } }
```

**Readable plan** (`textpipe plan -dir ws -var text=...`,
[docs/examples/3-plan-input-change.txt](docs/examples/3-plan-input-change.txt)):

```
  ~   file.input           update      input.txt
          the quick brown fox
          jumps over the lazy dog
        + then naps
  >   transform.upper      run         upper.txt
        why: input file.input changed since run #3 (sha256:1448d86390be -> sha256:0c38f6731729)
```

**Apply and observed outcome**: `apply` executes the plan, prints what
each step did, then plans the same intent again against what is now on
disk and reports whether anything is still pending
([docs/examples/5-apply-partial-failure.txt](docs/examples/5-apply-partial-failure.txt)).

Every file in [docs/examples](docs/examples) is a golden output that
`go test ./e2e` checks byte for byte. A saved plan is a JSON file
([docs/examples/1-plan.json](docs/examples/1-plan.json)).

## Layers and what each owns

| Layer | Package | Owns | Performs effects |
|---|---|---|---|
| Foundation | `foundation` | atomic file writes, child processes, directories, observations, the evidence journal | yes: the only package that does |
| Typed API | `wb` | declarations, `Ref`/`Path` references, params, the normalized intent | no |
| Planner/applier | `engine` | the adapter contract, plan computation, stale-plan checks, apply order, the replay rule | only through adapters |
| Adapters | `adapters/*` | one kind each: typed constructor plus observe/plan/apply | through the foundation |
| CLI | `cli` | `intent`, `plan`, `apply`, and rendering | through the engine |
| Workflows | `workflows/*`, `cmd/*` | user source and the program built from it | no, they only declare |
| Baseline | `baseline` | the same workload as a plain program | through the foundation |

The foundation is the operational layer that owns real effects and
observations. The typed API is an optional client of it. The baseline
uses it directly, with no graph, intent or plan. The engine keeps **no
state of its own**. Backend facts are the workspace files and the
foundation's evidence journal (`<workspace>/.wb/evidence.jsonl`).
Planner output is the plan, which is printed or saved to a file wherever
you ask. Nothing in the engine duplicates ownership or authority records.

## Declared semantics

**Identity.** A node's identity is its address, `<kind>.<name>`
(`file.input`, `transform.upper`). The path is an attribute. Renaming a
node makes a new node with no history.

**Persistent resources, one-shot tasks and retained evidence.**
- *Resources* (`file`, `scratchdir`) are desired state. Every plan
  compares the observed object with the declaration and converges it:
  create, update, or a destructive replace.
- *Tasks* (`transform`) are one-shot work that produces an output
  artifact. A task is not converged. It runs again only when stale.
- *Evidence* is the append-only journal of what actually happened:
  `apply` records for resource changes, and `start`/`finish` records
  around every task run, with the fingerprint, input digests, status and
  output digest. Plans read task evidence. Resource records are for audit
  only, because resources are re-observed directly.

**Ownership of files.**
- Every node owns exactly one workspace path, and no two nodes may own
  the same path. A path may sit inside another node's path only if it
  belongs to a task that references that node (the report inside
  `work/`).
- Declared path names are limited to `A-Z a-z 0-9 . _ -` and compared
  case-insensitively. That way one file has one owner even on a
  filesystem that folds case or normalizes Unicode, as macOS does by
  default.
- `.wb` (in any case) and any name containing `.wb-` are reserved for the
  engine.
- These rules are enforced at declaration time. The engine enforces them
  again on every intent it consumes, including one read back from a
  saved plan. Each adapter refuses a spec whose path is not its node's
  owned path, so the rules cover the path an effect actually touches.
- The engine touches only declared paths, plus each scratch directory's
  two working names (`.<name>.wb-new`, `.<name>.wb-old`). All file
  operations go through `os.Root`, so no path, including one that
  resolves through a symlink, can leave the workspace.
- A file's content is owned by the workflow: an external edit is drift,
  which the next plan shows as a diff and the apply after that
  overwrites. A task owns its output, so an edited output makes the task
  run again.
- Removing a node from the source **abandons** its path; nothing is
  deleted. The only deletion is `scratchdir`'s declared replace, and it
  deletes only a directory carrying its ownership marker.

**Task replay and what makes a result stale.** A task is current when its
newest successful run had the same fingerprint (kind, spec and the digest
of every dependency) and its output is still byte-for-byte what that run
produced. It runs when any of these hold:
- it never succeeded, including a failed or interrupted latest attempt;
- a dependency's digest differs from the one recorded, which covers input
  content and a scratch directory's generation;
- its definition changed (command or paths);
- its output is missing or was changed outside the workflow;
- a dependency will be replaced, or is a task that runs first.

What is *not* fingerprinted: `PATH`, tool versions, and anything the
command reads without declaring it. Replay is **at-least-once**. A crash
between the effect and its `finish` record reruns the task, so commands
must tolerate running twice. Outputs are committed by temp file plus
rename, so a failed run never leaves a torn output and keeps the
previous one. There is **no rollback**: a failed apply keeps its
completed steps, skips the dependents of the failure, and still runs
unrelated steps.

**Stale plans.** A saved plan records the evidence head and what it
observed for every node. `apply -plan` first re-observes all of it. Any
difference refuses the whole plan with exit code 3, and performs no
effects. Just before each effect, the applier checks again: a resource
must still be as planned, and a task's inputs must match what the plan
predicted. That narrows the race with a concurrent writer but cannot
close it without backend locking.

**Recovery and what a fresh caller needs.** Only the workspace and its
evidence journal. There is no apply cursor, lock or progress file: a
fresh `plan` re-observes everything and explains what happened, for
example "run #2 started but never recorded a result (interrupted)". The
recovery path depends on **one piece of new persistent state introduced
by this entry**, the evidence journal. It belongs to the foundation, and
the baseline relies on it too. The typed layer adds none; saved plan
files are optional and transient.

## Does evaluating the source execute arbitrary code?

**Yes.** The workflow source is Go. `intent`, `plan` and plain `apply`
compile and run it with your full privileges, before any plan is shown.
A definition can read files, the network or the clock, and nothing
stops it. There is no sandbox, and none is claimed. What *is* enforced:
- **The boundary is the intent.** Specs must marshal to JSON, so a
  closure cannot become part of desired work; `Declare` rejects it. The
  engine consumes only the intent, and `textpipe intent` prints exactly
  what it will consume.
- **`apply -plan FILE` does not evaluate the definition.** It applies the
  reviewed plan as data. The program binary is still user code.
- Transform commands are arbitrary commands by design, and child
  processes are not confined. Only the foundation's own file operations
  are.

## Determinism

- **Intent:** a pure definition yields byte-identical intent. `Evaluate`
  runs the definition twice and refuses differing results, a cheap guard
  against the clock or randomness, not a proof of purity. Params are the
  sanctioned input: every value read is recorded in the intent, and an
  unknown `-var` is an error.
- **Plan:** a function of the intent, the observed files and the
  evidence. It contains no timestamps, which is why golden tests can
  compare it byte for byte.
- **Apply:** sequential, in declaration order. Declaration order is
  always topological, because a `Ref` exists only after its node is
  declared.
- **Not deterministic:** the commands themselves (assumed, not checked),
  and the evidence timestamps (recorded, never used for decisions).

## Extension: the third adapter

The disposable directory ([adapters/scratchdir](adapters/scratchdir/scratchdir.go))
is materially different from a file:
- A change is a **destructive replace** keyed by a generation, and stray
  contents are wiped.
- It **refuses to adopt** a non-empty directory without its marker. It
  adopts an empty one, such as one made by hand with `mkdir`.
- It is **crash-safe**. It builds the new directory beside the old one
  and swaps it in with renames, deleting old contents only after the new
  directory is in place. A kill at any point therefore leaves the old
  directory, the new one, or nothing at the path, never an unmarked
  directory, and the next apply of that node removes any leftover working
  copies.

The extension commit (`43e4973`) changed:

| File | Lines | What |
|---|---|---|
| `adapters/scratchdir/scratchdir.go` | +113 (new) | spec, typed constructor, `Plan`/`Apply` |
| `workflows/scratchpipe/scratchpipe.go` | +28 (new) | composes `textpipe.Declare` with the new node and a report task |
| `cmd/scratchpipe/main.go` | +19 (new) | registers three adapters |
| `foundation/workspace.go` | +19 | one new observation, `DirEmpty` |
| `wb/`, `engine/`, `cli/`, other adapters | **0** | |

The later review-fix commit made the adapter's apply crash-safe. It grew
to 155 lines and needed two more foundation effects, `Rename` and a
non-recursive `Remove`. That commit also changed `wb` and `engine`, but
for unrelated review findings: path aliasing, reference validation, and
validating saved plans. No adapter change required a planner change.

An adapter implements `engine.Resource` (`Plan`, `Apply`) or
`engine.Task` (`Observe`, `Run`), plus `Kind`. Registration is explicit
in the program's `main`. `TestCoreImportsNoAdapter` asserts that `wb`,
`engine`, `cli` and `foundation` import no adapter. **Dynamic plugin
loading was not needed and not built.** In a compiled host language, an
extension is a package that implements an interface, and linking it is
the build. Plugins would add an ABI and a loader and would buy nothing
here.

## Where the typed API helps, and where host freedom hurts

Helps:
- **Checked references.** `transform.Spec.Stdin` is a `wb.Ref`, so
  passing a literal path does not compile. In Go code a `Ref` comes only
  from a declaration, so dangling references and cycles cannot be
  written. Refs also decode from JSON, so the engine checks the intent as
  data: a `$ref` (a `Ref`) must name exactly the path its node owns, and
  an `$in` (a joined `Path`) may name a path inside it. That catches a
  cross-graph `Ref` and a forged reference with the wrong path. It does
  not catch a hand-built spec that uses case-variant keys (see Limits).
- **Composition is function calls.** `scratchpipe` reuses `textpipe`
  through `textpipe.Declare(g, p)` and wires its report to
  `pipe.Upper`: no module system, variables or includes to invent.
- **Existing tooling.** Types, go-to-definition, rename, `gofmt`, `go vet`,
  golangci-lint and `go test` all work on workflows and adapters
  unchanged. There is no parser, grammar or formatter to maintain.

Hurts:
- **Arbitrary code at evaluation.** See above. Reviewing a workflow
  means reviewing a program; the dependable review artifact is the
  intent and the plan, not the source.
- **Nondeterminism is only partly guarded.** Reading the environment or
  a changing file inside a definition is legal Go, and the double
  evaluation catches only changes between two back-to-back calls.
- **Abstraction can hide intent.** Loops and conditionals can generate
  nodes that nobody sees in the source.
- **A rebuild for every change,** and authors need the Go toolchain.
  Non-programmers cannot edit a workflow.
- **Types are only as precise as the API.** This POC has one `Path`
  type, params are strings, and adapter specs are checked at plan time,
  not compile time.

## Compared with a plain script and a data file

The baseline ([baseline/main.go](baseline/main.go), 119 lines) runs the
same workload directly over the same foundation and evidence. It is not
handicapped. Its test shows it handles the common cases correctly.

| Case | Baseline | Typed API |
|---|---|---|
| 1. initial run | runs | plan first, then apply |
| 2. unchanged re-run | no effects | no effects, and a plan that says so |
| 3. input change | reruns both transforms | shows the diff and the reasons before running them |
| 4. edit between plan and apply | no plan exists: it acts on whatever it finds, overwriting the edit unseen | refuses the stale plan; the new plan shows the overwrite |
| 5. partial failure + retry | retries only the failed step | same, and says why each step runs or doesn't |
| crash recovery | reruns via evidence | same, and explains the interrupted run |
| 6. new resource kind | edit the script's control flow | a new adapter package; planner untouched |

Size, counted as total lines including comments: the foundation is 477
lines, shared by both. The typed layer is `wb` 528 + `engine` 698 +
`cli` 510 lines, plus adapters of 87 to 155 lines each. The baseline is
119. The workflow source is 44 lines.

**For one small workflow run by one person, the baseline is sufficient
for correct execution.** The declarative layer earns its extra concepts
in three places:
1. a reviewable plan before any effect;
2. refusal to apply a plan against a workspace that changed underneath it;
3. uniform replay, ownership and stale rules across kinds, so that
   adding a kind does not mean re-deriving them.

Whether those matter enough is the operator's call.

A **data file** (JSON or YAML) could express this intent. The normalized
intent *is* one, and a data-file front end could feed the same engine
unchanged (not built). Go adds authoring over a data file: checked
references, composition and tooling. It gives up the one thing a data
file guarantees, that reading it runs no code.

## Value to a Workbench operator

Before anything happens, you see what will change and why, including
which task runs because of which input. A plan you approved cannot
silently act on a workspace that changed after you looked. A failed or
killed run is safe to retry: completed work is not repeated, and a
fresh session can reconstruct what happened from the workspace alone. A
new kind of resource is a new package, not a rewrite of orchestration
logic. There is no commercial evidence here, only these properties and
their tests.

**Fleet and Rooms** are not integrated, and nothing about them is
simulated or proven. The natural mapping would be for Fleet's records to
play the foundation's evidence role, with this typed layer as an
optional client that never becomes a second authority or supervisor.
That is a description of fit, not a tested claim.

## Dependencies

- Go standard library only. `go.mod` has no `require` lines.
- Go 1.25+, because the foundation uses `os.Root` (`Rename`, `RemoveAll`,
  `MkdirAll`) to confine effects to the workspace.
- The demo and tests shell out to `tr`, `awk`, `sort`, `sed` and `wc`;
  the demo uses bash.
- Optional: golangci-lint with the included `.golangci.yml`.

## Limits

- **One writer per workspace.** There is no lock. Concurrent applies can
  interleave; the evidence-head and per-step checks narrow the window but
  do not prevent it.
- **Check-then-act race.** It remains between each step's re-check and
  its effect.
- **No fsync.** Durability across power loss is not guaranteed.
- **Crash leftovers.**
  - A torn evidence line is skipped, so that work may run again.
  - A crash mid-write can leave a stray `.<name>.wb-tmp-*` file, which is
    not cleaned up.
  - A crash during a scratch directory's final cleanup leaves
    `.<name>.wb-old` until that directory next changes. If that leftover
    cannot be deleted (permissions), later applies of that directory fail
    until it is removed by hand.
- **Tasks are at-least-once** and commands must be idempotent. `PATH`,
  tool versions and undeclared reads are not fingerprinted.
- **Child processes are not sandboxed.**
- **Saved plans are trusted input.**
  - The engine re-validates a loaded intent's structure and matches steps
    to nodes. A mismatch, or a kind with no adapter, is refused with
    exit 1. A spec an adapter can no longer accept is reported as stale
    (exit 3). Either way, nothing is applied.
  - The intent digest is unkeyed, so it catches accidental edits, not
    deliberate tampering.
  - A resource step takes effect only if a fresh plan of that node, made
    just before the effect, proposes the same op and desired state. An
    edited task op can at most re-run or skip that task.
- **Open review finding (P2, deferred).** Reference validation reads
  `$ref`, `$in` and `path` by exact key, but Go's JSON decoding matches
  keys case-insensitively.
  - A hand-built raw-JSON spec, or an edited saved plan, with a key like
    `"PATH"` or `"$REF"` can make a task read an undeclared file or drop
    a dependency, so a re-run is missed.
  - Specs built from Go values through handles cannot produce this.
  - The fix is to parse references strictly on both sides. It is
    deferred from the original POC review.
- **No garbage collection.** Removing or renaming a node abandons its
  files, and resources cannot nest inside other resources.
- **Rendering.** Diffs cover only small UTF-8 text (4 KiB), and
  trailing-newline differences are not shown.
- **Scope.** Params are strings, execution is sequential, path names are
  ASCII-only, and the scratch marker is an ownership convention, not a
  security boundary.
- **Platform.** Tested on macOS only. Linux should work, and Windows is
  not supported.
