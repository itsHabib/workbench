# tracelens — pitch

## Problem
Agents fail quietly and expensively. A run doesn't crash — it *spins*: it
re-searches the same query, re-applies the same no-op edit, hammers a 503 until
the budget is gone, or churns for twenty steps without learning anything new.
The trace is right there in your logs, but nobody reads 200 lines of JSONL to
find the four steps that mattered. You just see the bill and the timeout.

## Who it's for
Engineers running agent loops in production or CI who need a fast, offline
answer to "what went wrong in run #4471, and what do I change?" — without
shipping their traces to a SaaS or standing up a dashboard.

## Agentic angle
tracelens is an **observability agent over a trajectory**. The interesting code
is not parsing — it's the detector pipeline and the progress model:

- A **tandem-repeat scan** over normalized `(tool, args)` signatures finds
  loops of any period (`A A A`, `A B A B A B`), picking the strongest repeat.
- A **progress model** replays the run building a set of seen state signatures
  and flags a *trailing stall* — steps that only revisit known states. This is
  genuinely distinct from loop detection: it catches an agent trying three
  different queries that all return the same nothing.
- A **retry/redundancy/cost** trio attributes wasted dollars, kept disjoint so
  the numbers don't double-count.
- A **policy layer** folds findings into a health verdict and, per finding,
  emits an evidence-filled repair ("memoize `web_search` by (tool,args)…").

Detectors are single-responsibility and swappable; adding a pathology is adding
one `Detector`. That composition *is* the mechanism a reviewer can see working.

## Why it could graduate
Every agent framework needs a post-run "why did this cost $2 and loop 40 times"
verdict, and none of the good ones want to be a hosted service. A single Go
binary that eats a JSONL trace and prints ranked, actionable findings (plus a
`-json` mode to gate CI) is a tool I'd wire into every driver run I own. The
loop/stuck/retry logic is the reusable core; dialect adapters grow around it.

## Honest limits
- Neutral JSONL plus Ship Cursor, Claude, and Codex dialects are supported;
  other producers need concrete adapters.
- Semantic loop matching deliberately normalizes only explicit volatile
  producer metadata; broader equivalence needs corpus evidence.
- "Wasted $" counts redundant successful recompute + retry-storm failures; it
  doesn't try to price the diagnostic value of a failing test in a loop, so
  it's a lower bound, not a full accounting.
- Repair suggestions are deterministic, evidence-filled templates, not
  LLM-authored prose.
