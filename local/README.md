# local — the shared local-model primitive

**One mechanism** for structured calls against a local model (Ollama), with an
escalate-on-uncertainty gate. Every seam and every agent sub-task is a *call
site* that supplies its own policy (prompt + schema); the mechanism is shared.
Don't build N local tools — build one primitive called N ways.

A top-level *mechanism* package under the workbench charter: it carries no
tool's decision logic, and CI leaf-checks it like `contracts`. Graduated
2026-07-06 from the `local-poc` incubator, migrated into workbench 2026-07-09;
its standalone history lives in the archived `pers/local` repo.

## Library

```go
import "github.com/itsHabib/workbench/local"

res, err := local.Ask(ctx, local.Req{Prompt, Input, Schema}, local.Opts{
    Verify:        func(json.RawMessage) bool { ... },              // strongest signal
    MinConfidence: 0.7,                                             // weakest signal
    Escalate:      func(ctx, req) (json.RawMessage, error) { ... }, // cloud fallback; nil = flag only
})
// res.Source is "local" or "cloud"; res.Reason says why it escalated.
```

- `local.Local(ctx, req)` — the raw structured Ollama call (prompt + schema → typed JSON).
- `local.Ask(ctx, req, opts)` — `Local` + the gate. Escalates when the **verifier fails**
  (strongest) or **confidence < MinConfidence** (weakest — self-reported confidence can be
  confidently wrong, so prefer a verifier). `Escalate` is injected, so this package has no cloud
  dependency; with none wired, a low-trust result is *flagged* (Reason set), not fetched.

`example_test.go` shows the primitive as a file-relevance filter with an
in-range verifier — the gate both ways.

## Commands

- **`cmd/local`** — the agent co-processor. Pipe input, get `{source, reason, result}`. An agent
  shells out to offload a cheap filter / extract / classify and falls back to its own reasoning
  when the result is flagged (the agent is itself the cloud escalation).
- **`cmd/eval`** — the local-exportability oracle. Scores a labeled JSONL dataset against a task's
  prompt + schema → a number + GO/NO-GO. Run it before wiring local into any seam; re-run it when
  the model or prompt changes. `-verbatim <field>` additionally checks the field is a substring of
  the input (the extract-shaped verifier, measured as a rate). `expected` may list acceptable
  answers separated by `|`.

```
go install github.com/itsHabib/workbench/cmd/local@latest
go install github.com/itsHabib/workbench/cmd/eval@latest
```

## Eval verdicts (the gate for keeping a task local)

Re-run these after any model or prompt change — the eval IS the trust signal.

- **CI-line classification: 10/10 → GO** (`cmd/eval/ci-lines.jsonl`, qwen2.5:7b, 2026-07-06,
  reproduced post-graduation with the canonical `ci-classifier` prompt).
- **Reviewer-comment severity extraction: 155/156 (99%) → GO** (dataset in the `local-poc`
  incubator, kept out of this repo).

**The prompt travels with the dataset.** The same 10 CI lines scored 7/10 under a bare one-line
prompt and 10/10 under the tuned prompt with per-bucket definitions. A `cmd/eval` verdict is only
meaningful next to the exact prompt + schema that produced it — check them in together, never
quote a score without them.

## The rule this encodes

A task is safe to run local when it's **verifiable or escalate-safe** — not when it "looks
rudimentary" (dense content breaks that). Extract / shallow-classify / retrieve go local; deep
judgment stays on cloud. The verifier is the real trust signal (verifier-fail > confidence), and
extract-shaped schemas (a verbatim-evidence quote) turn fragile classify tasks into verifiable
extract tasks. The gate makes the primitive safe to hand to any agent or seam: a hard input
escalates to cloud — or is flagged — never ships as confident garbage.
