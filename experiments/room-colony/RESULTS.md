# Room colony: what the run taught us

**Workers can own the loop inside separate Rooms and finish after losing an
author process.** The first live trial completed with two real model calls,
three distinct guest boots, and valid routes on all 27 qualification cases.

## Measured result

| Measurement | Live trial |
|---|---:|
| Real model calls | 2 |
| Guest boots | 3: author, verifier, replacement author |
| Development distance, baseline → both proposals | 13,640 → 4,290 |
| Held-out distance, baseline → selected proposal | 38,390 → 12,384 |
| Held-out reduction | 67.74% |
| Valid held-out cases | 27/27 |
| First author exit / replacement exit | 137 / 0 |
| Verifier exit | 0 |

Both proposals tied on development distance; the author retained the first.
Proposal two did not demonstrate further improvement. Time-limited search can
vary between runs, so saved measurements are observations, not exact numeric
replay promises.

The [live receipts](receipts/live/) precede four review fixes. The final harness
is checked separately with the same saved proposals in fresh Rooms; this avoids
claiming cached inference is a new model run. The [final replay](receipts/final-replay/)
passed all 13 audit checks with three new guest boots; completion apply started
no workers. Its held-out distance was 12384 across 27 valid cases.
All 16 offline tests pass, including real process interruption and HTTP handoff.
A follow-up review caught concurrent `stop`/`apply`; the subsequent shared-lock
fix has a real contention regression test. The published Room replay precedes
that lock-only fix; it is not presented as an exact final-head live run.

## The useful connections

| Idea | Working slice | What remains |
|---|---|---|
| Agents in Rooms | Author and verifier loops execute in separate Firecracker guests | Full coding-agent tools and native Fleet provider integration |
| Plan/apply | Keep live workers; replace a collected failed attempt; leave completed work alone | Cloud host loss, durable remote state and native Fleet observation |
| Passage-style handoffs | Immutable source → development receipt → selection → qualification → promotion | Adopt a common contract only when another consumer needs it |
| Self-improving software | Two model proposals improve a program against a fixed external scorer | Strong baseline comparisons and useful real workloads |

## Lessons worth keeping

- **Durable submission is the recovery point.** Kill the author after it submits;
  its replacement reads that candidate instead of buying the same work again.
- **A dead launcher is not a dead workload.** Missing collection/cleanup evidence
  blocks replacement. Otherwise an orphan can race its replacement.
- **The worker should own its decisions.** The host stores and transports; the
  guest author selects, and the guest verifier checks.
- **Measure the output, not the activity.** A route is an index permutation; the
  evaluator checks it and calculates distance itself. Extra tokens win nothing.
- **Use a deliberately broken candidate.** The negative control proves the
  checker rejects missing deliveries, rather than accepting every response.
- **Prove the final selection separately.** Development feedback picks one
  candidate; only then does it receive the qualification maps.
- **Infrastructure preparation expires.** An old image and rebooted networking
  both broke this trial before any model work. Record binary/image/toolstore
  hashes, and inspect actual network state.
- **Keep the network boundary.** A fixed host relay solved guest-to-broker access
  without opening forwarding to private networks.

## Read the result honestly

The baseline visits deliveries in input order. Beating it demonstrates a working
improvement loop, not a novel optimizer. The model prompt even suggests nearest
neighbor and 2-opt. A second proposal is allowed to lose; selection retains the
better development result.

This injected a **guest author-process kill**, followed by normal Room cleanup.
It did not kill a physical host, the broker, or the VM runtime. A Mac still hosts
inference and the mailbox. The verifier is deterministic code, not another model.
Generated code is cooperative and shares the verifier guest's trust boundary;
this is not hostile-code isolation or independent cryptographic attestation.

## Next experiment

Replace the specimen's transport and process observation with Fleet mail and its
provider lifecycle. Move broker/state to the Linux host, interrupt that host, and
recover on another host. Keep this route workload as the repeatable acceptance
case; add a stronger optimizer baseline before making algorithmic claims.

See [README](README.md) for commands and [receipts](receipts/) for the curated run.
Raw provider logs and credential-bearing launch commands stay private.
