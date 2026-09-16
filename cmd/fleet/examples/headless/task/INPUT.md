# Frozen component observations

Captured with `gh pr view N --repo itsHabib/workbench --json number,headRefOid,statusCheckRollup` for PRs 334, 337 and 340. Capture began 2026-09-14T00:44:01.725618+00:00 and ended 2026-09-14T00:44:03.247045+00:00. The calls are sequential observations, not a transactional snapshot. `input.json` retains the raw returned fields. It is a frozen input, not current PR status or merge authority.

The readout is the compact check summary used to inspect these component revisions. Preserve skipped review checks, native StatusContext labels and exact heads. The synthetic cases in test_report.py exercise absent and unfinished observations even when the frozen PRs have completed checks.
