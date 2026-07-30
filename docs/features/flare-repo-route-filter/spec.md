**Status**: draft
**Owner**: agent:codex
**Date**: 2026-07-28
**Related**: dossier task `flare-repo-route-filter` (`tsk_01KYP59QGHDCVN33DMV79CXA4E`)

# Flare per-repository route filter

## Goal

Let an operator suppress Flare delivery for selected repositories without
disabling Flare globally or splitting a shared producer log.

## Behavior

- Add a `repo` predicate to Flare's existing route match shape.
- Match it against the event's repository field using the same empty-pattern
  and pipe-alternation semantics as the other match fields.
- Preserve first-match routing. This configuration must drop one repository
  while allowing later routes or catch-all delivery for other repositories:

  ```json
  {
    "routes": [
      {
        "match": {"repo": "example/work-repo"},
        "channel": "drop"
      }
    ]
  }
  ```

- An event without repository metadata must not match a repository-specific
  route.
- Preserve throttling, cursor, journal, and delivery behavior.

## Acceptance

- Focused tests prove exact repository matching and existing alternation
  semantics.
- A route fixture proves one repository is dropped while another reaches the
  configured catch-all channel.
- `cmd/flare/docs/DESIGN.md` documents the `repo` predicate in the authoritative
  route-table contract.
- Flare operations documentation includes a sanitized per-repository disable
  example.
- No real work repository names, Slack identifiers, credentials, or rehearsal
  material appear in the change.

## Test plan

Run:

```text
gofmt -l .
go vet ./...
golangci-lint run ./...
go test ./...
```

## Non-goals

- Repository globbing or regular expressions.
- Changing event producers or separating their logs.
- A global Flare enable/disable switch.
