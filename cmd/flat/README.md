# flat

A fleet of coding agents with no management sessions. One Go binary, state in files under the
repository's git directory, works on macOS, Linux and Windows.

The runbook in `cmd/fleet/docs/desktop-fleet.md` puts a lead and sub-leads between builders and
the operator. `flat` is the claim that everything those sessions did is either a traversal or a
ruling, and that only the ruling needs a mind. It replaces the tree with five pieces:

| piece | verb | what the lead used to do |
|---|---|---|
| derived board | `flat board`, `flat verify`, `flat check` | read every branch tip, check RESULT.json pins, spot shared files |
| decision ledger | `flat ask`, `flat rule`, `flat decide`, `flat decisions` | rule, and remember what was ruled |
| claims with fencing | `flat claim`, `flat escalate`, `flat wait` | make sure one answer wins and a stale one cannot land |
| resource leases | `flat take`, `flat drop` | keep two builders off one fixture, device or live app |
| admission | `flat admit` | nobody did this; the disk filled |
| watcher and inbox | `flat watch`, `flat digest`, `flat inbox`, `flat hook` | nudge the silent, render a delta table, forward what arrived |

What remains a mind: writing a builder's handoff, and rulings that need intent nobody wrote
down. Those go to whoever holds the evidence (any peer) or to the operator, by tier. There is a
total order of tiers, peer < lead < operator, and no tree.

`poc/KILL.md` holds the kill conditions written before the adversarial run; `poc/RESULTS.md`
the measured result.

## Adopt it tomorrow

On the machine that runs the fleet:

```sh
go install github.com/itsHabib/workbench/cmd/flat@main     # or: go build -o ~/bin/flat ./cmd/flat
cd <repo>
flat init --operator <your name> --lead lead                # writes tiers.json
flat install-hook                                           # merges the hook into .claude/settings.json
flat watch --interval 30s --fetch --disk-min 10G            # one process per machine, leave it running
```

On Windows the binary is `flat.exe`; `install-hook` writes that path, and `git` must be on
PATH. State lives at `<git common dir>/flat`, so every worktree of the repository shares it with
no configuration. `FLAT_STATE` overrides.

Then put this in every builder prompt, or in one file every prompt points at:

```
before editing any file: flat check <path>. contended with no ruling means do not edit it:
  flat ask --scope <path> --question "..." --options "a|b"   then   flat wait <id>
to touch <resource>: flat take <name>; flat drop <name> when done.
ambiguous product behavior: never guess. flat ask --needs operator ... then flat wait <id>.
before you finish: flat requests. rule what you can from evidence (flat rule <id> ...);
  escalate what needs intent (flat escalate <id> --to operator --why "...").
when done: head=$(git rev-parse HEAD); write briefs/out/<branch>/RESULT.json with
  {"head_sha": "<head>", ...}; commit only that file; push; change nothing after.
```

The watcher writes `watch/digest.md`, `watch/board.md`, `watch/phone.txt` and
`watch/board.jsonl` under the state directory on every pass, and drops nudges into
`inbox/<seat>/`. The hook injects a seat's notes into its next tool call, so a builder that went
quiet or is sitting on an unruled overlap hears about it without anyone sending a message.

Read the digest instead of asking a lead for status:

```sh
flat digest
flat board --phone
```

## Identity and addresses

A seat is a branch. `FLAT_SEAT` names it explicitly; otherwise it is the branch checked out
where the command runs. A request carries the seat that asked; a ruling notifies that seat's
inbox. Session ids and window titles never appear, so a renamed or restarted session loses
nothing.

## Rulings and ties

`flat ask` files a request with a scope (paths, directories, or `resource:<name>`) and the tier
it needs (peer by default). `flat rule` claims it if free, refuses if another live claim holds
it, and refuses a stale epoch. A ruling on a scope that already has an effective ruling at the
same tier must name it with `--supersedes`; a higher tier overrides and the ledger records what
it replaced; a lower tier is refused as outranked. `flat escalate` raises the tier a request
needs and drops any claim. The ledger is hash-chained; a rewritten line breaks every later read.

## Exit codes

0 ok · 1 refused, with `refused <code>: <why>` on stdout · 2 `flat wait` timed out · 3 usage or
error. Refusal codes: `contended_unruled`, `claimed_by_other`, `claim_fenced`, `claim_expired`,
`tier_too_low`, `outranked`, `must_supersede`, `already_ruled`, `held_by_other`, `not_held`,
`not_admitted`, `timeout`.

## The proof of concept

```sh
flat poc init /tmp/flat-a && flat poc run --dir /tmp/flat-a --mode flat
flat poc init /tmp/flat-b && flat poc run --dir /tmp/flat-b --mode tree
flat poc stats --dir /tmp/flat-a --run /tmp/flat-a/runs/<stamp>-flat
flat poc stats --dir /tmp/flat-b --run /tmp/flat-b/runs/<stamp>-tree
flat poc compare --flat /tmp/flat-a/runs/<stamp>-flat --tree /tmp/flat-b/runs/<stamp>-tree
```

`poc init` writes a sandbox Go app with a bare origin, six task cards, the rules and the brief.
`poc run` starts real `claude -p` builders in worktrees under an admission gate, a watcher, a
deterministic operator that answers only what it has intent for, and in tree mode a lead
session on a tick. Faults A to D from `poc/KILL.md` are planted by the cards and the runner.
Evidence lands under `runs/<stamp>-<mode>/`: sessions, logs, the state directory, the ledger.
