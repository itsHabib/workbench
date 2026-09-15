# Optional concrete use case: sourced cards and named execution seats

Concept note for the common pack; no entry launched, winning syntax selected, or runtime changed. This supplements the common workload rather than replacing it. Fleet/Rooms are example adapters, not language-core types.

## Tiny authored description (syntax-neutral)

| Name | Intended meaning |
|---|---|
| `author_source` | Git source: `itsHabib/workbench` at `10b066cac0bb959ca3dfa7dc2d77886eed277e78`, paths `cmd/fleet/examples/lanes/author/{manifest.json,card.md}` |
| `notes_source` | Local fixture `cards/notes.md`: “Clarify the setup instructions; stop at a draft PR.” Capture exact bytes and digest during resolution. |
| `demo-author-1` | One seat beside existing disposable checkout `/sandbox/demo`, tenant `demo`, role kind `author`; instruction source `author_source`; configured provider `codex`. |
| `notes` | Work sourced from `notes_source`, branch `codex/setup-notes`, accountable `author:demo`, expected receipt `draft`, desired placement `demo-author-1`, explicit deadline. |

The local task fixture/path is illustrative, not an existing production card. The source locator is not a new registry entry. A source lock records locator, resolved revision, selected paths and content digests in the compiled artifact. Current inspected author card SHA256: `95fe7a33de49ddac88c51dc1f9a83f444a200ef86faa3c7a7ff08158eb3d055c`; manifest SHA256: `dd57eaabb3ba2434741764a2ee54275bbe385adee640788d6ab9956b696e582c`.

## Existing interfaces that can be reused

Grounding: Workbench source at #337 head `6aa2c9430f221e3895b16da4683c199e6e76f284`; paths below are repository-relative.

- `cmd/fleet/internal/fleet/lanes.go`: `$FLEET_LANES` or installed lanes provide manifests; role kind selects a manifest. `$ORG_STATE/roles.map` rows are path, tenant, role, optional seat name. This is an existing binding store, not evidence of a live occupant.
- `cmd/fleet/internal/verbs/role.go`: a six-field lane manifest (`kind`, `card`, `denies`, `requires`, `produces`, `slots`) names the card path. `fleet role` projects its instructions and harness configuration into a checkout. Codex configuration reads card bytes during projection; source edits alone are not proof installed instructions changed.
- `cmd/fleet/internal/verbs/views.go`: `fleet pool <checkout> author 1 --tenant demo` creates/tops up seats; names derive from checkout basename, kind and index (`demo-author-1`). Do not assume a manifest alias can rename existing seats arbitrarily. `fleet assign` and seated `dispatch` select an existing seat and perform checkout/placement under its lock.
- `cmd/fleet/README.md`, delivery section: `deliver.json` maps an address to cwd/provider and optional scheduling/routing settings. The Go watcher owns launch/occupancy decisions; pending assignments with briefs can wake configured seats. Installing delivery configuration is therefore an effectful change with potential launch consequences.
- `cmd/standup/internal/standup/record.go`: role proposals already contain kind, checkout, seat count, instructions and terms; work cards contain repo, branch/change, accountable role, seat/checkout, deadline and brief. A source resolver can supply these existing fields; arbitrary task-card locators are not currently a native Fleet dispatch input.

No general deployment transaction was identified in these interfaces. The installer offers dry-run/apply setup, but is not a new desired-state runtime. Source resolution and a diffing client must not imply backend CAS, rollback or launch guarantees that these commands do not provide.

## Example compiled explanation and subsequent diff

Initial hypothetical observation: checkout exists; desired seat binding and provider entry absent; work row absent. A rendered plan could show:

1. Lock the two card sources to captured bytes and resolve the task branch to an exact head.
2. Propose one pool/role projection using that lane source; show files/bindings affected.
3. Propose the specific delivery entry; disclose that eligible waiting work can launch after deployment.
4. Propose the work declaration/placement with its full brief, deadline and expected receipt.

These are ordered proposed backend effects, not an atomic deployment. Entries can simulate placement where the backend safety prerequisite is absent. #337 can execute only the unseated declaration portion today.

On an unchanged repeat: unchanged source locks and installed configuration should yield no configuration mutation; a retained identical work operation should not be dispatched again. “Seat currently busy” remains an observation, not drift to repair by evicting it. Do not call ordinary repeated `dispatch --due` idempotent: it can rewrite timestamps/deadlines. #337 has explicit retained-row replay for unseated work; seated dispatch still publishes assignment before its work row and can wake a worker before a later write fails.

If `cards/notes.md` changes, show old/new content digest and brief; require a new reviewed plan. Do not silently reinterpret a pinned source or mutate already-running work. If a Git source uses a moving branch locator, resolve it once to a full commit; either keep the captured revision or explicitly refresh and show the source change. Role-card changes produce a proposed projection update, not proof that an existing session reread it.

If someone changes the binding/provider entry after preview, show a stale-plan conflict and reobserve rather than overwrite it. When an earlier setup step succeeds and a later placement fails, retain actual evidence of the first step and report the remainder separately. Without native committed-operation evidence, a possibly launched execution is unknown and must not be blindly repeated or “rolled back” by unassigning.

## State and authority boundaries

Authored intent and source locks describe what was requested. Configuration/bindings describe what was installed. Sessions, leases and watcher records describe observed occupancy. Dispatch/assignment records describe declared work and placement. Provider attempts and result artifacts describe execution history. Exact-head receipts support completion claims. None substitutes for another, and card prose grants no permission or merge authority.

Optional Rooms execution is another adapter operation: existing `rooms run` is a synchronous fresh invocation, not caller-keyed idempotent submission. Unique output/lifecycle paths and separate workload/collection/cleanup outcomes matter; reusing paths destroys prior evidence. Real execution needs a qualified Linux/KVM host, unavailable natively on this Mac. The common pack may label its simulation explicitly. This use case requires neither a new source registry nor a replacement for Fleet's watcher or Rooms' custody.

## Setup experience: define once, inspect what resolves

Product direction, not an implemented interface: a user should declare the role/card source once, then reference it from a seat or pool. Work references the resulting seat and its task source; it should not repeat card prose, checkout identity, tenant and provider settings in several places. Reuse existing Fleet bindings and optional Org facilities where present rather than create another role/source registry.

Defaults should derive from explicit surrounding context: an existing seat supplies its checkout/binding; a project declaration can supply a tenant or provider default. Show the resulting values and where each came from. If context is missing or conflicting, report the specific unresolved field rather than invent an accountable role, change tenant, take over a busy seat or select a paid execution backend. A default is convenience, not authorization.

An inspection view should answer, in one place: which source revision and card bytes were selected; which role, tenant and named seat they resolve to; which checkout and provider configuration would be installed; and what differs from the current installation. References are authoring conveniences; the compiled plan contains the resolved values and source provenance needed to review it.

Edits need three distinct statements: **source changed**, **installed configuration updated**, and **running session observed using the new configuration**. The first does not prove the second or third. Updating a card should show a projection diff and explain when the backend consumes it; do not silently restart, replace or interrupt a session to make the display appear synchronized. If uptake cannot be observed, say it is unknown.

Hierarchy is a future option only when a concrete responsibility or navigation problem benefits from it. Existing optional Org relationships may provide context, but this use case requires no parent tree, derived routing, permission inheritance or new registry. Coordinate any eventual integration with the existing role/Org and Fleet supervision interfaces; this note does not establish a parallel supervisor.

## Concrete setup now available in the headless lab

Read-only inspection of `cmd/fleet/examples/headless/lab.py` in the integration lead's `codex/headless-workbench-poc` worktree confirms a useful first implementation of this setup experience. It is a working-tree implementation, not a claim of merged behavior or a completed live trial.

`prepare` accepts an optional destination, card directory and local Fleet source checkout. The example fixes the task, three roles and Codex provider; it derives lane manifests, the author pool seat, role bindings, isolated state and configuration. `resolved.json` records effective addresses/checkouts, source head/status, binary hash and initial card hashes. These are sufficient inputs/defaults for this bounded example; a general manifest need not ask users to reproduce every generated field.

Source semantics are precise: the supplied cards are **copied once** into the lab. Those local lane cards become the editable sources read by `cards` and `update`; changing the original `--cards` directory does not automatically refresh them. Inspection should distinguish original import location/hash from current editable copy/hash and installed projection. This is an explicit copy/import model, not a live source subscription.

`cards` displays the role/seat/checkout, source hash, fixed provider and projected-instruction diff. `update` checks that the watcher is stopped and attempt exit files are collected, calls existing `fleet role` only for changed projections, then verifies resulting instruction bytes. It explicitly leaves conversation uptake unverified and requests no restart or new turn. These checks and statements should remain visible rather than be hidden behind a generic “deployed” label.

Verdict for the manifest direction: preserve this source → resolved configuration → inspect → explicit update seam. The example already demonstrates useful derivation without a language, extra registry or new runtime. A future manifest must earn its addition through reusable project declarations, clearer source refresh or multi-backend composition. Introduce hierarchy only when an actual multi-project responsibility/navigation case needs it; the current three-role setup does not require a parent tree.
