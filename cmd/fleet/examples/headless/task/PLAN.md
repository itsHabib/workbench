# Frozen check readout plan

Owner: author seat `workbench-author-1`, branch `task/readout`, starting
at `9a02b16d53ba4e4ecdc8eeb23c8b916a66bda210`. This plan was left dirty
at the required recovery checkpoint before implementation.

1. Supervisor answer `readout-status-policy-answer-v1` confirmed preservation
   and was acknowledged. Keep skipped, neutral, unfinished and unknown labels
   distinct. A nonempty check list is successful only if every outcome is
   exactly `SUCCESS`; an empty list has `all_checks_successful: null`.
2. Implement a Python standard-library CLI accepting input and output paths.
   Validate the complete input before writing, including unique PR numbers,
   exact revision syntax and check field types. Preserve observation time,
   PR/check order, names (`name` then `context`) and outcomes (`conclusion`,
   `state`, `status`, then `UNKNOWN`). Empty checks remain unknown, and the
   report conveys observed checks without inferring merge authority.
3. Serialize deterministically. Leave identical existing output and its mtime
   untouched; refuse conflicting output without overwriting it.
4. Run all supplied tests and add focused validation/preservation cases if
   needed, without changing supplied tests or input. Generate `report.json`
   from the frozen input, commit only task-directory files, and record an
   `implementation/pass` receipt at the actual clean head. Mail the head and
   test evidence to the supervisor and leave a branch handoff.

Validation completed: all 8 supplied tests and 8 additional CLI tests pass via
`python3 -m unittest discover -s cmd/fleet/examples/headless/task -p 'test_*.py'`.
The additional cases cover malformed input, field types, fallback ordering,
repeated/Unicode names, unfamiliar outcomes and protection of existing files.
The frozen report preserves all 18 checks across PRs 334, 337 and 340, with
observed success flags `true`, `false` and `true`. Repeating generation preserves
bytes and mtime. The supplied input and tests remain unchanged.
