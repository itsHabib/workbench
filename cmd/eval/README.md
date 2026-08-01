# eval

One Go binary that measures whether a task can be exported to the local
model. Give it a task prompt, an output JSON Schema, and a labeled JSONL
dataset of `{"input","expected"}` rows; it runs every row through the local
primitive, compares the named output field against `expected`, prints a
per-row pass/fail table, and ends with `score: N/M (P%)` plus a verdict —
`GO for local` at 80% or above, otherwise `NO-GO for local — keep on cloud
(or raise the model tier)`. `expected` may list acceptable answers separated
by `|`. `-verbatim <field>` additionally checks that the named field appears
in the input after normalization (lowercased, punctuation stripped) and
reports that as its own rate — the extract-shaped verifier measured as a
number, because an extraction that is not a quote of its source is a
confabulation even when the scored field is right. This is the
**local-exportability oracle**: run it before wiring local into any seam.

Tenant of the shared `local/` mechanism package: the binary is `cmd/eval`, a
thin CLI (no `internal/`) over `local.Local` — the raw structured call, with
the escalate gate deliberately out of the loop so the score measures the
model, not the fallback. The seam is the process boundary: stdout table, or
`-jsonl` rows for downstream scoring.

## Use

```sh
eval -prompt "@cmd/gate/docs/features/ci-classify/eval/ci-classify.prompt.txt" \
     -schema "@cmd/gate/docs/features/ci-classify/eval/ci-classify.schema.json" \
     -dataset cmd/eval/ci-lines.jsonl -field bucket        # table + score + verdict

eval ... -verbatim evidence      # also score "is this field a quote of the input?"
eval ... -model qwen2.5:14b      # re-gate a NO-GO task at a higher tier
eval ... -jsonl                  # one {meta,expected,output} line per row, no table
```

| Code | Meaning |
| ---: | --- |
| 0 | the run completed — **including a NO-GO verdict**; read the verdict, not the code |
| 1 | prompt/schema/dataset could not be read or parsed |
| 2 | missing `-prompt`, `-schema`, `-dataset`, or `-field` (usage) |

The repo's own measured record: **10/10** on CI-line classification and
**155/156 (99%)** on reviewer-comment severity extraction — both GO. The same
class of model was a NO-GO as a final review judge. Those numbers are only
meaningful next to the exact prompt and schema that produced them — the same
10 CI lines scored 7/10 under a bare one-line prompt.

Constraints that are design decisions, not omissions:

- **Needs Ollama running** on `localhost:11434`; default `qwen2.5:7b` at
  `temperature: 0`. A row whose call fails prints `ERR` and is skipped — it
  is not counted as a pass, so an unreachable model drags the score down
  rather than faking one.
- **Verifiability, not difficulty, decides what goes local.** A high score is
  the evidence; "the task looks rudimentary" is not.
- **The threshold is a floor, not a blessing.** 80% earns GO here; a seam
  that cannot absorb a wrong answer should demand more, or a verifier.
- **Confidence is recorded, never trusted.** `eval` scores against known
  answers only; a model's self-reported confidence is not part of the score.

`local/README.md` is the canonical doc for the mechanism, the eval verdicts,
and the when-to-route-local rule.
