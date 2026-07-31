# An agentic-development glossary

Plain working definitions for the vocabulary of building software with AI
agents. Written for someone outside this codebase — no prior exposure to my
tools assumed; the last section maps the specific tools I name so the rest of
the docs make sense. For the deeper story behind these ideas, see
`docs/lessons.md` and `docs/plain-language-overview.md`; for this repo's own
internals, `docs/workbench-101.md`.

---

## Agents and the pieces around them

- **coding agent** — an AI model wrapped in a loop that can act: read files,
  edit code, run commands, open pull requests. The model supplies judgment;
  the loop supplies hands.
- **model vs agent** — the model is the mind you rent (and swap as better
  ones ship); the agent is the mind plus tools, instructions, and
  permissions. Most reliability problems live in the second part.
- **harness** — the runtime the agent lives in (Claude Code, Codex, Cursor…):
  what tools it exposes, what it asks permission for, what it logs. Two
  agents on the same model can behave very differently under different
  harnesses.
- **context** — everything the model can currently see: instructions, code,
  conversation. It is finite, and behavior degrades as it fills — which is
  why long-running work needs state *outside* the conversation.
- **instruction file** (CLAUDE.md / AGENTS.md) — a repo's standing guidance
  to any agent working in it. Powerful, and rots quickly: it is advice, not
  enforcement.
- **skill** — a named, reusable instruction file for a routine (`/review`,
  `/ship-feature`). Agents follow skills much more closely than ad-hoc
  prompts, which makes them the cheap way to experiment with workflow.
- **MCP server** — a standard way to hand an agent a *capability*: a typed
  tool surface over something real (a task tracker, a database, an internal
  API), instead of screen-scraping or copy-paste.
- **hook** — an automatic check that fires around agent actions whether the
  model wants it or not. A reflex, not advice — good for catching risky
  command shapes.
- **subagent** — a helper agent spawned for a bounded piece of work with its
  own clean context, reporting a summary back. How large jobs avoid drowning
  one conversation.
- **worktree** — a separate working copy of a repo so parallel agents can
  edit code without stepping on each other.
- **auto mode** — letting the agent act without approving every step, with
  the harness stopping it only at defined boundaries (secrets, production,
  merges, force pushes). Freedom for autonomy, guardrails around authority.

## The delivery loop

- **ticket-to-PR loop** — the shape of routine agentic work: a task goes in;
  code, tests, and a pull request come out; checks, review, and a merge
  decision follow. The loop, not the code-writing, is where the engineering
  effort now lives.
- **dispatch** — sending a task to an agent (cloud or local) and tracking
  the run to completion, instead of babysitting a chat window.
- **parallel streams** — several agents working independent tasks at once,
  each in its own workspace, each producing its own PR. Cheap to do; the
  bottleneck moves to reviewing the output.
- **AI review panel** — multiple AI reviewers reading a PR independently,
  with a coordinating judge deciding which findings are real and driving the
  fix cycle. Reviewers disagree; that disagreement is signal.
- **checks / CI** — the deterministic test-and-lint machinery that runs on
  every change. Necessary, and not sufficient: green checks say the build is
  internally sound, not that the system still does its job.
- **provenance** — a recorded answer to "who made this?": which agent, which
  model, which pipeline produced a change. Cheap to stamp on every PR, very
  hard to reconstruct later.

## Trust and verification

- **verification** — the discipline of deciding whether agent output is
  acceptable, using evidence rather than vibes. As agents make
  implementation cheap, this becomes the constraint on the whole system.
- **deterministic floor** — the non-negotiable checks that always run and
  that no model opinion can lower. Everything smarter sits *above* this,
  never instead of it.
- **escalate-only advisory** — the pattern for using a model in a safety
  path: it may raise the alarm level or ask for a human, and may never lower,
  approve, or veto. A wrong answer then costs attention, not an incident.
- **self-reported confidence** — the number a model attaches to its own
  answer. Record it; never trust it. Models can be confidently wrong at
  "100%", so trust comes from checking outputs, not from asking the model
  how sure it is.
- **verifiable or escalate-safe** — the routing test for giving work to a
  cheap model: either the output can be checked mechanically, or a wrong
  answer is harmless (it just escalates). Either property sends work down
  the ladder; difficulty is never the deciding flag.
- **eval** — a repeatable test suite for a *model or prompt* rather than for
  code: known inputs, expected judgments, a score. How you learn what a
  model can be trusted with before production teaches you.
- **candidate checks vs production evidence** — two different questions:
  "is this build internally sound?" (tests, E2E, lint) versus "is the system
  still doing its job?" (live data freshness, real behavior, incidents).
  Passing the first is routinely mistaken for the second.
- **exact-head evidence** — evidence pinned to the precise commit being
  judged. Any new push invalidates it; reviews and approvals must re-attach.
  Prevents "approved last week" from authorizing this week's code.
- **fail closed** — when input is unknown, malformed, or missing, the system
  stops or asks — it never assumes the safe answer. Absence never reads as
  green.

## Authority and safety

- **capability** — what an agent is *able* to do, as distinct from what it's
  told. The reliable way to limit an agent is to bound its capabilities, not
  to write sterner instructions.
- **grant** — authority as a signed, scoped, expiring object: this action,
  on this project, up to this risk level, until this time. A valet key
  rather than the master key — and the system refuses to act without one.
- **least privilege** — the old rule, newly urgent: an agent gets the
  minimum access the task needs, because an agent with standing broad access
  is one bad context away from using it.
- **risk tiers** — classifying every change by consequence, with human
  involvement scaling accordingly: from "auto-mergeable" through "one peer
  review" up to "owner plus a designated skeptic." The classification itself
  should be deterministic, and changing the rules is top-tier by definition.
- **pass / park / block / refuse** — the four honest outcomes of an
  authorization decision: proceed; stop and hand a packaged question to a
  human; stop on failed evidence; stop because the authority itself was
  invalid. *Park* is the load-bearing one — "I cannot decide this safely" as
  a first-class outcome instead of a guess.
- **escalation** — the agent→human→agent loop: the system stops, packages
  the full question, routes it to a person (Slack, dashboard, wherever they
  actually look), and their decision flows back as a recorded, auditable
  event that lets work resume.
- **audit trail** — an append-only, tamper-evident record of every piece of
  evidence, every verdict, every authority used, linked so any decision can
  be reconstructed later from the record alone. Receipts, not vibes.
- **credential broker** — a proxy that holds real tokens so agents never do:
  the agent asks for an action, the broker checks the grant, injects the
  credential upstream, and logs the exchange. The agent uses the capability
  without ever holding the key.
- **secrets hygiene** — the boring discipline that agentic work amplifies:
  agents copy what they see, so a path, token, or customer name that enters
  the context can resurface anywhere — including in public output.

## Operating over time

- **drift** — the gap that grows between documentation/instructions and
  reality, and between copies of the same thing maintained in two places.
  Agents follow instructions literally, so drift misleads them faster than
  it misleads people.
- **instruction rot** — the specific drift of agent guidance: scaffolding
  written for last year's weaker models that today's models no longer need.
  Prune it on a schedule; staleness misleads worse than verbosity costs.
- **prose shrinks, guarantees grow** — my working summary of the long-term
  direction: how-to instructions keep shrinking as models improve, while
  floors, grants, checks, and audit trails keep growing as agents run
  longer.
- **model routing / effort** — matching the model (and how hard it thinks)
  to the work: cheap and fast for checkable clerk work, the frontier model
  for genuine judgment. Ask the agent itself to recommend model and effort —
  it's a skill worth learning from.
- **observability** — being able to see what agents did and why, from the
  records alone, after the fact. The prerequisite for supervising less; a
  view must never quietly *become* the thing that decides.
- **context measured in time** — where this seems to be heading: agents that
  stay with a problem for days or weeks. At that horizon the hard problems
  are authority, state, verification, and escalation — not prompt writing.

## The tools you'll hear me name

My own stack, so examples elsewhere in these docs parse. The ideas above
stand without any of them.

- **Workbench** — this repo: the family of small tools where my
  safety-critical decisions live in code.
- **Driver** — the pattern of the chat agent managing the work end-to-end,
  given tools instead of being wrapped in an app.
- **Ship** — runs and records agent executions (the dispatch engine).
- **Dossier** — durable project memory: projects, phases, tasks, decisions.
- **Gate** — the merge-authorization boundary: evidence in, one of
  pass/park/block/refuse out, everything recorded.
- **Triage** — deterministic risk-tier classification for PRs, with an
  escalate-only advisory on top.
- **Custody** — the credential broker.
- **Flare** — routes escalations and blocks to notifications (e.g. Slack).
- **Escalate** — the return path: ingests a human's approve/deny and feeds
  it back to Gate.
- **Console** — read-only web view of what's parked and what authority
  exists.
- **Tracelens** — diagnostics over agent run traces.
