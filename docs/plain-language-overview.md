# The workbench, in plain language

A non-technical tour of this system: what it is, why it exists, and what it
means for how software gets built. No programming knowledge assumed. The
technical version is `docs/workbench-101.md`; the vocabulary reference is
`docs/glossary.md`.

## What this is

This is a system for letting AI assistants do real software work — writing
code, reviewing it, and shipping it — while a human stays firmly in charge of
the decisions that matter. Its author stopped hand-writing code and made AI
agents do essentially all of it, specifically to find out what breaks.

What breaks is trust.

## The problem: instructions are advice

The obvious way to keep an AI in line is to write it instructions: "always get
approval before merging," "never touch the credentials." The uncomfortable
truth is that written instructions are *advice*. An AI can misread them,
deprioritize them after hours of work, or follow them 99 times and skip them
the hundredth. For low-stakes work that's fine. For "what is allowed to ship
to production," it is not.

The longer an AI works unattended, the less you can afford rules that exist
only as words.

## The core move: rules that matter become software

The workbench's answer is to sort every rule into one of two bins:

- **Advice** — how to structure code, what style to use, how to write a good
  summary. This can stay as written guidance, and as AIs get smarter you need
  less and less of it.
- **Law** — what may merge, what may spend money, what may touch a secret.
  These get taken out of prose entirely and rebuilt as small programs that
  sit in the path of the action. A program doesn't get tired, doesn't
  reinterpret, and cannot be talked out of its job.

The house slogan is *prose shrinks, guarantees grow*: over time the advice
pile gets smaller while the enforced pile gets stronger.

## How a piece of work travels

1. **It starts on a task board.** Work is broken into small, well-described
   tasks.
2. **AI workers pick tasks up** — several in parallel, each producing a
   proposed change with a signature line recording exactly who (which AI,
   which pipeline) produced it.
3. **AI inspectors review the work.** Several reviewers examine each proposed
   change and file findings; another AI acts as the judge over the reviewers,
   deciding which findings are real and sending the work back for fixes.
4. **A gatekeeper program decides whether it may ship.** This is the heart of
   the system — a program (called *gate*) that checks the evidence and will
   not proceed without a valid **permission slip** from the human.
5. **Everything is written in a logbook** that quietly detects tampering.
   Every piece of evidence, every verdict, every decision is recorded and
   linked to what caused it — so you can always reconstruct not just *what*
   happened but *why*.

## The permission slip

The human doesn't approve each action by hovering over the AI. Instead they
sign a scoped, expiring permission — think of a valet key rather than the
master key:

- it works for **one project and one kind of action**, nothing else;
- it has a **risk ceiling** — routine changes only, unless the human said
  otherwise;
- it **expires** on a timer;
- it is **cryptographically signed**, so it can't be forged or quietly
  altered.

If the permission has expired or the work turns out riskier than the slip
allows, the gatekeeper stops — every time.

## A ladder of judgment

Not every question deserves the most expensive mind, and no AI opinion gets
to overrule a hard check. Judgment is arranged like a ladder:

- **The checklist always runs.** A deterministic program does the
  non-negotiable checks. Nothing above the checklist can lower it or talk
  around it.
- **A cheap AI handles clerk work** — but only where its output can be
  verified, or a wrong answer is harmless. It is allowed to raise its hand
  ("a human or better model should look at this") and *never* allowed to veto
  or approve on its own.
- **A top-tier AI handles genuine judgment calls** — and even it cannot
  overrule the checklist.

One hard-won rule sits under all of this: an AI saying "I'm 100% confident"
means nothing. The system checks outputs; it never takes confidence on faith.
And whenever the answer is unclear, the system stops and asks rather than
guessing — an unknown is never treated as a green light.

## Four levels of risk

Every proposed change is sorted into a risk level, and the level decides how
many humans must be involved:

| Level | What it means | Who must sign off |
|---|---|---|
| 0 | Trivial and mechanically safe | No one — may ship automatically |
| 1 | Normal change | One peer reviewer |
| 2 | Sensitive area | The area's designated owner |
| 3 | Critical (security, money, irreversible steps) | The owner *plus* a designated skeptic, with a written "why this is safe" defense |

The sorting itself is done by a program following a written rubric — and
changing that rubric is classified level 3, so the rules can't be quietly
loosened.

## Receipts, not vibes

The deepest idea: trust is built from **receipts**. At any moment the human
can ask "show me why this shipped" and get a complete, tamper-evident chain —
the evidence gathered, every verdict, the permission it ran under, who
produced what. Disagreements between AIs, stops, and escalations aren't
failures of the system; they're the system working, each one leaving a record.

## What the human still does

The human's job shifts rather than disappears. Machines answer *how*
questions; the human answers *should we* questions:

- decide what work matters and set the boundaries;
- sign the permission slips, sized to their comfort;
- resolve the escalations — the cases the system deliberately stopped and
  packaged up because they need human authority;
- read the trail and tighten or loosen the rules based on what it shows.

## An honest note on where this is

The system deliberately tracks the gap between what's designed and what's
fully enforced — today, some protections are audit-and-discipline rather than
physically impossible to bypass, and the final "merge" click still involves
the human. The direction of travel is the point: with every iteration,
another rule moves from advice to law, and the human moves from gating the
work in real time to reading the receipts afterwards — spending their
attention only where the system stopped and asked.
