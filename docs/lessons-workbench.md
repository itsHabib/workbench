# Lessons, grounded in the code

What building the workbench taught, stated as rules with the failures that
earned them. This is the code-grounded twin of `docs/lessons.md` (the
portable version, written for readers outside this codebase): the same
ideas, here with the war stories and the exact places each rule is enforced.
Every lesson cites where it lives in code or docs; a lesson that lives
nowhere is just an opinion. Companion to `docs/workbench-101.md`.

---

## 1. Prose shrinks, guarantees grow

A sentence in a CLAUDE.md is advice a model can skip, misread, or deprioritize
under a long context. A rule that gates something real is only as strong as its
enforcement. So the two curves run opposite ways: as models get stronger,
how-to prose becomes obsolete and gets deleted; as agents run longer
unattended, every *unenforced* invariant becomes more dangerous and gets
promoted into code. The test applied to every piece: was this compensating for
a weak model (shrink it), or is it what makes a strong model safe to trust
longer (invest in it)? (`docs/workbench-101.md` §1.)

## 2. Never trust self-reported confidence; route by verifiability, never difficulty

The formative failure: a local model labeled real bugs as false positives *at
confidence 1.0* — confident garbage. The rule baked in everywhere since: work
goes to the cheap rung only when its output is *verifiable or escalate-safe* —
a deterministic check can confirm it, or a wrong answer only costs an extra
cloud call. Difficulty is never the flag; confidence is recorded, never
trusted. When signals disagree: verifier failures beat model disagreement,
which beats self-reported confidence. (`local/local.go`; §1, §5.)

## 3. A small model may escalate, never block

Small models confabulate on dense content, so escalation is their only safe
failure mode. This is not a guideline — it is a named violation the verdict
code refuses outright: a "block" from the local rung is rejected as a ladder
violation, full stop. The inverse rule guards the top: premium judgment
resolves escalations but cannot override a code block. Between them, the
deterministic floor always runs and can never be lowered.
(`cmd/gate/internal/verify/verify.go`.)

## 4. The strongest enforcement can be the order of statements in one function

"Judgment cannot override a hard block" is not a policy engine or a config
flag. It is the shape of the one function that combines verdicts: the block
case returns before the judgment is ever looked at, so the forbidden path is
simply unreachable. The same move closed an early bug — a function literally
named `markMerged` that wrote "this PR merged" into state without any check
that it had. Now recording any outcome requires the supporting verdict and a
live grant as inputs to the single place that writes, so the unguarded write
is unrepresentable rather than discouraged.
(`cmd/gate/internal/verify/verify.go`, `cmd/gate/main.go`.)

## 5. Share contracts, not call stacks — and enforce the boundary mechanically

Before the shared `contracts` package, four tools had each hand-rolled their
own "is this OK?" parser, so nobody owned what a verdict even meant. The fix
was a shared *vocabulary* (types + schemas), never shared *decision logic* —
and a CI `hygiene` job that fails the build on a violation, because a boundary
kept by convention is one refactor away from gone. Tools compose through
artifacts on disk; the one forbidden import is another tool's decision path.
(`docs/DESIGN.md`; `.github/workflows/ci.yml`.)

## 6. Name the roles and postmortems get shorter

The five-plane decomposition (State, Execution, Verification, Capability,
Observability) earned its keep as a diagnostic, not an architecture diagram:
every recurring failure turns out to be one plane doing another plane's job.
`markMerged` was Execution writing State without Verification. A merge policy
in a skill's prose was Composition carrying what Capability should enforce —
it moved into a grant. Once the planes are shared vocabulary, the fix names
itself. (`docs/workbench-101.md` §4.)

## 7. Views must never become sources

Observability is read-only and owns no authoritative state — a dashboard that
can *cause* a decision destroys the ability to reconstruct what happened. The
honest carve-out, because purity that forces bad engineering isn't worth it: a
notification sink may keep derived operational state (a read cursor, a dedupe
set), never an authoritative decision. The log stays the truth; flare only
tails it. (§4, Amendment 3.)

## 8. Exit codes are load-bearing seams — treat them like APIs

Callers branch on 0 pass / 1 blocked / 2 parked / 3 refused / 4 error, so the
codes are a contract. Taken seriously down to flag parsing: Go's default
behavior on a bad command-line flag is to exit with code 2 — the same number
as "parked" — so gate configures parsing to never do that. Otherwise a typo'd
flag would be indistinguishable from a deliberate stop. When a boundary is a
seam, even your error handling is part of the interface. (`cmd/gate/main.go`.)

## 9. Fail closed on the unknown, everywhere

Unknown tiers rank highest. Unknown producer classes and unknown decisions are
rejected outright. An unknown path means T1, never T0. Empty or malformed
input fails rather than classifies; an error is never a tier; a missing floor
escalates rather than passes. The umbrella phrase: absence never reads as
green. Fail-open defaults are how automation quietly eats a safety margin.
(`cmd/gate/internal/verify/verify.go`; `cmd/triage/internal/floor/parse.go`.)

## 10. A handoff must carry the whole question

A parked escalation packages everything the eventual judge needs — the
question, the verdicts, the recorded diff — because parking with a pointer
back into prose is exactly the leak the design exists to prevent. The
discipline cuts both ways: if a good judgment would need more than the
artifacts carry, that is a contract bug in the artifacts, not a reason to let
the judge go fishing. (`cmd/gate/internal/verify/judge.go`.)

## 11. State your tamper model honestly — and stress it

The hash chain is tamper-*evidence*, not access control: naive replay catches
edits and reordering but not truncation or rewrite-with-rehash, so a keyed
anchor (HMAC over chain head + entry count, key held outside the state dir)
closes those — while the docs say plainly this is not non-repudiation and the
realistic adversary is drift, not a funded attacker. And mechanisms earn
trust by surviving hostility: the naive state layer lost data three runs out
of three under a six-process stress, and the current locking exists because
Windows reports a racing file create as "access denied" rather than "already
exists" — which quietly defeated the obvious retry logic.
(`cmd/gate/internal/state/anchor.go`.)

## 12. Independent reinvention is evidence a rule is real

Floor-in-code plus escalate-only-model was invented separately twice — triage,
then tracelens — before being promoted to plane law. When two tools converge
on the same shape under different pressures, that is decent evidence the
shape is load-bearing, and the right response is promotion to shared law, not
deduplication into a library. (§5.)

## 13. Engines absorb prose

The biggest instruction-file reduction ever achieved here was not editing —
it was moving a state machine into code. When the driver engine took over
dispatch/poll/judgment/land, hundreds of lines of skill prose died ("no
sleep-polls — those are the engine's job now, not prose for the LLM to
re-derive"), and what remained was policy. The corollary, proven within
weeks: prose regrows unless the mechanism owns the behavior, so a diet
without a mechanism is temporary.

## 14. Docs rot by staleness before verbosity — keep a drift log

Verbosity costs tokens; staleness actively misleads. Inventories (tool maps,
verb lists, feature status) fall behind within weeks of active development
unless updating them is part of the merge ritual. Two disciplines keep docs
honest: `verified` / `intent` markers on every claim, and a drift log — an
explicit list of where docs and code currently disagree, in both directions —
preferred over docs that quietly overclaim. (`docs/workbench-101.md` §12.)

## 15. Land the honest conclusion; hold warts openly

The escalation work set out to build a sixth plane and concluded it had built
a contract plus a seam — and the docs say exactly that, because a symmetric
architecture diagram is worth less than a true one. Same posture inside the
code: multiple judgments are last-one-wins today, a known wart held
deliberately until join semantics are designed, tracked in writing rather
than papered over. Trust the system builds comes from what it admits as much
as what it enforces. (`docs/features/escalation-plane/spec.md`; §5.)
