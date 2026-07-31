# AI agents doing real work, in plain language

A non-technical explanation of how AI agents can carry real work — and how a
human stays firmly in charge of the decisions that matter. Nothing here
requires a programming background, and none of it is specific to software:
the same shape applies wherever an AI acts on your behalf and some of its
actions have consequences. The technical vocabulary lives in
`docs/glossary.md`; the hard-won rules in `docs/lessons.md`.

## Where this comes from

I stopped hand-writing code and made AI agents do essentially all of it,
specifically to find out what breaks.

What breaks is trust.

## The problem: instructions are advice

The obvious way to keep an AI in line is to write it instructions: "always
get approval before shipping," "never touch the credentials." The
uncomfortable truth is that written instructions are *advice*. An AI can
misread them, deprioritize them after hours of work, or follow them 99 times
and skip them the hundredth. For low-stakes work that's fine. For "what is
allowed to reach customers" or "what can spend money," it is not.

The longer an AI works unattended, the less you can afford rules that exist
only as words.

## The core move: rules that matter become enforced

Sort every rule into one of two bins:

- **Advice** — how to do the work well, what style to use, what good looks
  like. This can stay as written guidance, and as AIs get smarter you need
  less and less of it.
- **Law** — what may ship, what may spend, what may touch secrets. These get
  taken out of prose entirely and rebuilt as checks that sit *in the path*
  of the action. An enforced check doesn't get tired, doesn't reinterpret,
  and cannot be talked out of its job.

The direction of travel, summed up in one line: *advice shrinks, guarantees
grow.*

## How a piece of work travels

1. **It starts on a task list.** Work is broken into small, well-described
   pieces.
2. **AI workers pick pieces up** — several at once, each producing a
   proposed change with a signature recording exactly who (which AI, which
   pipeline) produced it.
3. **AI inspectors review each proposal** and file findings; another AI acts
   as the judge over the reviewers, deciding which findings are real and
   sending the work back for fixes.
4. **A gatekeeper decides whether it may ship.** This is the heart of the
   system: an automated check that examines the evidence and will not
   proceed without a valid permission from the human.
5. **Everything lands in a logbook** that quietly detects tampering — every
   piece of evidence, every verdict, every decision, linked to what caused
   it. You can always reconstruct not just *what* happened but *why*.

## The permission slip

The human doesn't hover. Instead they sign a scoped, expiring permission —
a valet key rather than the master key:

- it covers **one project and one kind of action**, nothing else;
- it has a **risk ceiling** — routine changes only, unless the human said
  otherwise;
- it **expires** on a timer;
- it is **cryptographically signed**, so it can't be forged or altered.

If the permission has expired, or the work turns out riskier than the slip
allows, everything stops — every time.

## A ladder of judgment

Not every question deserves the most expensive mind, and no AI opinion gets
to overrule a hard check:

- **The checklist always runs.** Deterministic, non-negotiable checks that
  nothing can lower or talk around.
- **A cheap AI handles clerk work** — but only where its output can be
  verified, or a wrong answer is harmless. It may raise its hand ("someone
  should look at this") and may *never* veto or approve on its own.
- **A top-tier AI handles genuine judgment calls** — and even it cannot turn
  a failed check into a pass.

Two rules sit underneath: an AI saying "I'm 100% sure" means nothing — the
system checks outputs, never confidence. And when the answer is unclear, the
system **stops and asks** rather than guessing. "I can't decide this
safely" is a first-class answer, and when it happens, the full question —
evidence and all — is packaged and routed to a person (in my setup, a Slack
message with approve/deny buttons). The person's decision flows back in as
a recorded, signed event, and work resumes.

## Four levels of risk

Every proposed change is sorted by consequence, and the level decides how
many humans are involved:

| Level | What it means | Who must sign off |
|---|---|---|
| 0 | Trivial and mechanically safe | No one — may ship automatically |
| 1 | Normal change | One peer reviewer |
| 2 | Sensitive area | The area's designated owner |
| 3 | Critical (security, money, irreversible steps) | The owner *plus* a designated skeptic, with a written "why this is safe" defense |

The sorting is done by a program following written rules — and changing
those rules is itself classified level 3, so the bar can't be quietly
lowered.

## Receipts, not vibes

At any moment the human can ask "show me why this shipped" and get a
complete, tamper-evident chain: the evidence gathered, every verdict, the
permission it ran under, who produced what. Stops, disagreements between
AIs, and escalations aren't failures of the system — they *are* the system
working, each one leaving a record. Receipts are also what let the human
stop watching in real time: supervision moves to after the fact, spent only
where the system stopped and asked.

## What the human still does

The job shifts rather than disappears. Machines answer *how* questions; the
human answers *should we* questions:

- decide what work matters and set the boundaries;
- sign the permissions, sized to their comfort;
- answer the escalations — the cases the system deliberately stopped
  because they need human authority;
- read the trail and tighten or loosen the rules based on what it shows.

And one thing that never transfers to the machines: accountability. Someone
still owns the result.

## An honest note on maturity

A system like this is built in stages, and it's worth being precise about
which stage each protection is in: some rules are physically impossible to
bypass, others are audit-and-discipline today and enforcement tomorrow, and
the final say on the riskiest actions still belongs to a person. The
direction is the point: with every iteration, another rule moves from
advice to law, and the human's attention narrows to exactly the decisions
that deserve it.
