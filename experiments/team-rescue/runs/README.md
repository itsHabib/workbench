# Reproduce the local evidence

Start with [results and lessons](../RESULTS.md). Commands below run from
`experiments/team-rescue`. Use the pinned Fleet binary and native Codex setup
described in the [main guide](../README.md).

## Check the saved code without model calls

```sh
python3 verifier.py runs/rescue-01/candidates/improved-solo --phase 1
python3 verifier.py runs/rescue-01/candidates/strong-lead --phase 1
python3 runs/pilot-01/posthoc_probe.py
```

Both rescue candidates should pass. The probe prints the invalid-input and
scalar-payload evidence for the earlier candidates. Its exit code only means
the probes ran; inspect the JSON to see the defects.

```sh
python3 verifier.py runs/pilot-01/candidates/pair --phase 1
```

This must exit 1: the pair's stored-state validator rejects valid non-object
payloads, and unsupported methods return HTML/501. The code is intentionally
preserved as evidence. Do not "fix" these archived candidates.

## Run the rescue comparison again

This makes paid/native model calls. It is a new trial, not an exact replay of
stochastic output. Freeze both arms before running either:

```sh
python3 lab.py init .runs/rescue-repeat/solo \
  --fleet /tmp/fleet-team-rescue --mode solo --model gpt-5.6-luna \
  --source runs/pilot-01/candidates/pair --context runs/rescue-01/context.json \
  --max-calls 5 --call-timeout 120 --wall-seconds 600
python3 lab.py init .runs/rescue-repeat/lead \
  --fleet /tmp/fleet-team-rescue --mode lead --model gpt-5.6-luna \
  --lead-model gpt-6-astra \
  --source runs/pilot-01/candidates/pair --context runs/rescue-01/context.json \
  --max-calls 5 --call-timeout 120 --wall-seconds 600
python3 lab.py run .runs/rescue-repeat/solo
python3 lab.py run .runs/rescue-repeat/lead
```

Use `lab.py status RUN` for outcome, tested-source match, calls, tokens and time.
Both must explicitly finish with passing checks. Never extend a failed trial's
budget invisibly; retain it and register the next comparison separately.

## What the receipts mean

- `pilot-01/phase1.json` and `phase2.json`: original frozen checker grades,
  every arm, model actions, usage, configuration and source hashes. Phase 2
  retains the pair's unfinished phase-1 row; it was not advanced.
- `pilot-01/posthoc.json`: independent probes run after the original trial.
  `posthoc_manifest.json` records the portable probe's adaptation and hash.
- `pilot-01/corrected-checks.json`: later checks of all phase-1 and eligible
  phase-2 candidates. These qualify, rather than replace, the original scores.
- `rescue-01/results.json`: corrected checker fixed before the follow-up,
  both policies, all calls and source hashes. `context.json` is the same
  synthetic team history each received.
- `candidates/` and `phase2-candidates/`: exact generated source versions.
  `input_hashes` hashes individual files; `candidate_sha256` hashes the sorted
  JSON map of editable filenames to contents, using `lab.digest`.

Original pilot runtime: `5bf1545ae5b94f278f5133ed34afb13e5e428341`.
Rescue runtime: `9e79875293d45a3c4036823ab6716c591cd39eb9`.
Fleet job binary: source `026b7ea4467a21fd0cf32f7d60bf7347a04b38c4` from PR #363.
Implementation hashes and per-call prompt hashes are retained. Raw prompts,
native events and machine configuration remain private under ignored `.runs/`.
The public receipts are derived from those local files, not a signed external
audit. No missing usage or human answers occurred in these trials.

The old driver left the exhausted pair marked `ready` after its eighth call;
one extra `run --steps 1` normalized it to `budget` without another model call.
The current driver records that state immediately. Both histories remain
eight-call trials.

## Next tests

Use the [fixed protocol](../PROTOCOL.md) for new local trials. The next useful
comparison needs independently observed stalls on different tasks, fresh final
checks, and repeated improved-solo/optional-lead trials. Adapting another task
requires its own workload and checker; `--source` alone still targets this
webhook contract. Concurrent native workers come after that. Rooms is last;
there is no cloud command or unattended-readiness claim in this experiment.
