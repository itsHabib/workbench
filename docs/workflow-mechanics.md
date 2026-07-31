# Workflow mechanics — how the work actually runs on disk

The low-level companion to `docs/workbench-101.md` (the system tour) and
`docs/lessons.md` (the rules and war stories). This doc is about the daily
machinery: which instruction layers act on an agent and how strongly, how a
task picks its entry path, what each workflow skill actually obligates, and
how behavior migrates from prose to code. Everything here cites the public
[skills repo](https://github.com/itsHabib/skills) or this repo; the private
skill catalog behind it is described only by shape.

Status honesty: this describes practiced workflow as of 2026-07-31. Where a
piece is proposal or measurement-gated (tier-routed review, for one), it
says so.

---

## 1. The instruction stack

"Prompting" flattens what is really a stack of layers with different
strengths and different failure modes. From weakest to strongest:

| Layer | Role | Strength / failure mode |
|---|---|---|
| Conversation prompt | Immediate intent | Flexible; easy to reinterpret or lose in a long context |
| Global `CLAUDE.md` / `AGENTS.md` | Cross-repo operating law | Loaded broadly; right for stable universal boundaries |
| Repo + scoped guidance | House style, checks, invariants | Reviewed with code; drifts if duplicated |
| Invoked skill | Workflow choreography | Loaded into active instructions — followed far more reliably than casual context |
| Memory | Past feedback, decisions, references | Valuable, can be stale; never an authority by itself |
| Settings / rules | Allowed and denied tool shapes | Mechanical harness policy; accumulates stale approvals |
| Hooks | Reflexes before/after tool use | Run whether or not the agent remembers |
| Engines | Durable state machines | Own retries, transitions, exact subjects, recovery |
| External gates / brokers | Authority and credential boundaries | Strongest; the agent cannot talk its way around them |

The load-bearing distinction is the skill layer. A skill changes the
*physics* of a session: it enters the agent's instructions, not its
reading pile, and it can carry choreography across many tools. That is
why the workflow lives in skills rather than in ever-longer prompts — and
why the `/floor` skill exists to render the *effective* harness rulebook
for a machine/project, since what a session may actually do is the merge
of several layers no one file shows.

Precedence when sources conflict, highest first: enforced external
boundary and executable code; current global harness instructions;
checked-in repo guidance and configuration; the invoked skill; recent
dated design docs; memory; old drafts. Higher means more authoritative,
not more interesting.

## 2. Choosing the entry path

Planning depth follows the work. The decision is roughly:

```
one bounded task, clear outcome          -> /drive
several settled tasks, designed already  -> /work-driver-seed -> -prep -> /work-driver
large or ambiguous initiative            -> /tdd first, then seed/prep/drive
just review existing work                -> /review-coordinator or /code-review
```

`/tdd` is not ceremony for routine work; it's for when architecture,
failure model, data shape, or rollout genuinely deserve review before
implementation. The older habit — design doc for everything substantial —
became wasteful as models improved.

PR sizing has working bands, in weighted LOC (tests count half;
docs/generated/config excluded from the weight but not from review
attention): under 500 excellent, under 700 the practical target, under
1000 a stretch. The system once over-split into many 90-line PRs; current
preference is fewer, coherent PRs near the 500–700 band when changes share
a natural seam.

The single-task boundary matters in the other direction too: the moment a
`/drive` session starts growing extra PRs, it should have been a
work-driver batch. The skill's own anti-triggers say exactly this.

## 3. `/drive` — one task to the safe boundary

The single-task skill's stable contract, compressed from its own text:

1. Pin the task and what "done" means — one exchange, then go.
2. Ground in the repo's established patterns before writing anything
   (global guidance, repo guidance, nearest scoped docs, design charter).
3. Isolate: an agent works in its own worktree, never the operator's
   dirty checkout.
4. Implement to local green using the repo's complete documented checks.
5. Exercise a runtime/E2E path when the change has one.
6. One PR, repo format, repository-owned reviewer roster (`review.panel`
   is used exactly — never a remembered or hard-coded roster).
7. Track review completion against the exact current head: `requested`,
   `completed`, `pending`, `missing` are different states.
8. Wait `settle_minutes` before gate; a required reviewer still missing is
   a stop, not a shrug.
9. Fold every actionable finding smallest-safe, re-verify after every head
   change; large/architectural findings become follow-ups, not scope creep.
10. Stop at the safe boundary: in a gated repo, run gate and hand off with
    the emitted merge command; never mint grants, never merge unasked.
11. Leave the trail — friction entries and a zero-context handoff.

Two laws sit above all of that in the skill itself: never cross human-only
boundaries, and report honestly — "done" only when the stated criteria are
met, merged means actually merged.

## 4. The batch chain: seed → prep → drive

**`/work-driver-seed`** turns a described chunk into a dossier phase plus
PR-sized, dependency-ordered task docs — explicit files, acceptance
signals, and a recommended model/effort per task. It's the front end for
work too big for one task and too small for a `/tdd`.

**`/work-driver-prep`** turns N dossier tasks into one runnable spec doc
per task (scope / goal / behavior / acceptance / test plan), scans specs
for file overlap and dependency language, groups streams into
parallel-safe batches, and emits ready `/work-driver` invocations.
Conflict detection is deliberately conservative and surfaced for operator
correction rather than silently trusted.

**`/work-driver`** executes the manifest. The division of labor is the
whole point: **the engine owns the loop** — import → dispatch → poll →
judgment → land → record, as durable state in ship — **and the skill owns
the policy**: review-cycle caps, strategy selection, the merge call. That
split is the worked example of prose-to-code (§8): the loop used to be
skill prose the model re-derived every run; now it's engine code, and the
skill shrank to opinions.

The engine owning recovery is not hypothetical caution: the friction log
records a confirmed pattern (three runs, more than one model) of cloud-SDK
streams dying mid-edit during long tool calls with the work complete but
uncommitted. Transport death is a normal state a durable engine resumes
from, not an anomaly a skill can prose its way around.

**`--engine session`** relocates execution without changing the artifact
boundaries: the current chat becomes a thin state-machine driver,
delegated subagents implement each task in isolated worktrees, and a
two-ledger model (parent ledger + child ledgers) keeps the run resumable
while the parent holds only structured task state and PR URLs — heavy
diff/test context stays in the children. Used when cloud execution is
unsuitable or the operator wants local control. It is a different
placement, not a lesser path.

## 5. Review machinery

**`/review-coordinator`** is the judge over the AI reviewer panel (Codex,
Claude, Cursor Bugbot, Copilot), and its details are where the discipline
lives:

- It resolves the PR's current `head_sha` first; everything binds to it.
- Settle policy: fire when the fast pack has posted on the current head
  (or a short timeout), record slow bots as `pending`. But
  **merge-patience** keys on green-while-slow-pending: a `go` verdict is
  withheld while a slow bot is still pending on the current head — the
  fast path governs iteration, the patient path governs merge.
- Severity normalization is a *function, not a menu*: each bot-native
  severity maps to exactly one shared level, because an ambiguous mapping
  lets an agent file a real blocker under the non-gating row.
- Findings group by location, rank by agreement, and bucket into exactly
  two tiers; the output is a JSON verdict plus a sticky comment, both
  carrying the head SHA and cycle.
- A local pre-pass (`/review-digest`) extracts each comment's headline and
  stated severity with the local model before frontier judgment reads
  anything — the volume rung doing the clerical half.

The producer boundary matters as much as the judgment: consolidated
findings cross to execution as a versioned artifact (exact head,
requested/completed/missing reviewers, sourced findings, stable identity),
and ship validates it again — refusing stale heads and duplicate
consumption. A clean panel does not manufacture an empty artifact.

**Tier-aware review** is measurement-gated, not policy. The deterministic
risk floor (`cmd/triage`) and per-repo path overrides exist; spend
telemetry per cycle and per bot exists; the T0–T3 reviewer mapping is a
proposal. Measure first, cut second: a 30-day retrospective showed review
effort barely differentiating by risk, so the reduction waits on evidence,
not enthusiasm.

## 6. The local rungs and status clocks

Three skills route mechanical volume to the local model (`local/`,
$0, offline): `/offload` (narrow a big file list, extract structure from
noisy output, shallow classification), `/review-digest` (the
comment-pile pre-pass), and `/ask-portfolio` (offline RAG over your own
repos, docs, and memory). The routing rule is verifiability, never
difficulty — the deep version is `docs/lessons.md` lessons 3 and 24, and
the model/effort dials are part of every seeded task's metadata.

Three skills answer "what's happening?" on different clocks: `/status` is
the in-flight update (what happened / what's next / recommendation / what
I need from you, one to three sentences each); `/wip` is the standing
cross-store board joining ship runs, dossier tasks, and PR state right
now; `/shipped` is the retrospective — merged PRs with SHAs, weighted
LOC, task closures, friction deltas, what became newly usable. Keeping
the clocks separate is what stops every update from becoming an unbounded
project dump.

## 7. Session mechanics — what runs all day

The delivery loop is seed/prep/drive; the hour-to-hour texture is worktrees,
chips, and continuation briefs. These are the highest-frequency commands in
the whole workflow.

**Worktrees, constantly.** Every piece of implementation work gets its own
worktree under `.claude/worktrees/` — `/worktree-add <branch>` to create,
`/worktree-where` when a session needs to know what it's standing in,
`/worktree-list` and `/worktree-remove` for the lifecycle, and
`/worktree-transfer` to hand in-progress work between sessions. The law
behind the habit comes from the drive contract: an agent never competes
with the operator in the dirty root checkout, so isolation is the *first*
step of any change, not an option. [tower](https://github.com/itsHabib/tower)
is the TUI over the same worktrees and their PRs when the count grows.

**`/chip` — fork the tangent instead of chasing it.** Mid-conversation,
anything worth doing but not worth derailing the current thread gets
chipped into its own session: a title, a self-contained prompt (paths,
line numbers, decisions already made, acceptance criteria), and the main
thread resumes in the same breath. The skill's own bar for what *not* to
chip is half its value: vague observations, trivial fixes faster done
inline, anything that needs this conversation's context to even
understand, and low-confidence hunches. Chips are how one operator runs
many threads without any of them blocking the others.

**`/continue` — context management as a first-class move.** When a
session's context fills, the skill emits one paste-ready brief for a
fresh session: Goal, State (done as concrete outcomes — paths, SHAs,
PR numbers, no process narration), Next (concrete enough to start cold),
Key facts this session added (decisions with the one-phrase why, dead
ends already tried), and Pointers (the file worth opening first, the
command that verifies). The framing rule does the work: the fresh session
already has CLAUDE.md, memory, and skills — the brief carries *only what
this session added*. Long-running work survives context limits because
continuation is cheap, rehearsed, and lossless where it matters.

**`/recover` — after the crash.** Reboot, terminal loss, a session that
died mid-flight: recover scans recent session transcripts, classifies
each (task, cwd, last action, done vs. interrupted), and resumes the
interrupted one deliberately instead of re-deriving it from memory.

The common thread: sessions are cattle, work is durable. Worktrees keep
the work isolated, dossier and the ledgers keep it resumable, and chips,
continuation briefs, and recovery make the *session* the disposable part.

## 8. Prose → code

The maturation loop that everything above feeds:

```
write the workflow as a skill (prose)
      ↓ run it on real work, repeatedly
friction log: where it succeeded, where steps got skipped,
              where recovery was manual
      ↓ a category recurs
deterministic rule or engine verb absorbs it
      ↓
tests + receipts pin the behavior
      ↓
delete the prose the mechanism absorbed
```

The reference case is the merge tail: dispatch/poll/judgment/land lived as
skill prose until ship's driver engine absorbed the state machine, killing
hundreds of instruction lines in one commit and leaving policy behind.
The corollary held within weeks — prose regrows unless a mechanism owns
the behavior — which is why "delete the absorbed prose" is a step, not an
afterthought.

Friction logs are the loop's fuel, and they are obligations, not
suggestions: the work-driver contract requires recording rough edges with
the exact seam, severity, and recovery. Entries have driven real
mechanism work — receipt schemas that resolved ambiguities prose couldn't,
watcher budget fixes, exact-head re-review discipline. A correction should
not remain advice for one chat; it becomes part of the instruction layer
or, eventually, part of an engine.

## 9. Hooks: reflexes, not workflows

Two pre-tool hooks and five post-tool hooks currently run (the exact
rules are machine-local and stay private; the shapes are what matter):

- **Pre:** a command guard — the harness's deterministic tier-3 floor,
  refusing force pushes, destructive repo operations, credential and
  authority-state touches, and bare merge shapes (a merge must carry the
  exact-head form gate emits) in every permission mode. And a lesson-scrub
  guard that fails closed if its marker list is unavailable.
- **Post:** reflexes that turn activity into durable state without relying
  on agent memory — PR creation links the PR to its dossier task; a gate
  decision records its verdict artifact; a merge records the receipt and
  closes the task; ship dispatch and terminal reads link run evidence back
  to project state.

The design rule: hooks capture reliable local facts; choreography and
cross-tool judgment stay in skills and engines. A hook that starts making
decisions is a plane doing another plane's job.

## 10. Instruction hygiene

Instruction surfaces are code-like assets: they need ownership,
precedence, drift detection, and deletion. The working model — private
skills are authored and dogfooded in the live catalog; the public skills
repo is a reviewed, scrubbed projection kept in sync by
[skill-sync](https://github.com/itsHabib/skill-sync); managed blocks
change at their generator, not by hand-editing every repo; repo
`AGENTS.md` files point at canonical guidance plus true local differences.
Approvals that accumulate in scratch settings are an inbox, drained on a
cadence into designed rules or deleted (`docs/auto-mode-defaults.md`).
And the deletion discipline from `docs/lessons.md` lesson 21 applies to
everything in this document too: when the model outgrows a section here,
it goes.
