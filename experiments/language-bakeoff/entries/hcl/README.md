# HCL entry

The HCL entry in the workbench language bakeoff. `wb` is a small Go CLI.
You describe intended resources and one-shot work in HCL, inspect a plan,
apply it, and safely repeat or resume. It uses the real HCL parser
(`github.com/hashicorp/hcl/v2`) for syntax, references and expressions; a
small compiler and an adapter interface supply the execution semantics.

This is a proof of concept for assessment, not a platform. It declares no
winner.

## Try it

```sh
./demo.sh        # six cases in a fresh temp directory, about 2 s (Go 1.24+, tr)
go test ./...    # deterministic assertions for the six cases and more
baseline/run.sh baseline/desired "$(mktemp -d)"   # the direct-tools baseline
```

`DEMO.md` is the 60-second tour with excerpts from a real run.

## Pipeline

```
*.wb.hcl ──wb compile──▶ normalized intent (JSON: nodes, deps, evaluated attrs, owned/read paths)
         ──wb plan────▶ proposed changes + the observations that justify them  [-out plan.json]
         ──wb apply───▶ effects through adapters, journaled                    [-plan plan.json]
                        └▶ observed outcome: a fresh plan made after the apply
         ──wb log─────▶ retained evidence: every attempt, including interrupted ones
```

`wb plan` writes nothing unless `-out` is given, and it never touches the
workspace. Exit codes: 0 ok, 1 failed or incomplete, 2 usage or source
error, 3 stale saved plan refused.

## The model

Three things are kept apart.

| | Declared as | What it is | How change is detected | Where its truth lives |
|---|---|---|---|---|
| Resource | `resource "file" "input" {}` | persistent desired state | the adapter observes the backend on every plan | the workspace itself; there is no state file |
| Task | `task "exec" "shout" {}` | one-shot work | its receipt vs. current config, inputs and outputs | its outputs plus its latest journal entry |
| Evidence | (implicit) | append-only record of every effect attempt | never rewritten | `.wb/journal.jsonl` |

Parser and planner state (intent, plans) is derived and disposable. Backend
facts come from adapters' observations. The journal records only wb's own
attempts. It is not an ownership or authority registry, and planning never
consults it for resources.

**Identity.** A block's identity is its address `<type>.<name>`
(`file.input`). A resource's backend identity is what its adapter observes:
the path for `file` and `dir`.

**Dependencies.** A reference such as `stdin = file.input.path` is the
dependency edge. HCL extracts references (`Expression.Variables()`); the
compiler orders blocks topologically (ties keep source order) and reports
cycles with the loop spelled out.

**Task replay semantics.** A task runs when:

1. it has no successful receipt: never ran, last attempt failed, or the last
   attempt was interrupted (started, no result recorded);
2. its evaluated configuration differs from the receipt's;
3. a block it references has a planned change, or that block's observed
   state (a resource's fingerprint, a task's output digests) differs from
   the state the receipt recorded it ran against;
4. a declared input's content (sha256) differs from the receipt's;
5. a declared output is missing or differs from what the last run wrote.

Nothing else makes a result stale: not time, not the program binary, not the
environment (the child gets only `PATH` and `LC_ALL=C`), and not undeclared
files the command happens to read. The "planned change" half of rule 3 is a
projection, because a plan cannot know what an upstream block will contain
after apply. Once a task's upstream steps have run, apply re-checks every
rule with real observations and skips a task that turned out fresh (early
cutoff). The recorded half of rule 3 means that a referenced block that
really changed always re-runs the task, even when the reference goes
through argv rather than a declared input, and even after a crash that
separated the upstream's effect from the task.

**Ownership of files.** Adapters declare the paths a block owns and reads.
The compiler enforces the rules below without knowing any adapter:

- each path has exactly one owner, compared case-insensitively
  (`NOTES.txt` and `notes.txt` are one file on macOS's default filesystem);
- source files, `.wb/`, and any top-level `*.wb.hcl` a block could create
  cannot be owned;
- paths are workspace-relative, and `..` and absolute paths are rejected;
- a block never reads a path it writes;
- a block that reads a path another block owns, or writes inside a
  directory another block owns, must reference that block. The error names
  the reference to add (`Use dir.build.path`).

wb never deletes. A task output that is no longer declared is left in place,
and the plan says so. A task's first run over a file wb did not write says
so too. Replacing a file keeps its permission bits. The `file` adapter
refuses to manage symlinks, and the `dir` adapter refuses to replace a
non-directory.

**Saved plans.** A plan records what it observed: each resource's
fingerprint, and each task's receipt and input/output digests.
`wb apply -plan` re-plans and refuses (exit 3, no effects) if the source,
the steps or any observation differ, listing each difference. It also
refuses if the plan can no longer be checked, for example because the
source stopped compiling. Each resource step also re-checks its own
precondition immediately before its effect, so a change made during a long
apply fails that step instead of being overwritten. Apply never does more
than the reviewed plan. It can do less: a planned task run is skipped when
re-observation shows the task is already fresh.

**No exactly-once, no rollback.** A failed step blocks only its dependents.
Unrelated steps still run, and completed effects stay in place. A crash
between an effect and its journal entry leaves a `started` entry with no
result, and the task re-runs: tasks are at-least-once. Resources need no
evidence to recover, because the next plan observes them.

## Recovery: what a fresh caller needs

- Resources: nothing. The next `wb plan` observes them.
- Tasks: `.wb/journal.jsonl`, which **is new persistent state introduced by
  this entry**. A receipt holds the configuration, the upstream state,
  and the input and output digests the task ran with. `wb log` shows
  failed and interrupted attempts, and `wb plan` names the reason each task
  will run. A torn last line (a crash mid-append) is counted, reported and
  ignored. If the journal is deleted, every task re-runs once and resources
  are unaffected. A receipt with no recorded state for a referenced block
  (a reference added to an existing block, or a journal written before
  receipts recorded upstream state) also re-runs its task once.

`TestInterruptedTaskReRunsFromEvidence` and
`TestInterruptedResourceRecoversByObservation` exercise both paths through a
real process exit between effect and journal (`WB_FAULT=<address>:crash-after`).

## HCL: what earns its cost

Earns it:

- **A real parser with source ranges.** Every error carries file, line and a
  snippet through HCL's diagnostic writer, at no cost to this code:

  ```
  Error: Function calls are not supported

    on main.wb.hcl line 3, in resource "file" "input":
     3:   content = upper("hi")

  upper() could read the clock, files or the environment, which the planner cannot see. ...
  ```

- **Reference extraction.** `Expression.Variables()` yields the dependency
  graph, so there is no resolver to write.
- **Expression evaluation (cty).** Interpolation such as
  `"${dir.build.path}/output.txt"`, lists and conditionals come for free,
  with typed conversion errors.
- **Heredocs and a familiar shape.** Multi-line content stays readable in
  source and in source diffs. Anyone who has read Terraform reads this.

Left out deliberately, each with a specific error: `variable`, `locals`,
`module`, `provider`, `data`, `output`, `count`, `for_each`, `depends_on`,
`lifecycle`, and function calls (a function like `file()` or `timestamp()`
would be a hidden input). Unknown arguments, wrong kinds (`task "file"`),
undeclared references and wrong value types all fail at the offending line
(`internal/intent/compile_test.go` pins 28 of these).

What it costs:

- Six linked modules: hcl/v2, go-cty, go-textseg, levenshtein, go-wordwrap
  and x/text. The binary is 6.7 MB.
- The expression language is larger than the workload needs. The subset is
  enforced by this compiler, not by the grammar. Pure constructs such as
  conditionals and `for` expressions are allowed; calls are rejected.
- cty values stay at the compile boundary. Adapters only ever see Go
  strings and string lists; the `adapter` package does not import HCL.
- HCL's JSON syntax would be free to enable, but it is not wired up or tested.

Terraform is deliberately not copied: there are no providers to download, no
remote state, no registry, no "known after apply" machinery (references carry
configuration, runtime data flows through files), and no compatibility
surface.

## Extending: the third adapter

The extension contract is the `adapter` package: `Type`, `Kind`, `Fields`,
`Resolve`, `Paths`, plus either `Want`/`Observe`/`Apply` (resource) or
`Run` (task). Adapters are compiled in and registered in
`adapters.Builtin()`. Dynamic plugin loading was not needed: it would add a
process boundary, versioning and distribution (Terraform's provider model)
with no benefit for a single binary.

The `dir` adapter landed as its own commit. A later review fix corrected
its doc comment and nothing else:

```
feat(adapters): dir, the third adapter
 adapters/builtin.go |   1 +
 adapters/dir.go     | 111 +++++++++++++
 2 files changed, 112 insertions(+)
```

It is materially different from `file`. Its state is structural (existence
and permission bits), it refuses to replace a non-directory, and other
blocks nest inside it. The existing ownership check makes those blocks
reference it, with no dir-specific engine code. Two tests keep this honest:

- `TestAdapterAddedOutsideTheEngine` defines a symlink adapter inside the
  test file and drives compile, plan and apply with it;
- `TestEngineNeverImportsBuiltinAdapters` fails if any `internal/` package
  imports the builtin adapters.

## Compared with a plain script and data file

`baseline/run.sh` (44 lines of POSIX sh) is the same workload with direct
tools. Desired content lives in data files; each step is guarded (`cmp`, a
digest stamp); writes are temp-then-rename; the transform re-runs when the
input or output digest differs from its stamp. `TestBaselineOnTheCommonWorkload`
drives it through the same scenarios with the same witness:

| Case | wb | baseline |
|---|---|---|
| 1 initial apply | yes, after showing intent and plan | yes |
| 2 unchanged re-apply | no effects | no effects |
| 3 input change | plan shows the content diff and the downstream run before any effect | re-runs correctly; no preview |
| 4 edit between review and apply | reviewed plan refused (exit 3), nothing applied; re-plan shows the hand edit being overwritten | no plan to go stale; next run silently overwrites the hand edit |
| 5 partial failure | unrelated steps continue; retry runs only the failed task | `set -e` stops at the failure, so notes wait for the retry; guarded steps are not repeated |
| 6 third resource kind | new adapter file plus one line; the source adds a block | edit the script itself |

The baseline is small, dependency-free and correct for cases 1, 2, 3 and 5.
On one point it was better than wb's first version: after a hand edit is
reverted, its stamp saw the input was back to what the transform last
consumed and skipped the re-run. That finding is why wb's apply now
re-checks task staleness with real observations (early cutoff).

What wb adds over the script:

- intent and plan you can inspect before any effect;
- a reviewed plan that is refused if the world moved;
- compile-time ownership and reference checks;
- uniform evidence and interrupted-run reporting;
- extension without editing a procedure.

It costs about 3.1k lines of Go (below), six dependencies and new concepts:
resource vs. task vs. evidence, receipts, and saved plans. When one person
runs one workflow with no review step, the script suffices.

## Value to a Workbench operator

- **Review before effects.** `wb plan -out` is an artifact you can hold.
  `wb apply -plan` applies that plan, or refuses and applies nothing, and
  it never does more than the plan.
- **Resumability without memory.** A fresh session runs `wb log` and
  `wb plan` and sees what is failed, interrupted or pending, and why, with
  no conversation history.
- **Guard rails on files.** Two steps cannot both own a path, and a step
  cannot silently depend on a file it did not reference.

**Fleet and Rooms: explained, not integrated, not simulated.** wb would be
an optional client of the operational foundation. Each foundation operation
(observe a Fleet work record, open a Room) would be an adapter whose
`Observe` reads the foundation's own facts and whose `Apply` calls the
foundation's API. wb would never supervise and never hold ownership or
authority: its journal records only wb's own attempts. Nothing here touches
Fleet or Rooms, and no test here is evidence that such an integration works.

## Limits

- Tested on macOS (darwin/arm64, Go 1.26.5). The tests use inodes and `/bin/sh`.
- No locking: two concurrent applies in one workspace can interleave.
- Confinement is lexical. A symlinked parent directory can escape the
  workspace; the `file` adapter refuses symlinks only at the file itself.
- Path identity folds case, and HCL's value library normalizes every string
  (NFC), so composed and decomposed spellings in source also collide. On
  case-sensitive filesystems, declaring both `a.txt` and `A.txt` is refused
  even though they would be distinct files.
- A hard kill (`kill -9`) during a write can leave a `.<name>.wb-*` temp
  file beside the target. It is never renamed into place or read, and wb
  does not report or remove it.
- The per-step recheck narrows the window between observe and write but
  does not close it.
- Tasks are at-least-once. Only declared inputs and referenced blocks are
  tracked; a file named literally in `command` that no block owns is not
  (see rule 3 above).
- There are no deletes and no orphan detection. Removing a block leaves its
  file, and only task outputs get a "no longer declared" note.
- Diffs are shown only for small UTF-8 text (4 KiB, 200 lines).
- There are no task timeouts; Ctrl-C cancels the child.
- The journal grows without bound and is read whole.
- Plans carry digests, not signatures. They detect change, not tampering.

## Layout, dependencies and size

| Area | Files | Lines (incl. comments) |
|---|---|---|
| Extension contract | `adapter/` | 305 |
| Builtin adapters | `adapters/` (file 101, exec 142, dir 113, helpers 81) | 437 |
| HCL compiler | `internal/intent/` | 821 |
| Planner (policy, diff, render) | `internal/plan/` | 669 |
| Executor | `internal/apply/` | 258 |
| Evidence journal | `internal/evidence/` | 171 |
| CLI | `cmd/wb/` | 420 |
| Tests | `**/*_test.go` | 1421 |
| Demo, baseline | `demo.sh`, `baseline/run.sh` | 134 |

Direct dependencies: `github.com/hashicorp/hcl/v2 v2.24.0` and
`github.com/zclconf/go-cty v1.16.3`. Linked transitively:
`agext/levenshtein`, `apparentlymart/go-textseg/v15`,
`mitchellh/go-wordwrap`, `golang.org/x/text`. Everything else in `go.sum`
is HCL's own test and tooling dependencies, which are not linked. Lint
config (`.golangci.yml`) enables cyclop, gocognit, nestif and revive.
