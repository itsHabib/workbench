# Evidence — the Escalation plane closes the loop end-to-end

**Date:** 2026-07-26 · **Host:** local Windows checkout (worktree `escalation-plane-poc`)
**Binaries:** `gate` (with the new `resolve` verb + `escalation.v1` write) and
`escalate` (the new resolution back-channel), both built from this branch.

## What ran

One real agent→human→agent thread, driven through the actual binaries:

1. **A gate run parks** — writes a typed `escalation.v1` body.
2. **The inbox reads the typed body** (`gate next`).
3. **The back-channel ingests a decision by escalation id** (`escalate resolve`)
   — which drives `gate resolve`, records a judgment, and stamps the resolution.
4. **The inbox shows the park resolved** — now ready-to-merge.
5. **The audit chain still verifies** (`gate audit`).

**Honesty note on the seed.** A full `gate gate` run gathers evidence from a live
GitHub PR, which is not available offline. So the **park** was seeded through
gate's *own* `act()` code path (`TestSeedDemoState`, a guarded no-op unless
`GATE_DEMO_STATE`/`GATE_DEMO_KEY` are set) against a synthetic reduced verdict —
the on-disk log and keyed anchor are byte-identical to what a live gate writes.
**Everything downstream of the park — the inbox read, the resolve, the judgment,
the resolution stamp, the audit — is the real binary, end to end.**

Seed output:
```
DEMO_SEEDED grant=grt_63a310dfb8651b92 run=run_13d5d492ca31eeab escalation=esc_4ea400afe1ecc4c4 code=2
```

## Command transcript

### (1) `gate next` — the inbox reads the TYPED escalation body

```
awaiting judgment (1)

  itsHabib/ship#126  run_13d5d492ca31eeab
  "the reviewer panel disagrees on whether the retry loop can wedge"
  → gate judge -run run_13d5d492ca31eeab -grant grt_63a310dfb8651b92 -decision <pass|block> -why "..."
  → gate explain -run run_13d5d492ca31eeab -html

grants
  grt_63a310dfb8651b92  itsHabib/ship  merge  T2  in 59m
```

The question, PR subject, and grant all came out of the typed `escalation.v1`
body via `escalation.DecodeBody` — the same tolerant reader flare uses.

### (2) `escalate resolve` — the back-channel ingests the decision by escalation id

```
$ escalate resolve -escalation esc_4ea400afe1ecc4c4 -decision pass \
    -grant grt_63a310dfb8651b92 \
    -why "confirmed the retry loop has a ceiling; safe to land" \
    -who operator -gate ./gate.exe

{
  "run": "run_13d5d492ca31eeab",
  "pr": "itsHabib/ship#126",
  "decision": "pass",
  "tier": "T0",
  "outcome": "would_merge",
  "why": "judgment: confirmed the retry loop has a ceiling; safe to land",
  "action": "gh pr merge 126 -R itsHabib/ship --squash --delete-branch --match-head-commit abc123",
  "head_sha": "abc123"
}
(escalate exit: 0)
```

The back-channel held **only the escalation id** a notification would carry. It
validated the decision, shelled `gate resolve` (never imported gate), and passed
gate's outcome + exit code through faithfully. The loop closed: `would_merge`.

> A first run of this same thread against a seed with **no code-floor verdict**
> honestly **re-parked** (`outcome: parked_for_judgment`, exit 2, *"no code-floor
> verdict present — cannot verify readiness"*). That is the ladder law
> re-applying through `resolve` exactly as it does on a gate run — a judgment
> cannot launder a missing floor. The judgment and resolution were still recorded
> (the loop still closed with provenance); only the terminal differed. The
> transcript above is the clean content-park case.

### (3) `gate audit` — the chain still verifies after the loop closed

```
chain intact
```

### (4) `gate next` — the park is resolved; the PR is now ready to merge

```
nothing awaits judgment.
ready to merge (1)

  itsHabib/ship#126  run_13d5d492ca31eeab
  head abc123
  → gh pr merge 126 -R itsHabib/ship --squash --delete-branch --match-head-commit abc123

grants
  grt_63a310dfb8651b92  itsHabib/ship  merge  T2  in 59m
```

The projection transitioned the park out of "awaiting judgment" and into "ready
to merge" — the resolution took effect through the existing `act` path.

## The artifact bodies — closed-loop provenance in the log

Three consecutive artifacts from `state/log.jsonl`, joined by parent links:

**The escalation** (typed `escalation.v1`, the push):
```json
{
  "kind": "escalation",
  "id": "esc_4ea400afe1ecc4c4",
  "parents": ["vrd_f3ebc0f9c940793d", "grt_63a310dfb8651b92"],
  "body": {
    "schema_version": "escalation.v1",
    "outcome": "parked_for_judgment",
    "verdict": "vrd_f3ebc0f9c940793d",
    "grant": "grt_63a310dfb8651b92",
    "question": "the reviewer panel disagrees on whether the retry loop can wedge",
    "run_id": "run_13d5d492ca31eeab",
    "repo": "itsHabib/ship",
    "number": 126
  }
}
```

**The judgment** (the effect, parented to the escalation it resolves):
```json
{
  "kind": "judgment",
  "id": "jdg_150d4469fce2a861",
  "parents": ["esc_4ea400afe1ecc4c4"],
  "body": {
    "subject": {"repo": "itsHabib/ship", "number": 126, "head_sha": "abc123"},
    "source": "operator-judgment",
    "producer": {"class": "judgment", "impl": "operator"},
    "decision": "pass",
    "tier": "T0",
    "confidence": 1,
    "why": "confirmed the retry loop has a ceiling; safe to land"
  }
}
```

**The resolution** (the closed-loop stamp, parented to *both* the escalation and
the judgment):
```json
{
  "kind": "resolution",
  "id": "res_21f7cee11137bbc2",
  "parents": ["esc_4ea400afe1ecc4c4", "jdg_150d4469fce2a861"],
  "body": {
    "decision": "pass",
    "who": "operator",
    "at": "2026-07-26T14:12:15Z",
    "judgment_id": "jdg_150d4469fce2a861"
  }
}
```

The chain reads cleanly agent→human→agent: the escalation named its verdict and
grant; the judgment is parented to the escalation; the resolution names *who*
decided, *when*, and the *judgment it produced* — the provenance a bare judgment
artifact lacked — and links both. `gate audit` confirms none of it broke the
hash chain.

## Hardening (added in review)

Two independent reviews (a Fable-model pass + gate's bot panel) converged on
replay/idempotence as the sharpest gap; it was fixed and re-demonstrated:

```
# (A) first resolve → would_merge, exit 0   (as above)

# (B) REPLAY the same escalation (a double-tapped button, even flipping pass→block):
$ escalate resolve -escalation esc_57c65c406a982a72 -decision block -who attacker …
gate: resolve: escalation esc_57c65c406a982a72 is not the run's open park —
      it was already resolved or superseded by a re-park; nothing to resolve
(exit 4)

# (C) a well-formed but nonexistent escalation id — gate's own diagnostic now surfaces
#     through escalate (previously a silent nonzero exit with empty stdout):
$ escalate resolve -escalation esc_deadbeef …
gate: resolve: escalation esc_deadbeef: state: artifact esc_deadbeef: artifact not found
(exit 4)
```

(B) proves a retried notification callback or a double-tapped Slack button cannot
append a second authoritative outcome for one park — the guard fails closed
*before* any judgment is recorded.

## Reproduce

```bash
# from the worktree root
go build -o gate.exe ./cmd/gate && go build -o escalate.exe ./cmd/escalate
DEMO=$(mktemp -d); mkdir -p "$DEMO/state" "$DEMO/keys"
export GATE_STATE="$DEMO/state" GATE_KEY="$DEMO/keys"

# seed the park through gate's own act() path
GATE_DEMO_STATE="$DEMO/state" GATE_DEMO_KEY="$DEMO/keys" \
  go test ./cmd/gate -run TestSeedDemoState -count=1 -v   # prints DEMO_SEEDED grant=… escalation=…

./gate.exe next                                            # (1) inbox reads the typed body
./escalate.exe resolve -escalation <esc> -decision pass \
    -grant <grt> -why "…" -who operator -gate "$PWD/gate.exe"   # (2) resolve
./gate.exe audit                                           # (3) chain intact
./gate.exe next                                            # (4) now ready-to-merge
```
