**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-22
**Related**: dossier task `custody-tap-listener` (id: `tsk_01KY5C4H62K3M32GZ6QC6EG83J`), [grant-materialized rooms TDD](spec.md) §4 D2/D2b, §6

# custody: -tap-addr second listener + reach runbook — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/custody/internal/serve/serve.go` (+ new `tap.go`), `cmd/custody/main.go` | ~170 | 170 |
| Tests | serve/tap listener tests | ~220 | 110 |
| Docs (runbook) | `docs/features/grant-materialized-rooms/tap-runbook.md` | ~60 | 0 |
| **Total** | | | **~280** |

Band: **amazing** per repo PR sizing.

## Goal

A room's guest can reach custody: an explicitly-flagged second listener on the
room tap gateway (`custody serve -tap-addr <gw-ip>:8127`), sharing the one
proxy engine so rule semantics cannot diverge between listeners — plus the
source-binding enforcement that makes the tap listener safe to expose.

## Behavior / fix

In `cmd/custody/internal/serve` (+ `cmd/custody/main.go` flag wiring):

- `-tap-addr <ip>:<port>`: starts a second listener over the SAME engine/
  handler as the localhost listener. No flag → localhost only, byte-identical
  behavior (custody's original NFR intact).
- **Bind validation, fail closed at startup, coded errors:** refuse wildcard
  binds (`0.0.0.0`, `::`) and any address not on a tap interface. "Tap
  interface" = interface-name-prefix check, default prefix `tap`, overridable
  via `-tap-if-prefix <prefix>` for non-standard names — the runbook
  validation on the rooms-host confirms the real interface name and records
  the override if the default doesn't match.
- **Startup preflight guard:** `-tap-addr` fails closed unless the pinned
  source-restriction firewall rule is verifiably in force — probe the ruleset;
  refuse to serve with a coded error + remedy naming the runbook otherwise.
  The probe sits behind a small ruleset seam (policy/mechanism split; tests
  stub the seam); the Linux implementation targets nftables first with an
  iptables-save fallback, and the runbook validation settles which one the
  rooms-host actually uses.
- **Per-request source enforcement (D2b):** on the tap listener, the transport
  source must equal the presented grant's `bound_source` —
  `refused_source_mismatch` otherwise; a grant with empty `bound_source`
  (unbound) refuses outright on the tap listener. The localhost listener is
  unaffected (unbound grants keep working there).
- Runbook doc: firewall pins restricting the listener to the room subnet +
  custody port, with preflight checks, reproducible on the rooms-host.

## Acceptance

- Wildcard / non-tap binds refuse at startup with a coded error.
- No `-tap-addr` → localhost only; existing tests untouched and green.
- Listener parity: same request, same verdict on both listeners (source rules
  aside).
- On the tap listener: bound-source mismatch → `refused_source_mismatch`;
  unbound grant → refused; matching source → served.
- Runbook reproducible on the rooms-host.

## Test plan

Bind-refusal unit tests (wildcard, non-tap, override flag); listener-parity
test; `refused_source_mismatch` + unbound-refusal table tests; preflight-guard
fail-closed test (probe stubbed). Manual runbook validation on rooms-host is
follow-on validation, not part of this PR's CI.

## Non-goals

Per-room grant-to-source pinning (TDD §10.1, P4), the resolver, receipt
assembly, any vsock work.

**Model/effort:** sonnet/extra — mechanical listener + validation work; the engine is untouched.
