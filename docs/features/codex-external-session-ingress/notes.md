# Codex external session ingress — prototype notes

**Status:** exploration, 2026-08-06

**Goal:** let an external source submit a prompt to local Codex and make the
resulting conversation visible in the ChatGPT desktop app, with live-session
injection as the stronger possible form. A related question is whether Codex
has Claude Code's "chip" handoff for deferring a side quest into a fresh
operator-launched session.

This note distinguishes supported interfaces from local implementation details.
Do not treat the on-disk session format as an API.

## Desired behavior

The useful variants, from weakest to strongest, are:

1. Open a new desktop chat with a prompt prefilled for a human to send.
2. Start a prompt automatically, then open the resulting persisted thread in
   the desktop app.
3. Add a new turn to an idle persisted thread, then reopen that thread.
4. Push or steer a message into the exact thread currently running in the
   desktop app.

The first three have documented building blocks. The fourth does not currently
have a documented, first-class Codex equivalent to Claude Code Channels.

## Terminology: chips are not channels

Two Claude Code mechanisms came up during this investigation:

- A **chip** defers an out-of-scope item into a clickable UI object. Clicking it
  launches a fresh session and worktree from a self-contained prompt. It does
  not inject an external event into the current session.
- A **Channel** lets an MCP server push an external event such as a webhook,
  alert, or chat message into a running Claude Code session.

The original external-ingress goal is analogous to Channels. Chips are an
adjacent handoff UX that may also be useful when the desired result is a new
session rather than mutation of the current one.

### Claude's built-in chip mechanism

In the operator's Claude Code setup, the built-in tools are:

- `mcp__ccd_session__spawn_task` — create a clickable chip. One click launches
  a fresh session and worktree.
- `mcp__ccd_session__dismiss_task` — withdraw a chip that is no longer useful.

The spawned session inherits the target repo's `CLAUDE.md`, project
auto-memory, and global skills, MCP servers, and settings. It does **not**
inherit the conversation, files read, diffs seen, or decisions made in the
originating session. The chip prompt must therefore be self-contained.

### The operator's `/chip` and `/push chip` skills

The personal skill at `~/.claude/skills/chip/SKILL.md` turns "chip this" or
`/chip` into `mcp__ccd_session__spawn_task`. Its contract requires:

- an imperative title under 60 characters;
- a self-contained prompt with relevant paths, line numbers, decisions, and
  acceptance criteria;
- a short plain-English `tldr`; and
- an absolute `cwd` only when the work belongs to another repository.

A draft `/push` skill currently exists in three matching locations:

- private-catalog source: `~/dev/cc-skills/skills/push/SKILL.md`;
- installed Claude projection: `~/.claude/skills/push/SKILL.md`; and
- installed Codex projection: `~/.codex/skills/push/SKILL.md`.

Its `push chip` path first builds a structured handoff packet from the current
session, then uses that packet as the chip's self-contained prompt. This is
intended to preserve useful session state without pretending the new session
automatically inherits the old conversation.

As of 2026-08-06, the `cc-skills` source file is untracked and its entries in
`catalog.yaml` and `catalog.mac.yaml` are uncommitted modifications. The
operator reports that the `/push` prototype is not working reliably. It is
therefore a working draft, not a shipped or established capability, even though
individual steps in its skill text are marked as locally verified.

### Current Codex comparison

No documented Codex tool currently provides the same clickable,
operator-deferred `spawn_task` / `dismiss_task` lifecycle.

The nearest pieces are not equivalent:

- a Codex subagent can run in an agent thread, but it starts work as part of the
  current session rather than creating a dormant, clickable side quest;
- App Server `thread/fork` creates a new persisted thread from stored history,
  but it is a protocol primitive rather than a chip UI;
- `codex://new?prompt=...&path=...` can act like a clickable prefilled handoff,
  but it does not submit automatically and has no dismiss lifecycle; and
- an App Server client can create a thread with `thread/start` and open it by
  deep link, but that launches work rather than parking an operator-controlled
  chip.

A personal Codex `/chip` analogue could package a self-contained prompt and
emit a desktop deep link, or materialize a dormant task in an external task
store. That would be a custom workflow, not a built-in Codex chip feature.

## Confirmed in this local environment

Observed with `codex-cli 0.146.0` on macOS:

- The conversation used for this investigation is a persisted exec-originated
  thread:
  - thread ID: `019fdaa0-5d87-7042-ba92-200c6d08e2ba`
  - rollout metadata: `source: exec`, `originator: codex_exec`
  - SQLite thread row: `source: exec`, `thread_source: user`
  - rollout:
    `~/.codex/sessions/2026/08/06/rollout-2026-08-06T22-09-30-019fdaa0-5d87-7042-ba92-200c6d08e2ba.jsonl`
- That exec-originated thread is openable through its direct desktop deep link.
  Direct opening does not prove that an exec thread is reliably discoverable in
  the desktop sidebar.
- Codex also maintains SQLite state under `~/.codex/`, including
  `state_5.sqlite`. The JSONL rollout is therefore not the complete live state.
- The desktop app launches its own embedded `codex app-server` process. In this
  observation it was connected privately to the desktop parent process rather
  than exposing a documented external inbox.

These are useful prototype observations, not stable contracts. Recheck them
after Codex or desktop-app updates.

## Supported building blocks

### Desktop deep links

The desktop app supports the `codex://` URL scheme:

- `codex://new?prompt=<encoded>&path=<encoded>` opens a new local chat and
  preloads the composer. It deliberately does **not** send the prompt.
- `codex://threads/<thread-id>` opens a persisted local thread by its technical
  ID.

Example for a prefilled chat:

```zsh
prompt='summarize the current repository state'
workspace='/Users/mh/dev/workbench'

open "codex://new?prompt=$(jq -rn --arg v "$prompt" '$v|@uri')&path=$(jq -rn --arg v "$workspace" '$v|@uri')"
```

### Non-interactive execution

`codex exec` is the supported script/automation entry point. With `--json`, it
emits JSONL events including:

```json
{"type":"thread.started","thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a53"}
```

The thread ID can be used in the desktop deep link. `codex exec resume` is the
supported way to add a turn to a persisted session:

```zsh
codex exec resume <thread-id> 'follow-up prompt from an external source'
open "codex://threads/<thread-id>"
```

Prefer resuming an idle session. Two independent processes attempting to own or
write the same active conversation may race, and a desktop client already
holding the thread in memory may not receive another process's live events.

### App Server protocol

`codex app-server` is the underlying JSON-RPC 2.0 integration surface for rich
clients. Its relevant methods are:

- `thread/start` — create a conversation.
- `thread/resume` — load a stored conversation.
- `turn/start` — add a user turn and begin generation.
- `turn/steer` — append input to an in-flight turn.
- `turn/interrupt` — cancel an in-flight turn.

A connection must first send `initialize`, then the `initialized`
notification. App Server supports stdio, WebSocket, and Unix-socket transports.
The App Server and its WebSocket transport are experimental; they may change.

`turn/steer` is the conceptual match for live message injection, but it works
only when the integration is connected to the App Server instance that owns the
active thread. The desktop app's private embedded App Server is not documented
as a public endpoint for arbitrary local scripts.

The CLI also exposes managed-daemon plumbing:

```text
codex app-server daemon start
codex app-server proxy
codex remote-control start
codex remote-control pair
```

Remote Control is intended for supported paired clients. It is not documented
as a generic webhook API.

## Private disk injection: known-fragile, not a public API

Directly constructing rollout JSONL or modifying Codex's SQLite databases is
not a supported Codex interface. Do not use it casually or make it the default
when `codex exec`, `codex exec resume`, or App Server can provide the needed
behavior.

That approach bypasses:

- the live App Server that owns active in-memory state;
- event ordering and protocol validation;
- thread metadata and indexes;
- subscriber notifications used by the UI; and
- compatibility across Codex versions.

At best, the desktop app may not notice the change. At worst, concurrent writes
can leave history and metadata inconsistent.

The current private `/push codex` draft attempts a narrow, add-only version of
this internal technique to preserve a Claude session as a real Codex
conversation without spending tokens reconstructing every turn. Its proposed
flow:

1. mints a new UUIDv7 thread ID;
2. writes a new rollout containing session metadata and a condensed, honest
   transcript;
3. inserts a new `threads` row in `~/.codex/state_5.sqlite` and appends the
   session index;
4. runs one supported `codex exec resume` turn to warm and normalize the
   hand-written thread;
5. resets the pushed thread's title; and
6. opens `codex://threads/<thread-id>`, retrying the deep link once because the
   desktop list refresh can lag.

The draft's intended boundary is to never edit or delete existing rollouts or
thread rows and never touch authentication; the title of its own newly created
thread is its one planned update exception. It marks rollout shapes, SQLite
schema, source filtering, and desktop refresh behavior as internal and
version-fragile.

These steps have **not** been accepted as an end-to-end reliable flow. Claims
inside the draft such as "verified 2026-08" are prototype evidence that still
needs independent reproduction, especially where the desktop can open a thread
but not list it, displays a blank thread until warm-up, or refreshes only after
repeated navigation. Re-verify after every relevant Codex update.

The draft identifies an App Server client as the preferred end state: start
`codex app-server`, complete the `initialize` / `initialized` handshake, call
`thread/start`, then `turn/start`, and finally open the returned thread by deep
link. It claims this produced a desktop-visible `source: vscode` thread without
SQLite mutation on `codex-cli 0.146.0`. This note has not rerun that exact
end-to-end experiment, so it remains the leading hypothesis rather than a
landed solution.

## Gap versus Claude Code Channels

Claude Code Channels are MCP servers with a channel capability that can push
external events such as webhooks, monitoring alerts, or chat messages directly
into a running Claude Code session. They include explicit per-session opt-in and
sender gating.

No matching first-class MCP channel capability is documented for Codex today.
The closest Codex primitive is App Server's `turn/steer`, which requires control
of or a connection to the App Server hosting the thread.

## Recommended prototype

Start with a new exec-originated session rather than live injection into a
desktop-owned turn:

```text
external trigger
    -> authenticated helper on the Mac
    -> codex exec --json -C <workspace> <prompt>
    -> parse thread.started.thread_id
    -> open codex://threads/<thread-id>
```

For a trigger already running on the Mac, the helper can be a shell script,
Shortcut, or AppleScript wrapper. A remote source such as Google Apps Script
needs a local bridge: either a polling client or a narrowly authenticated
listener. Do not expose an unauthenticated App Server listener to the network;
use localhost or a Unix socket locally, and authentication plus TLS for any
remote transport.

If the prototype proves that exact live-thread steering is necessary, replace
the one-shot `codex exec` helper with a small App Server client that owns its
threads from creation onward. Do not make direct session-file mutation the
fallback.

## Validation experiments still needed

1. Start `codex exec --json`, open the deep link immediately after
   `thread.started`, and confirm whether the desktop app renders in-progress
   events or only persisted checkpoints.
2. Resume an idle exec-created thread, reopen it by deep link, and confirm the
   new turn appears without restarting the desktop app.
3. Test whether opening an already selected thread forces a disk refresh.
4. Build a minimal App Server client and verify `thread/start`, `turn/start`,
   and `turn/steer` against the same installed CLI version.
5. Record concurrency behavior rather than assuming separate App Server
   processes safely share an active thread.
6. Reproduce each `/push codex` variant separately: composer-only deep link,
   `codex exec --json` seeding, App Server creation, and private transcript
   injection. Record which property each actually achieves: sidebar discovery,
   direct-link opening, non-blank rendering, resumption, and live updates.
7. Do not call `/push` shipped until the canonical `cc-skills` source and
   catalogs are committed, the installed projections match, and a fresh
   end-to-end invocation passes its stated acceptance behavior.

## Design questions

- Must the message enter the exact currently open conversation, or is a new
  desktop-visible session sufficient?
- Is the trigger local to the Mac or remote?
- Is ingress one-way, or must Codex reply to the originating system?
- Can an inbound sender request tool execution or grant approvals? If so, sender
  authentication and authorization are part of the feature, not follow-up work.

## Sources

- [Codex App Server](https://learn.chatgpt.com/docs/app-server)
- [Codex non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode)
- [ChatGPT desktop app commands and deep links](https://learn.chatgpt.com/docs/reference/commands#deep-links)
- [Claude Code Channels](https://code.claude.com/docs/en/channels)
- [Claude Code Channels reference](https://code.claude.com/docs/en/channels-reference)
- `~/dev/cc-skills/skills/push/SKILL.md` — uncommitted private prototype
- `~/.claude/skills/chip/SKILL.md` — installed Claude `/chip` contract
