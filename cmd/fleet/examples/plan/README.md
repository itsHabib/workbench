# Fleet work plan/apply POC

Talk through work → write `fleet-work.v0` JSON → review `fleet plan` → approve its
exact digest → `fleet apply`. The POC records **unseated ownership rows**, using
Fleet's existing dispatch store and lock. It never launches a worker, sends mail,
checks out branches or claims completion. No new journal or reconciler.

## Try it without touching your Fleet

From this checkout, with Go, Git and Python 3:

```sh
go build -o /tmp/fleet-plan-poc ./cmd/fleet
python3 cmd/fleet/examples/plan/demo.py /tmp/fleet-plan-poc
```

The script creates three local Git repositories (Ivy, Rooms, RoxIQ names only;
no remote), isolated `FLEET_STATE` and `ORG_STATE`, and retained artifacts in a
printed temporary directory. It runs the real binary. It demonstrates:

1. Readable preview and saved normalized plan; preview writes no Fleet state.
2. Ivy recorded, intervening Rooms conflict, RoxIQ not attempted.
3. Retry preserves Ivy's exact record bytes and records the remaining work.
4. Changed brief **and** deadline with unchanged owner produce an explicit conflict.

The Go tests additionally inject a publication failure after the first success,
exercise concurrent competing plans, branch drift/replanning, tampered digest,
wrong state directory, read-only apply and existing-row drift. They do not launch
models. `go test -race ./cmd/fleet/internal/verbs` runs these with existing verbs.

## Input and commands

```json
{
  "schema": "fleet-work.v0",
  "work": [{
    "name": "ivy-notes",
    "repo": "/absolute/path/to/local/ivy",
    "change": "codex/ivy-notes",
    "for": "author:ivy",
    "as": "draft",
    "brief": "Clarify the first learner exercise; stop at a draft PR.",
    "due_at": "2030-01-01T12:00:00Z"
  }]
}
```

Branches must already exist locally. Deadlines are absolute, so retries cannot
extend them. Unknown fields, including `seat`, reject. `for` is accountable role;
`as` is Fleet's existing receipt relationship, not a worker state.

```sh
fleet plan morning.json --out morning-plan.json
# Read the printed changes and exact digest before authorizing this revision.
fleet apply morning-plan.json --expect-digest <printed-digest>
```

Plan files are created exclusively and never overwrite input or previous plans.
The plan binds the current state directory, caller, each target's repository
identity and local head, desired fields, and the existing row fingerprint (or
absence). The digest is version binding, not authentication or a grant. A voice
agent must obtain actual authorization for the displayed revision. This PR
provides the CLI/file seam; no new MCP tools or installed configuration.

`plan` reports `add`, `keep`, or `conflict`; a conflict plan cannot apply. Keeping
an existing row requires matching owner, brief, deadline, placement and reply
context, not merely matching owner. Receipt evidence and activity are observed by
existing Fleet views; recording a row proves neither work acceptance nor success.

`apply` checks conditions inside the same dispatch lock as ordinary dispatch.
Each action commits one atomic JSON replacement. The row carries `plan_digest`
and `plan_work`, allowing replay to return the existing result without touching
its timestamp, deadline or original head. No separate result file is needed to
recover a lost response. There is no redundant order send after declaration.

Results are JSON: `recorded`, `already recorded`, `kept`, `conflict`, `unknown`,
`not attempted`. A publication error is conservatively unknown. The first failure
stops the remainder and preserves prior successes. Conditions are checked per
action, so even drift present before invocation can yield partial application;
this is intentionally not a transaction across the batch. Replan to review changed
intent or head, and keep prior exact matches. Unrelated agenda activity does not
invalidate replay.

## Limits and assessment decision

- No seat placement, worker launch, automatic updates/deletes, takeover, resource
  acquisition, remote ref resolution, continuous reconciliation or compute/Rooms
  provisioning. Direct `dispatch`, `assign`, `request` semantics are unchanged.
- Replay is supported while the original row is retained. Explicit deletion via
  existing commands removes that evidence; an old add plan can then create it
  again. This POC has no tombstones or indefinite deduplication promise. Do not
  reuse old plans after retiring their rows.
- Dispatch locks serialize Fleet writers, not arbitrary file editors or Git
  commits. Head checks do not freeze Git; the recorded head names the reviewed
  revision, not a promise the branch will remain there.
- Existing standup's global-agenda retry rejection and same-owner skip remain in
  #331's separate compiler. This new path demonstrates the replacement semantics;
  switching standup to consume it is a later integration.
- Seated dispatch still has the known assignment-before-work-row failure window.
  This PR never enters it. Canonical seat publication must be fixed before extending
  this contract to wake workers; an extra journal would hide rather than fix it.
- Real desktop voice, interrupted voice confirmation, model execution, status UI
  usability and receipt-backed completion remain unproven.

Assess whether this small intent/preview/result boundary is useful enough to wire
into standup. If yes, the next slice is canonical placement and a bounded live
voice-to-worker trial, not a general desired-state platform.

## Language alternatives retained for assessment

`languages/morning.json`, `.hcl`, and `.fleet` preserve the earlier hypothetical
three-project example, including proposed seats. **These are comparison examples,
not accepted CLI inputs.** The runnable demo above emits the supported v0 schema.
All three examples were decoded into equivalent normalized JSON in the exploration.
HCL has useful named blocks and diagnostics; reference/dependency semantics would
still be Fleet's responsibility. The tiny invented DSL reduces punctuation but
needs its own grammar, errors, formatter and evolution policy. JSON is provisional:
voice/MCP can generate it and humans can review rendered effects. Reconsider HCL
if repeated human editing justifies a language dependency; no parser dependency
has been added here.
