# Gate executor operator runbook

Status: repository activation in progress. The App, protected environment,
layered rulesets, `gate-state` ref, replacement App key, and Workbench-only
signing secrets exist. The executor is deliberately disarmed while the
post-bootstrap preparation seam is reviewed and canaried.

This runbook deliberately uses a fresh Workbench-only Gate ledger. It never
copies, uploads, rewrites, or re-anchors the operator's machine-global Gate
state.

## 1. Know what is hosted

There is no hosted server or mounted disk. The durable hosted store is the
protected Git branch named `gate-state`:

- `gate-state:state/log.jsonl`
- `gate-state:anchor.json`

The two signing keys are not committed to that branch. After bootstrap they
live only as protected `gate-authorization` Environment secrets. A temporary
local copy is created under a dedicated Workbench path so the operator can
mint the first grant and produce the exact bootstrap action:

```powershell
$hostedRoot = Join-Path $env:USERPROFILE ".workbench\gate-hosted\workbench"
$hostedState = Join-Path $hostedRoot "state"
$hostedKeys = Join-Path $hostedRoot "keys"
```

Do not substitute `%APPDATA%\gate`, `~/pers/gate`, `$env:GATE_STATE`, or
`$env:GATE_KEY`. `gate executor bootstrap` requires explicit `-state` and
`-key` flags so ambient defaults cannot select the ordinary local ledger by
accident.

## 2. Re-verify the exact activation head

Before any key or grant work:

1. Confirm the implementation PR is open, ready, CI-green, and formally
   reviewed at its exact current head.
2. Build Gate from that exact head, not from an older checkout or an
   unreviewed branch.
3. Confirm the App installation remains repository-only with only
   `contents: write` plus mandatory metadata access.
4. Confirm `gate-authorization` accepts only `main`, requires the independent
   reviewer, prevents self-review, and disables administrator bypass.
5. Confirm all five reviewed ruleset layers remain active.

If any identity, head, permission, reviewer, environment, or ruleset differs,
stop. Never compensate with `--admin`, a dismissed check, a force-push, or a
weaker rule.

## 3. Operator-only fresh ledger and grant

Grant minting remains operator authority. The agent must stop and give the
operator this exact request:

```powershell
$hostedRoot = Join-Path $env:USERPROFILE ".workbench\gate-hosted\workbench"
$hostedState = Join-Path $hostedRoot "state"
$hostedKeys = Join-Path $hostedRoot "keys"

gate grant -repo itsHabib/workbench -max-tier T3 -ttl 24h -init `
  -state $hostedState -key $hostedKeys
```

The operator returns the `grt_...` value. The agent may then run Gate against
the same explicit paths:

```powershell
gate gate -repo itsHabib/workbench -pr <PR> -grant <GRANT> `
  -state $hostedState -key $hostedKeys
```

If Gate parks, resolve the recorded escalation through the normal judge
contract. No provider is implicit; a submitted exact-head judgment or an
operator decision is required. Re-run Gate after a pass. Do not reconstruct a
merge command.

The passing run records one newest `would_merge` action. Read its `act_...`
identity and exact command from:

```powershell
gate next -json -state $hostedState -key $hostedKeys
```

## 4. One-time bootstrap

Bootstrap has one narrow job: publish that fresh dedicated ledger to the
current `gate-state` tip, then byte-validate the exact stored
`gh pr merge ... --match-head-commit ...` intent and perform its commit-pinned
squash through GitHub's merge API as the Gate App. It does not invent an
approval receipt for a workflow that is not yet present on `main`, invoke
`gh pr merge`, or use `--admin`.

Capture the exact current state tip and load the existing bootstrap App key
without printing it:

```powershell
$stateTip = gh api repos/itsHabib/workbench/git/ref/heads/gate-state --jq .object.sha
$env:INPUT_APP_PRIVATE_KEY = Get-Content -Raw `
  "C:\Users\MichaelHabib\Downloads\itshabib-workbench-gate-executor.2026-07-29.private-key.pem"
```

Run the exact reviewed binary:

```powershell
gate executor bootstrap `
  -state $hostedState -key $hostedKeys `
  -state-tip $stateTip -action <ACT_ID> `
  -repo itsHabib/workbench -pr <PR> -head <EXACT_HEAD_SHA> `
  -app-id 4431951 -installation-id 149997077
```

Then remove the process copy:

```powershell
Remove-Item Env:\INPUT_APP_PRIVATE_KEY
```

Bootstrap refuses before App-token creation when the state/key flags are
implicit, the local ledger is empty or invalid, either state file exceeds the
transport limit, the ledger contains a structured repository identity other
than `itsHabib/workbench`, the remote state tip moved, the PR subject changed,
the action is malformed or superseded, or its stored command is not the exact
nine-element commit-pinned merge intent. State publication is a non-force
compare-and-swap. The exact merge still fails closed if the PR head moves after
preflight.

If bootstrap publishes state but does not confirm the merge, stop and inspect
the returned error and remote PR/state facts. Do not force the ref or execute a
hand-built merge command.

## 5. Retire the bootstrap App key

After the bootstrap merge is confirmed:

1. Create a replacement private key for the same GitHub App.
2. Replace the protected Environment secret `GATE_APP_PRIVATE_KEY` with the
   replacement key.
3. Delete the bootstrap key from the App settings.
4. Securely delete the downloaded bootstrap PEM.

This leaves no reusable App key in the local task after activation. Never put a
PEM in a command argument, log, issue, PR, repository file, or artifact.

## 6. Install the dedicated signing keys

Upload only the fresh Workbench key pair, through standard input:

```powershell
[Convert]::ToBase64String(
  [IO.File]::ReadAllBytes((Join-Path $hostedKeys "grant.key"))
) | gh secret set GATE_GRANT_KEY_B64 -R itsHabib/workbench `
  --env gate-authorization

[Convert]::ToBase64String(
  [IO.File]::ReadAllBytes((Join-Path $hostedKeys "anchor.key"))
) | gh secret set GATE_ANCHOR_KEY_B64 -R itsHabib/workbench `
  --env gate-authorization
```

Do not display either base64 value. Keep the local dedicated state and keys
until positive and negative canaries finish; then move the keys to the
operator's approved recovery custody or remove them.

## 7. Arm and canary

The final release switch is operator-owned:

```powershell
gh variable set GATE_EXECUTOR_ARMED -R itsHabib/workbench --body true
```

Set it only after the bootstrap head is on `main`, the replacement App key and
dedicated signing secrets are present, and all ruleset/environment checks in
section 2 still pass.

### Prepare every post-bootstrap action

Never reuse bootstrap. Generate an exact preparation request using an
operator-minted grant already present in hosted state:

```powershell
$replay = "evt_" + [Convert]::ToHexString(
  [Security.Cryptography.RandomNumberGenerator]::GetBytes(16)
).ToLowerInvariant()
$preparation = Join-Path $env:TEMP "gate-preparation.json"

gate executor prepare-request `
  -repo itsHabib/workbench -pr <PR> -head <EXACT_HEAD_SHA> `
  -grant <GRANT> -decision pass -why "<EXACT_JUDGMENT>" `
  -replay $replay -out $preparation

@{
  operation = "prepare"
  preparation = Get-Content -Raw $preparation
} | ConvertTo-Json -Compress |
  gh workflow run gate-executor.yml -R itsHabib/workbench --ref main --json
```

The command prints the exact approval comment. The independent reviewer pastes
that complete comment into the protected Environment approval. A successful
preparation publishes the resulting action but cannot merge.

Then generate `gate executor request` from the published action and dispatch
`operation=execute`. This is a second protected approval: preparation authority
and merge authority are deliberately separate.

The first live canary must begin with a red required `gate` check and prove:

- one independently approved, exact-head request creates a durable claim,
  refetches it, validates the stored commit-pinned intent, records one result,
  and merges that exact head;
- stale head, retarget, malformed artifact, duplicate request, replay, wrong
  approver, and self-approval refuse without merging;
- ambient user and generic Actions updates to `main` and `gate-state` refuse;
- the Gate App cannot update an ordinary branch; and
- claim/result, approver actor ID, argv, PR head/base, and merge commit agree.

Recovery accepts only one expired claim identity:

```powershell
gh workflow run gate-executor.yml -R itsHabib/workbench --ref main `
  -f operation=reconcile -f claim=gxc_<64-lowercase-hex>
```

The reconciler has no merge input or merge call. Its one-App token remains
technically merge-capable because GitHub uses the same `contents: write`
permission for state-ref updates and PR merge; the safety boundary is the
reviewed claim-only process plus branch rules, not a fictional branch-scoped
token.

## 8. Rollback

First disarm new executions:

```powershell
gh variable set GATE_EXECUTOR_ARMED -R itsHabib/workbench --body false
```

Do not weaken rulesets or force/reset `gate-state`. Preserve the ledger, close
any expired claim through the reviewed reconciler, rotate the App key if
custody is in doubt, and fix forward through a reviewed PR.
