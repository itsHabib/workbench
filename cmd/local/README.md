# local

One Go binary that hands a single sub-task to a local model (Ollama) and
returns structured JSON. Pipe the input on stdin, pass a task prompt and a
JSON Schema, and get `{"source","reason","result"}` on stdout — `result` is
the model's schema-constrained reply, `source` is `local` or `cloud`, and
`reason` is set only when the escalate-on-uncertainty gate distrusted the
answer. This is the **agent co-processor**: an agent shells out to offload a
cheap filter / extract / shallow-classify, then falls back to its own
reasoning when the result comes back flagged. The agent is itself the cloud
model, so it *is* the escalation — the CLI wires no escalator of its own, and
a distrusted result is flagged, not fetched. Each invocation also appends a
best-effort JSONL record to a usage ledger; `local usage` rolls it up.

Tenant of the shared `local/` mechanism package: the binary is
`cmd/local`, a thin CLI (no `internal/`) over `local.Ask`. The seam is the
process boundary — stdin in, one JSON line on stdout, exit code out. Callers
never import it.

## Use

```sh
# `env` sidesteps the `local` shell builtin in bash/zsh.
echo "Error: connect ETIMEDOUT registry.npmjs.org:443" | \
  env local -prompt "Classify this CI line: flake, infra, or real-break." \
            -schema '{"type":"object","properties":{"class":{"type":"string"}},"required":["class"]}'

env local -prompt "..." -schema '@schema.json' < input.txt  # @file: schema only
env local -prompt "..." -schema '...' -min-confidence 0.7 < in      # flag below this
env local usage                                                     # roll up the ledger
env local usage -json                                               # same, as JSON
```

`LOCAL_USAGE_LOG` overrides the ledger path; otherwise `$XDG_STATE_HOME` or
`~/.local/state`, under `local/usage.jsonl`.

| Code | Meaning |
| ---: | --- |
| 0 | ran; result on stdout (a *flagged* result still exits 0) |
| 1 | schema/stdin read failure, the local call failed, or encode failed |
| 2 | missing `-prompt` or `-schema` (usage) |

Constraints that are design decisions, not omissions:

- **Needs Ollama running** on `localhost:11434`; the default model is
  `qwen2.5:7b` and calls run at `temperature: 0`. No Ollama, no answer — the
  error says so rather than degrading silently.
- **Escalate-on-uncertainty, not escalate-for-you.** With no escalator wired
  the gate sets `reason` and returns the local answer anyway; acting on the
  flag is the caller's job.
- **Confidence is recorded, never trusted.** `-min-confidence` reads a
  top-level `confidence` from the model's own output — the weakest signal,
  since a model can be confidently wrong. A mechanical verifier is the real
  trust signal, and that lives at the library call site, not this flag.
- **Verifiability, not difficulty, decides what goes local.** A task is safe
  here when its output is mechanically checkable or a wrong answer is
  harmless — not when it "looks easy."
- **Usage logging is best-effort** — every failure path returns silently, and
  it records the prompt (truncated), never the stdin input.

`local/README.md` is the canonical doc for the mechanism, the eval verdicts,
and the when-to-route-local rule. Read it before wiring this into a seam.
