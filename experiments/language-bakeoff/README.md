# Workbench language bakeoff

Three runnable proofs of concept compare ways to express the same plan/apply
workflow:

| Entry | Bet | Run |
|---|---|---|
| [HCL](entries/hcl/README.md) | A familiar configuration language earns its parser and dependency cost through readable, hand-edited manifests. | `entries/hcl/demo.sh` |
| [Custom language](entries/custom/README.md) | A tiny `keep`/`run` syntax makes resource and task lifecycle clearer. | `entries/custom/demo.sh` |
| [Typed Go API](entries/typed/README.md) | Ordinary Go supplies composition and extension without a new syntax. | `entries/typed/demo.sh` |

Run every demo from this directory with `./demo.sh`. Each entry also contains
deterministic tests, examples, a direct-tools baseline, and its original
60-second tour.

## What we learned

All three implementations pass the same six cases. The syntax was not the main
source of value. The useful pieces were normalized intent, a readable plan,
stale-plan refusal, and retained per-action evidence. Direct tools remain the
best default for one bounded operation.

The initial review favored the typed Go API for programmer composition. That
recommendation was superseded when the target became a small declaration of
Fleet cards, seats, and compute. The active follow-up is the adjacent
[Terraform + Fleet + Rooms experiment](../terraform-fleet/README.md), which
tests declarative setup using an existing ecosystem before Workbench owns a
language or provider.

Read [the utility review](REVIEW.md) for the reasoning and
[the scorecard](SCORECARD.md) for the original measurements. The scorecard is
deliberately unscored; working demos do not establish product value.

## Provenance

The entries were developed independently in private repositories for the blind
bakeoff, then copied here as the public canonical home. Their source revisions
are preserved below; those repositories remain historical sources.

| Entry | Source revision |
|---|---|
| HCL | `itsHabib/hack-workbench-hcl@ebf32d245a1d885bccfbbf4d0c31610e30100458` |
| Custom language | `itsHabib/hack-workbench-language@dc1c79332128e64f7efdd72db0226a172af7d9c3` |
| Typed Go API | `itsHabib/hack-workbench-typed@6b757a8e0c81c1c2456874c7256a281221d99327` |

The imports now use Workbench's single Go module. No runtime behavior was
intentionally changed during consolidation.
