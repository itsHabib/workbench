# A real team, a wrong first answer, a checked repair

Four jobs produced this [document importer](app/server.py). All model turns used
Haiku 4.5. Total provider-reported usage was **$3.6660443**, known for all 20 turns.
Seven recorded steering/harness interventions mean this is a supervised integration
result. There is no solo baseline and no claim of general model uplift.

```sh
python3 app/server.py --port 8766 --state /tmp/importer-demo
```

Open `http://127.0.0.1:8766/` and upload `app/sample.csv`.

**Evidence to inspect**

- [receipt.json](receipt.json): model IDs, per-turn costs, jobs, interventions,
  failed checks, final checks and file hashes. Tokens, paths and raw provider
  transcripts are excluded.
- [mechanical.json](mechanical.json): a separate no-model drill killed/restarted
  the watcher, retained the lease, cancelled/reclaimed a worker and fenced its old token.
- [first-completion.patch](first-completion.patch): revert the final server to the
  first broken completion. The original coordinator had declared it finished.
- [test_app.py](app/test_app.py): the team's own evolving tests. The external
  oracle and browser check are separate, outside the worker source repository.

From the workbench root, rerun the API check:

```sh
python3 experiments/agent-work-loop/oracle.py \
  --app experiments/agent-work-loop/runs/haiku-01/app/server.py --seed 730251
```

For the browser plus API check, install Playwright outside this tree and set
`NODE_PATH` to that installation. Install its Chromium browser, or set
`PLAYWRIGHT_CHANNEL=chrome` for an installed Chrome. Then run from `app/`:

```sh
python3 ../../../check-importer.py
```

To reproduce the bad first answer, copy `app/` to a disposable directory, initialize
a Git repository there and apply `first-completion.patch`. Run the same external
oracle: it rejects the missing documents array. This deliberately broken version
also fails the later regression suite. Do not apply the patch to your working app.

API acceptance covers exact data, malformed input, eight concurrent retries,
restart/retry durability, CSV export and failed SQLite writes. Browser acceptance
covers quoted multiline upload, exact displayed values, an invalid-input error and
390px layout. Neither establishes production security, accessibility completeness,
load capacity or safe cloud isolation.
