# Lessons from building with coding agents

What I'd tell someone starting to put real work through AI agents — in any
stack, any industry. Each lesson is a portable rule, the experience that
earned it here, and where it's enforced in this codebase, because a lesson
that lives nowhere is just an opinion. The *Monday* block on each lesson
lists the literal first moves — commands to run, files to edit — not
themes to reflect on. This doc merged the old portable
list and its code-grounded twin (`lessons-workbench.md`, retired) into one
place; the vocabulary is in `docs/glossary.md`, the no-jargon overview is
`docs/plain-language-overview.md`, the mechanics of the daily loop are
`docs/workflow-mechanics.md`, and the system tour is `docs/workbench-101.md`.

Rules are grouped into four sections: trust and verification, authority and
boundaries, evidence and state, process and instructions. Three lessons get
the deepest telling — the confidence-1.0 failure (2), the engine that
absorbed its prose (20), and exact-head evidence paying for itself (11) —
because those three shaped most of the others.

---

## Trust and verification

### 1. Instructions are advice. Enforcement is law.

A rule written in an instruction file is something the agent can misread,
deprioritize, or skip — and the longer it runs unattended, the more likely
that becomes. Sort every rule into two bins: guidance (fine as prose) and
guarantees (what may merge, spend, or touch credentials — these must be
enforced by something the agent cannot route around). The two curves run
opposite ways: as models get stronger, how-to prose becomes obsolete and
gets deleted; as agents run longer unattended, every *unenforced* invariant
becomes more dangerous and gets promoted into code. The test applied to
every piece: was this compensating for a weak model (shrink it), or is it
what makes a strong model safe to trust longer (invest in it)?

*Where it lives:* `docs/workbench-101.md` §1.
*Monday:*

- `grep -inE 'never|always|must not|do not' CLAUDE.md` — those hits are
  your unenforced rules.
- Guidance stays prose: CLAUDE.md, skills. Guarantees — merge, spend,
  credentials — get code.
- Add a `deny` entry (`"Read(**/.env)"`, `"Bash(gh repo delete:*)"`) or a
  PreToolUse hook that exits 2. Copy both from `auto-mode-rulebook.md`
  §§2–3.

### 2. Never trust a model's confidence. Check its output.

The failure that shaped everything else built here. Early in the local-model
work, a small model was given real reviewer findings to screen, and it
labeled real bugs as false positives *at self-reported confidence 1.0*.
Not hedged, not uncertain — confident garbage. If that output had been
wired to anything with authority, the system would have laundered real
defects into "checked and fine" with a number attached that made it look
rigorous.

The lesson generalizes past small models. Confidence is a number a model
attaches to its answer, not a property of the answer. Nothing about
generating an output gives the generator standing to certify it. Trust
comes from verification — tests, mechanical checks, a second independent
reviewer, a deterministic floor — never from asking the producer how sure
it is. The rule baked in everywhere since: confidence is *recorded* (it's
useful telemetry) and never *trusted* (it gates nothing). When signals
disagree: verifier failures beat model disagreement, which beats
self-reported confidence, in that order.

This is also why "the agent says it's done" is never a completion signal
anywhere in the workbench. Done is a verdict from something that didn't do
the work: CI, the review roster at the exact head, gate's reducer, a trace
that reconstructs. The producer reports; the checker concludes.

*Where it lives:* `local/local.go` (confidence recorded, routing ignores
it); `cmd/gate/internal/verify/verify.go` (verdict precedence).
*Monday:*

- `grep -rn confidence` in the pipeline; find the threshold branch
  (`score >= 0.8` → auto-apply).
- Delete it — high-confidence output goes through the same verifier as
  low.
- No verifier yet? Schema validation plus one known-answer canary,
  failing loud.

### 3. Route work by checkability, not difficulty.

The right question for "can a cheap, fast model do this?" is not "is it
easy?" but "can I verify the output mechanically, or is a wrong answer
harmless?" If either holds, send it down and accept occasional slop — a
wrong answer costs a retry. If neither holds, no amount of "it's a simple
task" makes the cheap model safe. The measured record here: a local model
scored 10/10 classifying CI log lines (flake vs. infrastructure vs. real
break) and 155/156 extracting severity from reviewer comments — both jobs
where a schema constrains the output and a human can spot-check at a
glance. The same model was a NO-GO as a final review judge: dense-diff
judgment is neither mechanically checkable nor cheap to get wrong.

*Where it lives:* `local/README.md` (the eval verdicts and the
when-to-route-local rule); the `/offload` and `/review-digest` skills.
*Monday:*

- Pick your most repetitive agent chore.
- Define its output as a JSON schema; collect ~20 known-answer cases.
- Run them through a cheap model (`local -prompt … -schema '{…}'`) and
  score it.
- 19/20 or better and glance-checkable → route it down for good.
  Anything less, or unscoreable → keep the big model.

### 4. Helpers may raise their hand. They may never veto or approve.

When a model sits in a safety path, give it one power: escalate. It may say
"a human should look at this"; it may never lower an alarm, approve an
action, or block one on its own authority. A wrong escalation costs a few
minutes of attention. A wrong approval costs an incident. In gate this is
not a guideline — it is a named violation the verdict code refuses
outright: a "block" from the local rung is rejected as a ladder violation,
full stop. The inverse rule guards the top: premium judgment resolves
escalations but cannot override a code block.

*Where it lives:* `cmd/gate/internal/verify/verify.go`.
*Monday:*

- Per model in an automated path, write one line: what can its output
  *cause*?
- Anything beyond escalate: make the consumer reject it as an error
  (gate fails a local-rung "block" outright).
- Add the test: feed a forbidden verdict, assert the rejection.

### 5. No AI opinion overrides a hard check.

The non-negotiable checks form a floor that always runs, and nothing
smarter sits *instead* of it — only above it. Even the best model,
resolving a genuinely hard judgment call, cannot turn a failed
deterministic check into a pass. Red stays red. The moment judgment can
launder a failure, every failure eventually gets laundered.

The strongest enforcement of this turned out to be the order of statements
in one function: the block case returns before the judgment is ever looked
at, so the forbidden path is unreachable rather than discouraged. The same
move closed an early bug — a function literally named `markMerged` that
wrote "this PR merged" into state without any check that it had. Now
recording any outcome requires the supporting verdict and a live grant as
inputs to the single place that writes, so the unguarded write is
unrepresentable.

*Where it lives:* `cmd/gate/internal/verify/verify.go`, `cmd/gate/main.go`.
*Monday:*

- In your riskiest decision function, move the deterministic-fail
  `return` above the model call — the override path becomes unreachable,
  not just discouraged.
- Change the state-writer's signature to require the passing verdict as
  a parameter. The compiler now enforces what a comment used to request.

### 6. When the system doesn't know, it must stop — not guess.

Unknown input, missing evidence, an unrecognized case: the safe behaviors
are "stop" and "ask," never "assume the common case." In code that means:
unknown tiers rank highest; unknown producer classes and unknown decisions
are rejected outright; an unknown path means T1, never T0; empty or
malformed input fails rather than classifies; a missing floor escalates
rather than passes. The umbrella phrase: absence never reads as green.
Fail-open defaults are how automation quietly eats a safety margin. And
make "stop and ask a human" a first-class outcome with the whole question
packaged — a system that can say "I can't decide this safely" is more
trustworthy than one that always has an answer.

*Where it lives:* `cmd/gate/internal/verify/verify.go`;
`cmd/triage/internal/floor/parse.go`.
*Monday:*

- Pipe garbage in: empty input, an unknown enum value, truncated JSON.
- Every default that comes back instead of an error is a fail-open hole —
  fix it.
- Keep the garbage cases as permanent test fixtures.

### 7. A green suite proves only what it observes.

CI, unit tests, integration tests, and E2E against a candidate answer one
question: is this build internally sound? They say nothing about whether
production is receiving fresh data or whether the last deploy actually
happened. Those are different questions, and they need a different
observer — one that reads authoritative production state, not the
candidate's test output. The war story: a PR here could pass Go tests,
frontend checks, Playwright, deployment lint, database integration, and
API E2E while production data sat stale, because nothing owned the
question "is the system still doing its job?" The corollary cuts deeper:
auto-closing a specific bug requires a named regression check joined to
the fixing commit, the deployed SHA, and a production verification
receipt. A green suite that never exercised the bug proves nothing about
the bug.

*Where it lives:* the candidate/production split in
`docs/workflow-mechanics.md`; the roxiq observer evidence (external repo).
*Monday:*

- List the three production facts CI can't see: data freshness, deployed
  version, last good run of the job that matters.
- Give one an observer today: a scheduled check that reads live
  production state and alerts past a threshold.
- It reads the running system — it does not re-test the candidate.

---

## Authority and boundaries

### 8. Hand out authority like a valet key.

Standing broad access plus an agent is one bad context away from a mess.
Authority works better as a signed, scoped, expiring object: this action,
this project, this risk ceiling, this deadline — checked at the moment of
action, not just at the start. Same for credentials: a broker holds the
real token, the agent gets the capability, never the key. Custody is the
credential half made concrete: the caller sends no vendor token, the
manifest allows exactly the methods and paths the operator listed, the
broker injects the credential upstream, and everything else refuses before
forwarding — including query parameters nobody thought to list, whose
widening becomes an explicit, logged decision instead of a silent default.

*Where it lives:* `cmd/gate/internal/capability` (HMAC-signed, scoped,
timed, tier-capped grants); `cmd/custody`; `docs/auto-mode-defaults.md`
default 5.
*Monday:*

- In an agent session, try reading `~/.ssh/`, `~/.aws/credentials`,
  `.env`.
- If any works: copy the deny block from `auto-mode-rulebook.md` §2 into
  `~/.claude/settings.json` now.
- Next: put the call behind a broker that injects the secret upstream
  (custody's shape) — the agent gets the capability, never the key.

### 9. Review the output and the authority separately.

"Is this code good?" and "should this ship?" are different questions with
different owners. Reviewers and AI panels answer the first. The second is
an authorization decision — evidence, risk tier, who signed off — and it
deserves its own explicit mechanism rather than riding along on an
approving nod. Scale the human involvement with the consequence of being
wrong, not with how impressive the model is.

*Where it lives:* the review roster answers quality;
`cmd/gate` answers authorization; they meet only through artifacts.
*Monday:*

- Add one required merge condition that is *not* a code review: a status
  check asserting "may this ship" — risk label present, migration plan
  linked, grant on file.
- The reviewer keeps "is it good"; the new check owns "should it ship."

### 10. Agents propose capabilities. They never possess powerful identities.

The most concrete version of the valet key: the merge identity itself. An
agent that merges with your account is indistinguishable from you, forever,
in every audit. Here, agents produce code and evidence; gate authorizes one
action for one exact head; an independent approval releases execution; and
only then does a dedicated GitHub App — a separate identity — consume the
authorization once and perform the exact-head merge. The visible merge
actor is the App bot, not an agent borrowing a human. One real bootstrap
merge has run this path end to end (workbench PR #169); hosted activation
and adversarial canaries are still in progress, and the docs say so rather
than rounding up.

*Where it lives:* `contracts/gateauthorization`;
`docs/features/trusted-gate-judgment-bridge/`.
*Monday:*

- `gh pr list --state merged --limit 20 --json number,mergedBy` — read
  the actor column.
- Automation merging under a human's name is this lesson unpaid.
- Mint a single-purpose bot/App identity for the merge step; move the
  human token out of the agent's reach (lesson 8).

### 11. Pin evidence to the exact version it judged.

An approval, a test run, a review — each is evidence about one precise
state of the code. Any new change invalidates it, and the evidence must
re-attach. This feels pedantic until the first time "approved last week"
almost authorizes this week's different code.

It stopped feeling theoretical here on one production-observability PR.
The panel reviewed clean; then fixes landed, and the head changed; the
exact-head rule forced re-review instead of letting the earlier
completions ride. The re-review found four distinct real issues — resume
checkpoints, rollback compatibility, image existence, starvation of later
passes — that did not exist, or were not visible, at the previously
reviewed head. Every one of them would have shipped under "reviewed once
is reviewed."

So the invariant runs the whole pipeline: review completion binds to the
head SHA, findings bind to the head they judged, gate evaluates the exact
head, and the executor refuses to merge anything but the exact commit the
authorization names. A fix commit invalidates earlier completeness until
the policy is satisfied again — by design, every time, because the one
time it matters you won't know in advance. The friction log holds a
second instance from a different repo: a review cycle's own fix
introduced a contradiction that the *next* cycle's panel caught as a
High finding — the re-review catching the fix's wake, which is exactly
the failure "reviewed once" can never see.

*Where it lives:* the exact-head panel checks in gate (PR #163);
`contracts/reviewfindings`; the executor's `--match-head-commit` shape.
*Monday:*

- Branch protection / rulesets: enable "dismiss stale approvals when new
  commits are pushed" — the platform's native exact-head rule.
- Pin automated merges: `gh pr merge --match-head-commit <sha>`.
- Now "what was approved" and "what lands" cannot diverge in the gap.

### 12. Share contracts, not call stacks — and enforce the boundary mechanically.

Before the shared `contracts` package, four tools had each hand-rolled
their own "is this OK?" parser, so nobody owned what a verdict even meant.
The fix was a shared *vocabulary* (types + schemas), never shared
*decision logic* — and a CI `hygiene` job that fails the build on a
violation, because a boundary kept by convention is one refactor away from
gone. Tools compose through artifacts on disk; the one forbidden import is
another tool's decision path. The reuse test is the payoff: when an
unrelated repo consumed the escalation contract, four fields generalized
cleanly and one (`grant`) did not — and the loud sentinel value it ships
instead of a fake is exactly how a hidden coupling becomes a design input.

*Where it lives:* `docs/DESIGN.md`; `contracts/`;
`.github/workflows/ci.yml` (hygiene job).
*Monday:*

- Find two tools passing informal output; write the schema they're both
  pretending exists (`schemas/verdict.json`).
- Validate both sides against it in CI.
- Add a CI step that fails when one tool imports another's internals —
  the `hygiene` job here is a short script, not a framework.

### 13. Exit codes are load-bearing seams — treat them like APIs.

Callers branch on 0 pass / 1 blocked / 2 parked / 3 refused / 4 error, so
the codes are a contract. Taken seriously down to flag parsing: Go's
default behavior on a bad command-line flag is to exit with code 2 — the
same number as "parked" — so gate configures parsing to never do that.
Otherwise a typo'd flag would be indistinguishable from a deliberate stop.
When a boundary is a seam, even your error handling is part of the
interface.

*Where it lives:* `cmd/gate/main.go`.
*Monday:*

- `grep -rnE 'exit [0-9]|\$\?' scripts/` — find every code a caller
  branches on.
- For each, list what *else* produces that number: Go exits 2 on a bad
  flag, grep exits 1 on no match.
- Give real errors their own code; put the table in the README.

---

## Evidence and state

### 14. Demand receipts for everything.

Every piece of evidence, every verdict, every authority used, recorded and
linked — so "why did this ship?" is answerable later from the record alone.
Two corollaries learned the hard way. Nothing gets written as fact without
the evidence that produced it (`markMerged`, lesson 5, was this rule's
funeral). And read-only views must never quietly become the place
decisions come from — a dashboard that can *cause* a decision destroys the
ability to reconstruct what happened. The honest carve-out, because purity
that forces bad engineering isn't worth it: a notification sink may keep
derived operational state (a read cursor, a dedupe set), never an
authoritative decision. The log stays the truth; flare only tails it.
Receipts are also what let you *stop watching*: supervision shifts from
real time to after the fact.

*Where it lives:* gate's hash-chained log (`cmd/gate/internal/state`);
`docs/workbench-101.md` §4 Amendment 3.
*Monday:*

- Reconstruct yesterday's most consequential automated action from
  records alone.
- Each fact you had to *remember* is a missing receipt.
- Add the write at that decision point — one JSONL line: inputs, rule
  fired, verdict. Copy the hook in `auto-mode-rulebook.md` §4.

### 15. Build the checks before you leave the loop.

Autonomy is earned in one direction: first the harness rules, the strong
test suites, the reviewer agents, the boundaries around secrets and
production — then the stepping away. The practiced version: don't watch
the implementation at all; define the PR/evidence boundary you expect,
and look there. For personal tooling, checks plus gate plus real usage is
usually enough. For work code, wait for green checks and folded findings,
then read the whole PR yourself. Autonomy is not one global setting; it's
earned per repository, per service, per effect, in proportion to the
verification behind it.

*Where it lives:* the `/drive` skill's stop-at-boundary contract; the
tier model in `docs/auto-mode-defaults.md`.
*Monday:*

- Name the check whose absence makes you watch the transcript. Build it
  this week.
- Run one task without watching; inspect only the boundary — the PR, its
  checks, the folded findings.
- Every urge to peek mid-run names the next missing check.

### 16. Automation can only assert what you deliberately made observable.

Deterministic gates require deterministic evidence, and most evidence does
not occur naturally — it is designed. Folder and file conventions, naming
conventions, explicit state transitions, versioned schemas, stable IDs,
exact-head joins, receipt filenames carrying correlation IDs, process exit
codes, immutable deployment digests, tests whose names encode a regression
contract: every one of these is somebody deciding in advance that a fact
should be checkable by a script. The floor can only classify what a
fixture can express; the gate can only verify what an artifact records.
When a rule feels impossible to automate, the missing piece is usually not
a smarter model — it's an observable nobody engineered.

*Where it lives:* `docs/auto-mode-defaults.md` default 2 (classify
observables, not intent); `cmd/triage/internal/floor`.
*Monday:*

- Pick one judgment call you repeat.
- Build its observable: a path that means something (`migrations/` =
  high tier), a test name that pins the regression, a receipt filename
  carrying the run ID.
- Write the one-line floor rule keyed on it.

### 17. Verify the object, not its label.

A SHA-shaped tag does not prove an image exists. A success-shaped artifact
name does not prove its producing job passed. A reviewer once reporting
does not prove the current head was reviewed. The system refetches and
validates the underlying object before acting on it — and before mutating
anything externally visible, it proves the *next* mutation is possible
too, the operational equivalent of checking every precondition before a
transaction commits. Deployment and rollback chains validate the full
effect path before changing state, because discovering a broken rollback
*during* the rollback is the most expensive possible time.

*Where it lives:* the executor's refetch-and-validate step
(`docs/features/trusted-gate-judgment-bridge/`); the closure receipt
contract (`contracts/`).
*Monday:* replace one trusted label with a fetch —

- `docker manifest inspect <tag>` before rollout, not the SHA-shaped
  name.
- Re-fetch the PR head at merge time, not the approval's memory of it.
- Check the producing job's conclusion, not the artifact's filename.

### 18. Supervision fails silent unless the watcher itself is watched.

Two distinct ways a monitoring layer erases its own failure signal, both
paid for here. First, budgets compose: an outer observer's timeout must
exceed the inner verification budget plus the time to upload evidence and
reconcile state — the roxiq observer's budgets exist because getting that
arithmetic wrong means the failure the watcher exists to catch is the one
thing it can never see. Second, watchers die silently: the friction log
records a CI watcher whose output parsing never matched, so it reported
`CI_TIMEOUT` while CI was in fact green, and a detached engine launch
killed by a quiet shell guard, which cost a 25-minute watch on a run that
was never running. The discipline that came out of it: launch-check every
detached process (a beat later — is it alive, is its log non-empty), and
treat a watcher's "nothing happened" as a claim to verify, not a fact.

*Where it lives:* the observer budget design (roxiq, external); the ship
workbench friction log (watcher entries).
*Monday:*

- Sum the timeout chain by hand: inner verify + evidence upload +
  reconcile must fit inside the outer watchdog. Fix the inequality.
- Launch-check every detached process: one beat after launch,
  `kill -0 $pid && test -s $logfile`, else report dead-on-arrival.

### 19. State your tamper model honestly — and stress it.

The hash chain is tamper-*evidence*, not access control: naive replay
catches edits and reordering but not truncation or rewrite-with-rehash, so
a keyed anchor (HMAC over chain head + entry count, key held outside the
state dir) closes those — while the docs say plainly this is not
non-repudiation and the realistic adversary is drift, not a funded
attacker. And mechanisms earn trust by surviving hostility: the naive
state layer lost data three runs out of three under a six-process stress,
and the current locking exists because Windows reports a racing file
create as "access denied" rather than "already exists" — which quietly
defeated the obvious retry logic.

*Where it lives:* `cmd/gate/internal/state/anchor.go`.
*Monday:*

- Write the paragraph into the doc: what the trail catches (edits,
  reordering), what it doesn't (truncation, rewrite-with-rehash), the
  realistic adversary (drift).
- Stress it once: six concurrent writers, three runs. The naive layer
  here lost data on all three.

---

## Process and instructions

### 20. Experiment in instructions; promote what stabilizes into code.

Skills — named, reusable instruction files — are the cheap laboratory:
agents follow them closely, and they cost minutes to change. Run the
routine on real work, keep a friction log, watch what stays stable across
runs. Keep flexible judgment in the skill. Once a part needs a
*guarantee*, move it into deterministic code.

The biggest instruction-file shrink ever achieved here wasn't editing — it
was an engine absorbing the prose. The work-driver skill once carried the
whole merge tail as instructions: dispatch, poll, judge, land, record,
with the model re-deriving the loop every run and occasionally dropping a
step. When the driver engine took that state machine into code, hundreds
of lines of skill prose died in one commit — "no sleep-polls; those are
the engine's job now, not prose for the LLM to re-derive" — and what
remained in the skill was policy: the review-cycle cap, strategy
selection, the merge call. The engine owns the loop; the skill owns the
opinions.

The corollary proved itself within weeks: prose regrows unless the
mechanism owns the behavior. A diet without an engine is temporary,
because every new edge case gets patched back in as another paragraph.
The maturation path that actually holds is: friction → memory or skill
correction → measured recurring category → deterministic rule or engine
verb → tests and receipts → delete the prose the mechanism absorbed.

*Where it lives:* the `/work-driver` skill (policy) vs. ship's driver
engine (mechanism); `docs/workflow-mechanics.md`.
*Monday:*

- Split your longest instruction block into two lists: opinions, and
  loop mechanics — every "wait", "poll", "retry", "check again".
- Move the loop list into a script or engine the agent *calls*.
- Delete the absorbed prose in the same commit — left standing, it
  regrows.

### 21. Prune instructions on a schedule; staleness beats verbosity as a threat.

Agent guidance rots two ways: scaffolding written for last year's weaker
models (wasteful but survivable) and facts that are simply no longer true
(actively misleading, because agents follow instructions literally).
Inventories, tool lists, and how-tos fall behind within weeks of active
work. Two disciplines keep docs honest: `verified` / `intent` markers on
every claim, and a drift log — an explicit list of where docs and code
currently disagree, in both directions — preferred over docs that quietly
overclaim. And periodically re-ask of every instruction: is this
compensating for a weak model, or making a strong one safer? Delete the
first kind.

*Where it lives:* `docs/workbench-101.md` §12; FOLLOWUPS.md.
*Monday:*

- Delete ten lines of agent instructions the model has outgrown. See
  what breaks (usually: nothing).
- Start the drift log: where docs and code currently disagree, both
  directions, in writing.

### 22. Name the roles and postmortems get shorter.

The five-plane decomposition (State, Execution, Verification, Capability,
Observability) earned its keep as a diagnostic, not an architecture
diagram: every recurring failure turns out to be one plane doing another
plane's job. `markMerged` was Execution writing State without
Verification. A merge policy in a skill's prose was Composition carrying
what Capability should enforce — it moved into a grant. A notification
layer rendering mechanism failures as human decisions was Observability
impersonating Escalation. Once the planes are shared vocabulary, the fix
names itself.

*Where it lives:* `docs/workbench-101.md` §4.
*Monday:*

- Write one line per plane for *your* system: who holds state, who
  executes, who verifies, who grants, who observes.
- Re-read your last confusing postmortem against the five lines — the
  finding is usually "X doing Y's job," in a sentence.

### 23. Independent reinvention is evidence a rule is real.

Floor-in-code plus escalate-only-model was invented separately twice —
triage, then tracelens — before being promoted to plane law. When two
tools converge on the same shape under different pressures, that is decent
evidence the shape is load-bearing, and the right response is promotion to
shared law, not deduplication into a library.

*Where it lives:* `docs/workbench-101.md` §5.
*Monday:*

- Look for a shape built twice under different pressures: a retry
  envelope, an escalate-only helper, a receipt format.
- Promote it: name it, write it down, make new work cite it. Promotion,
  not deduplication — the second invention already field-tested it.

### 24. Model and effort are separate dials.

Change the *model* when the task needs a different kind of reasoning —
more creativity, architectural depth, another perspective. Raise the
*effort* when the model appears not to be searching, checking, or reaching
far enough despite having the right kind of intelligence. Conflating the
two wastes money in one direction and quality in the other. Learn the
personalities of the models you use; better yet, ask the agent to
recommend model and effort for a task instead of memorizing a table. The
routing ladder under it all: deterministic code first, a local model when
output is verifiable or escalate-safe (lesson 3), a frontier model for
irreducible judgment.

*Where it lives:* seed/prep task metadata (recommended model + effort per
task); `local/README.md`.
*Monday:*

- Wrong *kind* of reasoning → switch model. Right kind but shallow
  search → raise effort.
- They are two independent dials in the harness (`/model`, effort
  level) — turn one at a time.
- Cheapest correct move: ask the agent to recommend model + effort for
  the task before starting it.

### 25. The substrate fails like the agent — learn to tell them apart.

A whole recurring category in the friction log has nothing to do with
models and everything to do with the floor they stand on: shells, package
managers, SDK transports. A background dispatch fails because each
tool-call shell is a *fresh* shell that inherits no env, so the key that
worked a minute ago is gone. A sed anchored on column one misses an
indented config entry and a well-meaning guard silently kills the launch.
A workspace dependency reads as "file not found" when the real cause is a
stale symlink an install would fix. A cloud agent's SDK stream dies
mid-edit on long tool calls — a confirmed pattern across models — with
the work complete but uncommitted. Every one of these masquerades as "the
agent failed," and blaming the agent buys nothing. The rules that came
out: set env inline in the same command as the thing that needs it,
launch-check detached processes, prefer structured output (`--json`) over
parsing terminal text, run the install after any dep-graph change before
trusting a test run, and let engines treat transport death as a normal
state with resume — not an anomaly.

*Where it lives:* the ship workbench friction log (the densest category
in it); the engine's kill/resume semantics.
*Monday:* four habits, each deleting a class of "the agent failed" —

- Set env inline with the command that needs it (`KEY=x cmd`) — every
  tool call is a fresh shell; exports don't survive.
- Ask for `--json` and parse it; never scrape terminal text.
- Re-run the install after any dependency change, before trusting a
  test run.
- Launch-check detached processes (lesson 18).

### 26. Report the path that actually ran.

Never fabricate a driver receipt, a review artifact, or a gauntlet result
for work performed manually. Manual `/drive`, the ship driver, the session
engine, and cloud seats are distinct production paths, and the evidence
should say which one ran — because the moment provenance is decorative,
every downstream consumer of it is reasoning from fiction. The companion
discipline is scope: when an urgent objective hits broken infrastructure,
fix the objective, not the world. A talk-prep run here once lost its
workflow state to a database failure mid-task; the correct move was a
fresh isolated worktree and the approved fix, not an unplanned recovery
project. The objective was the talk.

*Where it lives:* driver-state receipts record their engine; friction log.
*Monday:*

- Add a required `actor` field to your receipts: which engine, which
  path, or `manual`.
- Enforce the rule behind it: hand-done work gets logged as manual or
  not at all — never dressed as automation.

### 27. Land the honest conclusion; hold warts openly.

The escalation work set out to build a sixth plane and concluded it had
built a contract plus a seam — and the docs say exactly that, because a
symmetric architecture diagram is worth less than a true one. Same posture
inside the code: multiple judgments are last-one-wins today, a known wart
held deliberately until join semantics are designed, tracked in writing
rather than papered over. The trust a system builds comes from what it
admits as much as what it enforces.

*Where it lives:* `docs/features/escalation-plane/spec.md`;
`docs/workbench-101.md` §5.
*Monday:*

- Add a "known warts" section to the README of the system you most want
  trusted.
- Write the ugliest entry first: what's broken, why it's held open.
- Watch what that does to how much people trust the rest.

### 28. Start with one consequential action. Someone still owns the result.

Inside an organization, don't boil the ocean — pick one action that
matters (the merge, the deploy, the spend), define what the system can
observe about it, decide who can grant authority and who answers when it
stops and asks. Autonomy grows from that seed, ratcheted by evidence:
false blocks, escapes, resolution time, recovery time. And no matter how
much the agents carry: accountability doesn't transfer to them. Someone
still owns the result — for my work, that's me.

*Where it lives:* everywhere above; this is the lesson the rest exist to
make survivable.
*Monday:*

- Pick the action: merge, deploy, or spend. One.
- Write four lines about it: what's observable, who mints authority, who
  answers the stop-and-ask, which numbers ratchet autonomy (false
  blocks, escapes, resolution time, recovery time).
- Build the deterministic floor for that one action first.
