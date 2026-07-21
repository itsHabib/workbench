# gate

A merge-decision procedure for pull requests: one small Go binary (standard
library only) that decides pass / park / block for a PR and records *why* as an
append-only, hash-chained artifact log. Autonomy is risk-scaled — clean
low-risk work is cleared to merge, clean high-risk work parks for a human,
dirty work escalates with the judge's question attached.

**What it does and does not force.** `gate` bounds its own sanctioned merge
verb — no live grant, no `gate`-driven merge — and gives every decision an
audit trail. It does **not**, by itself, prevent a merge: any identity holding
a merge-capable `gh` token can `gh pr merge` around it. The gate becomes
*enforcing* only when the target repo's branch protection requires the `gate`
status check (and token custody keeps the merge credential off the agents the
gate governs). See [docs/enforcement.md](docs/enforcement.md) for the honest
enforcement model — what forces merges through the gate, the named bypass, and
the operator precondition for going live.

gate is **enforceable via its canary status check**: the `gate` workflow
(`.github/workflows/gate.yml`, at the workbench module root) plus branch
protection makes a merge to `main` require the green check, closing the
direct-merge bypass on the repo that arms it — see the runbook in
[docs/enforcement.md](docs/enforcement.md). The workflow first shipped —
dormant, never armed — on the standalone itsHabib/gate repo; since the tenant
move it ships here, so the armable canary is itsHabib/workbench. The merge
itself stays dry-run advisory (`-live` is unbuilt) and token custody stays
open.

## Run it

```
go build -o gate.exe ./cmd/gate
export GATE_STATE=~/pers/gate/state                          # -state/-key default to $GATE_STATE/$GATE_KEY
./gate.exe grant -repo owner/repo -max-tier T2 -ttl 24h      # → grt_... (first ever mint into a fresh -state needs -init)
./gate.exe gate  -repo owner/repo -pr 181 -grant grt_...     # exit 0 pass / 1 block / 2 parked / 3 refused
./gate.exe next                                              # what needs you: parked runs + grant ledger
./gate.exe next -json                                        # the same projection as a machine feed
./gate.exe judge -run run_... -grant grt_... -decision pass -why "..."
./gate.exe judge -run run_... -grant grt_... -auto           # frontier model judges from artifacts alone
./gate.exe explain -run run_...                              # decision chain from state alone
./gate.exe audit                                             # replay the hash chain
./gate.exe backtest -repo owner/repo -prs 174,175,176
```

`-state` and `-key` default to `$GATE_STATE` and `$GATE_KEY`, so once those are
exported the whole verb surface drops its flag tail — and a stray `gate grant`
from the wrong directory can no longer mint into a fresh relative `state` tree.
An explicit flag still overrides the env.

`gate next` is the operator's inbox: it projects the log into what currently
needs a human — runs parked for judgment (each with a paste-ready `gate judge`
carrying the run's own grant id, so resolving a park is never an id hunt) and
the grant ledger (live grants soonest-to-expire first, plus grants expired in
the last day). It is read-only and sits outside the 0–3 decision codes: like
`explain` and `audit` it exits 0 or 4. The default projection collapses repeated
runs by PR from log evidence alone. Pass `-live` to additionally remove subjects
GitHub confirms are merged/closed; lookup failures remain visible as unknown.
Pass `-json` for the console feed.

Requires: `gh` authenticated; Ollama at `localhost:11434` with `qwen2.5:7b`
for the review-consolidation rung; the triage floor binary (`triage-floor` on
PATH or `-floor`); the `claude` CLI for `judge -auto`.

## How it decides

One `gate` invocation is a single pass:

1. **Capability** — no live grant, no gate. Grants are HMAC-signed artifacts,
   scoped (repo + action), timed (TTL), and capped (a ceiling risk tier they
   may clear). Expired, out-of-scope, or tampered grants refuse with a coded
   error before any evidence is gathered. This bounds the gate's *own*
   sanctioned merge path; it does not bound a merge performed directly with a
   `gh` token (see [docs/enforcement.md](docs/enforcement.md)).
2. **Evidence** — real reads (`gh pr view`, `gh pr diff`, both comment
   endpoints), each recorded as an artifact.
3. **Verification ladder** — three rungs, each a verdict artifact:
   - *readiness* (code): draft state, CI rollup, mergeability. Its blocks are
     final — no judgment can talk a red check green.
   - *floor* (code): the deterministic risk floor over the diff. Never blocks;
     it assigns the tier the grant ceiling is checked against.
   - *review consolidation* (local model): per-comment extract-don't-judge over
     the bot panel's findings. May pass or escalate, never block.
4. **Reduction** — monotone composition: worst decision wins, max tier wins,
   min confidence carries, unknown values fail closed.
5. **Outcome** — pass within the grant ceiling clears the merge and prints the
   exact `gh pr merge` command (`-live` execution is not wired yet; the dry run
   records `would_merge`); escalations park with the full question embedded; a
   later `judge` (operator or `-auto` frontier model) resolves the escalation
   from the recorded artifacts alone — and still cannot exceed the grant
   ceiling. Clearing a merge is a decision plus a printed command, not a
   forced merge: see [docs/enforcement.md](docs/enforcement.md).

`explain` reconstructs any run's full decision chain from the log; `audit`
replays the hash chain and names the first tampered artifact.

The contract behind this — artifact kinds, the verdict schema, the ladder
law — is specified in [docs/DESIGN.md](docs/DESIGN.md).

## Exit codes

| code | meaning |
|---|---|
| 0 | pass (`would_merge`; `-live` execution not yet wired) |
| 1 | blocked by a code verifier |
| 2 | parked for judgment (escalation or tier over grant ceiling) |
| 3 | capability refused (no live grant) |
| 4 | error |

Callers (a merge-tail skill, a driver engine, CI) branch on exit codes and the
JSON result on stdout — never on prose.
