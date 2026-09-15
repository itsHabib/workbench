# Terraform → real local Fleet experiment

Canonical source now lives in Workbench under `experiments/terraform-fleet/`.
Imported from the local `hack-fleet-terraform` repository at `cb100cb7`, plus
the subsequent results edit showing the actual Terraform code. The original
repository remains an archive for private Terraform state and operational logs;
those files and the retained agent labs were not moved into Workbench.
Historical evidence paths are intentionally unchanged. This remains a
machine-local prototype using pinned external checkouts, not a portable installer.

The subsequent [full GCP + Fleet + Rooms demo](cloud/RESULTS.md) passed its
fresh end-to-end audit. The original local-only trial below is retained as
history, including its failed audit.

Question: can a small HCL declaration prepare a useful Fleet without inventing
a language or teaching Terraform to be an agent scheduler?

This is a providerless baseline using Terraform's built-in `terraform_data`.
It reuses Workbench's real headless lab: supervisor, pooled author seat and
independent verifier, each in its own checkout. The workload implements a frozen
check-report contract, exercises supervisor interruption/recovery, and retains
exact-head verification evidence. It is not a generic Fleet deployment API yet.
The retained lab lives at `/Users/mh/dev/.codex-investigations/fleet-terraform-local`;
Fleet correctly refuses pooled checkouts nested inside another Git repository.

Trial result: real agents ran and independent tests passed, but the final audit
failed on verifier Git-head identity under the Codex sandbox. The lab is stopped;
see [EVIDENCE.md](EVIDENCE.md). This is a working setup POC, not a qualified
end-to-end runtime. Do not resume expecting that known setup issue to disappear.

## Run

Requires Terraform >=1.4, Python >=3.11, Go, Git, an authenticated Codex CLI and
the clean pinned Workbench source checkout named in main.tf. Uses existing model
authentication/usage, not cloud infrastructure. Credentials are not HCL inputs.

```sh
terraform init
terraform plan -out=local.tfplan
terraform apply local.tfplan
python3 fleet_lab.py cards
python3 fleet_lab.py run
python3 fleet_lab.py status
terraform plan -detailed-exitcode
```

Apply prepares real Fleet configuration; it does not launch an agent. Run starts
Fleet's own watcher for at most 20 minutes, including the deliberate interruption
fixture. `stop` requests cancellation and collects bridge exits; `resume` permits
one bounded continuation. Evidence remains under lab/result and lab/control.

Cards default to `file(...)` in main.tf. `card_overrides` can supply maintained
card text for one or more roles (or change the mapping to your own files).
A saved Terraform plan retains the planned card contents.
Card changes rerun the adapter against the existing lab only while stopped; they
do not restart workers. Runtime/source/backend changes require a separate lab.

## What this does NOT establish

`terraform_data` tracks inputs, not Fleet reality: a no-change plan is NOT a
drift check. Out-of-band deleted/edited seats will not be repaired automatically.
`terraform destroy` forgets the bootstrap record; it does not stop agents or
delete the lab. Explicitly stop first and retain evidence. Provisioner failure
may leave a partial lab, which this adapter refuses to adopt automatically.
Role count/names/workload are fixed by the reused three-role lab. Arbitrary
seats, import, live updates, remote Fleet and production reconciliation are absent.
The stopped-watcher check is a preflight, not a lock shared with concurrent
external starters; operate this disposable lab from one caller.

These are precisely the gaps a real Fleet provider would have to earn its keep
by solving: stable resource IDs, Read/drift, safe updates, import and removal
that does not erase work. A provider must not substitute its own state for Fleet
runtime evidence or rerun a task simply because Terraform refreshes a resource.

## Rooms and AWS

Optional `rooms` input accepts an EXISTING Lima host, Rooms binary, guest image
and toolstore. The existing lab verifier then tests the frozen patch in a cold
Room. Agents still run on the host. No host/image download or cloud launch is
performed by this POC.

AWS would be a second experiment: use the standard AWS provider for machines,
network and disks; feed host details to Fleet/Rooms setup. A custom Fleet provider
would manage Fleet configuration, not duplicate EC2. Neither AWS nor remote
execution is implemented by this baseline.
