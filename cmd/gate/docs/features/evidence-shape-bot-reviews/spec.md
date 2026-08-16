# gate — evidence shape for bot reviews posted as issue comments

**Status:** shipped in part (§4 landed), the rest is design.
**Date:** 2026-08-13
**Source:** friction sweep `gate/sweep-friction-2026-08-12` #4.
**Related:** `internal/evidence/panel.go` · `internal/verify/panel.go` ·
`contracts/reviewpanel` · ship #248 (the attestation bug).

## 1. Problem

Gate's panel-completeness rung asks exactly one question per required
reviewer: *did this reviewer review the exact head under judgment?* The
directly answering artifact is a formal GitHub review whose `commit_id`
equals the head. Several providers never submit one — they publish their
whole review as an **issue comment**, which GitHub gives no commit anchor.

The panel then reports the reviewer `missing`, and the run parks. The operator
opens the PR, reads a final comment that plainly says the review is done, and
resolves the escalation by hand. That is manual judgment spent on evidence
**shape**, not on defects — the expensive kind of park, because it is
identical every time and teaches nothing.

## 2. The line that must not move

The tempting fix is to read the comment's verdict: if the bot's last comment
says *Approve* / *LGTM* / *ready to merge*, count it. Gate refuses this, for
two independent reasons:

1. **Prose is not authority.** The body is model output. A model can be
   argued into an approving sentence; the PR diff itself is part of the model's
   input. Any rule keyed on the words in a review makes the review's own
   content the thing that clears the gate.
2. **A verdict with no anchor cannot state which tree it applies to.** Even a
   truthful "approved" says nothing about *which* commit was read. Gate's whole
   claim is exact-head, so an unanchored verdict is not weaker evidence — it is
   evidence of a different proposition.

So the classification path is never "recognize the verdict". It is:
**recognize a harness-emitted, head-bound sentinel**, and let the verdict be
handled downstream as findings.

## 3. The completion ladder

`panelCompletion` tries these in descending directness. Each rung is
head-bound; none reads a verdict.

| Rung | Evidence | Head anchor | Recorded state |
|---|---|---|---|
| 1 | Formal GitHub review by the reviewer | `review.commit_id == head` | the review state |
| 2 | Codex connector issue comment | the connector's `**Reviewed commit:** \`<sha10>\`` line | `CLEAN` / `COMMENTED` |
| 3 | Repository-workflow attestation comment | `**Reviewed commit:** \`<sha40>\`` == head, whole-body match | `COMMENTED` |

Rung 2's anchor is emitted by the **connector harness**, not chosen by the
review; rung 3's is emitted by the repository's own Actions token
(`github-actions[bot]`, a login a comment cannot spoof) and matched against
the *entire* comment body, so a review quoting the format at any offset
clears nothing.

**Head-pinning caveats, stated explicitly:**

- Rung 2 matches a **10-hex prefix** as a prefix of the judged head. That is
  the connector's own format; it is not a full-SHA equality and carries the
  (negligible, but real) ambiguity of a short SHA. Rung 3 requires all 40.
- Rung 3 attests only that *a workflow ran the reviewer against that tree* —
  it carries no claim about what the reviewer concluded. Cleanliness is a
  separate fact from completion (§4).
- Every rung is a completion signal only. None of them authorizes a merge;
  readiness, findings, and the tier ladder all still run.

## 4. Completion is not cleanliness (landed)

Rung 2 previously required the connector's *no-findings* opener
(`Codex Review: Didn't find any major issues.`). That conflated two facts and
reproduced friction #4 in its worst form: a Codex review that did its job and
reported a P1 was recorded as *"codex never reviewed this head"*, so the run
parked on shape while the actual findings sat unread in the same comment.

`codexIssueCompletion` now accepts any comment opening with the connector's
`Codex Review:` framing that carries a head-matching reviewed-commit line, and
records `CLEAN` only for the no-findings variant, `COMMENTED` otherwise. The
findings themselves keep parking the run on their own merits — the
review-consolidation verifier reads the same comments and escalates on
anything actionable. What changed is *why* a run parks: defects, not shape.

## 5. The sweep-layer normalization option (not built)

For a provider with neither a formal review nor a harness sentinel, the
remaining safe path is for the layer that *ran* the review to normalize it:
the workflow checks out a head, runs the reviewer against exactly that tree,
and posts a rung-3 attestation naming the reviewer and
`git rev-parse HEAD`. Gate needs no per-provider knowledge for this — rung 3
is already generic over the reviewer name.

Requirements on such a workflow:

- Compute the SHA from the **checked-out tree it reviewed**, after the review
  body is fully produced and validated — never from an event payload that may
  describe a different head.
- Post under the repository's Actions token. An attestation from any other
  author is not evidence.
- Post exactly the rung-3 body and nothing else. Rung 3 matches whole-body.

## 6. What an agent must never do (ship #248)

Do **not** have an agent run `gh pr review` to "formalize" a bot's issue
comment into a review decision. GitHub pins a submitted review to the
**live head at submission time**, not to the head that was actually reviewed.
If the branch moved between the review and the formalization — which is the
normal case, since the formalization is a response to a park — the resulting
artifact asserts that the reviewed tree is a tree nobody read. That is
manufactured exact-head evidence, and gate's panel rung would consume it as
rung-1, the most direct rung there is.

The only safe formalization is §5: workflow-authored attestation of
`git rev-parse HEAD` after full-body validation.

## 7. Verification

`go test ./cmd/gate/... ./contracts/...` covers rung 2 completion for both the
clean and the with-findings shapes, and the refusals: stale SHA, malformed
anchor, wrong actor, a non-Codex-framed body, a prose verdict with no anchor,
and an inline comment. Rung 3 has the equivalent set in `panel_test.go`.
