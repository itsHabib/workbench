# Auto-mode doctrine — making autonomy deterministic

**Status:** v0 (2026-07-18)
**Scope:** every auto-classifier in the portfolio — the merge gate, triage, ship dispatch
policy, and the Claude Code harness config (permissions + hooks). One doctrine, many
rulebooks.

The thesis, in the redesign doc's terms: *prose shrinks, guarantees grow*. Autonomy is safe
in proportion to how much of the decision surface is deterministic code the model cannot
skip. The untapped potential of "auto mode" is not a smarter judge — it is a rulebook that
compounds.

## The contract

Every auto-decider in the portfolio has the same shape:

```
(action, observables, rulebook, grant) -> {pass | park | block} + rule-fired + artifact
```

- **action** — the thing about to happen (merge a PR, run a tool call, dispatch a stream).
- **observables** — facts computable without judgment: paths touched, diff size, CI state,
  command text, grant presence.
- **rulebook** — versioned deterministic rules mapping observables to a tier.
- **grant** — a human-minted, scoped, expiring ceiling on what may proceed unattended.
- **rule-fired + artifact** — which rule decided, recorded durably.

Gate implements this for merges. Triage implements the classifier half for PR risk. The
harness config (settings.json + hooks) implements it for tool calls. They should converge
on shared vocabulary in `contracts/`, not shared call stacks.

## The tier model

Every action falls into one of three tiers. The question is never "is this command
dangerous" — it is "what capability does it grant, and is that capability (a) reversible,
(b) observable after the fact, (c) bounded in blast radius?"

| Tier | Capability | Mechanism |
|------|-----------|-----------|
| 1 — free | read-only or trivially reversible | allow rules; must never prompt |
| 2 — consequential, auditable | durable state you can see and undo (open PR, create task, dispatch run) | allow rules + observability hooks |
| 3 — irreversible or authority-bearing | merge, force-push, delete, publish, spend, mint | deny rules + pre-execution guard hooks + external gates (grants) |

An allowlist entry is safe in proportion to the gates behind it, not how harmless the
command looks. `gh pr create` is tier 2 *because* merge is gated; the same entry in a world
without gate would be tier 3.

## The six principles

### 1. Deterministic floor, advisory ceiling

Split every classifier into a deterministic rule layer and an optional model layer. The
model may only *escalate* — never downgrade, never approve. A wrong model output can then
waste human attention but can never cause an action. This converts model accuracy from a
safety question into a cost question, which is what makes a cheap local model viable as the
advisory layer.

Triage is the reference implementation: a deterministic floor and a separate escalate-only
advisory, split at the binary level.

### 2. Classify observables, not intent

Floor rules key on things a script can compute: globs of files touched, net LOC, CI state,
presence of a grant, command text. Never "does this look risky" — that is judgment leaking
into mechanism. The litmus test: can the rule be a unit test with a fixture? If deciding
requires *understanding* the change, the question belongs to the advisory layer or a human.

### 3. Fail closed, and make closed cheap

Unknown input, engine error, ambiguous match: park, do not fail open and do not strand the
work. Every park/block/refuse must print its own remedy — the exact command a human types
to unstick it. Gate's park-with-mint-command is the pattern; the pretool guard's
per-denial remedy lines follow it. A fail-closed path without a printed remedy is a design
bug of the same severity as failing open, because expensive escape hatches train operators
toward bypass.

### 4. Every decision is an artifact

Record inputs, rulebook version, **rule that fired**, and verdict — hash-chained where the
decision carries authority (gate's audit log). Determinism you cannot replay is not
determinism: the test is re-running the classifier offline on the same inputs and getting
the same verdict with the same rule cited. Rule-fired is the field people skip, and it is
the one that turns the log into tuning evidence (principle 6).

### 5. Authority is minted, never inferred

The classifier decides *tier*; a grant decides *ceiling*; they are orthogonal forever. No
accumulation of correct verdicts widens what the classifier may do — only a fresh
human-minted grant does. This bounds every failure mode of the classifier itself (bugs,
updates, prompt injection of the advisory layer) at the ceiling. Harness twin: the
permission mode is the grant; a guard hook holds regardless of mode.

### 6. Tune by moving the boundary, not softening it

When auto-mode annoys (too many parks, too many prompts), the fix is a new deterministic
rule derived from audit-log evidence — never a model override. Each annoyance dies as a
rule, permanently, reviewably; the rule change is itself a PR that clears the gate. This is
why the deterministic layer compounds while a probabilistic judge stays at its error rate
forever: every promoted rule is a category of decisions now at 100%, and regressions
require a reviewed diff.

Cadence: periodically mine park/prompt logs for the most frequent cause and promote the top
one. The harness-side twin is prompt-mining the transcripts for allowlist candidates.

## The two rulebooks today

**Portfolio actions** (merges, dispatches): gate + triage, state in `~/pers/gate`, grants
minted by the operator, decisions in the hash-chained audit log.

**Harness tool calls** (what a session may do): three settings layers with distinct jobs —

1. global `~/.claude/settings.json`: the personal doctrine — universal tier-1 read-only
   floor, tier-3 deny list, the pretool guard hook, observability hooks;
2. project `.claude/settings.json`, checked in: the repo's rulebook, reviewed by PR, so the
   rulebook governs itself;
3. `settings.local.json`: scratch written by in-the-moment approvals — an inbox, drained on
   a cadence into the designed layers or deleted. Wildcards accreted here are how allow-rule
   holes fail open (a `PowerShell(gh *)` entry silently undoes per-verb curation done in
   Bash rules; dual-shell platforms need every rule in both shells plus a deny backstop).

The pretool guard (`pers/hooks/scripts/pretool-guard.sh`) is the harness's tier-3 floor: a
PreToolUse hook that regex-matches never-sanctioned command shapes (force push, repo
delete, visibility flips, credential and gate-state touches) and refuses them with a remedy,
in every permission mode. It deliberately does not gate `gh pr merge` — merge authority
belongs to gate, and duplicating it would create a second policy source.

## Non-goals

- No model-in-the-loop approvals anywhere in the floor.
- No generic configurable rules engine for hypothetical users; rulebooks encode our
  workflow (see the house rule: opinionated, not generic).
- No new grant system per tool. If auto-mode generalizes beyond merges, the grant substrate
  generalizes with it, scoped by domain.
