# Portfolio Control Room five-minute demo

Start `go run ./cmd/controlroom serve --mode demo --addr 127.0.0.1:4317`, open the printed URL, and keep the browser at a laptop viewport.

## 0:00–0:45 — establish the contract

- Point out the `DEMO` badge, fixed generated time, monotonic version, and six-source summary.
- State the boundary: read-only owner facts, loopback only, no daemon/cache, no producer mutation, and no generic retry/resume control.

## 0:45–1:30 — scan consequences before inventories

- Read the first three Attention cards: rule ID, stable entity ID, score, reason, and evidence location.
- Explain that only current supporting receipts may create urgent/actionable conclusions; stale tool-health remains informational.

## 1:30–2:45 — show unattended-run legibility

- In Runs, contrast `drv_demo_waiting`, `wf_demo_stalled`, and a failed workflow.
- For waiting, call out `awaiting_judgment`, the exact one-hour-old durable update, and “Inspect the named wait boundary and its owner.” Waiting is not a stall.
- For stalled, call out the 20-minute age and evidence-first intervention wording.
- For failed, call out the terminal failure class and the deliberate “before deciding whether retry is safe” language.
- Open `wf_demo_fail_3`. Show producer status, operator state, stage/update, Tracelens verdict/finding, and unavailable token/cost/latency telemetry in one drawer.

## 2:45–3:30 — show PR truth

- Open PR `example-repo#42`; show failed CI, review required, zero unresolved threads, and blocked merge facts.
- Close it and contrast PR `#43`: successful CI does not erase the missing-review condition.

## 3:30–4:15 — demonstrate partial failure

- In Sources, show Tracelens `degraded`, tool-health `stale`, and Tower `unavailable` together with healthy Ship/Dossier/GitHub.
- In Tool health, point out “Stale retained data.” Explain that retained rows remain visible but cannot fabricate readiness or urgency.

## 4:15–4:45 — filter and resize

- Filter status to `waiting`, then clear it.
- Filter severity to `high`, then clear it.
- Narrow the viewport below 760px and show the one-column rows/panels and usable drawer; restore laptop width.

## 4:45–5:00 — refresh and close

- Click **Refresh snapshot**. Show exactly one higher version and the live-region success message.
- Close on the safe operating rule: current facts can suggest where to inspect; only owner evidence and authority decide intervention, retry, resume, or merge.

## Canonical evidence

- [Healthy](screenshots/healthy.png)
- [Degraded](screenshots/degraded.png)
- [On fire](screenshots/on-fire.png)
