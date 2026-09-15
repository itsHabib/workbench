# wb: a small language for kept resources and one-shot work

This is the proof of concept for the "purpose-built language" entry in the
Workbench language bakeoff. `wb` reads a `.wb` file that declares
persistent resources (`keep`) and one-shot work (`run`). It compiles the file
to an inspectable normalized intent, shows a readable plan against the
workspace as it is now, and applies that plan. It refuses a stale plan, stops
at the first failure without rolling back, and keeps evidence so that a
fresh caller can pick up the work.

The build is Go standard library only: one binary, with its file effects
confined to one workspace directory. It makes no claim to be the winner.
The comparison with a plain script is [below](#compared-with-a-plain-script).

## Try it

```bash
./demo.sh
```

The demo runs the six common cases in a freshly created temporary
directory. It needs Go 1.25+, `sh`, `tr`, `awk` and `sort`. The transcript
is checked in at [examples/demo.transcript.txt](examples/demo.transcript.txt),
and the tests compare it against real output. [DEMO.md](DEMO.md) is the
60-second tour.

```bash
go test ./...
```

## The language

```
keep file notes {
  path "notes.txt"
  text "the quick brown fox jumps over the lazy dog\n"
}

run command shout {
  stdin  notes.path          # a named reference: a dependency edge and a tracked input
  argv   "tr" "a-z" "A-Z"
  stdout "notes.upper.txt"   # a path this declaration owns
}
```

This is the whole grammar:

```
file  = { decl }
decl  = lifecycle kind name "{" { attr } "}"
attr  = name value { value } (newline | ";" | "}")
value = string | name "." name
```

- **The lifecycle comes first.** `keep` declares persistent desired state
  that wb converges on every apply. `run` declares one-shot work that wb
  replays only when it is stale. The checker rejects a keyword that does not
  match the kind: `command is one-shot work that wb replays when stale:
  write "run command shout"`.
- **Kinds come from adapters, not from the grammar.** `file`, `command` and
  `dir` are registered adapter types. The grammar knows only words, strings
  and `name.attr` references. `wb kinds` prints each kind's schema.
- **A reference** such as `notes.path` resolves to another declaration's
  attribute value. It also orders the two declarations and makes the
  upstream's version an input of the downstream work.
- **Strings** are double-quoted and fit on one line, with the escapes
  `\n \t \" \\`. There are no numbers, expressions, interpolation,
  variables, loops, modules or conditionals.

The checker reports every error at once, as `file:line:col` (see
[examples/invalid.errors.txt](examples/invalid.errors.txt)). It checks:

- that each lifecycle matches its kind
- unknown kinds and attributes, missing required attributes, and the number of values
- references that are unknown, point at the declaration itself, or name an attribute that is not exported
- dependency cycles
- paths that leave the workspace, are not canonical, are not printable ASCII, fall inside `.wb/`, or use the `.wb-partial` suffix in any component
- owned paths that overlap, compared without regard to case
- a literal read of another declaration's owned path, which would hide the dependency

## Source to outcome

| Step | Command | Example |
|---|---|---|
| source | | [pipeline.wb](examples/pipeline.wb) |
| normalized intent: resolved values, dependency order, owned paths, edges | `wb intent` | [pipeline.intent.json](examples/pipeline.intent.json) |
| readable proposed changes, with reasons and line diffs | `wb plan` | [pipeline.plan.txt](examples/pipeline.plan.txt), [change.plan.txt](examples/change.plan.txt) |
| saved plan: intent, changes and the facts observed | `wb plan -out` | [pipeline.plan.json](examples/pipeline.plan.json) |
| apply, reporting journal sequence numbers | `wb apply`, `wb apply -plan` | [demo transcript](examples/demo.transcript.txt) |
| observed outcome and retained evidence | the files, `wb evidence` | same |

## Declared semantics

| Topic | Rule |
|---|---|
| Resource identity | A declaration's name is its identity in the journal, and its owned path is its identity on disk. Renaming a declaration creates a new identity. Work under the new name runs once. Work that references a renamed declaration also runs once, because its inputs are keyed by upstream name. A renamed kept resource re-converges, which does nothing if the content already matches. |
| Ownership | Only `owned path` attributes are owned: `file.path`, `command.stdout` and `dir.path`. Owned paths must not overlap. Paths are printable ASCII and compared without regard to case, so two spellings cannot name one file on a case-insensitive filesystem. wb writes only to owned paths, to parent directories it creates for them, and to `.wb/`. Each write goes through a fresh temporary `<path>.wb-partial`, created exclusively, so a write never follows a link planted there. wb writes only through real directories: an owned path under a symlinked parent directory is refused, and `.wb` and its journal must be a real directory and a regular file. It never deletes anything, except a leftover temporary file at `<path>.wb-partial`, which it removes before writing `<path>`. Removing a declaration leaves its files in place, unowned. |
| Kept resources | wb converges them from observation alone. It creates what is missing, updates what differs, and leaves what matches. It reports a conflict, and apply refuses, when converging would mean destroying something, such as a directory or a symlink where a file belongs. An outside edit is overwritten, and the plan says so first. |
| Task replay | A `run` declaration runs when it never completed, when its definition changed, when an input's version differs from its last receipt, or when its owned output differs from that receipt. Otherwise wb skips it. Work whose output path holds a directory or a symlink is a conflict, found before anything runs. Replay is at-least-once, never exactly-once: if a crash lands after the child exits but before the finish record, the work runs again. Output goes to a temporary file that is renamed into place only on success, so a failed or interrupted run leaves the previous output in place. |
| What makes a result stale | Four things. The definition digest covers the kind and the resolved attributes, including argv and paths. Each upstream has a version: the desired content digest for a kept file, a constant for a dir, and the output digest for work. Each literal `path` attribute contributes the digest of the content it reaches; a symlink inside the workspace is followed. Last is the output's own digest. **Not tracked:** environment variables (including `PATH`), the program binary, time, and anything the command reads that is not declared. |
| Work downstream of work | Its input does not exist until the upstream runs, so the plan marks it `?` "decided during apply". Apply runs it only if the upstream output actually changed. |
| Stale plans | A plan records every fact it observed: each owned path's digest, each piece of work's last journal record, and each literal input's digest. `apply -plan` observes all of them again before any effect. If any fact differs, it refuses with exit 3. It also rechecks each resource immediately before writing it. A saved plan carries its source text: `apply -plan` re-checks that text with the same checker and refuses, with exit 2, a plan whose declarations no longer match it. |
| Failure | Apply stops at the first failure and rolls nothing back. Completed effects are recorded. To retry, run `wb apply` again; it skips work that is current. |
| Interruption | wb writes the start record before the child process starts. A SIGKILL mid-run leaves a start record with no outcome. The next plan reports "attempt #N was interrupted" and runs the work again. |

## Where state lives

wb keeps two layers apart.

- **The foundation** ([internal/foundation](internal/foundation)) owns the
  workspace handle, an `os.Root`, and every workspace effect goes through
  it: adapters receive the handle and never open paths themselves. It also
  owns the evidence journal, `.wb/journal.jsonl`. It handles effects and
  observations and knows nothing about the language. (The CLI separately
  reads the source file and writes a saved plan wherever `-out` points.)
- **The language and planner** ([internal/lang](internal/lang) →
  [internal/intent](internal/intent) → [internal/plan](internal/plan)) keep
  no persistent state of their own. Every plan is recomputed from the
  workspace and the journal. A saved plan file is an optional review
  artifact, not state, and deleting it loses nothing.

Adapters ([adapters/](adapters)) implement each kind's effects on top of the
foundation.

**Disclosure: the journal is new persistent state that this entry
introduces.** Recovering *work* depends on it, because without it wb cannot
tell whether a run's output matches the run's current inputs. Deleting the
journal makes every `run` declaration run once more. Kept resources are
unaffected, since wb derives them from observation. In Workbench, the
operational foundation's existing receipts (Fleet's work records, or gate's
log) would fill the journal's role, and wb would read them instead of
keeping its own. That integration is not built. Nothing here reads or
changes Fleet or Rooms state.

A fresh caller needs three things: the source file, the workspace and its
journal. From those, `wb plan` shows what is current, what failed (with the
error), what was interrupted, and what changed outside wb. Tests check each
of these (see [Verification](#verification)).

## Extending: the third adapter

`keep dir` is a disposable scratch directory. wb makes sure it exists and
leaves its contents alone. Recreating it never makes dependent work stale,
and wb refuses to replace a non-directory at its path. The whole extension
is one commit:

```
adapters/dir/dir.go  | 55 +++   the adapter: Schema, Observe, Propose, Apply
cmd/wb/main.go       |  3 +-   one import and one registry entry
cmd/wb/case6_test.go | 55 +++
examples/scratch.wb  | 16 +++
```

```go
reg, err := kind.NewRegistry(
	[]kind.Resource{file.Adapter{}, dir.Adapter{}},
	[]kind.Work{command.Adapter{}},
)
```

`TestLanguageAndPlannerImportNoAdapters` runs `go list -deps` to confirm
that `internal/lang` and `internal/plan` import no adapter package. Adapters
register at compile time; dynamic plugin loading was not needed.

Some things an adapter cannot do without core changes: add a lifecycle, add
a field type (numbers, booleans), or reference a computed value. A `keep`
resource also cannot consume upstream *content*, only attribute values.

## Compared with a plain script

[baseline/pipeline.sh](baseline/pipeline.sh) runs the same workload in 28
lines of POSIX sh. It uses the same operations and keeps checksum stamps as
its records. [baseline/baseline_test.go](baseline/baseline_test.go) holds it
to the same bar wherever the comparison applies.

| Case | Baseline script | wb |
|---|---|---|
| 1 initial apply | yes | yes, with a preview first |
| 2 unchanged re-apply | yes, from its stamps | yes, from its receipts |
| 3 input change | yes, reruns both steps | yes. Before anything runs, the plan shows the line diff and why each step reruns |
| 4 change between plan and apply | does not apply: the script has no plan step, and it silently overwrites a hand edit on the next run | the saved plan is refused and nothing changes. The re-plan names the hand edit |
| 5 partial failure, then retry | yes: `set -e`, and a stamp is written only after success | yes, and the plan names the failed attempt and its error |
| 6 third kind | edit the script: a `mkdir` and a step in a subshell | a new adapter (38 code lines) and a registry entry, with no language or planner change. The `.wb` file gains two declarations |
| interrupted run | the step reruns because its stamp is absent | reruns, and the plan says the attempt was interrupted |

**What wb adds over the script:** a reviewable preview with reasons,
refusal of stale plans, validation before any effect (ownership,
confinement, references, cycles), and evidence that explains recovery.

**What wb costs:** about 2,500 lines of Go against 28 lines of sh, plus a
new syntax to learn. Every new need, such as numbers, conditionals or
commands with several inputs, becomes a language change.

A JSON or YAML data file with the same schema would get the same planner
benefits without writing a parser; the next section weighs that trade-off.

## Is the syntax earning its keep?

Building the POC gave a clear answer in both directions.

- **What the purpose-built syntax earns:**
  - The lifecycle is the first word of each declaration, so a reader sees
    "converged" or "replayed" without knowing the kind.
  - Diagnostics speak the domain: `reference notes.path instead so wb
    settles notes first and tracks it as an input`.
  - It is brief: keys need no quotes and lists need no brackets.
- **What it does not earn: most of the value.** Ownership checks,
  staleness, stale-plan refusal, evidence and recovery all live in the
  checker and planner. Those work on the normalized intent, and they would
  run unchanged behind HCL, JSON or a typed API. The requirement for
  external adapter kinds also forced a generic block-and-attribute grammar,
  which ends up close to generic configuration anyway.
- **The ongoing cost:**
  - The lexer and parser are about 355 lines. The checker adds about 410,
    plus about 210 lines of tests and a fuzz target.
  - There is no formatter, editor support, syntax highlighting, language
    server, schema-driven completion or comment-preserving round trip.
    HCL and JSON ecosystems provide all of those for free.
  - The pressure to grow is real: commands with several inputs, string
    interpolation for derived paths, numbers and booleans, a per-run
    environment. Each one means grammar, checker, documentation and test
    work.
- **A problem found while using it:** newlines end attributes, so a one-line
  block with several attributes needs `;`. The tests hit this, and `;` was
  added.

## Value to a Workbench operator

A plausible use is setting up a local agent workspace: seeding task inputs
and running derivation steps. The operator sees exactly what will change and
why, gets a refusal instead of a surprise when a reviewed plan has gone
stale, and hands any fresh agent session a `wb plan` that explains what is
done, what failed and what was interrupted. If the operator never wants a
preview or a refusal step, the plain script does the same work with far less
machinery. No evidence has been gathered about a buyer.

## Limits

- One writer per workspace. There is no locking, so two concurrent applies
  can interleave.
- Stale-plan refusal compares facts when apply starts and rechecks each
  kept resource right before writing it. A window remains between a check
  and its effect. Work is not rechecked against the plan just before it
  runs; it is re-decided from fresh observation instead. So if someone
  edits a work's owned output while earlier work in the same apply is
  running, wb overwrites the edit rather than refusing (see REVIEW.md).
- Child processes are not sandboxed. wb confines its own file effects with
  `os.Root`, but a command can write anywhere its user can. Only its stdout
  is captured into the owned file.
- wb trusts exit codes. A pipeline that hides a failure, such as
  `sh -c "wc -w | tr -d ' '"`, records a success with whatever output it
  produced. Building the demo turned this up; the demo now calls `awk`
  directly.
- Some inputs are not tracked: the environment, `PATH`, program binaries and
  undeclared files.
- There is no deletion or garbage collection. Removed declarations leave
  their files behind. After a SIGKILL, a `*.wb-partial` file can remain; the
  next attempt overwrites it.
- A symlink that aliases another declaration's owned path hides that
  dependency: the reading work is neither ordered after its owner nor
  previewed correctly, so it can lag one apply behind.
- Paths are limited to printable ASCII.
- Apply stops at the first failure, so independent work after that point
  waits for the next apply.
- A command gets one input, on `stdin`, and `argv` cannot reference other
  declarations.
- The journal grows forever, with no compaction.
- Only macOS was tested. The interruption test needs Unix process groups.
- The tests cannot tell `os.Root` apart from plain `os` calls in the write
  primitives: wb checks each path before writing, and those checks refuse
  every escape a deterministic test can set up. `os.Root` matters only if
  a symlink is swapped in concurrently, which is defense in depth that
  the tests do not pin down.

## Verification

```bash
go test ./...                 # six cases, crash recovery, confinement, examples, demo transcript, baseline
go test -race -count=3 ./cmd/wb/...
go test ./internal/lang -run '^$' -fuzz FuzzCompile -fuzztime 20s
```

| Case | Test |
|---|---|
| 1 | `TestCase1InitialPlanAndApply` |
| 2 | `TestCase2UnchangedReapplyRepeatsNothing`: same bytes, same mtimes, no new journal records, no child processes |
| 3 | `TestCase3InputChangePlansDownstreamWork`, `TestDownstreamWorkIsDecidedDuringApply` |
| 4 | `TestCase4StalePlanIsRefusedWithoutEffects`, `TestCase4StalePlanOnOwnedOutput` |
| 5 | `TestCase5PartialFailureRetrySkipsCompletedWork` |
| 6 | `TestCase6DirAdapter`, `TestLanguageAndPlannerImportNoAdapters` |
| recovery | `TestInterruptedApplyIsVisibleToAFreshCaller` (SIGKILL mid-run), `TestTornJournalLineIsIgnoredAndSealed` |
| safety | `TestEffectsStayInsideTheWorkspace`, `TestConflictIsRefusedBeforeAnyEffect`, and effect-level tests in `internal/foundation` and `adapters/command` that reach the write itself |
| review findings | `cmd/wb/hardening_test.go` has one test per finding the independent review confirmed; see [REVIEW.md](REVIEW.md) |

The test commands append to a log outside the workspace. That way
"nothing was repeated" is measured against real child-process invocations,
not only against wb's own journal.

## Layout, dependencies and size

| Package | Role | Code lines |
|---|---|---|
| `internal/lang` | lexer (155), parser (200), checker (408) | 763 |
| `internal/intent` | normalized representation | 39 |
| `internal/kind` | adapter contract and registry | 145 |
| `internal/plan` | planning, stale checks, apply, rendering | 622 |
| `internal/foundation` | `os.Root` workspace, digests, journal | 295 |
| `internal/cli` | commands, saved-plan checks, exit codes | 367 |
| `adapters/{file,command,dir}` | the three kinds | 81 + 110 + 38 |
| `cmd/wb` | wiring | 27 |
| tests | Go | about 1,320 |
| `baseline/pipeline.sh`, `demo.sh` | sh | 28, 84 |

Code lines exclude blank lines and comments. Dependencies: the Go standard
library only (Go 1.25+ for `os.Root`). The demo and tests also call `sh`, `tr`,
`awk`, `sort` and `wc`. Exit codes: 0 ok, 1 failed partway, 2 invalid input,
3 refused (stale plan or conflict).
