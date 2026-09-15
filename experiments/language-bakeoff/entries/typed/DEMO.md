# 60-second demo

```
./demo.sh
```

This builds two workflow programs and runs everything in a fresh
temporary directory that it deletes on exit (`KEEP=1 ./demo.sh` keeps
it). Every case ends in an executable `check:` line; any deviation stops
the demo with `DEMO FAILED`. Exit codes: 0 ok, 1 failed, 3 stale plan
refused.

**0. Source to intent.** The workflow is a Go function. `textpipe intent`
prints the JSON the engine consumes; `"$ref": "file.input"` is the typed
reference, kept as data.

**1. Fresh directory.** `plan -out plans/1.json` shows `+ create` and
`> run (why: never run)`. The check confirms that planning wrote nothing.
`apply -plan` runs it and then reports "Observed after apply:
converged".

**2. Re-apply.** "No changes. All 3 nodes match the intent." The check
compares inodes and the evidence checksum: nothing was rewritten.

**3. Input change.** A line diff on `input.txt`. Both transforms run,
and each says why: `input file.input changed since run #3 (sha256:... -> sha256:...)`.

**4. External edit between plan and apply.** Someone edits `input.txt`
after the plan. `apply -plan` refuses with exit 3: `file.input: input.txt
was sha256:... when planned, is now sha256:...` and "No effects were
performed." The re-plan shows the real diff, `- edited by hand`, before
anything overwrites it.

**5. Partial failure, then retry.** With
`WB_FAULT=fail:transform.wordcount` the apply updates the input and runs
`upper`, but `wordcount` fails: "Apply incomplete ... nothing was rolled
back." The retry plan runs only `wordcount`, and the check confirms that
`input.txt` and `upper.txt` were not rewritten.

**5b. Crash.** `WB_FAULT=crash:transform.upper` kills the process after
`upper.txt` is committed and before its evidence is recorded (exit 137).
A fresh `plan` reads the evidence and says: "run #2 started but never
recorded a result (interrupted)". The retry runs it again, because
replay is at-least-once.

**6. Third adapter.** The demo prints the complete extension: a 155-line
disposable-directory adapter, the 28-line workflow that composes
`textpipe` with it, the registration line, and the three small
foundation operations added for it (`DirEmpty`, `Remove`, `Rename`). A
check confirms that the planner, API, CLI and foundation import no
adapter. Bumping `-var generation=2` plans `-/+ scratchdir.work replace
(deletes everything inside work/)`, and only the report inside it
reruns. A pre-existing non-empty `work/` without the ownership marker is
refused rather than adopted.

The same cases run as Go tests with stronger assertions (`go test
./...`); golden outputs live in [docs/examples](docs/examples).
