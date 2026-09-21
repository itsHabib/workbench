# Give a local team a goal

One coordinator, two available workers, existing Fleet jobs and the existing watcher.
Agents decide what to do. The bridge renews claims and records their results. This is
an experimental local launcher, not a cloud scheduler or an intelligence benchmark.

```sh
go build -o /tmp/fleet ./cmd/fleet
# Install/authenticate the provider as described in cmd/fleet/docs/headless.md.
python3 experiments/agent-work-loop/team.py \
  --fleet /tmp/fleet --repo /absolute/path/to/clean/repo \
  --goal-file /absolute/path/to/goal.md --run-dir /absolute/path/to/new/run
```

The source stays untouched. The launcher clones its committed HEAD, removes the
clone's remote, and creates separate coordinator/worker worktrees. It sets up private
Fleet state, role cards and project-local tool allowances. These are trusted local
agents using the provider's existing security settings; a worktree is not a sandbox.

The coordinator submits jobs, assigns an idle worker by changing its `jobs.id`, checks
results and integrates commits. New work goes in `REQUESTS.md`. A stuck worker can get
a reproducer, a focused helper job or a fresh continuation; the coordinator records
its choice in `PLAN.md`. There is no mandatory helper team or automatic success claim.

## Watch and continue

- `REPORT.md`: queue states and known provider costs.
- `PLAN.md`: the coordinator's decisions and next steps.
- `DONE.md`: the coordinator's completion claim, revision and checks; independently verify it.
- `fleet/watch/delivery/`: raw prompts, provider traces, lease-bound results and exits. Private.

The launcher defaults to Haiku, a $1 **per-turn** SDK limit, a $10 reported-cost stop
threshold and 30 minutes. Set `--model`, `--turn-budget-usd`, `--budget-usd` or `--minutes`
for the actual work. Reported cost arrives at turn completion, so the run threshold can
overshoot by active turns (up to three); provider cost limits are not prepaid reservations.
Missing costs remain unknown. Engineering, evaluation and local compute are excluded.

Ctrl-C stops future starts, asks current turns to stop and preserves work. A killed
launcher leaves its watcher running; inspect the private state before resuming:

```sh
export FLEET_STATE=/absolute/path/to/run/fleet
export ORG_STATE=/absolute/path/to/run/org
/tmp/fleet watch status --json
# Resume only the addresses you intend to restart after inspecting retained workers.
/tmp/fleet resume address:coordinator:team
python3 experiments/agent-work-loop/team.py --run-dir /absolute/path/to/run --resume
```

A provider cancellation can be resumed after terminal evidence. SIGKILL of a bridge
mid-tool is different: unknown child cleanup retains the workspace reservation. Never
force-release it merely because its job lease expired. The current loop does not prove
safe unattended recovery of arbitrary escaped child processes or external side effects.

## Reproduce the engineering workload

[WORKLOAD.md](WORKLOAD.md) defines the public import application and three timed requests.
`fixture/` is an intentionally incomplete starting repository, including a small public
development test. Commit a copy and pass it to the launcher. Keep the external oracle
outside worker checkouts; give workers the requirements, not the final fixture seed.

```sh
python3 oracle.py --app /absolute/path/to/run/coordinator/server.py --port 8765 --seed 730251
```

The oracle checks exact data, malformed input, concurrent retries, a locked SQLite write
and persistence across a service restart. Browser QA is a separate real user-path check.
A green oracle alone does not establish a good UI or production readiness. The seed is
expected to fail. Raw provider records must be scrubbed before public publication.

The larger [comparison protocol](https://github.com/itsHabib/specialist-workshop/blob/docs/agent-lessons-next-trial/docs/experiments/agent-work-loop.md)
compares native messages plus a task file against jobs, then evaluates targeted boost
separately. A live integration run only establishes that the pieces connect; it does not
establish comparative productivity. Local evidence comes before Rooms and cloud.
