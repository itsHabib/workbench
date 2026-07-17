**Status**: draft
**Owner**: @michael
**Date**: 2026-07-06
**Related**: dossier task `tamper-threat-model-hardening` (id: `tsk_01KWW342C5EC8JV3AV028SW95F`), [docs/FOLLOWUPS.md](../../FOLLOWUPS.md)
**Model/effort**: opus / max

# State and harden the tamper threat model — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `internal/state/state.go` (Audit + keyed tip anchor + truncation), `internal/capability/capability.go` + `cmd/gate/main.go` (`newEnv` key path), `internal/state/lock.go` (clock injection, minor) | ~95 | 95 |
| Tests | `internal/state/state_test.go` (truncation, deletion, rewrite cases) | ~110 | 55 |
| Docs | `docs/DESIGN.md` (threat model section) | ~40 | 12 |
| **Total** | | | **~160** |

Band: **ideal** per repo's PR sizing convention.

## Goal

Tamper-evidence is narrower than the docs claim. `Audit` catches naive body edits, broken `Prev` links, and reordering — but a truncated or wholesale-deleted log still reports "chain intact", the unkeyed SHA-256 chain can be rewritten end-to-end by anyone with file-write, and `grant.key` sits in the same directory as `log.jsonl`, so whoever can tamper the log can also mint grants. State the real threat model honestly, then land the cheap hardening that closes truncation, deletion, and rewrite — or defer any piece explicitly with a stated reason.

## Behavior / fix

### 1. Honest threat model — `docs/DESIGN.md`

Add a short, unqualified statement of what the hash chain does and does not defend against:
- **Catches:** naive body edits (stored hash no longer matches), broken `Prev` links, mid-log reordering.
- **Did not, before this change:** tail truncation / whole-log deletion (reported "chain intact"); wholesale rewrite-with-rehash (unkeyed SHA-256, recomputable by anyone with file write); last-entry edits.
- **Trust boundary:** every agent on this box has shell access, so file-write is in-scope for the realistic adversary (drift + accidental corruption); the chain is tamper-*evident* against accidental and naive modification, and — after this change — against rewrite and truncation via a keyed anchor whose key lives outside the state dir.

Keep the doc claims matched 1:1 to what the code now enforces. Do not claim cryptographic non-repudiation.

### 2. Keyed tip anchor — defeat wholesale rewrite (`internal/state/state.go`)

Maintain a small anchor, updated under the same lock as `Append`, that binds the chain head with a key held **outside the state dir**:
- An anchor value = HMAC(anchorKey, headHash ‖ count) (or signature over head + count), persisted to an anchor file **outside** `log.jsonl`'s directory.
- `Audit` recomputes the chain head and count from the log and verifies them against the anchor's HMAC. A suffix rewrite (even with every hash recomputed) fails the anchor check because the attacker with only state-dir write cannot forge the HMAC.
- The anchor key must be loaded, never silently created on the read/verify path (mirror `capability.loadKey`'s "missing key is a loud error, not a fresh key" discipline). Creating the anchor key is a mint-time / first-append concern only.

### 3. Truncation + whole-log deletion detection (`internal/state/state.go`)

Record the expected log length / tip in the anchor (the `count` above). `Audit` compares the actual replayed count + head against the recorded expectation:
- Fewer entries than recorded, or an empty/missing log where the anchor expects entries → report tampered (truncation / deletion), not "chain intact".
- Whole-log deletion with the anchor still present → detected (anchor expects N ≥ 1, log has 0).

`Audit`'s return contract may need widening beyond "id of first tampered artifact" to also express truncation/deletion (which have no surviving artifact id to name) — e.g. a sentinel string or a small result type. Keep `cmd/gate/main.go`'s `cmdAudit` output honest with whatever shape you choose (`TAMPERED: ...` with a reason).

### 4. Move `grant.key` out of the state dir (`cmd/gate/main.go` `newEnv`, `internal/capability/capability.go`)

`newEnv` currently sets `keyPath = filepath.Join(stateDir, "grant.key")`. Move the signing key (and the new anchor key) to a location outside the state dir's trust domain — e.g. under `os.UserConfigDir()/gate/` or a sibling directory, with a `-key` flag override. The point: an actor who can write the state dir must not thereby be able to read/forge the signing key. Preserve existing grants — the key *content* moves, it is not regenerated; a moved key must still validate previously-minted grants (so migrate/relocate rather than re-mint). Keep the `main.go` edit confined to `newEnv` / flag wiring so it stays in a different region from the backtest change the sibling capability task makes.

### 5. Minor: inject the clock in `lock.go`

`lock` uses `time.Now()` directly while the rest of the package injects the clock. Thread the store's `now` through so staleness is testable. Small, optional-if-it-balloons — defer with a note if it pulls in more than a few lines.

## Acceptance

- `docs/DESIGN.md` states the threat model honestly (catches / does-not-catch / trust boundary), matched to what the code enforces.
- `Audit` detects **tail truncation** and **whole-log deletion** (no longer reports "chain intact").
- `Audit` detects a **keyed-anchor rewrite**: a fully rehashed suffix fails because the HMAC anchor (key outside the state dir) doesn't match.
- `grant.key` no longer lives in the state dir; previously-minted grants still validate after the move.
- New tests pin truncation, deletion, and rewrite detection.
- Any deferred piece (e.g. the `lock.go` clock, or a hardening sub-part) is explicitly called out in the doc with a stated reason.

## Test plan

`go test ./internal/state/...` with new cases:
- append N artifacts, truncate the last K lines → `Audit` reports tampered.
- append N, delete the whole log (keep the anchor) → `Audit` reports tampered.
- append N, rewrite a suffix recomputing every `Prev`/`Hash` → `Audit` reports tampered (anchor mismatch), where without the anchor it would have said intact.
- untouched log → `Audit` reports intact (regression guard).
- capability: mint a grant, relocate the key to the new path, `Check` still validates it.

Then `go vet ./...`, `gofmt -l .` clean, `golangci-lint run ./...`, full `go test -race ./...`.

## Non-goals

- The enforcement-model doc (capability task) — this task only supplies the key-custody decision it references.
- The verifier-ladder fail-opens — sibling task.
- Driver wiring. The stale-lock TOCTOU takeover race (red-team finding 6) and SQLite migration (finding 13) are **out of scope** — this task is truncation/rewrite integrity + key custody only; note them as untouched.
