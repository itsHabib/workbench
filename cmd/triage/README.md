# triage

A PR risk-classification engine — a
[workbench](https://github.com/itsHabib/workbench) tenant. Routes scarce human
review attention to the PRs that need judgment, and machine-clears the ones
that don't — so review load scales with **risk**, not **PR count**.

A deterministic floor (real Go code over the unified diff) assigns a risk tier
that nothing may lower; a verified agent advisory may only *escalate* above it,
and only when its evidence is a verbatim quote from that same diff. The final
tier is `max(floor, trusted escalation)`, and it routes the PR to the right
amount of review. The one hard guarantee: it must never *under*-classify a
dangerous PR.

- **Run it:** the `/pr-risk` skill (canonical in `~/.claude/skills/pr-risk/`) —
  floor engine → agent advisory → route → log. This repo is the engine +
  design home; the skill is the runtime.
- **The classifier policy:** [`RUBRIC.md`](RUBRIC.md)
- **The oracle (test-first):** [`labels/`](labels/)
- **Design + evidence:** [`docs/features/`](docs/features) — the engine spec
  plus `EXPERIMENT-01.md` / `HELDOUT-01.md`, and the advisory verifier's own
  spec and evals.

## Use

Two binaries, both reading a unified diff on stdin. From the module root:

```
gh pr diff N -R owner/repo | go run ./cmd/triage/triage-floor -v
gh pr diff N -R owner/repo | go run ./cmd/triage/triage-floor -repo owner/repo
gh pr diff N -R owner/repo | go run ./cmd/triage/triage-advisory -v \
  -proposal '{"escalate":"T2","trigger":"invariant-relocation","evidence":"<verbatim quote from the diff>","why":"..."}'
```

`-v` prints the human-readable summary; without it each binary emits JSON.
`-repo owner/name` opts the floor into that repo's compiled-in path overrides
(`RUBRIC.md` §5.7) — absent, none apply and the result is byte-identical to the
pre-override floor. `-proposal` takes inline JSON or `@path`; empty means no
escalation was proposed.

### Exit codes

Both binaries share one contract — **exit 0 = classified, 1 = operational
failure, never a tier**. The tier lives in the output, never in the exit code,
so a caller can never read a parse failure as a low-risk PR. `triage-floor`
uses an explicit `ContinueOnError` flagset for exactly this reason: the
stdlib default would exit 0 on `-h` and 2 on a bad flag, and gate's exit-code
ladder misreads both.

Both binary names are load-bearing seams — the `/pr-risk` skill and gate's
verifier ladder shell them by name.

## How it works

- **Floor** ([`internal/floor`](internal/floor)) — a unified-diff parser plus
  the rubric as code: path rules, added-content rules, and the per-repo
  override table each fire signals, and the floor is the max tier over
  everything that fired. Sensitive (T2+) rules also match the *old* path, so a
  rename into a benign name cannot shed them. Tiers are T0 auto / T1 peer /
  T2 owner / T3 critical.
- **Advisory** ([`internal/advisory`](internal/advisory)) — the trust boundary,
  and deliberately not judgment. A host agent's proposal must escalate to T2 or
  T3, name one of the known escalation triggers in `RUBRIC.md`, and quote at
  least 20 normalized characters of the diff verbatim. The proposal's
  `confidence` is recorded but never trusted. Every check failure is named so
  the host can retry on the reason; a rejected proposal contributes nothing and
  the floor stands.
- **Route** — T0 `auto-eligible (recommend-only)`, T1 `peer`, T2 `owner`,
  T3 `owner+adversarial`.

Status: **dogfooding.** Engine built + tested + run live on ship/dossier/rooms
PRs. Recommend-only until the auto-merge safe-slice is flipped on.
