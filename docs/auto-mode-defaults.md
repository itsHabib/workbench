# Auto-mode defaults — making autonomy deterministic

**Status:** v0 (2026-07-18)
**Scope:** the auto-classifiers in the portfolio — the merge gate, triage, ship dispatch
policy, and the Claude Code harness config (permissions + hooks). One set of defaults,
many rulebooks.

**How to read this:** these are working defaults distilled from what gate and triage got
right, not law. Each one names the failure mode it guards; if the trade-off behind a rule
changes, revisit the rule — deliberately, in a PR — rather than treating it as forbidden
territory. The bar is "works well", not "conforms".

The thesis, in the words of the [driver-state spec](features/driver-state/spec.md): *prose
shrinks, guarantees grow*. Autonomy is safe
in proportion to how much of the decision surface is deterministic code the model cannot
skip. The untapped potential of "auto mode" is not a smarter judge — it is a rulebook that
compounds.

## The contract

The auto-deciders we've built so far share one shape, and new ones should start from it:

```
(action, observables, rulebook, grant) -> {pass | park | block | refuse} + rule-fired + artifact
```

- **action** — the thing about to happen (merge a PR, run a tool call, dispatch a stream).
- **observables** — facts computable without judgment: paths touched, diff size, CI state,
  command text, grant presence.
- **rulebook** — versioned deterministic rules mapping observables to a tier.
- **grant** — a scoped, expiring ceiling on what may proceed unattended. "Human-minted" is
  a key-custody precondition, not a property of the record: `MintedBy` is unauthenticated,
  so grant presence signals human authority only while the signing keys and `gate grant`
  stay outside governed sessions' reach.
- **refuse** — distinct from block: the *request* is unauthorized or malformed (no grant,
  bad envelope) rather than a judged-and-denied action. Gate's exit codes carry all four
  (0 pass / 1 blocked / 2 parked / 3 refused, plus 4 for engine error).
- **rule-fired** — which rule decided; the field that turns the log into tuning evidence.
- **artifact** — the durable record of the decision, hash-chained where it carries
  authority.

Gate implements this for merges. Triage implements the classifier half for PR risk. The
harness config (settings.json + hooks) implements it for tool calls. They should converge
on shared vocabulary in `contracts/`, not shared call stacks.

## The tier model

Sort actions by capability, not by how scary the command looks. The useful question is
not "is this command dangerous" but "what capability does it grant, and is it (a) reversible,
(b) observable after the fact, (c) bounded in blast radius?"

| Tier | Capability | Mechanism |
|------|-----------|-----------|
| 1 — free | read-only or trivially reversible | allow rules; shouldn't prompt (every prompt here is friction tax) |
| 2 — consequential, auditable | durable state you can see and undo (open PR, create task, dispatch run) | allow rules + observability hooks |
| 3 — irreversible or authority-bearing | merge, force-push, delete, publish, spend, mint | deny rules + pre-execution guard hooks + external gates (grants) |

An allowlist entry is safe in proportion to the gates behind it, not how harmless the
command looks. `gh pr create` is tier 2 *because* merge is gated; the same entry in a world
without gate would be tier 3.

## The six defaults

### 1. Deterministic floor, advisory ceiling

Split a classifier into a deterministic rule layer and an optional model layer, where the
model may only *escalate* — not downgrade, not approve. A wrong model output can then
waste human attention but can never cause an action. This converts model accuracy from a
safety question into a cost question, which is what makes a cheap local model viable as the
advisory layer.

Triage is the reference implementation: a deterministic floor and a separate escalate-only
advisory, split at the binary level.

### 2. Classify observables, not intent

Floor rules key on things a script can compute: globs of files touched, net LOC, CI state,
presence of a grant, command text — not "does this look risky", which is judgment leaking
into mechanism. The litmus test: can the rule be a unit test with a fixture? If deciding
requires *understanding* the change, the question belongs to the advisory layer or a human.

### 3. Fail closed, and make closed cheap

Unknown input, engine error, ambiguous match: park, do not fail open and do not strand the
work. Have every park/block/refuse print its own remedy — the exact command a human types
to unstick it. Gate's park-with-mint-command is the pattern; the pretool guard's
per-denial remedy lines follow it. The failure mode this guards: a fail-closed path with an
expensive escape hatch trains operators toward bypass, which costs more safety than the
rule bought.

### 4. Every decision is an artifact

Record inputs, rulebook version, **rule that fired**, and verdict — hash-chained where the
decision carries authority (gate's audit log). Determinism you cannot replay is not
determinism: the test is re-running the classifier offline on the same inputs and getting
the same verdict with the same rule cited. Rule-fired is the field people skip, and it is
the one that turns the log into tuning evidence (principle 6).

### 5. Authority is minted, not inferred

The classifier decides *tier*; a grant decides *ceiling*; keep the two axes separate. An
accumulation of correct verdicts doesn't widen what the classifier may do — a fresh grant
does, minted as a human act. That act is enforced by key custody, not by the grant record
(see the contract note): agents don't run `gate grant`, and the mint keys stay out of
governed sessions. This bounds the failure modes of the classifier itself (bugs, updates,
prompt injection of the advisory layer) at the ceiling. Harness twin: the
permission mode is the grant; a guard hook holds regardless of mode.

### 6. Tune by moving the boundary, not softening it

When auto-mode annoys (too many parks, too many prompts), prefer a new deterministic
rule derived from audit-log evidence over a model override — an override quietly makes the
model the authority, which unwinds defaults 1 and 5. Each annoyance dies as a rule,
reviewably; the rule change is itself a PR that clears the gate. This is why the
deterministic layer compounds while a probabilistic judge stays at its error rate: every
promoted rule is a category of decisions now handled exactly, and regressions require a
reviewed diff.

Cadence: weekly is the working default — mine park/prompt logs for the most frequent cause
and promote the top one. Harness-side twin, same cadence: mine session transcripts for
allowlist candidates.

## The rulebooks today

**Portfolio actions** (merges): gate + triage, state in `~/pers/gate` (the operator's
machine — gate state deliberately lives outside any repo), grants minted by the operator,
decisions in the hash-chained audit log.

**Dispatch placement** (`cmd/dispatch`, phase 1): already an instance of this shape — a
versioned, content-hashed policy file first-match-scanned against a task descriptor,
fail-closed everywhere (an unmatched descriptor is an error, never a default placement),
deterministic by law, with append-only receipts. It decides *placement* only; ship
executes. New dispatch-policy work grows this rulebook rather than starting another.

**Harness tool calls** (what a session may do): three settings layers with distinct jobs —

1. global `~/.claude/settings.json`: the personal defaults — universal tier-1 read-only
   floor, tier-3 deny list, the pretool guard hook, observability hooks;
2. project `.claude/settings.json`, checked in: the repo's rulebook, reviewed by PR, so the
   rulebook governs itself;
3. `settings.local.json`: scratch written by in-the-moment approvals — an inbox, drained on
   a cadence into the designed layers or deleted. Wildcards accreted here are how allow-rule
   holes fail open (a `PowerShell(gh *)` entry silently undoes per-verb curation done in
   Bash rules; dual-shell platforms need every rule in both shells plus a deny backstop).

The pretool guard (`pers/hooks/scripts/pretool-guard.sh`, on the operator's machine — not
in this repo) is the harness's tier-3 floor: a PreToolUse hook that regex-matches command
shapes with no sanctioned use today (force push, repo delete, visibility flips, credential
and gate-state touches) and refuses them with a remedy, in every permission mode.

**Merge, specifically:** merge *policy* belongs to gate, and the guard does not duplicate
it. But while gate is advisory (no `-live` wiring, no branch protection requiring its
check), a bare `gh pr merge` from any governed session is a direct-merge bypass with no
grant, verdict, or artifact. The guard therefore enforces *shape*, not policy: it passes
merge commands that carry `--match-head-commit` (the form gate emits) and refuses bare
merges with the remedy pointing at gate. This raises the bar rather than closing the hole —
an agent could add the flag by hand — so the honest boundary is: discipline + the harness
self-merge classifier carry merge authority today; the hole closes structurally when gate's
check is required by branch protection or merge credentials move behind a broker. Revisit
this paragraph when either lands.

## Not now, and why

- Model-in-the-loop approvals in the floor — the floor's whole value is that a model
  failure can't cause an action. Revisit only if some future mechanism restores that
  property another way.
- A generic configurable rules engine for hypothetical users — rulebooks encode our
  workflow (opinionated, not generic). Revisit if a real second user shows up.
- A separate grant system per tool — if auto-mode generalizes beyond merges, grow the
  existing grant substrate, scoped by domain, rather than minting a parallel one. Revisit
  if a domain's grant semantics genuinely don't fit.
