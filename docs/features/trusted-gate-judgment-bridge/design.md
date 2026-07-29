# Trusted Gate judgment bridge

Status: blocked — exact-head adversarial review rejected the status authority

## Exact-head adversarial verdict

PR #169 reached green CI at
`16179ef0c31c2de548fb7657e0bd47519313d690`, but the required fresh review
blocked the design on three P1 findings:

- current environment settings do not prove this run received an independent
  approval;
- export-time newest-terminal validation does not revoke an artifact after a
  newer local Gate block or park; and
- a GitHub commit status is SHA-scoped and can transfer to another PR sharing
  that SHA, while Gate's decision and merge argv are PR-scoped.

The first two findings need a run-bound approval receipt and a trusted
consumption-time freshness mechanism. They are insufficient on their own:
the third finding means the commit status cannot honestly be the final carrier
of PR-specific authority. The implementation below is retained for review
evidence but must not be merged or armed.

## Required replacement decision

The recommended replacement joins the unfinished Gate App-mint track with a
narrow PR-specific execute boundary:

1. A protected workflow running trusted default-branch code verifies its own
   GitHub approval history and an independent approving actor. The approval
   comment binds the repository, PR, head, protected base ref, evaluated base
   SHA/merge-base, canonical Gate evidence digest, and exact judgment question.
2. The approved job mints a short-lived, head-bound Gate grant under the
   authenticated App-mint contract, then re-evaluates the exact PR/head with
   Gate at consumption time. Clean/pass, escalate, and block use the existing
   `contracts.Verdict` and `ReviewPanelV1` evidence; `ReviewFindingsV1` remains
   address-work-only and is never fabricated for a clean review. A Gate-owned
   judge adapter binds the canonical evidence digest to the verified approval
   receipt; workflow prose never fabricates a verdict.
3. Gate appends the judgment and action to its anchored hosted state. Under the
   same per-repository serialization boundary it re-audits, requires the action
   to remain the newest terminal for the PR, and atomically appends a one-time
   `GateExecutionClaimV1`. Claimed actions can never be retried; failure requires
   a fresh Gate run. This append uses the workflow's separate state-writer
   token before any Gate App credential exists; a branch ruleset permits
   `github-actions[bot]` to update only `gate-state`, never `main`. A
   non-force compare-and-swap ref update must succeed, then a fresh fetch and
   anchored audit must prove the durable claim before credential release.
4. Only the claimed `would_merge` reaches the custodied executor process. That
   process alone receives the App private key, exchanges it internally for a
   short-lived installation token, and never returns, prints, persists, or
   exports the token to the workflow.
5. The executor executes the exact argv stored by the claimed action, including
   `--match-head-commit`; it never reconstructs flags, broadens the command,
   adds `--admin`, or mints an operator grant.
6. Layered branch rules make the identities mutually exclusive:
   - `main`: Restrict updates; the Gate App is the sole `Integration` with
     `bypass_mode=pull_request`;
   - `gate-state`: Restrict updates; `github-actions[bot]` is the sole writer;
   - every other base-repository branch: repository human roles and explicitly
     approved existing integrations may update, but the Gate App may not; and
   - a separate no-bypass `main` ruleset retains the app-pinned required
     `gate` check.
   A second PR may inherit a commit status, but ordinary users cannot update
   `main` and the Gate App accepts only its one claimed PR. Retargeting the
   claimed PR away from `main` also fails structurally because the Gate App has
   no update authority on any other branch.
7. The run appends the approval receipt, decision, claim, token identity,
   unchanged command, and GitHub merge result to the auditable Gate state
   channel.

This makes the App useful policy-bearing custody rather than a thin wrapper:
the invariant is one approved Gate action to one exact PR merge, with no
reusable commit-scoped green acting as sufficient authority. Repository-wide
executor serialization plus sole-App `main` updates keep the approved base SHA
stable from claim through merge; any unexpected base movement or retarget
refuses. Before arming, a disposable canary must prove the exact Gate argv
succeeds under the App installation token without `--admin`, ambient-user merge
fails even with green checks, the state writer cannot update `main`, and the
Gate App cannot update a non-`main` branch. Otherwise this architecture is
rejected. It is a material security and branch-rule choice, so implementation
waits for explicit operator authority.

## Rejected status-promotion prototype

The remaining sections describe the implementation rejected by the adversarial
review. They are retained only as evidence of what was tried and why it must not
be armed.

### Decision

The bridge uses a protected GitHub Environment named `gate-authorization` as
its trust root. `GateAuthorizationV1` is intentionally unsigned: the
environment's independent deployment approval authenticates the promotion.
Calling the JSON "signed" would be false because every local agent on this box
can read ordinary user files.

The environment is acceptable only with all of these settings:

- at least one required reviewer whose identity is not available through the
  governed task's ambient GitHub credential;
- prevent self-review enabled;
- administrator bypass disabled;
- deployment restricted to protected branches.

The trusted job verifies those settings through GitHub's environment API before
it writes a status. It also requires a GitHub Actions token identity, the
default-branch workflow ref, a live open PR at the artifact's exact head, and
exactly one open PR backed by that head. If the only possible approver is the
same `itsHabib` identity available to the task, this design is not armed and
must not be represented as secure.

This was the smallest attempted bridge. The exact-head review demonstrated that
it is not an honest final enforcement boundary. The unfinished GitHub App mint
design in PR #143 must be revised if the operator chooses the replacement above;
its existing non-goal for execution is no longer tenable.

### No-split argument

This change exceeds the normal 700 weighted-LOC target because the contract,
Gate exporter, trusted consumer, workflow shape, and adversarial refusal tests
form one authority boundary. Splitting before the consumer leaves an
unconsumed artifact; splitting before the producer leaves a workflow accepting
hand-authored input without the local newest-terminal proof. Either split also
creates a second PR trapped behind the same bootstrap deadlock. The added bulk
is schema/DTO plumbing and negative tests rather than another capability or
framework, so one focused T3 review is the smaller risk.

### Artifact

The shared leaf contract is:

- Go: `contracts/gateauthorization`
- schema: `contracts/gateauthorization/schema/gate-authorization-v1.json`
- schema major: `1`

It binds repository, PR, 40-character lowercase head SHA, Gate run, deciding
action ID and hash, `would_merge`, Gate's exact stored merge argv, issuance,
expiry, natural replay ID, and the protected-environment trust-root name.
`authorization_id` is `gau_<action-hash>`, so repeated export of one Gate
decision has one identity.

Gate's action body now stores both its display command and the original argv
array. Export copies that array from the audited state snapshot and refuses if
it differs from the action's command or the exact commit-pinned squash shape.
It never reconstructs a different command for execution, never mints a grant,
and never merges.

### Flow

After a fresh exact-head Gate pass:

```powershell
gate authorization export `
  -run run_... `
  -state "$HOME/pers/gate/state" `
  -out gate-authorization.json

gh workflow run gate-authorization.yml `
  -R itsHabib/workbench `
  --ref main `
  -F authorization=@gate-authorization.json
```

The dispatch creates a deployment waiting on the `gate-authorization`
environment. An independent required reviewer inspects the run parameters and
approves or rejects it. After approval, the workflow builds Gate from `main`,
passes the artifact to `gate authorization promote`, and posts the existing
required `gate` context only when every check passes. The merge remains the
exact command emitted by the original Gate run; the workflow never executes it.

### Refusal matrix

| Condition | Result |
|---|---|
| Valid, current, approved exact-head artifact | one app-authored `gate=success` |
| Unknown major, malformed JSON, bad IDs/times/trust root | fail job; post nothing |
| Expired or not-yet-valid artifact | fail job; post nothing |
| Closed PR, moved head, wrong repo/PR | fail job; post nothing |
| Zero or multiple open PRs on the head | fail job; post nothing |
| Superseded local Gate terminal at export | refuse export |
| Missing/weak environment protection | fail job; post nothing |
| Status not created by `github-actions[bot]` | fail job; branch protection's app pin rejects it |
| Changed flags, method, ordering, or head argv | refuse; post nothing |
| Same authorization already consumed | `authorization_duplicate`; post nothing |
| GitHub read/write or transport failure | fail job; post nothing |

Invalid deliveries deliberately do not overwrite an existing valid status.
They cannot create authority, and allowing arbitrary dispatchers to turn a
valid head red would add an unnecessary denial-of-service rail. A new PR head
has no inherited exact-head success; normal CI/Gate re-evaluation also
invalidates prior status as documented in `cmd/gate/docs/enforcement.md`.

### Trusted workflow shape

`.github/workflows/gate-authorization.yml`:

- has only `workflow_dispatch`;
- binds the job to the static `gate-authorization` environment;
- runs only when the workflow ref is the repository default branch;
- restricts environment deployments to protected branches;
- checks out the default branch explicitly with persisted credentials disabled;
- never checks out or executes PR/fork code;
- grants only `actions: read`, `contents: read`, `pull-requests: read`, and
  `statuses: write`;
- serializes all promotions for the repository, so untrusted fields cannot
  select a separate replay-concurrency group.

### Bootstrap and rollback

The bridge cannot authorize its own first merge: the trusted workflow is not on
`main`, while branch protection already requires the app-pinned `gate` context.
The implementation PR therefore stops after local/full CI, exact-head review,
and provider-neutral T3 Gate judgment. The operator must choose a one-time
bootstrap path; the agent does not use `--admin`, alter branch protection, or
invoke a prohibited reviewer.

After the PR is on `main`, configure the environment before dispatching an
artifact. Rollback is to reject/cancel pending deployments and revert the
bridge through a normal gated PR. The required `gate` context, admin
enforcement, and direct-push protection remain unchanged.
