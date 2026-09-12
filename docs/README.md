# docs/ — the map

Table of contents for this directory, one line per thing. The repo-level
[README](../README.md) is the front door.

New here: [plain-language-overview.md](plain-language-overview.md) →
[workbench-101.md](workbench-101.md) → [lessons.md](lessons.md).

| Doc | What it is |
|---|---|
| [workbench-101.md](workbench-101.md) | The system top to bottom: the loop, the five planes, gate as the flagship. Ground an agent here. |
| [plain-language-overview.md](plain-language-overview.md) | The same picture, zero jargon. |
| [glossary.md](glossary.md) | The vocabulary, one place. |
| [lessons.md](lessons.md) | 28 rules: the failure that earned each, where it's enforced, runnable Monday bullets. |
| [workflow-mechanics.md](workflow-mechanics.md) | The daily machinery: instruction stack, entry paths, `/drive`, review, session mechanics, hooks. |
| [auto-mode-defaults.md](auto-mode-defaults.md) | The autonomy doctrine: decision contract, six defaults, tier model, rulebooks. Includes the per-repo branch-protection table the merge-gate flow depends on. |
| [auto-mode-rulebook.md](auto-mode-rulebook.md) | The doctrine's executable half: settings.json, the pretool guard, install + verify. |
| [review-credit-strategy.md](review-credit-strategy.md) | Reviewer-spend measurement; tier-routed review (data-gated, not yet default). |
| [bounded-approval-example.md](bounded-approval-example.md) | Why the approval button names a recorded action instead of carrying merge authority. |
| [DESIGN.md](DESIGN.md) | The repo charter: one module and why, the boundary law, split triggers. |
| [projects.md](projects.md) | The public project family around this repo. |
| [mutation-audit-2026-07-20.md](mutation-audit-2026-07-20.md) | Dated audit record. Evidence, not guidance. |
| [features/](features/) | One directory per feature: spec, evidence, runbooks. Trust `Status:` headers over titles. |
| [features/org-fleet-boundary/spec.md](features/org-fleet-boundary/spec.md) | Editable Org role cards, independent Fleet work/mail/handoffs, Go headless runtime visibility and the legacy cutover. |
| [../cmd/fleet/docs/fleet-101.md](../cmd/fleet/docs/fleet-101.md) | Fleet top to bottom, in the style of workbench-101: the store and who writes it, leases, mail, the watcher's launch path, the Quint models, open gaps and a drift log. |

Per-tool docs live with the tools, not here: `cmd/<tool>/README.md`, plus the `CLAUDE.md` +
`AGENTS.md` pair CI keeps identical where a tool has one. The fleet guides for people and
agents (Fleet 101, overview, onboarding, the minimum, run a fleet, headless Go runtime, e2e) are under `cmd/fleet/docs/`.
