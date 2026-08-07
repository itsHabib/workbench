# session-claims v0 skills

The three personal skills implementing docs/features/session-claims/spec.md
(/claim, /release, /roster). Canonical install location is the personal skill
dir — this copy exists so any machine can pull them from the repo:

```sh
cp -r skills/session-claims/claim skills/session-claims/release skills/session-claims/roster ~/.claude/skills/
```

The claims log lives at `~/.claude/session-claims/claims.jsonl` and is
machine-local by design (advisory, deletable). Keep edits here and re-copy —
the repo copy is the source of truth.
