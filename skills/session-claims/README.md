# session-claims v0 skills

The three personal skills implementing docs/features/session-claims/spec.md
(`/claim`, `/release`, `/roster`). This repo copy is the source of truth so any
machine can install them into its harness-specific personal skill directory:

```sh
cp -r skills/session-claims/claim skills/session-claims/release skills/session-claims/roster ~/.claude/skills/
# Codex:
cp -r skills/session-claims/claim skills/session-claims/release skills/session-claims/roster ~/.codex/skills/
```

The claims log lives at `~/.claude/session-claims/claims.jsonl` and is
machine-local by design (advisory, deletable). Keep edits here and re-copy —
the repo copy is the source of truth.
