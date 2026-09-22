# Freedom within phases, evidence at handoffs

Status: local Fleet POC, 2026-09-14. Source:
[Relay PR #1](https://github.com/itsHabib/relay/pull/1),
[brief at abfae4f](https://github.com/itsHabib/relay/blob/abfae4f5c54d65fda92437137c86ca4ff6de98dc/briefs/2026-09-14-sdlc-as-pipeline.md).

## Recommendation

Use the existing tools to compose a delivery process. A first-class pipeline
object is not needed for this slice. The useful addition is a consumer checking
the evidence it demands when work is handed over, and retaining that decision.
Keep the choice of methods, tools and iteration inside a phase in task prose.
Do not require five ceremonies for every change.

For example, a lead can declare verification work today even when implementation
has no passing evidence. `fleet done` can answer a separate evidence question,
but nothing connects that answer to dispatch. This POC lets the receiver demand
an implementation receipt in the dispatch operation itself. Missing, failed,
malformed or wrong-revision evidence refuses the handoff before a work row is
published. A successful handoff retains the exact observations it consumed.

This is an **opt-in check on local dispatch**, not a compulsory portfolio policy.
Ordinary dispatch remains available. The fields do not authenticate the caller,
prevent arbitrary tool use, establish independent verification, or authorize
release. Making a requirement unavoidable belongs at the actual receiving
capability, with requirements it controls rather than ones the producer chooses.

## What the current primitives actually provide

The Workbench references below were inspected at `1c0ba652dfc62ae4687d431581d96ece59b314cb`.

| Concern | Existing owner and evidence | Consequence |
|---|---|---|
| Work and responsibility | [Fleet work](../../../cmd/fleet/internal/verbs/work.go): repository + branch + relationship; state is derived | Reuse the row; no new task store or phase enum |
| Observations | [Fleet receipts](../../../cmd/fleet/internal/verbs/receipts.go): live session, clean actual checkout, full head, kind, verdict, observable, provenance; history joins SHA spellings | Reuse the receipt format and history |
| Context for the next agent | [Fleet handoffs](../../../cmd/fleet/README.md): authored context, replaceable | Useful context, insufficient as admission evidence |
| Execution history | [Driver contract](../../../contracts/driverstate/driverstate.go), [ledger](../../../driverstate/doc.go) | Execution events and a hash chain do not certify discovery or design |
| Driver integration | [Ship emitter at 804c27e](https://github.com/itsHabib/ship/blob/804c27e1df4a7373b11074fed21116a3f43f89ff/packages/driver/src/driverstate-emit.ts) | Emission is best effort; its failure does not block a tick, so it is not an admission boundary |
| Project memory | [Dossier protocol at 88fb307](https://github.com/itsHabib/dossier/blob/88fb307a7349ec8144212ebbc5d61d97c3101b5c/PROTOCOL.md) | Phases are ordered project subdivisions; artifacts are pointers, not certified transitions |
| Release | [Gate design](../../../cmd/gate/docs/DESIGN.md) | Keep exact-head verdicts, operator-minted grants and the emitted merge command in Gate |

The brief's `org-work` map and combined driver-state sequence are historical
orientation, not today's API. Fleet now owns the relevant work/mail/handoff
operations. Driver-state statuses include `pending`, `dispatched`, `landed`,
`pr_open`, `merged`, `failed`, `skipped`; they are not the proposed five SDLC phases.
Gate's advisory mode and shell hooks also do not isolate arbitrary shell effects.

## Where the phase boundaries belong

These are useful questions for the receiving phase, not new mandatory records.

| Handoff | What the receiving phase needs | Owner |
|---|---|---|
| Discovery → design | Sources and examples, the problem, relevant rules, unresolved uncertainty | Task/spec author; Dossier can link sources |
| Design → implementation | Intended behavior, tradeoffs, meaningful verification plan and escalation decisions proportionate to the work | Receiving implementer and repository instructions |
| Implementation → verification | Exact repository/revision, what changed, reproduction, observed results and remaining uncertainty | Fleet receipt + receiving dispatch; implemented here |
| Verification → release | Evidence for the current head, findings and justified residuals, required checks and review coverage | Existing verifiers/review artifacts, then Gate |
| Release → recorded outcome | Exact merge result and authorized action joined to the task | GitHub/Gate receipt; Dossier points to it |

The unit in this POC is one existing Fleet work row, addressed by repository,
branch and receiving relationship. The evidence subject is repository + full
commit + receipt kind. Discovery and design can live at task or project scope
before a branch exists; forcing them into this commit-shaped receipt would be a
mistake. A design shared by several PRs remains one referenced design.

Verification can send work back to implementation without a stored back-edge.
A changed head needs fresh implementation evidence; prior receipts remain in
history. A design need not be invalidated by every repair: reconsider it when
the requirements, interfaces or decisions it covers change. That is a semantic
judgment, not something a commit hash proves. A base/environment-sensitive check
also needs its producer to identify those inputs; this POC only binds the commit.

Receipt age alone is not a universal expiry rule. Discovery about a changing API
may need refreshing tomorrow; a deterministic observation of an immutable tree
does not become false after a week. Define freshness around the claim's inputs.
Risk determines which evidence is demanded, using existing repository policy and
triage, rather than imposing discovery/design receipts on a one-line repair.

## Try the implemented boundary

From the module root, with an existing live Fleet session in a clean checkout:

```sh
go build -o /tmp/fleet-handoff-poc ./cmd/fleet
revision=$(git rev-parse HEAD)
/tmp/fleet-handoff-poc receipt "$revision" implementation pass \
  "Regression fails before the fix; focused tests pass after it; see the PR for commands"
/tmp/fleet-handoff-poc dispatch codex/my-change --as verification --for lead:my-repo \
  --requires implementation --head "$revision" \
  --brief "Reproduce the regression and challenge the edge cases"
```

Use a truthful observable from checks actually run. `--requires` is a comma-separated
set of existing receipt kinds; there is no built-in meaning for `implementation`
or `verification`. MCP's `fleet_dispatch` exposes the same `requires` and `head`
fields and calls the same implementation. The ordinary `--repo` selector works.

For a completely offline reproduction that uses temporary Git repositories and
a test session fixture, run:

```sh
python3 docs/features/sdlc-handoff/demo.py
```

The demo builds Fleet, runs real checks against a tiny conversion function,
records their results, and exercises refusal → repair → dispatch → later failure
→ new revision → fresh receipt. It prints its temporary directory so the work
row, receipt history and admission journal can be inspected. GitHub and watcher
activity are disabled; no agent, model, cloud worker or global configuration is
started or changed. The fixture is not a live independent reviewer trial.

## What admission checks and records

1. The caller supplies a full 40-character SHA and at least one receipt kind.
2. Fleet resolves the branch using its existing local-ref rules: `origin/branch`
   when present, otherwise the local branch. That revision must equal the demand.
   This is **not a fresh GitHub read**; fetch first when remote currency matters.
3. Under the same lock as receipt writes, Fleet reads each canonical history and
   selects the last appended observation for the exact repository/head/kind.
   A later failure overrides a pass even if its timestamp is earlier or the
   producer spelled the SHA differently. Missing history, torn lines, malformed
   observations, absent provenance or a non-passing latest observation refuse.
   An old latest-only store needs a new ordinary receipt to seed its history.
4. Fleet appends a `dispatch_admission` event to the existing `actions.jsonl`,
   with the demand, result/reason and consumed receipt snapshots. Failure to write
   this record prevents publication. Refusal preserves an existing work row.
5. On satisfaction, the published work row embeds the same `admission` snapshot
   alongside `head_at_dispatch`. A later receipt cannot rewrite that packet.

The admission journal result says `satisfied`, not `dispatched`: a process can
stop after this append and before publishing the row. The row is the evidence
that dispatch happened. A refused attempt is also recorded once the target and
entry demand are valid; argument/target-resolution failures remain CLI errors.
Exit 0 means work was declared, 1 means refusal, and an I/O error exits 4.

Receipt history and this journal are ordinary same-user local files. They are
inspectable and append-only through these verbs, not signed or tamper-proof.
The producer's observable remains a claim by that session. Even a hash chain
would only protect recorded bytes under an appropriate custody model, not make
the claim true. Gate remains the consequential release authority.

## Deliberate limits and next evidence

This first slice rejects combining receipt admission with `--slot`. Slot
placement fetches/checks out a moving branch and can launch a worker; it needs
revision checking at placement and execution entry before claiming the same
handoff guarantee. A row without a slot does not start a worker. A branch can
also move after a local admission: the snapshot remains about the recorded head,
and a worker must check its actual checkout before acting or recording evidence.
Guarded dispatch also keeps its row local: today's GitHub ownership mirror does
not carry the admission packet. Ordinary dispatch keeps its existing mirroring.

No new pipeline schema, graph, scheduler, approval phrase, Gate policy or
driver-state event was necessary. The substantive new behavior is the check
inside dispatch and its required audit write. Requirements are supplied for this
handoff; this POC neither infers them nor prevents a caller from choosing ordinary
dispatch instead. It is suitable for trying the contract, not claiming an
unavoidable security boundary across all agent tools.

Next useful trial: one real implementer/verifier handoff with a concrete claim,
first at a stale head and then at the current one. Measure whether the refusal
saved a wasted or misleading verification run. If that earns automatic placement,
extend the existing placement/entry path with the revision check. Add pre-code
receipt types only when a real consumer can state what breaks without them.

## Validation

```sh
go test -race ./cmd/fleet/...
gofmt -l .
go vet ./...
golangci-lint run ./...
go test ./...
```

The new behavior tests use real temporary Git checkouts and the ordinary receipt
writer and CLI dispatch path. They exercise missing/malformed/foreign/stale
evidence, later failures across SHA spellings, all-required-kind checks, audit
write failure, refusal preserving work, repair, and malformed CLI/MCP inputs.
Existing Fleet checks cover live-session and clean-tree recording requirements.
