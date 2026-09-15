# Terraform + Fleet + Rooms + one GCP VM

One deliberately bounded, machine-local demo. Operator approved a $50 total GCP
cap. No model credentials go to the VM. No production project is modified.

Terraform's Google provider owns a dedicated network, subnet, /32 SSH firewall
and n2-standard-8 Ubuntu VM in rooms-lab-20260914/us-east1-b. Nested KVM is enabled,
there is no service account, and GCP automatically DELETES the VM after 3 hours.
The 100GB boot disk auto-deletes. One such short run should be comfortably below
$5 (estimate, not a billing receipt); do not treat budget alerts as a kill switch.
Destroy the owned resources explicitly after collecting results.

The remaining Terraform graph is two bootstrap records:

1. `rooms_ready`: ship pinned Rooms revision, run its tests, build Firecracker
   guest image and Python toolstore, and run doctor on the GCP host.
2. `fleet`: prepare the three local Fleet roles and their cloud Rooms adapter.

`fleet_demo.py run` then starts Fleet's own watcher. The author implements the
frozen report task; the verifier tests its exact commit locally AND sends its
exact patch to a cold Firecracker Room on GCP. The supervisor requires both
receipts. The outer audit checks patch identity, independent receipt provenance,
interruption/recovery, guest success, collection and cleanup.

## Run on this machine

```sh
python3 cloud.py tf init -input=false
python3 cloud.py tf plan -out=cloud.tfplan
python3 cloud.py tf apply cloud.tfplan
python3 fleet_demo.py run
python3 fleet_demo.py status
python3 cloud.py tf plan -detailed-exitcode
# Export evidence before teardown. Stop Fleet first if the run did not finish.
python3 fleet_demo.py stop
python3 cloud.py tf destroy
```

`cloud.py tf` uses the existing gcloud login through a short-lived token in the
Terraform subprocess environment. Tokens are not HCL variables or artifacts.
`.runtime/ssh-key` is a NEW demo-only SSH key; only its public key enters VM
metadata. First connection uses a private known_hosts file; Fleet's subsequent
connections require that recorded host key. No global SSH/cloud settings change.

The current lab is `/Users/mh/dev/.codex-investigations/fleet-terraform-gcp-v2`.
The first cloud trial is retained at the same path without `-v2`; its cloud
execution passed but audit rejected missing adapter-freeze metadata. The v2
preparation freezes both adapters, the entry point and target before launch,
and checks the upstream freeze predicate before permitting the run.
Its verifier uses a separate Git administration directory BEFORE first launch,
so standard git and the audit resolve the same HEAD. Only its Codex process gets
outbound network enabled for the cloud SSH step; filesystem limits are unchanged.
The author and supervisor retain network-disabled wrappers. This is not a
hostile-agent security qualification.

## Lifecycle limits

This is still not a custom Fleet provider. Bootstrap records have no live Fleet
Read/import/update/remove. The fixed lab refuses reuse so evidence cannot be
overwritten. Terraform destroy removes CLOUD resources, not local work/results.
The VM deadline is the backstop if the controller disappears; Terraform owns
normal teardown. Failed cloud attempts stay in the evidence, never counted as
successful execution. New runtime attempts need a new preserved lab directory.

Source scripts reused: Rooms cloud setup/rootfs/toolstore recipes at
9512b2a50089a31d000b38e3ae097973f88f693d and Workbench headless lab at
3cea02ad796cd90eb772e57c9560249d0c34984e.

References: [Google VM resource](https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/compute_instance),
[GCP general-purpose prices](https://cloud.google.com/products/compute/pricing/general-purpose).
