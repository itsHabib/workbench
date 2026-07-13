# Portfolio Control Room completion record

## Release delta

- Added the lockfile-pinned `@playwright/test` 1.61.1 package under `cmd/controlroom/e2e`; production Go remains standard-library-only.
- Added laptop (1440×900) and narrow (390×844) Chromium coverage for deterministic demo load, refresh publication, filters, PR/run drawers, partial/disconnected sources, real core→enrichment settlement, and responsive layout.
- Made unattended-run release semantics executable: progressing, named waiting, stalled, terminal timeout/failure, and done/unknown boundaries retain exact update time, stage, evidence, and conservative action language without a generic retry promise.
- Added Tracelens findings and unavailable token/cost/latency facts to run drill-down.
- Added exact Host/CSRF/body/path hardening coverage, a pinned CI browser job, the operator runbook, five-minute demo script, and deterministic healthy/degraded/on-fire screenshots.

## Browser evidence

The automated CI matrix passes 14 behavior tests across both viewports and never writes the checked-in evidence. A separate explicit screenshot command passes one canonical laptop capture test. A separate in-app browser smoke verified:

- version/source facts and partial-source qualifications on the live demo server;
- the failed-run drawer's policy, failure, Tracelens finding, and unavailable telemetry;
- a `waiting` filter that leaves only `awaiting_judgment` with its named-owner next action;
- one-column masthead and panel grids under the narrow override; and
- no application-origin browser console errors.

Screenshots were rendered from the fixed demo clock and inspected individually:

- [Healthy](screenshots/healthy.png)
- [Degraded](screenshots/degraded.png)
- [On fire](screenshots/on-fire.png)

## Trace analysis

The canonical Phase 5 Ship dispatch (`drv_01KXDXV517GHV58MWPG7E68DV6`, stream `ds_01KXDXV516YW8B2NPNZFFP6HF2`, workflow `wf_01KXDXVJBADMWDVAEJADBD2A0B`) failed before agent creation because `CURSOR_API_KEY` was absent. The stream was truthfully skipped and no implementation trace was produced, so there is no eligible Tracelens trace to analyze. Phase 5 and Phase 6 proceeded in isolated manual worktrees; the limitation is recorded rather than replaced with invented reliability evidence.

## Retrospective

The locked read model and deterministic demo made browser hardening cheap: the same owner facts drive unit goldens, Playwright routes, screenshots, and the live demo. Staged core/enrichment publication also gave the real-mode browser test a precise observable contract instead of timing guesses.

Reviewer pressure materially improved unattended operation. Missing Tower/tool-health commands now degrade only their own panels, `timed_out` is terminal failure, and every failure action stops at evidence/ownership inspection before retry safety. The final browser layer keeps those semantics visible rather than collapsing them back to producer status.

The remaining deliberate limits are product boundaries, not hidden TODOs: snapshots are process-memory-only, optional diagnostics have a strict global budget, source stores remain owner-controlled, and Control Room offers no arbitrary mutation/retry/resume surface.
