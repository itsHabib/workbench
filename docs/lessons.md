# Lessons from building with coding agents

What I'd tell someone starting to put real work through AI agents — in any
stack, any industry. Each lesson is a portable rule plus the experience that
earned it here. The vocabulary is defined in `docs/glossary.md`; the
no-jargon version of the whole picture is `docs/plain-language-overview.md`.

---

## 1. Instructions are advice. Enforcement is law.

A rule written in an instruction file is something the agent can misread,
deprioritize, or skip — and the longer it runs unattended, the more likely
that becomes. Sort every rule into two bins: guidance (fine as prose) and
guarantees (what may merge, spend, or touch credentials — these must be
enforced by something the agent cannot route around). My experience is that
the prose bin keeps shrinking as models improve, and the guarantee bin keeps
growing as I trust agents with longer leashes.

## 2. Never trust a model's confidence. Check its output.

The failure that shaped everything else I built: a model labeling real bugs
as false alarms *at 100% self-reported confidence*. Confidence is a number a
model attaches to its answer, not a property of the answer. Trust comes from
verification — tests, mechanical checks, a second independent reviewer —
never from asking the model how sure it is.

## 3. Route work by checkability, not difficulty.

The right question for "can a cheap, fast model do this?" is not "is it
easy?" but "can I verify the output mechanically, or is a wrong answer
harmless?" If either holds, send it down and accept occasional slop — a
wrong answer costs a retry. If neither holds, no amount of "it's a simple
task" makes the cheap model safe.

## 4. Helpers may raise their hand. They may never veto or approve.

When a model sits in a safety path, give it one power: escalate. It may say
"a human should look at this"; it may never lower an alarm, approve an
action, or block one on its own authority. A wrong escalation costs a few
minutes of attention. A wrong approval costs an incident.

## 5. No AI opinion overrides a hard check.

The non-negotiable checks form a floor that always runs, and nothing
smarter sits *instead* of it — only above it. Even the best model,
resolving a genuinely hard judgment call, cannot turn a failed deterministic
check into a pass. Red stays red. The moment judgment can launder a failure,
every failure eventually gets laundered.

## 6. When the system doesn't know, it must stop — not guess.

Unknown input, missing evidence, an unrecognized case: the safe behaviors
are "stop" and "ask," never "assume the common case." Absence never reads
as green. And make "stop and ask a human" a first-class outcome with the
whole question packaged — a system that can say "I can't decide this
safely" is more trustworthy than one that always has an answer.

## 7. Hand out authority like a valet key.

Standing broad access plus an agent is one bad context away from a mess.
Authority works better as a signed, scoped, expiring object: this action,
this project, this risk ceiling, this deadline — checked at the moment of
action, not just at the start. Same for credentials: a broker holds the
real token, the agent gets the capability, never the key.

## 8. Review the output and the authority separately.

"Is this code good?" and "should this ship?" are different questions with
different owners. Reviewers and AI panels answer the first. The second is an
authorization decision — evidence, risk tier, who signed off — and it
deserves its own explicit mechanism rather than riding along on an
approving nod. Scale the human involvement with the consequence of being
wrong, not with how impressive the model is.

## 9. Demand receipts for everything.

Every piece of evidence, every verdict, every authority used, recorded and
linked — so "why did this ship?" is answerable later from the record alone.
Two corollaries learned the hard way: nothing gets written as fact without
the evidence that produced it, and read-only views must never quietly
become the place decisions come from. Receipts are also what let you *stop
watching*: supervision shifts from real time to after the fact.

## 10. Build the checks before you leave the loop.

Autonomy is earned in one direction: first the harness rules, the strong
test suites, the reviewer agents, the boundaries around secrets and
production — then the stepping away. Green checks alone are not enough;
they say the build is sound, not that the system still does its job in
production. Live evidence is a different check, and it's the one that
matters most once nobody is watching.

## 11. Pin evidence to the exact version it judged.

An approval, a test run, a review — each is evidence about one precise
state of the code. Any new change invalidates it, and the evidence must
re-attach. This feels pedantic until the first time "approved last week"
almost authorizes this week's different code.

## 12. Experiment in instructions; promote what stabilizes into code.

Skills — named, reusable instruction files — are the cheap laboratory:
agents follow them closely, and they cost minutes to change. Run the
routine, keep a friction log, watch what stays stable across runs. Once a
part needs a *guarantee*, move it into deterministic code. The biggest
instruction-file shrink I ever got wasn't editing — it was an engine
absorbing the prose. And it regrows unless the mechanism owns the behavior.

## 13. Prune instructions on a schedule; staleness beats verbosity as a threat.

Agent guidance rots two ways: scaffolding written for last year's weaker
models (wasteful but survivable) and facts that are simply no longer true
(actively misleading, because agents follow instructions literally).
Inventories, tool lists, and how-tos fall behind within weeks of active
work. Make updating them part of shipping, keep an honest list of known
doc-vs-reality gaps, and periodically re-ask of every instruction: is this
compensating for a weak model, or making a strong one safer? Delete the
first kind.

## 14. Start with one consequential action. Someone still owns the result.

Inside an organization, don't boil the ocean — pick one action that
matters (the merge, the deploy, the spend), define what the system can
observe about it, decide who can grant authority and who answers when it
stops and asks. Autonomy grows from that seed. And no matter how much the
agents carry: accountability doesn't transfer to them. Someone still owns
the result — for my work, that's me.
