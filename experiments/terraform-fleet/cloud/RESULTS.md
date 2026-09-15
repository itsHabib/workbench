# GCP full-stack demo — 2026-09-14

Status: **fresh full-stack trial PASSED all 27 audit checks; cloud teardown verified**. The first cloud
execution passed but its full audit caught missing adapter-freeze metadata.
Both attempts remain separate; no pre-run evidence was backfilled.

## What the Terraform actually looks like

This is the complete [cloud/main.tf](main.tf) used for the successful v2 run,
not a proposed provider API. The Google resources manage the VM and networking;
the two `terraform_data` blocks call the existing Python setup adapters.

```hcl
terraform {
  required_version = ">= 1.10, < 2.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 7.0"
    }
  }
}

provider "google" {
  project = "rooms-lab-20260914"
  region  = "us-east1"
  zone    = "us-east1-b"
}

variable "operator_cidr" { type = string }
variable "ssh_public_key" { type = string }

locals {
  name = "tf-fleet-rooms-0915"
}

resource "google_compute_network" "demo" {
  name                    = local.name
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "demo" {
  name          = local.name
  network       = google_compute_network.demo.id
  region        = "us-east1"
  ip_cidr_range = "10.77.0.0/24"
}

resource "google_compute_firewall" "ssh" {
  name          = "${local.name}-ssh"
  network       = google_compute_network.demo.name
  source_ranges = [var.operator_cidr]
  target_tags   = [local.name]
  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}

resource "google_compute_instance" "rooms" {
  name             = local.name
  machine_type     = "n2-standard-8"
  min_cpu_platform = "Intel Cascade Lake"
  tags             = [local.name]
  labels           = { experiment = local.name }
  boot_disk {
    auto_delete = true
    initialize_params {
      image = "ubuntu-os-cloud/ubuntu-2404-lts-amd64"
      size  = 100
      type  = "pd-balanced"
    }
  }
  network_interface {
    subnetwork = google_compute_subnetwork.demo.id
    access_config {}
  }
  advanced_machine_features {
    enable_nested_virtualization = true
  }
  scheduling {
    automatic_restart           = false
    on_host_maintenance         = "TERMINATE"
    instance_termination_action = "DELETE"
    max_run_duration { seconds = 10800 }
  }
  metadata = {
    ssh-keys               = "rooms:${var.ssh_public_key}"
    block-project-ssh-keys = "true"
    enable-oslogin         = "false"
  }
  # No service_account block: no cloud identity is attached.
}

output "host" {
  value = google_compute_instance.rooms.network_interface[0].access_config[0].nat_ip
}
output "instance" { value = google_compute_instance.rooms.name }

resource "terraform_data" "rooms_ready" {
  triggers_replace = {
    instance = google_compute_instance.rooms.instance_id
    source   = "9512b2a50089a31d000b38e3ae097973f88f693d"
  }
  depends_on = [google_compute_firewall.ssh]
  provisioner "local-exec" {
    command = "python3 cloud.py bootstrap"
    environment = {
      ROOMS_HOST = google_compute_instance.rooms.network_interface[0].access_config[0].nat_ip
    }
  }
}

resource "terraform_data" "fleet" {
  triggers_replace = {
    host            = google_compute_instance.rooms.instance_id
    fleet_revision  = "3cea02ad796cd90eb772e57c9560249d0c34984e"
    demo_generation = 2
  }
  depends_on = [terraform_data.rooms_ready]
  provisioner "local-exec" {
    command = "python3 fleet_demo.py prepare"
    environment = {
      ROOMS_HOST = google_compute_instance.rooms.network_interface[0].access_config[0].nat_ip
    }
  }
}
```

Reading it from the bottom up: `fleet` waits for `rooms_ready`, which waits for
the VM and SSH firewall. The VM's address flows directly into both setup commands
as `ROOMS_HOST`. The operator CIDR and public SSH key are supplied by the local
wrapper; no private key or model credential is embedded in this configuration.

The important limitation is visible here: there is no `fleet_card` resource.
Role/card preparation lives in [fleet_demo.py](fleet_demo.py), which reuses the
pinned Workbench lab. Terraform prepares that local fleet; the separate command
`python3 fleet_demo.py run` launches the actual workload. This demonstrates HCL
composition with real cloud lifecycle management, not yet declarative lifecycle
management for individual Fleet cards.

## Frozen setup

- Operator-approved total GCP cap: $50; one VM, three-hour DELETE deadline.
- Project/zone: rooms-lab-20260914 / us-east1-b.
- Instance: tf-fleet-rooms-0915, id1690442913898919918, n2-standard-8,
  Intel Cascade Lake, nested KVM; created 2026-09-14T21:26:41.862-07:00.
- Dedicated network/subnet, SSH /32, no service account, auto-delete100GB disk.
- Terraform1.15.8; Google provider7.46.1 locked in .terraform.lock.hcl.
- Rooms9512b2a50089a31d000b38e3ae097973f88f693d;
  Workbench3cea02ad796cd90eb772e57c9560249d0c34984e.
- First lab: /Users/mh/dev/.codex-investigations/fleet-terraform-gcp.
- Corrected fresh lab: same path with -v2 suffix.
- Private operational evidence: cloud/.runtime (no credentials in this report).

## Setup and controls

Terraform created the VM, built/tested Rooms, then prepared all three Fleet
roles. Repeat plan returned exit0/no changes. Actual KVM exists, Rooms doctor
passed, and idle inventory showed zero Rooms and no Firecracker processes.

The first source-archive-only bootstrap failed a Git-publication test. Adding
the exact pinned Git history fixed the setup; no test was disabled. The rerun
passed614 tests with4 ignored. A subsequent full `make check` also passed
formatting, strict Clippy, the Rust suite and14 Python rehearsal tests.
Release binary, guest image and Python toolstore
were built and hashed. The final bootstrap log is retained in .runtime/bootstrap.log;
the first failure is retained in the task's command output, not a separate full log.

Positive control: previous frozen patch b3fbe81e... ran8tests successfully in a
cold GCP Room. Rooms command exit0, returned patch hash identical, collection_done
and cleanup_done present. Rooms elapsed10.094s, excluding SSH/staging/collection.

Negative control: failure.patch intentionally adds a failing unittest. It reached
the guest, failed with the intended assertion, and returned command/CLI exit1.
Collection and cleanup still completed. Rooms elapsed9.634s. No pass inferred
from successful transport or artifact collection. The returned patch encoding
differs from the hand-authored negative patch, so no byte-equality claim there.

Real drift probe: changed the VM's managed experiment label using gcloud.
Terraform plan returned exit2 and proposed exactly one in-place update (zero
create/destroy). Saved-plan apply restored the label without replacing the VM
or rerunning either bootstrap record. This is actual provider Read/update,
unlike the Fleet bootstrap's input-only state.

## Live Fleet trial

The original local failed run remains unchanged. This fresh verifier was prepared
with separate Git metadata before any workers started; standard Git resolves it
through the checkout's .git file. Only the verifier has outbound network enabled
for its demo SSH connection. Agents and model credentials remain on the Mac.

First cloud trial: all three roles ran, recovered from the supervisor interruption
and produced implementation/verify/rooms pass receipts at
6af29c7b4dfa054bfb9c61e2da7e278a57055887. The exact patch
d4ea44902a78edadd5fa2e11ebd09f4e025e78e6e8eee02791c2ec5cdab0968f
passed8tests in a GCP Room (10.240s Rooms time), with identical returned bytes and
collection/cleanup events. Final audit nevertheless FAILED solely on
rooms_adapter_unchanged because my new bridge omitted the expected pre-run
adapter file/hash fields. All provider exits were collected and the watcher stopped.

Generation2 retained that failure and prepared a NEW local fleet on the SAME VM.
Both adapters, entry point and connection target are frozen before first launch;
the actual upstream freeze check is now a preparation gate with a regression
test that detects subsequent changes. The second run passed the original audit
without modification.

Final head: `ef0c1c8478b80a51207db3cef2f40bb064b1cd04`.
Final patch: `397936b9021ae8dc0f705b664da3dc339cb477464982727424b4b9d768e5b6b3`.
All10 tests passed in the GCP Room (8 supplied plus2 author-added validation
tests); Rooms elapsed10.139s. The returned patch is byte-identical. Separate
implementation, verify and rooms receipts name the same exact revision and
independent author/verifier sessions. The full Fleet trial took about17.9 minutes.

Evidence copies: [final audit](evidence/final-audit.json),
[cloud result](evidence/final-rooms-summary.json),
[Fleet cleanup](evidence/final-fleet-cleanup.json),
[first failed audit](evidence/first-cloud-audit.json),
[positive](evidence/positive-control.json) and
[negative](evidence/negative-control.json) controls.

## Cleanup and scope

Watcher stopped, all provider attempts terminal and bridge exits collected.
Pre-teardown GCP inventory showed no live Rooms or Firecracker processes.
Terraform teardown completed. A subsequent independent GCP inventory returned
exit 0 and empty arrays for the exact demo VM, disk, network, subnet and firewall;
`terraform state list` is empty. See [cloud cleanup](evidence/cloud-cleanup.json).
No demo cloud resources remain. Actual billed cost has not yet been reconciled;
the run used one bounded VM for less than an hour, within the planned $50 budget.
General local
descendant-process quiescence is not proven by the Fleet cleanup receipt.

This demonstrates Terraform provisioning and drift repair, real local Fleet
coordination/recovery, and exact-patch execution in Firecracker on GCP. The agents
did NOT run inside Rooms or on GCP. No custom Fleet provider was necessary for
the demo; live Fleet Read/import/safe removal are still absent from the bootstrap
records. This is a qualified bounded experiment, not a production deployment or
a general hostile-agent isolation guarantee.
