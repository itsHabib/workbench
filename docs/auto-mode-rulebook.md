# Auto-mode rulebook — the copy-able half

`docs/auto-mode-defaults.md` is the doctrine: the contract, the six
defaults, the reasoning. This file is the executable half: literal
settings, a working guard hook, and the verification steps — written so a
person (or an agent pointed at this file) can reproduce the setup exactly
and then adapt it.

Honesty first: this is a *reference implementation* of the doctrine. The
production guard on the operator's machine carries additional
machine-specific rules that stay private; nothing below depends on them.
Syntax is current for Claude Code as of 2026-07-31 — after installing,
verify with the `/floor` skill (renders the effective merged rulebook)
rather than trusting this file's snapshot.

---

## 1. The three settings layers

| File | Job | Discipline |
|---|---|---|
| `~/.claude/settings.json` | Personal defaults: the tier-1 allow floor, the tier-3 deny list, hook registrations | Designed, edited deliberately |
| `<repo>/.claude/settings.json` | The repo's rulebook, checked in and reviewed by PR | The rulebook governs itself |
| `<repo>/.claude/settings.local.json` | Scratch written by in-the-moment approvals | An inbox — drained weekly into the designed layers or deleted |

The failure mode the third row guards: wildcards accreted in scratch
(`Bash(gh *)`) silently undo per-verb curation done above. Drain it on a
cadence; a long list of historical approvals is not a security model.

## 2. Global settings — the allow floor and deny list

`~/.claude/settings.json`, trimmed to the shape that matters:

```json
{
  "permissions": {
    "allow": [
      "Bash(git status:*)",
      "Bash(git log:*)",
      "Bash(git diff:*)",
      "Bash(git show:*)",
      "Bash(git branch:*)",
      "Bash(git worktree list:*)",
      "Bash(gh pr view:*)",
      "Bash(gh pr checks:*)",
      "Bash(gh pr diff:*)",
      "Bash(gh run view:*)",
      "Bash(go build:*)",
      "Bash(go vet:*)",
      "Bash(go test:*)",
      "Bash(gofmt:*)",
      "Bash(golangci-lint run:*)"
    ],
    "deny": [
      "Read(~/.ssh/**)",
      "Read(~/.aws/**)",
      "Read(**/*.pem)",
      "Read(**/id_rsa*)",
      "Read(**/.env)",
      "Read(**/.env.*)",
      "Read(~/dev/gate/**)",
      "Read(~/dev/.keys/**)",
      "Bash(gh repo delete:*)",
      "Bash(gh auth token:*)"
    ]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "$HOME/.claude/hooks/pretool-guard.sh" }
        ]
      }
    ]
  }
}
```

Mapping to the doctrine's tiers:

| Tier | Mechanism | Above, concretely |
|---|---|---|
| 1 — free / read-only | `allow` entries; should never prompt | the git/gh read verbs, build/test |
| 2 — consequential, auditable | `allow` + observability hooks | `gh pr create` may be allowed *because* merge is separately gated |
| 3 — irreversible / authority-bearing | `deny` + guard hook + external gates | credential paths, repo delete, and everything the guard refuses below |

Two deliberate choices worth copying. Credential and gate-state *reads*
are denied outright — an agent that can read the mint keys makes
"human-minted grant" a fiction, so the deny list is what makes the
capability model true. And the deny list is short: shape-based refusal
belongs in the guard, where each rule can carry a remedy.

## 3. The pretool guard — a working tier-3 floor

`~/.claude/hooks/pretool-guard.sh`, executable. Claude Code invokes it
before every Bash call with JSON on stdin; exit `0` allows, exit `2`
blocks and feeds stderr back to the model as the reason. Every refusal
prints its remedy — fail closed, and make closed cheap.

```bash
#!/usr/bin/env bash
# Tier-3 floor: refuse command shapes with no sanctioned use, with remedies.
set -euo pipefail

cmd="$(jq -r '.tool_input.command // empty' 2>/dev/null || true)"
[ -z "$cmd" ] && exit 0

deny() { echo "pretool-guard: $1" >&2; exit 2; }

# force push — no sanctioned use from a governed session
if echo "$cmd" | grep -qE '(git|gh)[^|;]*push[^|;]*(--force|--force-with-lease|-f( |$))'; then
  deny "force push refused. Rebase onto the remote or hand the conflict to the operator."
fi

# repository destruction / visibility flips
if echo "$cmd" | grep -qE 'gh repo (delete|edit[^|;]*--visibility)'; then
  deny "repo delete/visibility refused. Operator-only action."
fi

# credential, key, and gate-state touches (read or write, any verb)
if echo "$cmd" | grep -qE '(\.ssh/|\.aws/|\.pem|id_rsa|/dev/gate/|/dev/\.keys/|\.env( |$))'; then
  deny "credential or authority-state path refused. Use custody or ask the operator."
fi

# bare merge — merge authority belongs to gate; only its emitted shape passes
if echo "$cmd" | grep -qE 'gh pr merge' && ! echo "$cmd" | grep -q -- '--match-head-commit'; then
  deny "bare merge refused. Run gate; merge only with the exact head-pinned command it emits."
fi

# admin bypass — never
if echo "$cmd" | grep -qE 'gh pr merge[^|;]*--admin'; then
  deny "--admin merge refused. There is no sanctioned admin bypass; resolve the failing check."
fi

exit 0
```

Notes that keep this honest:

- The bare-merge rule enforces *shape*, not policy — merge policy lives in
  gate, and the doctrine's merge-boundary section says exactly where the
  structural close stands (the executor App path).
- Regexes over command text are a floor, not a proof: an agent could
  construct an unmatched shape. The floor raises the bar and buys an
  artifact trail; the guarantees live in the external gates behind it.
- The guard runs in *every* permission mode — that's the point of a hook
  over an instruction.

## 4. Post-tool hooks — reflexes that write state

Same registration shape, `PostToolUse`. The pattern (one concrete example,
shapes for the rest): capture a reliable local fact, append one artifact
line, never decide anything.

```bash
#!/usr/bin/env bash
# posttool-pr-link.sh — after a successful `gh pr create`, record the PR URL.
set -euo pipefail
in="$(cat)"
cmd="$(jq -r '.tool_input.command // empty' <<<"$in")"
echo "$cmd" | grep -q 'gh pr create' || exit 0
url="$(jq -r '.tool_response // ""' <<<"$in" | grep -oE 'https://github.com/[^ ]+/pull/[0-9]+' | head -1)"
[ -z "$url" ] && exit 0
printf '{"event":"pr_created","url":"%s","ts":"%s"}\n' "$url" "$(date -u +%FT%TZ)" \
  >> "$HOME/dev/artifacts/pr-events.ndjson"
exit 0
```

The production set follows this pattern five times: PR creation links the
PR to its task, a gate decision records its verdict artifact, a merge
records the receipt and closes the task, dispatch records run evidence,
and a terminal read links final run state back to project memory. Hooks
capture facts; choreography stays in skills and engines.

## 5. Install and verify

```sh
mkdir -p ~/.claude/hooks
# copy the guard above into place
chmod +x ~/.claude/hooks/pretool-guard.sh

# smoke-test the guard directly — expect exit 2 and a remedy on stderr
echo '{"tool_input":{"command":"git push --force"}}' | ~/.claude/hooks/pretool-guard.sh; echo "exit=$?"
echo '{"tool_input":{"command":"gh pr merge 42"}}'   | ~/.claude/hooks/pretool-guard.sh; echo "exit=$?"
echo '{"tool_input":{"command":"git status"}}'       | ~/.claude/hooks/pretool-guard.sh; echo "exit=$?"
```

Then, inside a session: run `/floor` to render the effective merged
rulebook and confirm the layers compose the way you think they do — the
merge of global, project, and local is what actually governs, and no
single file shows it.

## 6. Tuning without softening

Weekly, per the doctrine's default 6:

1. Drain `settings.local.json` — promote repeated approvals into designed
   allow rules (per-verb, both shells on dual-shell platforms) or delete
   them.
2. Mine the guard's refusals and gate's park log for the most frequent
   cause; promote the top one into a reviewed deterministic rule.
3. Never answer a repeated annoyance with a model override — that quietly
   makes the model the authority, which is the one thing this whole file
   exists to prevent.
