# Token-optimization hackathon — 5 entries, independent sessions, operator judges

Topic: **get monthly agent token spend from ~5B toward ~3B.** Five
self-contained briefs. Each goes to a FRESH session that has never seen the
others — do not share context between entries. Every entry ends in a runnable
demo the operator can judge in under five minutes, hands-free.

Origin: a review of the `babel-protocol` experiment
(https://github.com/itsHabib/babel-protocol) found its 88%-compression
headline was an artifact of counting whitespace tokens instead of tokenizer
tokens — real-BPE remeasurement showed plain YAML beating Babel with zero spec
overhead, and its own A/B test showed only 4% total-API-token savings because
payloads are a thin slice of spend. So this round attacks the whole spend
portfolio; Babel-style compression is one entry among five.

## Entries

| # | slug | bet |
|---|------|-----|
| 1 | `hack3-spend-audit` | You can't cut what you can't see — a dollars-first breakdown of where 5B tokens actually go |
| 2 | `hack3-babel-bpe` | Rerun the Babel bake-off in the right unit (real BPE tokens) against honest baselines; winner becomes the house wire format |
| 3 | `hack3-context-diet` | Tool results are the fattest slice — mechanical hygiene rules recoup tokens with zero behavior change |
| 4 | `hack3-offload-router` | Mechanical sub-steps don't need frontier tokens — a deterministic quality gate makes local-model offload trustworthy |
| 5 | `hack3-cache-max` | Cache misses are silent dollars — find the prefix-busting events and price what stability would save |

Launch one per session with:

> Read ~/dev/workbench/docs/hackathon-token-opt/entry-<slug>.md and build it.
> You are one of 5 independent entries; you win by demo, not by design.

## House rules (same for every entry — fairness is the point)

- **One session, one repo.** Scaffold at `~/dev/<slug>`. Go stdlib, plain
  Node, or Python stdlib; dashboards are a single static HTML file (vanilla
  JS/CSS); NO build steps, NO frameworks, NO new deps unless unavoidable.
  (A Python venv for `tiktoken` counts as unavoidable — see below.)
- **Keyless and local.** No API keys exist on this machine and none may be
  added. What IS here:
  - **Session transcripts:** `~/.claude/projects/**/*.jsonl` — 172 sessions,
    114MB, all within the last 30 days. Each assistant message carries
    `message.usage` with `input_tokens`, `cache_creation_input_tokens`,
    `cache_read_input_tokens`, `output_tokens`, and a `cache_creation` object
    splitting ephemeral 1h vs 5m tokens. Tool results appear as
    `tool_result` content entries. This corpus is ground truth for every
    measurement entry.
  - **Local model:** ollama serving `qwen2.5:7b` (`ollama run qwen2.5:7b`).
    If an idea "needs" a better model, the demo must work with this one (or
    with no model) and merely note the upgrade path.
  - **Tokenizer:** `tiktoken` installed into a project-local venv
    (`python3 -m venv .venv && .venv/bin/pip install tiktoken`). Use
    `o200k_base` and state in the README that it is a proxy for the actual
    Claude tokenizer, not the real thing.
- **Known machine breakage:** system `pip3 install` is PEP-668-blocked —
  always use a venv. Don't name a shell function or script `local` (zsh
  builtin collision).
- **Privacy:** transcripts contain private work data. Everything stays on
  this machine — no publishing, no artifacts pages, no uploads. Fixture data
  bundled into repos must be synthesized or scrubbed.
- **Dollar math:** keep $/MTok prices in ONE editable table in your repo,
  values marked TODO for the operator to fill from
  https://docs.claude.com/en/docs/about-claude/pricing — do not hardcode
  prices from model memory. Cache reads, cache writes, fresh input, and
  output are all priced differently; the table must have a column for each.
- **Correctness is computed, never model-judged.** Any grading, matching,
  gating, or budget lives in deterministic, table-tested code. The model (if
  used at all) is phrasing — or, in entry 2's case, the *subject under test*,
  never the grader. House invariant, every entry.
- **No spec, no design doc.** README + working demo + tests on the policy
  layer only. Simplify until it hurts.
- **Required at the finish line:** (a) README with one command to run, (b) a
  scripted/canned demo mode that needs no live input so the judge runs it
  hands-free, (c) `DEMO.md` — the exact 60-second walkthrough, (d) local
  green: build/vet/tests pass.

## Judging (operator, after all 5 land)

100 points:

- **30 — the 60-second demo.** Does a person watching get it, and does it
  land? Canned mode counts; a live moment that works is a bonus.
- **25 — would someone pay.** Named buyer, evidence money already moves in
  the space. Assert it in DEMO.md; the judge will push back.
- **20 — deterministic share.** How much of the correctness path is real code
  with tests vs model vibes. Show the test file.
- **15 — token-necessity.** If the entry's savings would be matched by the
  obvious alternative — flipping the model picker to a cheaper tier and
  moving on — it loses these points. Name that alternative in your README
  and beat it with numbers.
- **10 — restraint.** Small LOC, no seams for futures that don't exist,
  deleted requirements > built mechanisms.

Tie-breaker: which repo would the operator actually open again next week.

## After judging

Winner gets the follow-through: operator's call — a deeper brief, a live
session against the real 5B/month spend, or promotion into the workbench
(e.g. entry 2's winning format becoming the standard subagent report format,
or entry 1's dashboard becoming a standing `/health`-style board). Losers
keep their repos as reference — nothing gets ported into a platform.
