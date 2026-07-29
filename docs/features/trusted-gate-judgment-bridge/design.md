# Trusted Gate judgment bridge

Status: implementation

## Decision

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

This is the smallest honest bridge. The unfinished GitHub App mint design in
PR #143 remains the later authenticated grant/credential-authority track; it
does not replace this judgment promotion boundary.

### No-split argument

This change exceeds the normal 700 weighted-LOC target because the contract,
Gate exporter, trusted consumer, workflow shape, and adversarial refusal tests
form one authority boundary. Splitting before the consumer leaves an
unconsumed artifact; splitting before the producer leaves a workflow accepting
hand-authored input without the local newest-terminal proof. Either split also
creates a second PR trapped behind the same bootstrap deadlock. The added bulk
is schema/DTO plumbing and negative tests rather than another capability or
framework, so one focused T3 review is the smaller risk.

## Artifact

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

## Flow

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

## Refusal matrix

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

## Trusted workflow shape

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

## Bootstrap and rollback

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
