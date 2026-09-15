# Workbench language bakeoff: scorecard

All three entries began as draft PRs in private repositories and now live here.
This sheet records **facts only**. The scores and winner remain yours to fill in
after trying the demos; no entry declares a winner.

## Try each one (about a minute apiece)

| Entry | Bet | Run it | PR |
|---|---|---|---|
| **HCL** | An established config language, with a small compiler and adapters supplying the semantics | `entries/hcl/demo.sh` (about 2 s) | [`ebf32d2`](README.md#provenance) |
| **Custom language (`wb`)** | A small syntax whose first word says how each declaration behaves over time: `keep` for resources, `run` for one-shot work | `entries/custom/demo.sh` (about 5 s) | [`dc1c793`](README.md#provenance) |
| **Typed API** | A plain Go function emits JSON intent, and the planner reads only that JSON | `entries/typed/demo.sh` (about 1 s once built) | [`6b757a8`](README.md#provenance) |

Each repo has a `DEMO.md` (a 60-second tour) and a README that compares it with its plain-script baseline.

## Facts (what the entries report and their tests assert)

| | HCL | Custom language | Typed API |
|---|---|---|---|
| Six common cases | all pass | all pass | all pass |
| Tests | 32 (race-clean, 10 repeat runs) | 40 (race-clean), plus fuzzing: 2.4M and 34M runs | 38 (race-clean), lint 0 issues |
| Mutation check (break code on purpose; a test must fail) | 12 of 12 caught | forcing every run stale fails cases 2 and 5 | 11 of 11 caught |
| Third adapter without parser or planner edits | yes: `adapters/dir.go` plus one registration line | yes: 4 files, and `lang` and `plan` untouched (enforced by an import-graph test) | yes: API, planner and CLI untouched (enforced by a test). Needed 3 small operations in the effects layer |
| Dependencies | `hashicorp/hcl/v2`, `go-cty`, plus 4 transitive modules | Go stdlib only (1.25+) | Go stdlib only (1.25+) |
| Size | about 3.1k lines of Go + 1.4k of tests; 6.7 MB binary | about 2.5k lines of Go (763 of it lexer, parser and checker) + 1.3k of tests | about 1.7k lines of typed layer on a 477-line foundation |
| Plain baseline | `baseline/run.sh` | 28-line shell script: meets cases 1, 2, 3 and 5, and has no plan step, so no case 4 | 119-line script: correct for one small workflow |
| Review | 1 serious finding fixed (a task whose command used an input didn't re-run when it changed), plus 9 smaller fixes | round 1: 11 findings (1 P0, 2 P1), all fixed; round 2: no P0 or P1, 8 findings fixed, 2 deferred | 4 P2 and 7 P3 fixed |
| Still open | `kill -9` mid-write can leave a stray temp file | 2 deferred minor items (in REVIEW.md) | hand-edited JSON can hide a dependency by changing a key's letter case; Go-authored workflows can't do this |
| New persistent state | `.wb/journal.jsonl` (recovery depends on it) | `.wb/journal.jsonl` (recovery depends on it) | a run journal (recovery depends on it) |
| Honest note from the entry | no locking; a symlinked parent directory can escape the workspace; nothing is ever deleted | its own README says most of the value is in the planner and would survive behind HCL, JSON or a typed API | evaluating the user's source runs arbitrary Go with your privileges |

Shared limits: tested on macOS only; one writer per workspace; tasks can run more than once after a crash; Fleet and Rooms are described but not integrated (by the pack's rules).

## Caveats about the process

- **Independence:** HCL and Typed API ran as agents inside the same supervisor session and shared a scratch folder. HCL's PR-description file overwrote a file of the same name there, and Typed API saw that text *after* its own PR description was already published. It reports that it used none of it. Custom language ran in a separate session and wasn't exposed.
- **Fleet cost guard:** HCL and Typed API couldn't run their own `go test ./...` or lint directly, because a Fleet hook assumes CI covers any path containing "workbench", and these repos have no CI. Custom language hit the same guard for lint. All three used the hook's override and report their tests fully run.

## Your scores

Rubric from the pack (100 points). Score after trying the demos.

| Criterion | Weight | HCL | Custom language | Typed API |
|---|---:|---:|---:|---:|
| Demo clarity | 30 | | | |
| Practical value / plausible buyer | 25 | | | |
| Executable correctness | 20 | | | |
| Need for this representation vs a simpler alternative | 15 | | | |
| Restraint | 10 | | | |
| **Total** | **100** | | | |

**What to pursue:** _(blank: your call. Keeping the plain baseline is a legitimate outcome.)_
