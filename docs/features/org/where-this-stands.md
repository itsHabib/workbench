# Where this stands — 2026-08-23

> **2026-08-24 build update.** The re-entry slice this doc calls closest to
> buildable is now BUILT and live: `cmd/org` (the Baton home — chains as JSONL
> under `~/dev/org/state`, flock-serialized appends, admission delegated to
> `org.Advance`, `org boot` re-entry index, SessionStart/Stop hook scripts) in
> PR #262, and `cmd/org-mcp` + JSON receipts + `ORG_INCARNATION` identity +
> operator `context.d` boot sources in PR #263, both CI-green. Real chains
> exist for `lead:agentic-development` and `lead:rooms`; `org-mcp` is
> registered user-scope. Deliberate deviations, recorded in the PRs: no SQLite
> store yet (the chain is not the discharge store §store-decision chose SQLite
> for), write-as-holder default with `-strict` opt-out, marks-not-checkpoints
> from the Stop hook. The discharge-rate measurement against the 18/40/0
> baseline starts when the hooks are pasted into `~/.claude/settings.json`.

A synthesis written at the point where the first real code landed and the first
real numbers came in. It is deliberately separate from [`vision.md`](vision.md),
which argues what to build, and [`p0-findings.md`](p0-findings.md), which
records measurements. This file says what is now **true**, what is still a
**claim**, and what that combination argues for doing next.

Its central conclusion is not the one the vision expects.

---

## 1. The shape of the thing, in one paragraph

The workbench is five planes that compose through typed artifacts and exit
codes rather than call stacks — State (dossier, gate's hash-chained log),
Execution (ship's driver), Verification (the escalate-only verifier ladder),
Capability (scoped, timed grants), Observability (flare, console, `/wip`). One
law holds them apart: **no plane imports another plane's decision logic.** gate
is the flagship because it is the only tool spanning two planes — it decides
authorization, which is not the same question the reviewers answer.

Baton (working name `org`) proposes four more planes — Continuity, Effect,
Work, Surface — on one thesis: **ownership and continuity are the same fact,
and the fact is a compare-and-swap.** A role is a durable office with an
append-only chain; an incarnation is a disposable session that reads the tip,
takes it, acts, and writes back. To act you must append; to append you must
present the tip you read. From that single rule fall continuity, mutual
exclusion, handover, and revocation.

---

## 2. What is now proven, what is measured, and what is still a claim

The distinction matters more than any individual item, because the project has
been running on prose numbers that did not survive measurement.

### Proven — code on `main`, tested harder than anything else in the portfolio

`contracts/org` implements the ownership fold. **"Holding the tip is being the
role" is now a property of code rather than a convention a runtime is trusted
to follow.** Specifically established:

- The compare-and-swap, with identity settled **before** chain position — the
  stale writer that matters re-read the tip and presents a correct `prev`, so a
  position-first reducer calls that chain healthy and misdiagnoses the one
  failure the law exists to catch.
- The one-active-claim law, whose payoff is that an effect stamp's `work_ref`
  is **derived from state rather than supplied by the caller**. That deletes a
  misattribution class instead of documenting it.
- Inherited obligation: a takeover mid-claim leaves a dangling claim the
  successor must discharge before it may claim anything, and teardown refuses
  while any obligation is open.

Evidence: 98.1% line coverage, 98.6% mutation efficacy, and an exhaustive walk
of all 86 reachable states asserting totality and eight invariants.

### Measured — evidence that changed decisions

| finding | number |
|---|---|
| gate blocks that came from a **judge** | 56/87 (64%) |
| gate blocks that a **wait** would have cleared | 24/87 (27.6%) |
| parked runs never resolved *on that run* | 106/342 (31%) |
| …of those, resolved by a **sibling run on the same PR** | 92/106 (87%) |
| PRs merged with **no recorded gate decision at all** | 2 |
| affordable role ceiling at the half-shift rate | **~25**, not 75 |

### Still a claim — the load-bearing ones

- **Collisions.** Unmeasured, and *unmeasurable retrospectively* — "mentioned
  the same PR" is not "held the same work item," because holding was never
  recorded. This is the input the vision's own gate turns on.
- **Distillation quality.** §3.3 names it "the thesis's only untested
  load-bearing assumption," and nothing has tested it. The chain can carry
  commitment; whether it carries *thought* is unknown.
- **A second human.** Every claim about the org shape is unfalsifiable until
  someone else holds a role.

---

## 3. The finding that reframes the project

Four separate observations turned out to be one defect wearing four costumes:

1. 87% of "stalled" traces were decided — by a *sibling run on the same PR*.
2. The stalled percentage measures the observer's keying, not system health.
3. Two PRs merged with no gate decision recorded anywhere.
4. `gate next` showed 149 obligations that were already merged.

**gate records runs; the thing that matters is the subject's decision history.**
A park on run A that run B resolves is invisible to a run-keyed reader. A merge
that happens out of band leaves no trace at all. Gate has no continuity across
runs about a subject — which is precisely, exactly, the problem Baton exists to
solve.

So the honest framing is not "build an org substrate, then find users." It is:

> **Baton's first customer is gate, and gate is already suffering the exact
> failure Baton fixes.**

That reframing matters because it changes the scale at which the thesis has to
be true. An ownership substrate for one flagship tool needs no fleet, no 75
roles, no second machine, and no tenancy. It needs a chain per subject and a
fold. Both exist and are on `main`.

---

## 4. The vision's own gate, evaluated

§7 P0 sets the condition for proceeding past P1:

> If collisions ≥ 10 in 90 days *and* false positives < 20% *and* a role-day is
> affordable at target scale, proceed to the full design. If collisions come
> back at 3, the honest product is a good `/continue` that fires from a hook —
> one week of work — and everything about ownership is premature. Write the
> numbers down before deciding.

| input | threshold | measured | verdict |
|---|---|---|---|
| collisions / 90 days | ≥ 10 | **unmeasured** | unknown |
| false positives | < 20% | **27.6%** | **over** |
| role-day at target scale | 75 roles | affordable to **~25** | **3× under** |

Two of three fail; the third is unknown.

**On the false-positive number, both readings belong on the record.** Declining
to authorize on incomplete evidence is not *wrong* — under that reading the
rate is ~0%. But the threshold was written to catch "gate blocked something
that was actually fine," and a block a wait would have cleared is exactly that.
The threshold means the friction reading. It is breached.

**The 72% that this decision was previously resting on does not survive
re-derivation.** Reaching it requires counting judge blocks whose prose mentions
an evidence gap as "evidence not ready" — defensible as description, but by
then a cycle is spent and a judgment recorded. Three sessions produced 72%,
75%, and 33% because the classifier boundary was never stated. That is the
project's characteristic failure, and it is worth naming: **every number that
lived only in prose drifted, and every number re-derived from data came back
different.**

---

## 5. What to do next

**Run POC-A, and nothing else from the roadmap.**

`SessionStart` → attach → append; `Stop` → release; count collisions for a
week. Two properties make it the correct next move regardless of what it finds:

- It is the **one missing gate input**, and it cannot be obtained any other way.
- It is the **same work as the host adapter** the roadmap needs anyway, so
  nothing built is wasted.

If collisions come back at 3, the vision's own fallback is the product — a
`/continue` that fires from a hook, a week of work — and P2 through P7 were
correctly never started. If they come back at 15, you have the number *and* the
adapter, and P1's validation gate is half-built.

**Second, re-key gate's observer by subject.** Cheap, and it fixes the stalled
metric, the ghost inbox, and the "two merges with no decision" hole at once. It
is also the smallest possible demonstration of the ownership thesis against a
real tool.

**Hold `contracts/mandate` (`p1-t5`).** It is delegation machinery for P4+, and
building it before the gate resolves is exactly the premature work the gate
exists to prevent.

---

## 6. What to stop doing

- **Stop treating the blocked/not-yet split as the highest-value change.** It
  was ranked there on the 72%. At 27.6%, and with a separate analysis rejecting
  it because its backstops derive from prior artifacts for the same subject —
  and CI mints state into a `mktemp -d` deleted on exit, so they are
  structurally unreachable there — the ranking does not hold.
- **Stop designing for 75 roles.** The measured ceiling is ~25 at the
  half-shift rate, and a true 8-hour agent shift does not exist anywhere in the
  data: the longest session-day ever recorded is 268 active minutes, median 14.
- **Stop quoting numbers that live only in prose.** Every one that was
  re-derived came back different.

---

## 7. The pattern worth keeping

Exhaustive enumeration keeps being the instrument that works, and keeps being
rediscovered independently: parley's differential catching a hand-written table
under-specifying, warrant's mutation pass finding a property its suite
structurally could not observe, and this package's 86-state walk finding two
obligation-stranding sequences that 96% coverage and a 98.5% mutation score
both missed.

The generalization is worth stating once, here, so it stops being rediscovered:

> **For a finite state machine, enumerate it. Sampling finds what you thought
> of; enumeration finds what you did not.** Reach for Lean when a second
> implementation needs the transition table, and for a model checker when there
> are genuine interleavings — not before.

The corollary is the same lesson the numbers taught: an instrument that runs in
CI beats an argument in a document, because the document cannot notice when it
becomes false.
