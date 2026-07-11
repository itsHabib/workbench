# workbench — design charter

**Status:** v0, 2026-07-08
**What:** the home repo for the Go agentic-infra family — one repo, one Go module.

This is the repo's own charter: why it is one module, what lives here, the one
boundary law, how tools migrate in, and the triggers that would later split
`contracts` into its own module. Recorded here so none of it is re-debated.

## One repo, one Go module

One `go.mod` at the root (one `go.sum` once there are third-party deps to pin;
today there are none). Not multi-module. No `go.work`.

`go install github.com/itsHabib/workbench/cmd/<tool>@latest` works cleanly from a
single module.

**Why single-module, not multi-module.** A Go *module* is the unit of
dependencies, version, and distribution; a *directory* is the unit of
`internal/` privacy. Every boundary we care about is directory-based and
survives a single module: each tool's guts stay private under
`cmd/<tool>/internal/`, and `contracts` is a leaf package. Multi-module only
earns its cost when something *outside* the module must pin `contracts` at its
own version — and nothing does today (ship is TypeScript, dossier is Rust; both
read the JSON schema file, not the Go type). So: single module now, with the
layout kept multi-module-*ready* so a future split is a `git mv` + a `go.mod`,
not a refactor.

## What's in, what's out

**In:** gate, triage, tracelens, local, flare, huddle, sense, future planes, and
new infra POCs by default. Today the repo holds `contracts`, `local`, and
`flare`; the rest migrate in lazily (see below).

**Out — do not migrate:**

- **ship** — TypeScript, frozen surface, heavy history.
- **dossier** — installed Rust, external shape. Judgment call: leave it out.
- Product POCs with independent public futures — **protocol, roxiq, wellness-ai,
  ivy**.
- Public skill registries — **skills, cc-skills**.

## The boundary law — share contracts, not call stacks

Tools compose at runtime through **artifacts**: exit codes + JSONL on disk.
Co-locating them in one repo/module does not change that. What they may share
in-process is the **vocabulary** — the types and schemas in `contracts`.

The one forbidden import: **a tool importing another tool's decision logic** —
gate importing flare's routing, a classifier importing the gate reducer. If you
ever find yourself reaching for another tool's decision path, stop: that is the
single line that turns a structural invariant back into discipline. When a tool
needs another tool's *output*, it reads an artifact.

This is enforced mechanically, not by convention: CI's `hygiene` job fails if
`contracts` imports anything else in the module, or if any `cmd/<tool>` reaches
into another tool's tree.

`contracts` itself carries **no decision logic** — only types, schema, and
tolerant decoders. The verdict reducer, the ladder law, every routing rule: each
stays in the tool that owns the decision. The verdict type's behavioral source
of truth is gate's `internal/verify`; `contracts` mirrors that shape and
conformance-tests against the schema, but never copies the `Reduce` function.

One deliberate carve-out: **contract-law validation** is vocabulary, not
decision logic. Pure, stdlib-only, I/O-free invariant functions over a
contracts domain's own types (e.g. `contracts/execution/validate.go` rejecting
path traversal, malformed secret refs, or invalid status/reason/phase
combinations) live beside the types they constrain — they define what a valid
instance *is*, for every consumer identically. Anything that decides what to
*do* with a valid instance (routing, lifecycle, retries) stays in the owning
tool.

## Shared mechanism packages

`local` (structured local-model calls + the escalate-on-uncertainty gate) is not
a tool like flare — it is a shared *mechanism* library plus CLIs, so the library
lives as a top-level package like `contracts`. The amendment the migration made:
a shared mechanism package is allowed when it carries **no tool's decision
logic** — pure plumbing any tool may call, never a place a routing rule or a
reducer can hide — and CI leaf-checks it like `contracts` (it may import at most
`contracts`, and today imports nothing in the module). Migrating local *in* is
also what keeps the contracts-split trigger unpulled: an outside-the-module Go
consumer of the shared types would force `contracts` into its own module; an
inside one is the point of the repo.

## Lazy migration, not big-bang

New planes are born here. Existing tools graduate in **when next touched** —
history preserved via `git subtree`/`filter-repo` where cheap, a plain copy
where the layout restructure would touch every file anyway (note the choice in
FOLLOWUPS). Adoption of `contracts` by an already-migrated tool is likewise done
when that tool is next in hand, never as a stop-the-world sweep.

flare is the founding tenant: unpushed and small, it was the cheapest first
migration, and rewiring it to `contracts` deleted the third hand-rolled copy of
the verdict parser — the debt this repo was stood up to pay (redesign
RED-TEAM #7). local is the second: going public touched it, so it graduated in
as the shared mechanism library (see above) plus its `cmd/local` and `cmd/eval`
faces.

## When to split `contracts` into its own module

Not now. Do it only when an **outside-the-module Go consumer** appears:

- a new Go repo *outside* workbench that imports these types, or
- publishing `contracts` as a standalone OSS artifact.

Explicitly **not** triggers:

- a tool *inside* workbench importing `contracts` (that is the whole point);
- ship or dossier consuming the JSON *schema file* (they read JSON, not the Go
  type).

Until then the layout stays multi-module-ready — a leaf package with no upward
imports — so the split is a `git mv` the day it is actually earned.
