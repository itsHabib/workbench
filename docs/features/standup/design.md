# Standup: a conversation that compiles to Fleet

**Status:** built to rung 1 on 2026-09-10; see "As built" at the end.
**Depends on:** [`org-fleet-boundary/spec.md`](../org-fleet-boundary/spec.md)
(one Fleet workflow, merged 2026-09-10) and the Fleet delivery contract in
[`cmd/fleet/README.md`](../../../cmd/fleet/README.md#delivery-and-lateness).
**Name:** `standup`. `huddle` is already the Slack-rooms MCP repo.

## The decision

The standup is a front end, not a system. The operator talks to one lead
agent; the conversation ends in a single JSON record; that record compiles,
field by field, into Fleet verbs that already exist. Nothing new holds state
between standups except the record itself.

```text
agenda  (derived: fleet work · receipts · mail · org status · open PRs)
   ↓
conversation  (text in the lead's session today; OpenAI Realtime voice later)
   ↓
standup record  (one JSON file, fail-closed until the operator confirms)
   ↓
apply  (fleet role/pool · fleet dispatch · fleet send · fleet decide)
   ↓
fleet watch delivers mail → launches `claude -p` in the seat
   ↓
next agenda  (rows late/done, receipts, reports, unanswered questions)
```

Roll Call already proved the shape and the trap. Its own verdict says the
deterministic prebrief supplied most of the value and the custom voice layer
(1,487 lines of transport) did not. This design keeps the three Roll Call
ideas that earned their place and rebuilds none of the rest:

- the agenda is derived from observed state, never narrated by the operator;
- the model never advances or commits on a vague affirmation; confirm is a
  phrase recognized by code, not by the model;
- the plan is read back before anything is written.

Not carried over: the lane cursor state machine, Jira/dossier connectors, the
conflicts engine, minutes export, profiles, and the Go Realtime client.

## The record

Lead with the format. Every field maps to exactly one verb (next section).

```json
{
  "schema": "standup.v1",
  "id": "standup-2026-09-10-1",
  "tenant": "mh",
  "lead": "lead:mh",
  "at": "2026-09-10T15:00:00Z",
  "agenda": "standup-2026-09-10-1",
  "agenda_digest": "sha256:…",
  "roles": [
    {
      "role": "author:switchboard",
      "kind": "author",
      "checkout": "/Users/mh/dev/switchboard",
      "seats": 1,
      "instructions": "Switchboard has no gate. Two AI review cycles, then merge with --match-head-commit.",
      "terms": { "scope": ["github:itsHabib/switchboard"], "supervisors": ["lead:mh"] }
    }
  ],
  "cards": [
    {
      "id": "standup-2026-09-10-1-c1",
      "repo": "itsHabib/ivy",
      "change": "#62",
      "as": "draft",
      "for": "steward:ivy-cnc",
      "seat": "ivy-author-1",
      "due": "4h",
      "brief": "Answer the two open review threads at the current head, re-run checks, report SHA."
    }
  ],
  "decisions": [
    { "kind": "rule", "subject": "ivy", "text": "content PRs stop at two fix-rounds; residuals go to one follow-up PR" }
  ],
  "deferred": [
    { "subject": "rung billing", "why": "needs product direction, not engineering" }
  ],
  "confirm": null,
  "next": "2026-09-11T09:00:00-07:00",
  "applied": []
}
```

Rules the record enforces:

- `confirm` is `null` until the operator says the phrase. `apply` refuses a
  record with `confirm: null`. The phrase is matched by code against the
  transcript or the typed line; the model cannot fill it in.
- `agenda` and `agenda_digest` pin the plan to the agenda it was made against.
  The digest is over the agenda's *projection*: what exists and how it ended
  (rows as open/done/dead, receipts, unacked mail ids, lanes, PRs with draft
  and mergeable state), never timestamps or whether hands are live this
  second. An apply whose live projection differs from the record's is a
  refusal with a diff, not a silent re-plan; `--force-stale` is the operator's
  override and is recorded. Same law as a verdict pinned to a SHA.
- Every `cards[].id` and `roles[].role` is a stable identity. Re-running
  `apply` with the same record is a no-op; a changed payload under the same
  id refuses. This is the Fleet retry contract, reused rather than re-derived.
- `applied[]` is written by `apply`, one entry per verb, with the verb's own
  receipt (dispatch row, mail id, pool result). It is the standup's only
  ledger. There is no second chain.
- The operator's word "card" is a `cards[]` entry, which is a Fleet dispatch
  row plus the mail that carries its brief. The role's *behavior* is the kind's
  `card.md` in `$FLEET_STATE/lanes/<kind>/`. Two different files, one word;
  this document says "card" for the work item and "kind card" for behavior.

## What each field compiles to

| Record field | Verb | Notes |
|---|---|---|
| `roles[]` with an existing `kind` | `fleet pool <checkout> <kind> <seats>` | Creates `<basename>-<kind>-<i>` seats and binds them in `roles.map`. Idempotent top-up. |
| `roles[]` with a new `kind` | write `$FLEET_STATE/lanes/<kind>/manifest.json` + `card.md`, then pool | The kind card is authored in the standup as prose and written as a file the operator can open. Manifest copied from `author`'s and edited, no new keys. |
| `roles[].instructions`, `terms` | staged as `fleet-role.v1` at the spec's path; also folded into every card brief for that role | The per-role store is proposed in the boundary spec, not implemented. The standup is its first writer; until Fleet startup reads it, the brief is the consumer. Say so in the file. |
| `cards[]` | `fleet dispatch <change> --as <as> --for <for> --due <due> --slot <seat> --brief "<brief>"` | `--as` is the receipt kind that means done (`draft`, `checks`, `reviews`, `ready` for authors; `verify` for a verifier seat, matching its manifest's `produces`). The row is the one authoritative assignment. |
| `cards[]` (launch) | `fleet send <seat> --id <card id> --kind task --subject "<change>" --body "<brief>"` | `fleet watch` delivers unacked mail to an absent seat by running the seat's `deliver.json` command once. No launcher is added; delivery is the launcher. |
| `decisions[]` | `fleet decide <rule\|park\|drop\|ignore> <subject> "<text>"` | Every session sees it at its next turn. |
| `deferred[]` | nothing | Re-raised verbatim in the next agenda until decided or dropped. |
| `confirm` | precondition | No write happens without it. |
| `next` | `fleet send lead:mh --id <id>/next --kind agenda --body "standup"` scheduled for that time | The lead's own delivery entry wakes it headless to build the agenda. |

The lead itself is a Fleet role, not a new Org lane:

```sh
fleet role ~/dev/lead-mh lead:mh          # binds the directory; kind `lead`
```

with a `deliver.json` entry so mail can wake it:

```json
{"lead:mh": {"cwd": "/Users/mh/dev/lead-mh",
             "cmd": ["claude", "-p", "{{prompt}}", "--model", "opus"],
             "LATE_TO": "human:mh"}}
```

Why not an Org charter for the lead: the boundary spec says new work takes the
Fleet continuity path and routine writes must not depend on holding an Org
incarnation. The record above is the continuity artifact. The existing Org
lanes (the ivy stewards, `supervisor:mh`, `lead:agentic-development`) stay
where they are and appear in the agenda through `org status`; the standup
reads them and dispatches to them, it never appends to their chains.

## The `lead` kind card

`supervisor` is the wrong kind: its card says read-mostly, escalate, never
act. The lead acts. A new kind, `$FLEET_STATE/lanes/lead/`, with a card whose
whole content is roughly:

- You run the standup and own the record. You may propose roles, cards,
  decisions, deferrals. You write nothing until `confirm` is set.
- After apply you own follow-through: answer seat questions from mail, one
  send per addressee per turn; re-dispatch a dead row; escalate a late row
  to the human only after the watcher's own report has gone unanswered.
- You never mint a gate grant, never merge outside the command gate emits,
  never flip visibility, never spend past the ceiling named in the record.
  Those are the human's, by design, and the card says so in one line each.
- End each turn with: record id · rows working/late/done · questions open ·
  next action.

Manifest denies mirror `author`'s (`gh pr merge`, reviewer edits) plus
`gate grant`, `gate judge`, `gh repo edit`.

## The conversation

One protocol, both surfaces.

1. **Agenda.** Derived, byte-bounded, cached to a file whose digest becomes
   `agenda_digest`. Sources, in order: `fleet work --json` (each row's state:
   dead · late · undeclared · working · idle · dispatched · done),
   `fleet receipts --since 24h --json`, `fleet mail --for lead:mh --unacked
   --json` (questions, escalations, the watcher's late reports), `org status`
   for the legacy cohorts, `gh pr list` per scoped repo, and the previous
   record's `deferred[]`. The lead reads this aloud or in prose, by row, and
   proposes one of: keep · re-dispatch · close · defer · new card.
2. **Proposals.** Each proposal is a diff to the draft record. The operator
   corrects in place. New roles get their kind card drafted here as prose.
3. **Readback.** The lead reads the whole draft back: roles, cards with due
   and boundary, decisions, deferrals, what stays untouched.
4. **Confirm.** The operator says the phrase. Code, not the model, sets
   `confirm`. Anything else, including "sounds good", leaves it `null`.
5. **Apply.** Deterministic. Each verb's receipt lands in `applied[]`.
6. **Readout.** What launched, what refused and why, when the next agenda is.

Facilitator rules, taken from Roll Call's prompt because they held up:
speak only from the agenda; never invent evidence, source access, or an
action item; a row with no observation has nothing to report, say so; never
turn a recommendation into a commitment; audio is live only, never retained.

## Two front ends, one record

**Text (rung 1, no new code beyond a skill).** Open Claude Code or Codex in
`~/dev/lead-mh`. Both already have `org-mcp` wired; Fleet is reachable as
`fleet mcp`. A `/standup` skill in cc-skills carries the protocol above and
runs the verbs itself, writing `applied[]` as it goes. The record lives at
`$FLEET_STATE/standup/<id>.json`.

**Voice (rung 2, OpenAI Realtime).** Copy the shape of
`ivy/spikes/voice-teacher/server.mjs`: a zero-dependency Node server that
mints an ephemeral Realtime client secret with the persona and the agenda
baked into the session instructions, exactly as the spike bakes in lecture
notes. Keys resolve from `~/dev/.keys`; the page never sees them. What the
spike does not have and this needs is function tools on the session:

| Tool | Does | Side effect |
|---|---|---|
| `agenda()` | returns the cached agenda by row | none |
| `propose(patch)` | applies a JSON patch to the draft record | writes the draft file |
| `readback()` | returns the draft as prose for the model to speak | none |

`confirm` is not a tool. The server watches the input transcript for the
phrase and sets the field itself, then runs `apply` and speaks the readout
through one final scripted response. The model can propose all day; it
cannot commit.

If the Realtime model plans badly, `propose` routes through the lead session
(`claude -p --resume <lead session>`), the same answer path the fleet
rehearsal on 2026-09-09 already used. The record does not change; only who
fills it in.

Realtime can also attach a remote MCP server directly as a tool. That is the
later, cleaner seam (surfaces work both ways), but it needs Fleet's MCP served
over HTTP rather than stdio. Not for rung 2.

## Autonomy ladder

| Level | Who decides | Who acts | What wakes the lead |
|---|---|---|---|
| 0, today | operator types every verb | operator | nothing |
| 1, rung 1–2 | lead proposes, operator confirms once | apply + watcher delivery | the operator opening the session |
| 2 | standing `fleet decide rule` decisions cover routine cards; the lead applies those without a confirm and parks the rest | same | scheduled `agenda` mail to `lead:mh`; late reports from `fleet watch` |
| 3 | the lead re-plans between standups from mail alone; the operator reads a readout on the phone | same | mail, and the escalation plane once it is stood up |

What never moves up the ladder: minting gate grants, `gate judge` and
`gate resolve`, repository visibility, spend above a named ceiling, deleting
data. Those stay a human's, and every card says so.

## What exists and what to build

| Piece | State | Work |
|---|---|---|
| Roles, seats, `roles.map` binding | `fleet role`, `fleet pool` on origin/main | none |
| Work rows | `fleet dispatch`, `fleet work`, `fleet board` | none |
| Launch | `fleet watch` + `deliver.json`; a watcher is running on this machine now | add the `lead:mh` entry |
| Follow-through | mail, receipts, lateness-as-mail | none |
| Decisions | `fleet decide` | none |
| Agenda | pieces exist as separate reads | one script that joins them and digests the result |
| Record + apply | none | `cmd/standup` in this repository, Go, beside `cmd/fleet`: `standup agenda`, `standup apply <record>`. ~300 lines. Rung 1 lets the skill run the verbs by hand; the binary is extracted when voice needs a non-agent caller. |
| `lead` kind | none | manifest + kind card, ~40 lines of prose |
| `/standup` skill | none | protocol + verb sequence, in cc-skills |
| Voice page | ivy spike has mint + page, no tools | tools bridge + confirm matcher + readout, ~250 lines |
| Per-role definition store | proposed in the boundary spec, unimplemented | standup writes it; nothing reads it yet |

Housekeeping before rung 1: the root workbench checkout is behind origin/main
(no `cmd/fleet` locally) and `~/.fleet/fleet.py` is the legacy Python. Pull
main and reinstall `fleet` from it, then confirm `fleet -h` shows `watch`.

## First rung: one afternoon, text only

1. Pull main, reinstall `fleet`. Write the `lead` kind card. `fleet role
   ~/dev/lead-mh lead:mh`. Add its `deliver.json` entry.
2. Write the agenda script. Run it against real state: three ivy steward
   lanes are LATE on the board today (#56, #60, #62), which is a real agenda.
3. Run the protocol in text in `~/dev/lead-mh`. Plan exactly two cards on
   those PRs. Confirm. Apply by hand-running the verbs the table names, and
   write `applied[]`.
4. Walk away. Read `fleet work` and `fleet receipts` an hour later.

Measured, not felt:

- operator verbs typed between `confirm` and the next agenda: target zero;
- cards that reached a receipt or a watcher report without the operator: 2 of 2;
- minutes from confirm to first observed hands.

Kill conditions, written now so the verdict is not argued later:

- If typing the record by hand is faster than the conversation, the standup
  collapses to `standup agenda` + `standup apply`. Keep the format, drop the
  talk. Same test Roll Call set for itself and never ran.
- Voice is not started until rung 1 passes twice on real work.
- If the watcher launches the same card twice or launches over a live seat,
  stop and fix delivery; do not add a launcher to the standup.

## As built, 2026-09-10

What landed the same day the design was written, and what did not.

| Piece | State |
|---|---|
| `cmd/standup` | `agenda`, `new`, `show`, `confirm`, `apply`; exit codes 0/1/2/4; tests drive the CLI against fake `fleet`/`org`/`gh` scripts. `~/.fleet/bin/standup` is built from this branch. |
| `lead` kind | `~/.fleet/lanes/lead/{manifest.json,card.md}`; canonical copy in cc-skills `docs/features/agent-fleet-rules/lanes/lead/`. Denies the gate write verbs, the merge verb and repository edits. |
| `lead:mh` | `~/dev/lead-mh`, a git checkout bound with `fleet role`; `roles.map` carries the line. |
| `/standup` skill | cc-skills `skills/standup/SKILL.md`, in both catalogs; the Lead row and card in `ROLES.md`. |
| Watcher | the stray `fleet watch` from the 2026-09-09 build had no delivery support (zero references to `deliver.json` in the binary); it was stopped and `~/.fleet/bin/fleet.main watch`, built from origin/main, runs in its place. |
| Delivery entries | staged at `~/dev/lead-mh/deliver.standup.json` for `lead:mh`, `ivy-author-1` and `fleet-demo-sandbox-author-1`. `~/.fleet/deliver.json` is operator-owned launch configuration and the harness refuses agent writes to it; the README there has the one merge command. Until it lands, the mail `apply` sends waits, which is the contract. |
| Config | `~/.fleet/standup/config.json`: lead `lead:mh`, phrase `ship it`, five repositories. |
| Real agenda | runs against live state from `~/dev/lead-mh`: 16 rows, 8 receipts, 15 lanes, 24 open PRs across five repositories; `fleet mail` reads as unavailable from a directory with no live session, which is the expected shape outside the lead's own session. |

The rehearsal (a headless lead session in `~/dev/lead-mh`, sonnet, driving the
binary in the sandbox repository) proved agenda → new → edit → show → confirm →
apply inside the lead's session, with `fleet mail` readable there. Its dispatch
was refused by Fleet because the branch existed nowhere yet, which settled the
branch contract below. The launch leg waits on the delivery entries.

Details the design under-specified and the build settled:

- **Identity is the launch directory, not the cwd.** A session started in
  `~/dev` that walks into `~/dev/lead-mh` gets a lease on that checkout's
  branch but is still refused by `fleet mail` ("no role in its launch
  directory"). Only a session launched in the lead's directory is the lead.
- **New work needs its branch before dispatch.** `fleet dispatch` refuses a
  branch with no local or origin head. `apply` now fetches and, when the branch
  is absent on origin, creates it there from the repository's default branch
  (`git push origin refs/remotes/origin/<default>:refs/heads/<branch>`), never
  switching the seat's checkout. A `#<n>` change skips both steps.
- **Receipts are not projected.** They are a sliding 24-hour window, so an
  agenda would go stale by the clock alone; a row's done state already carries
  what a receipt proved. Rows, unacked mail, lanes and open PRs are projected.
- **A stale refusal costs one readback.** `standup new --agenda <fresh id>
  --from <stale record>` carries roles, cards, decisions, deferrals and next
  into an unconfirmed record against the fresh agenda, re-keying card ids.

- **Card ids carry no slash** (`standup-2026-09-10-1-c1`), because the id is
  also the mail id and the dispatch file name.
- **The agenda must be built by the identity that applies.** `fleet mail`
  refuses a caller with no live session in its directory, so an agenda built
  from a stray shell projects "mail unavailable" while the lead session's
  rebuild projects the rows, and apply refuses as stale. Both steps run in
  the lead's session; the skill says so.

Not built: the per-role definition store (the record carries `instructions`
and `terms`; nothing reads them yet), the scheduled `next` mail, and voice.
