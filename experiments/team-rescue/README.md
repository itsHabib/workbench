# Help a stuck team deliver

A local experiment: give an agent team an unfinished webhook service, let a
lead diagnose and route repairs, and check what it actually delivers. Compare
that with solo iteration and an independent challenger. [Protocol](PROTOCOL.md)
sets the decisions before the runs. [Results](RESULTS.md) separates mechanics
from model outcomes.

This is an experiment driver, not a replacement Fleet watcher. Fleet's job
store owns atomic claims and reported/accepted actions. The driver owns trial
context, versioned candidate files and independent product checks. There is no
second job queue or background scheduler. Turns are sequential in this first
slice; the two worker identities preserve separate role memories.

## Try one local trial

Requires Python 3.10+, a signed-in native `codex` CLI and the `fleet job` binary
from Workbench PR #363 (`026b7ea4467a21fd0cf32f7d60bf7347a04b38c4`). Build that
revision with `go build -o /tmp/fleet-team-rescue ./cmd/fleet`. This experiment
consumes its CLI contract and refuses an older Fleet binary. No API key is
copied into the trial.

From this directory:

```sh
python3 lab.py init .runs/lead --fleet /tmp/fleet-team-rescue --mode lead
python3 lab.py run .runs/lead
python3 lab.py status .runs/lead
```

Default workers use Luna. `--model`, `--lead-model` and `--critic-model` select
models explicitly. Native calls use low reasoning effort and the existing CLI
authentication/configuration. The default limit is ten calls, 180 seconds per
call, and 1,200 charged model seconds. Unknown interrupted calls retain their
whole time reservation. Token usage is recorded; exact dollar cost is unknown.

Use `--source /path/to/service-snapshot --context handoff.json` to start from
existing work. The context is a JSON array of role/message objects. This
imports a snapshot; it does not attach to live sessions or take over their
working directory. Only `service.py` and optional `README.md` are copied.

```sh
python3 lab.py run .runs/lead --steps 1
python3 lab.py change .runs/lead 'Add replay while preserving existing delivery behavior.'
python3 lab.py answer .runs/lead 'The answer to the specific pending question.'
python3 lab.py stop .runs/lead
```

`change` reveals phase 2 without resetting call/time limits. `answer` resumes a
pending question and counts a human intervention. `stop` signals a running
native child and prevents another turn; it is intentionally independent of the
driver lock. A stopped trial remains stopped. Inspect before creating a new run.

## Run a comparison

```sh
python3 matrix.py plan .runs/comparison --fleet /tmp/fleet-team-rescue --repeats 3
python3 matrix.py run .runs/comparison
python3 matrix.py report .runs/comparison
```

Planning freezes every trial before any calls and randomizes arm order within
each repeat. Add `--reference gpt-6-astra` when planning to include stronger solo.
These are fixed-call pilots, not equal-dollar comparisons. Every attempted and
unstarted row remains in the report. To stop the matrix, create its `STOP` file;
also stop its currently active trial to cancel that call immediately.

## Recovery and evidence

- `mission.json`: integrated version, role memories, transitions and usage.
- `jobs/`: Fleet's authoritative action claims and acceptance records.
- `versions/`: candidate service snapshots, independently checked after edits.
- `calls/N/`: exact prompt, raw native events, returned action and receipt.
- `manifest.json`: frozen inputs, spec, implementation and binary hashes.

On restart, a saved response is reconciled without another model call. A call
with no durable response requires `abandon RUN 'what was checked'`; its outcome
and usage remain unknown, its reservation remains charged. Do not blindly
retry it. A small helper watches the driver and terminates its own native
process group if the driver dies. A second driver refuses the kernel lock.

The driver accepts a **valid action** in Fleet. Only the separate checker can
accept the **delivered service**. A worker saying "done" cannot bypass it.
Code edits are restricted to candidate files. Native tools are forbidden by
the prompt and detected uses invalidate that turn; this is not a security
sandbox against a hostile model. Candidate Python runs on the trusted local
host with loopback recipients. Keep trials synthetic and don't run untrusted
third-party code here. Snapshot/result fencing does not fence arbitrary effects.

## Check the harness

```sh
FLEET_BIN=/tmp/fleet-team-rescue python3 -m unittest discover -s . -p 'test_*.py' -v
python3 verifier.py workload --phase 1
```

The second command **must exit 1**: the seed loses state on restart. Tests use
clearly marked scripted model responses to test mechanics, never to claim
model quality. Raw runs stay under ignored `.runs/`; publish reviewed summaries
and synthetic candidate artifacts only.
