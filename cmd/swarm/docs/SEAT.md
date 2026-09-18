# The seat seam

A seat is one agent session in one checkout, run as a process. `swarm seat run` is that
process. It is the line between a **runner**, which decides who works on what, and a
**substrate**, which decides where a process runs: a shell on this machine, a worktree, a
Rooms clone, a rented box.

```
swarm seat run --seat p3 --prompt /path/prompt.txt --dir /work/p3 --remote git://host/origin.git \
               [--resume SESSION_ID] [--model M] [--turns N] [--secrets FILE] [--skip-permissions]
```

- Clones `--remote` into `--dir` when it is not a checkout, names the seat as committer.
- Loads `--secrets` (KEY=VALUE lines) into the session's environment. Defaults to
  `/run/rooms/secrets.env` when that file exists, so a Rooms clone started with
  `rooms clone --secret CLAUDE_CODE_OAUTH_TOKEN` needs nothing else. The substrate never
  exports the secret; the seat reads the file itself.
- Passes `SWARM_STORE` and `SWARM_INCARNATION` through and sets `SWARM_SEAT`.
- With `--resume`, continues that session with the prompt as its next message. This is the
  wake path: a runner resumes a seat that stopped while holding a unit or when the store has
  something for it.
- Prints one JSON line last on stdout and exits 0 when the turn ran to its end:

```
{"seat":"p3","session_id":"…","num_turns":41,"total_cost_usd":1.07,"wall_s":212.4,"exit":0}
```

## Plugging a substrate into the team harness

`swarm gym team --seat-cmd '<shell>'` hands every turn to that shell instead of running
`claude` itself. The shell sees `SEAT`, `PROMPT_FILE`, `RESUME` (empty on the first turn),
`DIR`, `REMOTE`, `MODEL`, `TURNS`, `SWARM_SEAT` and `SWARM_STORE`, and must leave the seat's JSON
line last on stdout. Where it runs is its business.

The substrate that runs here, used to prove the seam:

```
--seat-cmd 'swarm seat run --seat "$SEAT" --prompt "$PROMPT_FILE" --dir "$DIR" --remote "$REMOTE" --resume "$RESUME" --model "$MODEL" --turns "$TURNS"'
```

A Rooms substrate has to do three things the local one gets for free: put `PROMPT_FILE`
where the guest can read it, point `--remote` and `SWARM_STORE` at addresses the guest can
reach (the host's LAN address today), and run the resume in a room that still has the
seat's checkout and session state. If a room cannot be re-entered after its command exits,
the wake path needs the seat's `--dir` on a volume that outlives the room.
