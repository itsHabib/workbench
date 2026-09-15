# 60-second demo

```sh
./demo.sh      # builds wb, runs the six cases in a fresh temp dir, about 2 s
```

The excerpts below are from a real run. Every assertion about them lives in
`go test ./...`; the output alone proves nothing.

## 1. Source -> intent -> plan -> apply -> observed

The source is two resources and one task. The reference is the dependency:

```hcl
task "exec" "shout" {
  command = ["tr", "a-z", "A-Z"]
  stdin   = file.input.path        # edge: file.input -> exec.shout
  stdout  = "output.txt"
}
```

`wb compile` prints the normalized intent: nodes in dependency order with
evaluated attributes, `deps`, `owns` and `reads`. Then:

```
$ wb plan -out ../plan.json
  + file.input  create     input.txt: 34 bytes, sha256 1797f093c474
      + hello workbench
      + second line stays
  > exec.shout  run        never run
  + file.notes  create     notes.txt: 12 bytes, sha256 cf2f1fb5dae6
Plan: 2 to create, 0 to update, 1 to run, 0 unchanged.

$ wb apply -plan ../plan.json
  file.input  created    input.txt: 34 bytes, sha256 1797f093c474
  exec.shout  ran        wrote output.txt sha256:d22e7fe49b7b
  file.notes  created    notes.txt: 12 bytes, sha256 cf2f1fb5dae6
Observed after apply: converged (a fresh plan finds no changes).
```

## 2. Unchanged re-apply

```
$ wb apply
No changes. 3 unchanged.
Nothing to apply.
```

No new journal entries, no rewritten files (checked by inode), no child
process.

## 3. Input change -> downstream plan

```
  ~ file.input  update     input.txt: 34 bytes, sha256 1797f093c474 -> 32 bytes, sha256 4d5c2602b68b
      - hello workbench
      + hello bakeoff
        second line stays
  > exec.shout  run        upstream file.input changes
  = file.notes  unchanged
```

## 4. External edit between plan and apply

```
$ echo "edited by hand" >> input.txt
$ wb apply -plan ../plan.json
error: saved plan sha256:243cbbf528a7 is stale; nothing was applied.
  - file.input: state was sha256:4d5c2602b68b at plan time, now sha256:32466914be46
  - exec.shout: input input.txt was sha256:4d5c2602b68b at plan time, now sha256:32466914be46
[exit 3]
```

Re-planning shows exactly what applying would overwrite
(`- edited by hand`). That plan is reviewed, then applied.

## 5. Injected failure -> retry

```
$ WB_FAULT=exec.shout wb apply
  file.input  updated    input.txt: 30 bytes, sha256 6b3b24010791
  exec.shout  FAILED     injected fault before effect
  file.notes  updated    notes.txt: 26 bytes, sha256 22ab3ffcb35c   <- unrelated, still applied
[exit 1]

$ wb plan
  = file.input  unchanged
  > exec.shout  run        last attempt failed (run 4): injected fault before effect
  = file.notes  unchanged
```

The retry runs only `exec.shout`. `WB_FAULT=<address>:crash-after` kills
the process between an effect and its journal entry. `wb log` then shows
`INTERRUPTED`, and the next plan re-runs the task (at-least-once; tested).

## 6. Third adapter, no parser or planner edits

```
commit 610640b feat(adapters): dir, the third adapter
 adapters/builtin.go |   1 +
 adapters/dir.go     | 111 +++++++++++++
```

The source change is one interpolated reference and one new block:

```diff
-  stdout  = "output.txt"
+  stdout  = "${dir.build.path}/output.txt"
+resource "dir" "build" {
+  path = "build"
+  mode = "0750"
+}
```

```
  + dir.build   create     build: directory, mode 0750
  > exec.shout  run        config stdout: "output.txt" -> "build/output.txt"
                           upstream dir.build changes
      note: output.txt is no longer declared; left in place, no longer owned
```

Writing `"build/output.txt"` without the reference fails to compile
(`exec.shout writes build/output.txt, which dir.build owns, but does not
reference dir.build. Use dir.build.path`).

## The tests behind it

| Case | Tests (`e2e/`) |
|---|---|
| 1 | `TestCase1InitialPlanAndApply` |
| 2 | `TestCase2UnchangedReapply` |
| 3 | `TestCase3InputChangeDownstreamPlan` |
| 4 | `TestCase4ExternalModificationRefusesStalePlan`, `TestCase4SourceEditAfterPlanRefusesStalePlan`, `internal/apply` `TestChangeDuringApplyFailsStepInsteadOfOverwriting` |
| 5 | `TestCase5PartialFailureThenRetry`, `TestCase5FailedResourceBlocksItsDependents`, `TestCase5ChildProcessFailureThenRetry`, `TestInterruptedTaskReRunsFromEvidence`, `TestInterruptedResourceRecoversByObservation` |
| 6 | `TestCase6ThirdAdapter`, `TestCase6WriteInsideDirRequiresReference`, `TestCase6DirRefusesToReplaceAFile`, `TestAdapterAddedOutsideTheEngine`, `TestEngineNeverImportsBuiltinAdapters` |

A `tr` wrapper placed first on `PATH` counts real child-process runs outside
the workspace, so "not repeated" is checked without trusting wb's own
journal. Regression tests for the independent review's findings:
`e2e/findings_test.go` (F1, F5 to F8), `internal/intent` (F2 to F4, F11) and
`internal/apply` (F10).
