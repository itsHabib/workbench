# Fresh-agent recovery comparison, 2026-09-13

Both fresh agents completed the useful local task without clarification or a
replacement worker. The direct baseline sufficed. This trial demonstrates no
material Fleet advantage and does not justify a new Fleet supervisor or a
declarative language layer. The checkpoint fix earns a documented way to read
existing context; the old hashed file was already recoverable with direct tools.

This was one case per approach, following the separate deterministic
[mechanism demo](DEMO.md). It used actual agents with no predecessor conversation
and an interrupted, still-running original agent. It was not a provider crash
or a real Fleet provider launch.

## Work and interruption

The useful output was a review agenda for five real, frozen PR observations:
334, 337, 338, 339 and 340. The source was captured at
`2026-09-13T23:22:28.277805Z`. The supervisor had to validate the source, preserve
an existing renderer with a dirty draft, independently check its finished queue,
and commit a concrete next human assessment for each PR. The snapshot was never
used as live merge readiness or approval.

An original agent validated and committed `snapshot-check.md`, then naturally
wrote a 5,374-byte handoff with no padding or length target. Both local renderer
processes were already waiting with identical dirty drafts. The driver copied
the completed inputs into two isolated cases, published the same handoff, then
interrupted the original agent while it was still running. Both renderers
remained alive. This setup work is part of the trial, not an operator rescue
omitted from the comparison.

Each fresh agent received one initial prompt, a case directory and the same
task and authority constraints. Neither inherited the original conversation or
read its sibling's results. Neither received a reconstruction followup.

- The direct case used a saved `HANDOFF.md`, Git, process inspection and a plain
  worker record containing PID, start time and input/draft hashes.
- The Fleet case used `inspect`, work rows and receipts as well as those direct
  checks. Its actual agent-authored handoff was published through a short local
  fixture process. Checkpoint session/PID provenance identifies that bridge,
  not the original model. Fleet ran at `03f541a8d7d525968112ba9cc1387fbaacf94c45`.

Both were authorized to signal the existing renderer only after establishing
its ownership and validating the completed input. Both then compared the final
Markdown directly with the frozen JSON, without running the renderer to derive
an expected result, and committed only their assessment in the supervisor repo.

## Observations

| Observed result | Direct tools | Fleet plus direct tools |
| --- | --- | --- |
| Extra reconstruction or clarification prompts | 0 | 0 |
| Renderer start/completion events | 1 / 1 | 1 / 1 |
| Original worker continued | PID 92511 | PID 92495 |
| Completed input bytes and mtimes preserved | Yes | Yes |
| Independently matched PR rows / named checks | 5 / 26 | 5 / 26 |
| Final queue bytes | Identical SHA256 in both cases | Identical SHA256 in both cases |
| Concrete per-PR assessment committed | Yes | Yes |
| Agent's recorded clock interval | About 4m47s | About 6m33s |

The clock samples exclude some initial reading and are descriptive only. One
agent per approach, different tool exploration and concurrent execution do not
support a speed or reliability conclusion. Zero extra prompts in both cases
means no avoided operator reconstruction was demonstrated by Fleet.

Both queues preserve all 21 successful, two skipped, one neutral and two
unfinished check observations. Root separately verified the actual queue cells,
Git objects, output hashes, completed input hashes/mtimes and event counts. The
worker's assertion at release and matching hashes support draft preservation;
there was no continuous audit of every write or protection against hostile
writers. A matching implementation receipt remains separate from acceptance.

Direct retrieval had two initial Git commands run at the case container instead
of its documented checkouts. Fleet retrieval included an unnecessary missing
handoff-file read, `work --help` returning rows, absent traces, and a failed raw
checkpoint-hash comparison. The latter was explained by the existing writer's
`TrimSpace`: it stored 5,373 bytes, dropping the original file's final newline.
The new reader returned that stored text intact. Adding the newline reproduced
the source hash. The old binary returned only its 1,024-byte display excerpt for
this same natural checkpoint.

## Inspectable evidence

[observations.json](agent-trial/observations.json) retains the root audit,
immutable file hashes/mtimes, process events, commits, reported clock samples and
retrieval friction. The useful source and outputs are copied without editing:

- [Frozen source](agent-trial/snapshot.json), [original validation](agent-trial/snapshot-check.md)
  and [authored handoff](agent-trial/handoff.md).
- [Verified queue](agent-trial/queue.md), [direct assessment](agent-trial/direct-assessment.md)
  and [Fleet assessment](agent-trial/fleet-assessment.md).

Paths within the copied artifacts refer to the original disposable checkouts.
Their Git commits identify those retained local repositories, not this repo.
The observations are an authored evidence report, not tamper-proof attestations
or a new completion registry. Raw case directories and Git objects were also
retained for independent review.

To repeat the comparison with fresh agents, freeze the same kind of source,
start equivalent local workers with owned dirty drafts, have an original agent
validate and checkpoint without a length target, and interrupt it while active.
Give two new agents the same authorization and task facts, one through ordinary
files and one through Fleet, without prior conversation or rescue prompts.
Measure actual interventions, retained bytes, original worker completion and
independent output acceptance. Do not call the deterministic demo a repeat of
this agent trial. No general trial platform is required.

## Foundation and language decision

Use native task coordination and direct Git/process/artifact operations for
this workload. Where Fleet's existing addressed records are useful, the complete
reader removes one retrieval detail. Checkpoints still require useful authored
context and retained bindings/state; Fleet did not supply missing knowledge,
prevent a crash, or recover lost state in this trial.

The optional planner can own desired intent, dependencies and a reviewable
proposal. Existing backends should remain responsible for facts, ownership,
effects, replay behavior and authority. The richer prepared language bakeoff
must earn an advantage on changed dependencies, stale plans, partial failures
and another adapter against direct use of those same operations. It remains
prepared and unlaunched; Fleet and Rooms are possible adapters, not its scope.

This result establishes no arbitrary descendant quiescence, distributed
exclusion, atomic snapshot, exactly-once execution, production provider recovery
or merge authority. Seated assignment publication remains a separately identified
backend gap. Fixing an actual operation gap is better grounded than introducing
another orchestration layer to promise around it.
