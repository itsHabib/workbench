# Import desk: work arrives as we learn

**Start with [RESULTS.md](RESULTS.md): what worked, what failed, and what we learned.**

Two fresh Luna builders receive [the same brief](BRIEF.md). One worker's assignments and
results go through the experimental controller; the other uses native messages and TASKS.md.
The same parent agent coordinates both. A separate Sol runner freezes external checks before
reading either app. This is a feasibility comparison, not a statistically controlled benchmark.

Start with a useful CSV import desk. Apply pressure to the app, then give both builders the
same newly explicit requirements. Record the coordinator's routing decisions before repairs.
Preserve initial failures. Do not tell the builders which infrastructure they must choose.

## What this can establish

- Whether the app works through an independent HTTP client.
- Whether new needs produce explicit work, and whether existing context is reused.
- Whether saved assignments and result evidence survive process restarts/reassignment.
- Whether accepted imports survive a real service kill, when a pending window is observed.

It cannot establish a universal staffing policy from one pair of runs. The parent knows the
scenario; builders are held to a shared API seam. Shared host load affects timings. Provider
billing and exact token accounting may not be exposed. Do not infer cost savings from model
choice alone. The runner is independent from app authoring, not from the entire experiment.

## Run it

Each app launches with:

```sh
python3 server.py --port 4341 --data-dir /tmp/import-desk
```

Run from controller-app or native-app. Use a separate data directory for each. Open the printed
loopback URL. Synthetic input only; this is a local experiment without user accounts.

The harness runs with `python3 harness/harness.py --app-dir ABSOLUTE_APP_PATH --round build
--out OUTPUT_PATH` (use `pressure` or `recovery` for later stages). It starts and stops only its
own server processes. Read harness/PROTOCOL.md before interpreting a pass or not_covered.
Run evidence is retained under runs/local-01, including failures and all revisions used.
