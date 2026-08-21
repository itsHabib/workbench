#!/bin/sh
# Manage the flare-watch + escalate-serve LaunchAgents (workbench's
# notification plane on darwin).
#
# One entry point for the unattended-operation lifecycle on macOS, the darwin
# twin of scripts/flare-task.ps1. It encodes the things that are easy to get
# wrong by hand:
#
#   - launchd does NOT expand ~ or read your shell profile, so the plist needs
#     an absolute binary path and absolute log paths rendered in;
#   - secrets must not live in a LaunchAgent, so the plist wraps the daemon in
#     `sh -c 'set -a; . ~/.flare/env; exec <bin>'` and the env file stays 0600
#     and uncommitted;
#   - a running daemon holds the compiled binary, so `update` must stop the
#     agents before `go install` overwrites them, then start again.
#
# No verb needs sudo: these are per-user agents bootstrapped into gui/$UID.
#
# Verbs:
#   install    render templates + plutil -lint + bootstrap both agents
#   update     stop -> go install ./cmd/flare ./cmd/escalate -> start
#   restart    bounce both agents (reload ~/.flare/routes.json after an edit)
#   uninstall  bootout both agents and remove the installed plists
#   status     agent state, last exit status, flare status, log tail hints
#   render     render the plists to a directory and lint them; no launchd (test hook)
#
# This is machine wiring that lives beside flare, never inside it.

set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
# scripts/ -> flare/ -> cmd/ -> repo root
REPO_ROOT=$(CDPATH='' cd -- "$SCRIPT_DIR/../../.." && pwd)

FLARE_LABEL='com.workbench.flare-watch'
ESCALATE_LABEL='com.workbench.escalate-serve'
AGENT_DIR="$HOME/Library/LaunchAgents"
FLARE_HOME="$HOME/.flare"
LOG_DIR="$FLARE_HOME/logs"
ENV_FILE="$FLARE_HOME/env"
DOMAIN="gui/$(id -u)"

warn() { printf '%s\n' "$*" >&2; }
die() { warn "$*"; exit 1; }

# resolve_bin NAME — absolute path to an installed binary, PATH first then the
# GOBIN/GOPATH default. launchd has no PATH worth trusting, so this must be
# absolute before it reaches a plist.
resolve_bin() {
	name=$1
	found=$(command -v "$name" 2>/dev/null || true)
	if [ -n "$found" ]; then
		printf '%s\n' "$found"
		return 0
	fi
	fallback="${GOBIN:-${GOPATH:-$HOME/go}/bin}/$name"
	[ -x "$fallback" ] || die "$name not found on PATH or at $fallback -- run 'go install ./cmd/$name' from the repo root first."
	printf '%s\n' "$fallback"
}

# render_plist TEMPLATE DEST FLARE_BIN ESCALATE_BIN — substitute the template
# placeholders and lint the result. A plist that fails plutil is never handed
# to launchd.
render_plist() {
	template=$1 dest=$2 flare_bin=$3 escalate_bin=$4 gate_bin=$5
	[ -f "$template" ] || die "template not found: $template"
	sed -e "s|__FLARE_BIN__|$flare_bin|g" \
		-e "s|__ESCALATE_BIN__|$escalate_bin|g" \
		-e "s|__GATE_BIN__|$gate_bin|g" \
		-e "s|__HOME__|$HOME|g" "$template" >"$dest"
	plutil -lint "$dest" >/dev/null || die "rendered plist failed plutil -lint: $dest"
}

render_all() {
	dest_dir=$1
	flare_bin=$(resolve_bin flare)
	escalate_bin=$(resolve_bin escalate)
	# gate is not built by `update`, but escalate serve shells it to resolve a
	# park -- a plist carrying the bare name would produce an ingress that only
	# fails at the tap.
	gate_bin=$(resolve_bin gate)
	mkdir -p "$dest_dir"
	render_plist "$SCRIPT_DIR/$FLARE_LABEL.plist" "$dest_dir/$FLARE_LABEL.plist" "$flare_bin" "$escalate_bin" "$gate_bin"
	render_plist "$SCRIPT_DIR/$ESCALATE_LABEL.plist" "$dest_dir/$ESCALATE_LABEL.plist" "$flare_bin" "$escalate_bin" "$gate_bin"
	printf 'rendered %s + %s into %s\n' "$FLARE_LABEL" "$ESCALATE_LABEL" "$dest_dir"
	printf '  flare:    %s\n' "$flare_bin"
	printf '  escalate: %s\n' "$escalate_bin"
	printf '  gate:     %s\n' "$gate_bin"
}

# bootout_label — idempotent: a not-loaded agent is not an error here.
bootout_label() {
	launchctl bootout "$DOMAIN/$1" 2>/dev/null || true
}

bootstrap_label() {
	launchctl bootstrap "$DOMAIN" "$AGENT_DIR/$1.plist" ||
		die "launchctl bootstrap failed for $1 (already loaded? try '$0 restart')"
}

require_installed() {
	[ -f "$AGENT_DIR/$1.plist" ] || die "$1 is not installed. Run: $0 install"
}

# print_agent — one agent's launchd state. `launchctl print` fails when the
# label is not loaded; that is reported, not swallowed, so a dead escalate
# serve (missing secrets) stays visible.
print_agent() {
	label=$1
	printf -- '--- %s ---\n' "$label"
	if [ ! -f "$AGENT_DIR/$label.plist" ]; then
		printf 'not installed (no %s/%s.plist)\n' "$AGENT_DIR" "$label"
		return 0
	fi
	out=$(launchctl print "$DOMAIN/$label" 2>&1) || {
		printf 'installed but NOT loaded: %s\n' "$out"
		return 0
	}
	printf '%s\n' "$out" | grep -E '^[[:space:]]*(state|pid|last exit code|last exit status) =' || true
}

# warn_missing_env — the day-1 trap: escalate serve refuses to start without
# SLACK_SIGNING_SECRET + ESCALATE_ALLOWED_SLACK_USERS, so say so up front
# instead of leaving the operator to decode a restart loop in the log.
warn_missing_env() {
	if [ ! -f "$ENV_FILE" ]; then
		warn "note: $ENV_FILE is missing -- escalate serve will refuse to start (SLACK_SIGNING_SECRET, ESCALATE_ALLOWED_SLACK_USERS). flare watch still runs if ~/.flare/routes.json is valid."
		return 0
	fi
	for key in SLACK_SIGNING_SECRET ESCALATE_ALLOWED_SLACK_USERS; do
		grep -q "^[[:space:]]*$key=" "$ENV_FILE" ||
			warn "note: $key not set in $ENV_FILE -- escalate serve will refuse to start."
	done
}

cmd_install() {
	mkdir -p "$AGENT_DIR" "$LOG_DIR"
	[ -f "$FLARE_HOME/routes.json" ] ||
		warn "note: $FLARE_HOME/routes.json is missing -- copy $SCRIPT_DIR/routes.example.json and fill it in, or flare watch will exit at load."
	render_all "$AGENT_DIR"
	bootout_label "$FLARE_LABEL"
	bootout_label "$ESCALATE_LABEL"
	bootstrap_label "$FLARE_LABEL"
	bootstrap_label "$ESCALATE_LABEL"
	printf 'installed %s + %s into %s\n' "$FLARE_LABEL" "$ESCALATE_LABEL" "$DOMAIN"
	cmd_status
}

cmd_uninstall() {
	bootout_label "$FLARE_LABEL"
	bootout_label "$ESCALATE_LABEL"
	rm -f "$AGENT_DIR/$FLARE_LABEL.plist" "$AGENT_DIR/$ESCALATE_LABEL.plist"
	printf 'removed %s + %s (state under %s left in place)\n' "$FLARE_LABEL" "$ESCALATE_LABEL" "$FLARE_HOME"
}

cmd_update() {
	# Both, before touching anything: a half-installed pair would rebuild fine
	# and then die on the second bootstrap with a misleading "already loaded".
	require_installed "$FLARE_LABEL"
	require_installed "$ESCALATE_LABEL"
	bootout_label "$FLARE_LABEL"
	bootout_label "$ESCALATE_LABEL"
	printf 'rebuilding from %s ...\n' "$REPO_ROOT"
	# `if` keeps set -e from killing the script on a build failure: the daemons
	# must come back up either way.
	build_ok=0
	if (cd "$REPO_ROOT" && go install ./cmd/flare ./cmd/escalate); then
		build_ok=1
	fi
	# Always bring the daemons back up, new binary or old.
	bootstrap_label "$FLARE_LABEL"
	bootstrap_label "$ESCALATE_LABEL"
	[ "$build_ok" -eq 1 ] || die "go install failed -- restarted the previous binaries."
	printf 'update complete (new binaries running).\n'
	cmd_status
}

cmd_restart() {
	require_installed "$FLARE_LABEL"
	require_installed "$ESCALATE_LABEL"
	# bootout sends SIGTERM and waits, so escalate serve's in-flight resolve
	# drains instead of dying under kickstart -k's SIGKILL.
	bootout_label "$FLARE_LABEL"
	bootout_label "$ESCALATE_LABEL"
	bootstrap_label "$FLARE_LABEL"
	bootstrap_label "$ESCALATE_LABEL"
	printf 'restarted both agents (config reloaded).\n'
	cmd_status
}

cmd_status() {
	print_agent "$FLARE_LABEL"
	print_agent "$ESCALATE_LABEL"
	# A crash-looping escalate serve shows up above as a non-zero last exit and
	# nothing else. Name the usual cause here so the operator does not have to
	# decode an exit code against the err.log.
	warn_missing_env
	printf -- '--- flare status (exit 1 == stale; informational) ---\n'
	flare_bin=$(command -v flare 2>/dev/null || true)
	if [ -z "$flare_bin" ]; then
		printf 'flare not on PATH; skipping liveness check.\n'
	else
		"$flare_bin" status || true
	fi
	printf -- '--- logs ---\n'
	printf 'tail -f %s/flare-watch.err.log\n' "$LOG_DIR"
	printf 'tail -f %s/escalate-serve.err.log\n' "$LOG_DIR"
}

usage() {
	printf 'usage: %s {install|update|restart|status|uninstall|render <dir>}\n' "$0" >&2
	exit 2
}

case "${1:-status}" in
install) cmd_install ;;
update) cmd_update ;;
restart) cmd_restart ;;
status) cmd_status ;;
uninstall) cmd_uninstall ;;
render) [ $# -ge 2 ] || usage; render_all "$2" ;;
*) usage ;;
esac
