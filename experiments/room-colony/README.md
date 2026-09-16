# Room colony: improve a program, lose its author, finish the work

**[Results and lessons](RESULTS.md)** · [Host-loss recovery + Passage](HOST-RECOVERY.md) · [Run receipts](receipts/)

Two workers live in separate Firecracker Rooms. The author asks a model for a
better delivery-route planner. The verifier compiles and tests it, returning a
receipt tied to the source digest. The author selects a candidate, the verifier
checks unseen maps, and the author promotes only a qualifying result.

Between submission and review, the first author kills itself. A fresh `apply`
starts a replacement Room. It recovers the submitted source from the mailbox
instead of spending another model call. Applying while workers are alive keeps
them; applying after successful completion does nothing.

This combines three existing questions: headless workers in Rooms, desired
state versus observed processes, and evidence passed between lifecycle phases.
It adds a fourth: can those workers improve an actual program against an
unchanged evaluator? It is a working experiment, not a new Workbench plane.

```text
Mac: durable mailbox + Codex inference adapter (credentials stay here)
             ↕ fixed HTTP relay on the Linux host
       author Room ↔ mailbox ↔ verifier Room
       generate              compile + check + score
       submit → SIGKILL       receipt for exact source
       replacement resumes   unseen-map qualification
       promote or reject

lab plan/apply: observe processes and completion; start missing workers
```

The verifier is deterministic software in another VM, **not a second LLM
reviewer**. The author is a fixed two-round loop around real model calls, not a
general coding assistant. Neither requires desktop task tools. This first Mac-hosted mode is described below;
[host.py](host.py) adds a Linux-owned replay and host-power-loss experiment. The host relay
does not select, score, or promote candidates. Inference still depends on the
Mac; this is not an independently operating cloud fleet.

## Try it

Requires Python 3.12+, authenticated `codex exec`, and an already prepared Lima
Rooms host with nested KVM, the current image, and a sealed Python toolstore.
The CLI accepts `--lima`, `--rooms`, `--image`, and `--toolstore` at initialization
because those are existing backend resources. Defaults describe the first lab,
not a portable installation. Use a new absolute output directory for each run.

```sh
limactl start rooms-host
# Host reboot clears Rooms' network rules. Run its existing setup script:
limactl shell rooms-host -- sudo bash /path/to/rooms/scripts/setup-tap.sh --host

python3 experiments/room-colony/lab.py init /absolute/path/to/new-lab \
  --rooms /path/in/linux/to/rooms --image /path/in/linux/to/agent.ext4 \
  --toolstore /path/in/linux/to/python-toolstore
python3 experiments/room-colony/lab.py plan /absolute/path/to/new-lab
python3 experiments/room-colony/lab.py apply /absolute/path/to/new-lab
# Later: plan observes the dead author; apply replaces only that worker.
python3 experiments/room-colony/lab.py apply /absolute/path/to/new-lab
python3 experiments/room-colony/lab.py audit /absolute/path/to/new-lab
python3 experiments/room-colony/lab.py stop /absolute/path/to/new-lab
```

`apply` returns immediately. Wait for the first author's terminal receipt before
the recovery apply; inspect `attempts/*/terminal.json` and `plan`. Every Room has
a ten-minute wall bound. `stop` refuses to remove a still-running workload;
let Rooms collect and clean up first. It then stops the broker/relay, verifies
the experiment's Room inventory is empty, and removes its private guest HOME.
Stop the Lima host afterward if this run started it and no other work uses it.

The relay is needed because Rooms blocks forwarding to private networks. It
exposes only the experiment API on the guest's host gateway and forwards to a
fixed loopback broker. It does not weaken those forwarding rules. Requests
require an ephemeral experiment token. It is a trusted single-operator mailbox,
not tenant isolation or signed peer identity.

## What counts as improvement

The original planner visits deliveries in input order. Candidate Python returns
only an index permutation. The verifier checks every delivery is visited once,
rejects invalid indices (including Python booleans), and computes the Manhattan
round-trip distance itself. A syntax error, failed subprocess, or timeout rejects
the candidate. Candidate processes cannot submit their own score.

Two proposals receive development feedback. Selection uses that feedback alone;
the selected candidate then gets one held-out evaluation. Promotion requires
valid routes and at least 15% lower aggregate distance than the baseline. Empty,
duplicate-coordinate, and single-delivery cases are included. An intentionally
wrong planner is a negative control. These synthetic maps establish this narrow
result, not a production logistics advantage or a novel optimization algorithm.

Candidate code runs as a subprocess in the verifier's disposable Room. **This is
not a hostile-code sandbox within that Room.** Cooperative generated code, the
verifier, and its mailbox token share the guest trust boundary. A hostile-code
extension needs a separate execution Room with no verifier/mailbox access.

## Receipts and failure lessons

Each attempt retains its original Rooms output, lifecycle events, guest boot
identity, process exit and elapsed time. The mailbox retains candidate source,
development reviews, selection, qualification and promotion. Model requests and
responses are saved privately, including incomplete attempts. Initialization records
backend binary, image and toolstore hashes and checks them before an apply that
launches workers. These checks do not lock external files; keep backend resources
unchanged throughout each run. Archived audits work after shutdown and report
whether the current harness source matches the recorded run. The audit checks
distinct guest boots, interrupted/replaced author, source binding, negative
control, two model results, and collection/cleanup for every attempt.

Do not publish the entire lab directory: it contains an ephemeral token and
provider logs that can include local configuration. Share the curated results
and source instead.

| Failure encountered | Lesson |
|---|---|
| Old image rejected the current toolstore | Pin the binary, image and toolstore together; do not infer compatibility from filenames. |
| Host reboot removed Rooms forwarding chains | Host preparation and a successful past run are different facts. |
| Guest could not reach the Mac's private address | Use a narrow relay at the allowed boundary, not broader network permission. |
| BusyBox returned extra routes for `ip route show default` | Match the `default` row explicitly; desktop command behavior is not guest behavior. |

## How this fits the other experiments

- [Terraform/Fleet](../terraform-fleet/): provides infrastructure and bootstrap.
  This specimen's `plan` observes live worker processes and results; it does not
  pretend a no-change Terraform plan proves that workers still exist.
- [Headless Fleet POC](https://github.com/itsHabib/workbench/pull/344): demonstrated
  host-side agents and a verifier using Rooms. Here the workflow loops themselves
  are in Rooms. Native Fleet mail/wakes and provider bridges are not wired in yet.
- [Passage](https://github.com/itsHabib/workbench/pull/353): separates handoffs from
  freedom inside a phase. Candidate → review → qualification → promotion is one
  concrete instance, not adoption of Passage's entire proposed schema.

The next integration should replace this experiment's transport with native
Fleet mail and its process observer with the existing provider lifecycle—not
add a second production scheduler. Moving the credential adapter and durable
mailbox off the Mac is the next cloud step. Test genuine VM loss separately from
this experiment's author-process loss.

Offline checks:

```sh
python3 -m unittest discover -s experiments/room-colony -v
```
