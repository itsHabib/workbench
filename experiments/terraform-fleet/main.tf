terraform {
  required_version = ">= 1.4, < 2.0"
}

variable "fleet_source" {
  type    = string
  default = "/Users/mh/dev/workbench/.claude/worktrees/codex/headless-workbench-poc"
}

variable "fleet_revision" {
  type    = string
  default = "3cea02ad796cd90eb772e57c9560249d0c34984e"
}

variable "rooms" {
  description = "Optional existing Lima Rooms host; does not provision infrastructure."
  type = object({
    host      = string
    binary    = string
    image     = string
    toolstore = string
  })
  default = null
}

variable "card_overrides" {
  description = "Optional maintained card text, keyed by supervisor, author or verifier."
  type        = map(string)
  default     = {}
  validation {
    condition     = alltrue([for role in keys(var.card_overrides) : contains(["supervisor", "author", "verifier"], role)])
    error_message = "Card overrides must name supervisor, author or verifier."
  }
}

locals {
  lab = {
    root           = abspath("${path.root}/../.codex-investigations/fleet-terraform-local")
    fleet_source   = abspath(var.fleet_source)
    fleet_revision = var.fleet_revision
    provider       = "codex"
    rooms          = var.rooms
    cards = {
      for role in ["supervisor", "author", "verifier"] :
      role => lookup(var.card_overrides, role, file("${var.fleet_source}/cmd/fleet/examples/headless/cards/${role}.md"))
    }
  }
}

# Deliberately a bootstrap experiment, NOT a drift-aware Fleet provider.
# Replacement reruns the adapter, which updates cards in the SAME stopped lab.
resource "terraform_data" "fleet" {
  triggers_replace = local.lab

  provisioner "local-exec" {
    command = "python3 ./fleet_lab.py apply"
    environment = {
      FLEET_LAB_CONFIG = jsonencode(self.triggers_replace)
    }
  }
}

output "lab" {
  value = local.lab.root
}

output "run" {
  value = "python3 fleet_lab.py run"
}
