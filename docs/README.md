# docs/ — the map

Every document in this directory, what question it answers, and the order
to read them in. The repo-level [README](../README.md) is the front door;
this is the directory's own index.

## Start here, by intent

| You want to… | Read | Then |
|---|---|---|
| Understand the whole system | [workbench-101.md](workbench-101.md) | [lessons.md](lessons.md) |
| Get the picture with zero jargon | [plain-language-overview.md](plain-language-overview.md) | [glossary.md](glossary.md) |
| Run work the way this repo's operator does | [workflow-mechanics.md](workflow-mechanics.md) | the [skills repo](https://github.com/itsHabib/skills) |
| Let agents act unattended, safely | [auto-mode-defaults.md](auto-mode-defaults.md) | [auto-mode-rulebook.md](auto-mode-rulebook.md) — copy-able settings + hooks |
| Steal the rules without the tools | [lessons.md](lessons.md) | each lesson's "Monday" line |
| Evaluate the architecture decisions | [DESIGN.md](DESIGN.md) | per-tool `cmd/<tool>/docs/DESIGN.md` |
| See the wider project family | [projects.md](projects.md) | — |

## Every file

**The teaching set**

- [workbench-101.md](workbench-101.md) — the full methodology top to
  bottom: why it exists, the loop, the five planes, gate as the flagship,
  where it's going. The doc to point an agent at to ground it fast.
- [plain-language-overview.md](plain-language-overview.md) — the same
  picture with no jargon.
- [glossary.md](glossary.md) — the vocabulary, one place.
- [lessons.md](lessons.md) — 28 rules from building this, each with the
  failure that earned it, where it's enforced, and a Monday action.
- [workflow-mechanics.md](workflow-mechanics.md) — the daily machinery:
  the instruction stack, entry paths, skill contracts, session mechanics
  (worktrees, chips, continuation), review machinery, prose→code, hooks.

**Autonomy and policy**

- [auto-mode-defaults.md](auto-mode-defaults.md) — the doctrine: the
  decision contract, the six defaults, the tier model, the rulebooks.
- [auto-mode-rulebook.md](auto-mode-rulebook.md) — the executable half:
  literal settings.json, a working pretool guard with remedies, post-tool
  hook patterns, install + verify steps.
- [review-credit-strategy.md](review-credit-strategy.md) — reviewer-spend
  measurement and the (data-gated, not yet default) tier-routed review
  proposal.

**Charter and records**

- [DESIGN.md](DESIGN.md) — the repo charter: one module and why, the
  boundary law, what's out of scope, the split triggers.
- [projects.md](projects.md) — the public project family around this repo
  (ship, dossier, rooms, tower, and the sandboxes).
- [mutation-audit-2026-07-20.md](mutation-audit-2026-07-20.md) — a dated
  audit record; kept as evidence, not guidance.

**Feature design docs**

- [features/](features/) — one directory per feature: the spec/kickoff,
  evidence files, and runbooks (escalation-plane, custody, the
  trusted-gate judgment bridge, and the rest). These are working design
  documents with explicit status lines; trust their `Status:` headers
  over their titles.

**Per-tool docs** live with the tools, not here: each
`cmd/<tool>/` carries its own `CLAUDE.md` (scoped guidance) and
`docs/DESIGN.md` (the tool's charter). The pair is CI-required so any
harness discovers the same exit codes and invariants.

## Reading order for a newcomer

1. [plain-language-overview.md](plain-language-overview.md) — ten minutes,
   no prerequisites.
2. [workbench-101.md](workbench-101.md) — the real tour.
3. [lessons.md](lessons.md) — what it cost to learn.
4. [workflow-mechanics.md](workflow-mechanics.md) +
   [auto-mode-rulebook.md](auto-mode-rulebook.md) — when you're ready to
   run it yourself.
