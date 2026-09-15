# Local trial — 2026-09-14

## Identity

- Terraform v1.15.8, darwin_arm64; built-in terraform_data, no provider download.
- Workbench source: clean `3cea02ad796cd90eb772e57c9560249d0c34984e`.
- Source: `/Users/mh/dev/hack-fleet-terraform`.
- Runtime/evidence: `/Users/mh/dev/.codex-investigations/fleet-terraform-local`.
- Fixed lab task: frozen Workbench check-report implementation; host Codex workers.

## Observed result

1. `terraform init`, `terraform validate`, `terraform fmt -check`: passed.
2. First apply failed: Fleet refuses pooled checkouts inside another Git checkout.
   Partial setup retained under source repo `lab/`; no worker launched there.
3. Changed destination to the dedicated sibling lab; saved plan and apply passed.
   Actual pool/role commands created the author, supervisor and verifier bindings.
4. `python3 fleet_lab.py cards`: all three `projection_matches: true`.
5. `terraform plan -detailed-exitcode`: exit 0. Repeat apply while watcher running:
   0 added, 0 changed, 0 destroyed. No provisioner was executed.
6. Actual running-watcher card-update probe: refused before writing; original
   author card bytes unchanged.
7. Nine adapter unit tests passed, including planned-card preparation, stopped
   update, running/uncollected-exit refusal, partial-lab refusal, runtime-change
   refusal, projection-failure checkpoint behavior and redirected-card refusal.
8. Live Fleet run launched all three roles, interrupted the supervisor after the
   dirty draft appeared, and resumed through Fleet without task facts. It took
   approximately 15.6 minutes, with seven provider attempts. Both implementation
   and independent verification/pass receipts name
   `181a08466c7157764a38ccce1bbab8859e8abdba`. The verifier ran all eight supplied
   tests plus separate semantic/retry/conflict checks successfully.
9. Final artifact audit **FAILED** solely on `verifier_same_head`. The Codex
   sandbox refused writes to the verifier clone's administrative .git directory.
   The verifier used separate temporary Git metadata for the actual fetched head
   and clean-tree receipt; ordinary `git rev-parse HEAD` in the original checkout
   still names the seed. Do not equate that receipt with a full lab pass.
   All other audit checks passed. Audit and assessment remain unchanged under
   `result/audit.json` and `result/ASSESSMENT.md`.
10. Cleanup: watcher stopped; all bridge exits collected; all providers terminal;
    all three addresses paused. General descendant-process quiescence is not proven.
11. Saved a Terraform plan containing one author-card addition. Applied it AFTER
    shutdown: card reprojected in the same lab, no seat recreation, no new attempts
    (still seven). Restored the original card through another apply. Terraform
    labels both changes replacement of its bootstrap record, not an in-place
    Fleet update: another reason not to mistake this wrapper for a provider.

## Interpretation

HCL through existing Terraform can bootstrap a real Fleet without a new parser
or provider. This wrapper is not a managed Fleet resource: Terraform's standard
“infrastructure matches” output only covers its stored input in this experiment.
It does not prove live drift detection, removal, import or full workload success.

## Next useful experiment

First fix and independently rerun the headless lab's verifier Git layout under
Codex; do not weaken the audit or retrofit a passing head into this retained run.
Then test a minimal real Fleet Terraform resource with live Read, honest card
updates and preservation-aware removal. Standard AWS resources should own cloud
machines/network/storage; Fleet should continue to own work/mail/recovery.
Do not build another general planner or configuration language.

Rooms input is wired to the existing headless lab adapter but not exercised.
The local Lima rooms-host was stopped at inventory. No AWS operations or new
cloud resources were requested. The earlier typed-Go proposal was superseded,
not validated by this trial.
