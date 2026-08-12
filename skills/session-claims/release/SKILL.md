---
name: release
description: Release the current session's claims in the session-claims log — the inverse of /claim. Use when the operator says "/release", "release this session", "done with this session", "hand this off", optionally with a note ("/release <note>"). The note lands in the release event as the generated handoff (replaces the hand-written resume-tomorrow.md genre). Spec: workbench docs/features/session-claims/spec.md.
---

# /release — release this session's claims

Append one `release` event to `~/.claude/session-claims/claims.jsonl` closing
THIS session's open claims. Append-only: never edit prior lines.

## Steps

1. `session`: this session's id — the UUID in your scratchpad directory path.
2. If the operator gave a note, include it as `note`. If they asked for a
   handoff (or the note is empty and the session has meaningful open loops),
   draft a 1–3 sentence note yourself summarizing state + next steps, show it,
   and include it.
3. Append:
   ```sh
   mkdir -p ~/.claude/session-claims && printf '%s\n' '<the JSON>' >> ~/.claude/session-claims/claims.jsonl
   ```
4. Confirm in one line: `released: session <first 8 chars>…`.

## Event shape

```json
{"ts":"2026-08-06T18:40:00Z","session":"<uuid>","event":"release","note":"PR #73 green, awaiting review; next: fold panel findings."}
```

A release closes all of the session's open claims — no per-claim targeting in v0.
Releasing a session with no prior claim is fine (append it anyway; harmless).
