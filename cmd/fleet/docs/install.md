# Install Fleet from a public checkout

The repository contains the binary source, example task-owner (`author`), verifier
and supervisor cards, and the setup instructions. These roles are optional examples,
not an organizational model imposed by Fleet. Adapt their names, responsibilities
and acceptance criteria to your own organization. No private skill repository or
Python runtime is needed for installation.

## Build and install

On macOS or Linux, install Git, Bash and the Go version in the root `go.mod`
(currently Go 1.26). Then:

```sh
git clone https://github.com/itsHabib/workbench.git
cd workbench
bash cmd/fleet/install.sh          # prints the plan without writing
bash cmd/fleet/install.sh --apply
export PATH="$HOME/.fleet/bin:$PATH"
```

Add that PATH entry to your shell startup file if desired. The shell installer only
builds `fleet` and copies this repository's `cmd/fleet/examples/lanes` assets. It neither
registers hooks nor runs a background service. `FLEET_HOME` changes the install
location (independently of runtime state); `FLEET_STATE` changes runtime state (default `~/.fleet`). For a separate
state directory, set `FLEET_LANES` explicitly to your installed lanes directory.
Use the same environment in your shell, desktop harness and headless runtime.

Reinstalling identical cards is safe. The installer refuses to replace different
cards: preserve your edits separately before updating. Set `FLEET_LANES` to a
separate directory for custom manifests/cards. There is no migration, shadow install
or rollback mode. During an existing-install cutover, stop active Fleet work and
remove old global Fleet/Python hook registrations yourself before generating fresh
roles. Preserve unrelated hooks and existing state; this installer does not clean them.

`go install github.com/itsHabib/workbench/cmd/fleet@latest` installs only the binary;
you still need the public lane assets, or your own `FLEET_LANES` directory. The shell
installer is the complete source-checkout path. On Windows, run it from Git Bash: it emits
`bin\fleet.exe`, and the extension matters, because Console and the harness exec the binary
rather than running it from a shell, and an extensionless `fleet` makes every projected hook
fail silently. Building by hand, keep the name: `go build -o %USERPROFILE%\.fleet\bin\fleet.exe
./cmd/fleet`, then copy `cmd/fleet/examples/lanes`. `fleet role` refuses to project a hook
command from an extensionless binary. The packaging test runs wherever `bash` is on PATH.

## Configure one repository

Use an existing Git repository with a committed `main` branch. Run from its ordinary
unroled checkout; put the lead and worker directories beside it:

```sh
fleet pool /absolute/path/to/repo author 1 --tenant demo
fleet pool /absolute/path/to/repo verifier 1 --tenant demo
git -C /absolute/path/to/repo worktree add --detach /absolute/path/to/repo-lead main
fleet role /absolute/path/to/repo-lead supervisor:demo --tenant demo
```

`fleet pool` creates seats and calls the same role projection. `fleet role` writes
`roles.map` under `ORG_STATE` (default `~/dev/org/state`), `CLAUDE.local.md`, project-local
Claude lifecycle hooks, project-local Codex instructions/rules, and the six global
Codex hooks in `CODEX_HOME/hooks.json` (default `~/.codex`). It preserves unrelated
hook groups. Generated local files are excluded through Git's common-dir exclude
file. The root repository needs no role. Paths bound in `roles.map` cannot contain
whitespace. Org's executable and registry are optional.

The example cards contain responsibilities, evidence boundaries and handoff guidance;
the manifests impose no extra command denies, cadence or required receipt kinds.
Choose receipt kinds in your task brief and query `fleet done SHA --kind KIND`.
Repository instructions and explicit authorization still govern effects.

## Start and verify

Start a fresh supported Claude or Codex session in a prepared directory. Configure
the harness to trust/load its project instructions and enable its hook support.
Look for `[fleet]` startup context naming the role. Then inspect:

```sh
fleet board
fleet work
fleet status --all
fleet watch status
```

SessionStart can start the Go watcher. `FLEET_WATCH=off` disables this auto-start for
isolated setup checks. A generated hook file proves configuration, not that your
harness loaded it: confirm actual startup context and observed events before using
Fleet to coordinate real work. Claude/Codex installation, authentication and model
access are separate prerequisites; provider execution may incur charges. For
headless provider dependencies and configuration, follow [headless.md](headless.md).

Continue with [run-a-fleet.md](run-a-fleet.md) to write a brief, assign work, communicate,
verify and finish. [The onboarding guide](ONBOARDING.md) explains the working routine.

## Disposable packaging check

```sh
go test ./cmd/fleet -run '^TestPublicInstall$' -count=1 -v
```

This builds and installs into a temporary home without private lanes or existing
harness settings. It checks all three roles, repeated setup, both hook projections,
synthetic startup context and preservation of edited cards. It disables watcher
auto-start and never uses your installed Fleet state or harness configuration.
It does not launch a model/provider or establish live workload success.
