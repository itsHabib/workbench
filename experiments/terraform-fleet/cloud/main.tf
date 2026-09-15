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
