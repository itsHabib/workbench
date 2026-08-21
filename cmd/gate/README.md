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
move it ships here, so the armable canary is itsHabib/workbench. Ordinary
`gate -live` stays unbuilt. A separate App executor owns one durable PR claim.
Fresh security review found the generic-Actions `gate-state` writer unsafe. The
revised repository code instead keeps App token creation, claim/result CAS, and
exact merge inside one Gate action. It uses a fresh Workbench-only ledger and
remains unarmed until exact-head review, operator bootstrap, live canaries, and
the operator-owned `GATE_EXECUTOR_ARMED` release switch.

Post-bootstrap decisions first use `gate executor prepare-request`. The
protected preparation job evaluates the exact head against the hosted ledger
and lets the App CAS-publish the audited action without calling the merge
endpoint. Exact-head passes can then be presented for a separate independent
approval with `gate executor request`. The default-branch executor verifies its
own protected-environment approval history, creates the App token only after
preflight, CAS-publishes and refetches one permanent claim, executes only the
stored command, then CAS-publishes one result. It never promotes commit status.
The path is installed but unarmed until the operator completes the runbook. See
[`../../docs/features/trusted-gate-judgment-bridge/design.md`](../../docs/features/trusted-gate-judgment-bridge/design.md).

## Run it

```
go build -o gate.exe ./cmd/gate
export GATE_STATE=~/dev/gate/state                           # -state/-key default to $GATE_STATE/$GATE_KEY
./gate.exe grant -repo owner/repo -max-tier T2 -ttl 24h      # → grt_... (first ever mint into a fresh -state needs -init)
./gate.exe gate  -repo owner/repo -pr 181 -grant grt_...     # exit 0 pass / 1 block / 2 parked / 3 refused
./gate.exe next                                              # what needs you: parked runs + grant ledger
./gate.exe next -json                                        # the same projection as a machine feed
./gate.exe preflight                                         # a whole sweep's inventory + every mint it needs, up front
./gate.exe preflight -repo owner/a -repo owner/b -deny owner/b#7
./gate.exe judge -run run_... -grant grt_... -decision pass -why "..."
./gate.exe judge -run run_... -grant grt_... -judgment judgment.json
./gate.exe judge -run run_... -grant grt_... -auto -provider codex
./gate.exe judge -run run_... -grant grt_... -auto -provider claude
./gate.exe resolve -escalation esc_... -grant grt_... -decision pass -why "..." -who NAME
./gate.exe executor prepare-request -repo owner/repo -pr 181 -head <sha> -grant grt_... -decision pass -why "..." -replay evt_... -out preparation.json
./gate.exe executor request -action act_... -repo owner/repo -pr 181 -head <sha> -question "..." -replay evt_... -out request.json
./gate.exe executor bootstrap -state DIR -key DIR -state-tip <sha> -action act_... -repo owner/repo -pr 181 -head <sha> -app-id N -installation-id N
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
The live reconcile is batched: one `gh pr list` per DISTINCT repo (not one
`gh pr view` per row), so its cost is O(repos), serving the parked, ready, and
needs-grant surfaces from one snapshot. Pass `-json` for the console feed.

Each parked row is labelled `cycles N/M` (`cycles_used` / `cycles_max` in JSON):
the review cycles the PR has consumed — this park's own run included, since a
content park is a ladder decision — against the `-max-cycles` ceiling of the
grant the run parked under, the same grant the judge line names (`∞` when
unbounded). Judging the park spends no new cycle; a fresh `gate gate` would be
cycle N+1, and once that exceeds M it refuses **pre-flight** — exit 3,
`grant_cycle_exceeded`, nothing gathered — rather than sweeping the PR and
parking. `gate explain -run` prints the same label on an escalation still
awaiting judgment. The label exists because a budget overrun used to surface only
at `gate judge`, the last step, after repeated `gate gate` runs had already spent
it.

Every recommended PR — parked or ready to merge — carries an **inventory check**
(`grant_coverage` in JSON) answering the question that used to be discovered only
at gate time: does a live merge grant actually cover this repo, and would its
tier or cycle ceiling park the next run before it reaches a decision. A row is
`absent` (never minted), `expired` (lapsed, so it's a re-mint), `ceiling` (a live
grant whose tier or `-max-cycles` would park), or `covered`. The first three
print the gap and a paste-ready `gate grant` under the row they belong to;
`covered` prints nothing, so the inbox stays a queue of things to do. The cycle
count mirrors gate's own rule — one counting outcome per distinct run,
authorization parks and capability refusals excluded — and the tier is the run's
composed verdict tier, so the ceilings compared are the ones gate would compare.

Like every `observe` projection it is **advisory**: `capability.Check` and the
ladder remain the only authority. A `covered` row can still refuse, and the
advisory count skips outcomes whose parent verdict it cannot follow rather than
failing the whole inbox — it may undercount a corrupt log, never overcount.

`gate preflight` is the same inventory question asked in BATCH, before a sweep
starts rather than one PR at a time. It walks every open PR in scope (`-repo`,
repeatable or comma-separated; with none, every repo the log already names),
minus a `-deny` list of repos or `owner/repo#N` PRs, groups them by repo, and
prints per repo: the branch-protection shape the merges land against (strict —
where a BEHIND PR costs a refresh, a CI re-run, and a fresh gate judgment — the
required contexts, conversation resolution), each PR's historical review-cycle
count, its `grant_coverage` row, and **one** `gate grant` wide enough to cover
that repo for the whole sweep. Every such command is then reprinted as a single
batch at the end. That block is the point: the operator mints once, up front,
instead of being interrupted per repo mid-sweep — the friction that left a clean,
green PR unmerged purely for want of a grant, and ceiling-parked PRs whose review
history had already spent the default `-max-cycles 3`.

The composed mint never narrows a ceiling a live grant already carries, and a
repo every PR of which is covered asks for nothing. **`preflight` PRINTS mint
commands and never runs one** — minting is operator-only, and like `next` it is
read-only, exiting 0 or 4. Both live reads are per repo and best-effort: one
`gh pr list`, and a protection read against the repository's **resolved default
branch** (`gh api repos/<repo>` then `.../branches/<default>/protection`) — never
an assumed `main`, since a 404 on a guessed branch name would report a strict
repo as unprotected. A repo that cannot be read keeps its row and records the
failure. A 404 from the protection endpoint is an *answer* (no protection); any
other failure, including an unresolvable default branch, stays an error, so a
repo is never reported unprotected because `gh` could not reach it.

An all-clear is claimed only over a **complete** inventory: a repo whose open PRs
could not be listed was never assessed, so it is reported as unread (`unread` in
JSON) and the tail prints the partial mint list rather than "every repo is
covered" — an all-clear over an unassessed repo would start the very sweep-stall
this verb exists to prevent. A saturated `gh pr list` page counts as *not listed*
for the same reason: a full page means the PRs past it were never seen, so it is
propagated as an incomplete read rather than returned as a short answer. That
also fixes the read for `next`, where an unseen-but-open PR would previously have
been reconciled as "not open" and had its row dropped.
The per-repo shapes verified by hand live in
[`../../docs/auto-mode-defaults.md`](../../docs/auto-mode-defaults.md); this verb
reads them live.

For profiling that live reconcile, `next` accepts three debug/experimental
flags — `-cpuprofile <path>`, `-blockprofile <path>`, `-trace <path>` — each
writing the corresponding `runtime/pprof` (or `runtime/trace`) artifact for the
run. They are off unless a path is given and touch nothing on the decision path.

`resolve` closes a park by its escalation id and stamps who resolved it. It is
the verb [`cmd/escalate`](../escalate) drives when a human's decision arrives
from the back-channel; escalate shells this binary and never imports it.

**Gate's one GitHub write.** On a pass, gate posts a `gate/authorized` commit
status carrying the deciding run id and the action artifact's chain hash, so
the PR page shows a verifiable pointer back into the audit chain. It is on by
default (`-stamp`, default true), fires only on exit 0, and is best-effort: a
failed post is a stderr warning, never a change to the decision. Every other
verb reads.

Two deliberate limits. The stamp is a commit *status*, never a review
approval — an approval would manufacture the review-decision signal gate reads
to judge readiness, which is the gate gaming itself. And it is forgeable by
anyone holding the same `gh` token, so it is a legible pointer to the
authorization, never the authorization: that stays the exit code plus the
hash-chained log.

Requires: `gh` authenticated; Ollama at `localhost:11434` with `qwen2.5:7b`
for the review-consolidation rung; the triage floor binary (`triage-floor` on
PATH or `-floor`). `judge -auto` has no implicit provider: it refuses unless
`-provider` is exactly `claude` or `codex`.

### Provider-neutral judgment

Gate has two built-in local CLI projections:

```text
claude -> claude -p --safe-mode --tools ""
codex  -> codex exec --ephemeral --sandbox read-only --skip-git-repo-check
          --ignore-user-config --ignore-rules --disable shell_tool
          --disable multi_agent -c forced_login_method="chatgpt"
          -c service_tier="flex"
          -c web_search="disabled" -
```

The caller selects the provider name, never an executable or argument vector.
Gate resolves that fixed CLI name to an absolute path, hashes the resolved file
into producer provenance, and runs it from a fresh temporary working directory.
It disables the agent's tools and customizations and gives the process a
small allowlist of runtime variables; Gate custody, GitHub credentials,
provider API keys, and arbitrary caller variables are not inherited. The CLI
uses its normal saved-login store. Gate sends one `gate-judgment-v1` request as
JSON on stdin and accepts one strict
`gate-judgment-v1` artifact on stdout. The request contains only recorded
state: run and escalation ids, the exact PR subject/head, the presented grant
id and tier ceiling, the recorded question, and the artifact context. The
provider echoes every binding and adds:

Codex 0.122 validates the merged base configuration before applying
`--ignore-user-config`. The fixed projection therefore overrides obsolete
`service_tier` values before startup. `flex` is an intentional
cost-conservative tier, not a neutral parser value: it may be slower or fail
when Flex capacity is unavailable. Gate treats that as
`judge_provider_failed` and does not append a judgment; it never falls back to
the higher-credit `fast` tier. `forced_login_method="chatgpt"` plus the
sanitized environment prevents this path from using API-key authentication.

```json
{
  "version": "gate-judgment-v1",
  "run": "run_...",
  "escalation_id": "esc_...",
  "subject": {"repo": "owner/repo", "number": 181, "head_sha": "<exact SHA>"},
  "grant": {"id": "grt_...", "max_tier": "T2"},
  "question": "<exact recorded question>",
  "producer": {"class": "judgment", "impl": "codex:gpt-5"},
  "decision": "pass",
  "tier": "T0",
  "confidence": 0.9,
  "why": "<reasoning>"
}
```

A Codex task may instead save that response and submit it with `-judgment`.
Unknown fields, wrong run/escalation/grant, stale head, a tier above the
presented ceiling, and a second judgment for the same escalation all refuse
before the log changes. A judgment that legitimately produces a newer ceiling
park may be followed by a judgment bound to that new escalation.
`confidence` is required and numeric (`0` is valid; omitted or `null` is not),
and `producer.impl` provenance is trimmed and must remain non-empty.
`producer` is the shared `contracts` struct every other artifact in the log
carries, not a bare string. Its `class` is the ladder rung, which a provider
does not get to assert: omit it and Gate stamps `judgment`, or echo `judgment`
exactly. Any other class is refused as `judgment_bad_producer_class` rather
than quietly rewritten. A judgment saved while Gate's decoder disagreed with
the contract and carried `producer` as a bare string is refused, not decoded
alongside the contract shape — a forgiving parser at an authority boundary is
how two shapes become permanent. The refusal names the fix: re-emit the
judgment, or wrap the old string as `impl`.
Judgment application is resumable across process interruption: if the
hash-chained judgment or its reduced verdict reached disk before the caller saw
success, the same run/escalation/grant retry reuses that artifact and appends
only the missing next stage. Once an outcome exists, the same retry is a
duplicate refusal.
If the judgment's original grant expires during that interruption, a new live
grant for the same repo/action may reauthorize the remaining reduction/action;
the immutable judgment retains its original grant parent and the outcome names
the replacement grant. Retry flags cannot change the persisted decision, and a
resolution stamp records the resumed verdict rather than the caller's repeated
flag.
Gate rechecks the live grant after a built-in provider returns and
immediately before appending its judgment, so a grant that expires during a
long provider call authorizes no state mutation.
A later `capability_refused` action remains audit history but does not complete
the persisted judgment chain; a replacement live grant may still append the
single authorized outcome.
The selected CLI provider, resolved wrapper filename, and SHA-256 digest are
prefixed into the stored producer provenance. The model implementation remains
provider-reported. PATH and the saved-login/config locations remain same-user
dependencies, so this local path is advisory automation under the same
operating-system identity as Gate—not independently custodied security
authority. Enforcement-grade judgment requires a separately controlled
executor identity.

## How it decides

One `gate` invocation is a single pass:

1. **Capability** — no live grant, no gate. Grants are HMAC-signed artifacts,
   scoped (repo + action), timed (TTL), and capped (a ceiling risk tier they
   may clear). Expired, out-of-scope, or tampered grants refuse with a coded
   error before any evidence is gathered. This bounds the gate's *own*
   sanctioned merge path; it does not bound a merge performed directly with a
   `gh` token (see [docs/enforcement.md](docs/enforcement.md)).
2. **Evidence** — real reads (`gh pr view`, `gh pr diff`, review submissions,
   requested reviewers, both comment endpoints, and the default branch's
   `.ship.json` declaration), each recorded as an artifact.
3. **Verification ladder** — four rungs on a green run, each a verdict
   artifact (a fifth, CI-failure classification over the failed runs' logs,
   is recorded only when the checks are red):
   - *readiness* (code): draft state, CI rollup, mergeability. Its blocks are
     final — no judgment can talk a red check green.
   - *floor* (code): the deterministic risk floor over the diff. Never blocks;
     it assigns the tier the grant ceiling is checked against.
   - *panel completeness* (code): the repository-required reviewer set against
     exact-head review submissions. Missing, pending, unknown, or stale-head
     evidence escalates; absence is never green.
   - *review consolidation* (local model): per-comment extract-don't-judge over
     the bot panel's findings. May pass or escalate, never block.
4. **Reduction** — monotone composition: worst decision wins, max tier wins,
   min confidence carries, unknown values fail closed.
5. **Outcome** — pass within the grant ceiling clears the merge and prints the
   exact `gh pr merge` command (`-live` execution is not wired yet; the dry run
   records `would_merge`); escalations park with the full question embedded; a
   later `judge` (operator, submitted artifact, or explicitly configured
   `-auto` provider) resolves the escalation
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
| 3 | refused (no live grant, or the PR is `already_merged` — a merged subject has no merge left to authorize, so it refuses before the ladder rather than parking on an unresolvable escalation; `backtest` is exempt, replaying merged history being its purpose) |
| 4 | error |

Callers (a merge-tail skill, a driver engine, CI) branch on exit codes and the
JSON result on stdout — never on prose.
