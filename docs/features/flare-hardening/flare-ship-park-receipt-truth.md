# Flare Ship park receipt truth

## Status

Approved for implementation.

## Owner

Workbench / Flare.

## Scope

Ideal, under 350 weighted LOC. One PR.

## Problem

Flare currently renders every Ship `parked` receipt as a human decision request
and reads a `terminal_at` timestamp that Ship does not emit. Ship receipts use
`generated_at`, so the ignored parse failure becomes Go's zero time and Slack
shows `Jan 1, 12:00 AM UTC`. A mechanism failure can therefore page the operator
with the false headline `Your call`.

The reproducing receipt is a Ship `parked` receipt whose reason is an SDK/runtime
failure and whose timestamp is present only as `generated_at`.

## Functional requirements

1. Parse Ship's canonical `generated_at` timestamp. Preserve compatibility with
   historical receipts that contain `terminal_at` if the surrounding contract
   already supports them.
2. Never silently convert a missing or malformed receipt timestamp into Go's
   zero time. Use the repository's existing malformed-input behavior.
3. Distinguish a park that actually requires a human policy decision from a
   mechanism/runtime failure. A mechanism failure must not use `SevEscalate`,
   must not render the `Your call` headline, and must not expose Approve/Block
   actions.
4. Preserve the existing human-decision escalation path for genuine policy
   parks.
5. Add regression fixtures/tests for:
   - canonical `generated_at` rendering with the real 2026 timestamp;
   - malformed/missing timestamp behavior;
   - mechanism failure parked receipt is informational/action-required but not
     a human decision;
   - genuine human-decision park still renders escalation actions.

## Constraints

- Flare remains a best-effort observability sink. It does not inspect or mutate
  Ship's database.
- Consume only authoritative receipt fields/artifacts.
- Do not broaden this task into debounce behavior for parks that later
  auto-resolve; that is a separate task.
- Follow `cmd/flare/CLAUDE.md`, `cmd/flare/docs/DESIGN.md`, and the root design
  boundary law.

## Validation

Run the Flare package tests plus the repository checks documented by the scoped
guidance. Demonstrate the reproducing Ship receipt no longer yields a zero-time
Slack card or a false human-decision action.

## Implementation plan

1. Locate the Ship receipt adapter and its tests/fixtures.
2. Correct timestamp decoding and fail-closed malformed-input handling.
3. Map park reason/category to honest Flare severity and action availability.
4. Add focused regression coverage and run the documented checks.
