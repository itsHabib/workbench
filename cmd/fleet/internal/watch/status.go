package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// RuntimeStatus observes workers now, without scheduling a tick or starting work.
// Process presence, last hook activity, and task completion are distinct facts.
func RuntimeStatus() fleet.Rec {
	now := fleet.Now()
	rows := []fleet.Rec{}
	sessions := sessionRecords()
	targets, configError := readDeliverTargets()
	for _, t := range targets {
		rows = append(rows, runtimeRow(t, sessions))
	}
	watcher, hb := WatcherHealth()
	return fleet.Rec{"at": now, "watcher": watcher, "heartbeat": hb, "workers": rows, "observations": filepath.Join(dir(), "observed.jsonl"), "configuration_error": configError}
}

func runtimeRow(t deliverTarget, sessions []fleet.Rec) fleet.Rec {
	path := launchPath(t)
	row := fleet.Rec{"address": t.address, "cwd": t.cwd, "state": "not_started", "launch_record": path, "output": strings.TrimSuffix(path, ".json") + ".log"}
	last, err := readLaunch(t)
	if err != nil {
		row["state"], row["error"] = "unknown", err.Error()
		return row
	}
	if last != nil && (fleet.S(last, "address") != t.address || fleet.S(last, "cwd") == "" || fleet.CanonPath(fleet.S(last, "cwd")) != fleet.CanonPath(t.cwd)) {
		row["state"], row["error"] = "unknown", "retained launch belongs to another binding; inspect the launch record"
		delete(row, "output")
		return row
	}
	if last != nil {
		row["started_at"], row["pid"], row["exit_file"] = last["at"], last["pid"], last["exit_file"]
		if output := fleet.S(last, "output"); output != "" {
			row["output"] = output
		}
		row["state"], row["error"] = processState(last)
		if exit := fleet.ReadJSON(fleet.S(last, "exit_file")); exit != nil {
			row["exit_code"], row["exited_at"] = exit["exit_code"], exit["at"]
		}
	}
	providerActivity(row, last)
	lastActivity(row, last, sessions)
	if last == nil && fleet.S(row, "session") != "" {
		row["state"] = "observed_session"
	}
	if info, err := os.Stat(fleet.S(row, "output")); err == nil && !info.IsDir() {
		row["output_bytes"], row["output_at"] = info.Size(), float64(info.ModTime().UnixNano())/1e9
	}
	if t.configError != "" {
		row["configuration_error"] = t.configError
	}
	if err := bound(t); err != nil {
		row["binding_error"] = err.Error()
	}
	_, _, slot := fleet.MapRowsFor(t.cwd)
	branch := fleet.BranchOf(t.cwd)
	if result := lastResult(fleet.S(row, "output")); result != nil {
		row["result"] = result
	}
	row["starts_paused"] = addressStopped(t.address) || (slot != "" && fleet.StopFlag("slot:"+slot) != nil) || (branch != "" && fleet.StopFlag(fleet.Scope(t.cwd, branch)) != nil)
	return row
}

func processState(last fleet.Rec) (string, string) {
	if fleet.S(last, "status") == "failed" {
		return "launch_failed", fleet.S(last, "error")
	}
	if fleet.S(last, "status") == "released" {
		return "released", "operator released the reservation: " + fleet.S(last, "release_why")
	}
	if exit := fleet.ReadJSON(fleet.S(last, "exit_file")); exit != nil {
		if fleet.F(exit, "exit_code") != 0 {
			return "failed", fleet.S(exit, "error")
		}
		return "exited", ""
	}
	pid := int(fleet.F(last, "pid"))
	if fleet.PidGone(pid) {
		return "gone_exit_unknown", "process is absent; no exit result was collected"
	}
	if !fleet.PidAlive(pid) {
		return "unknown", "process inspection did not establish presence or absence"
	}
	identity, err := processIdentity(pid)
	if err != nil || fleet.S(last, "process_identity") == "" {
		return "unknown", "process start identity is unavailable; inspect the launch before retrying"
	}
	if identity != fleet.S(last, "process_identity") {
		return "gone_exit_unknown", "PID belongs to a different process; original exit was not collected"
	}
	return "running", ""
}

func lastActivity(row, last fleet.Rec, sessions []fleet.Rec) {
	want := fleet.CanonPath(fleet.S(row, "cwd"))
	for _, s := range sessions {
		path := fleet.S(s, "launch_dir")
		if path == "" {
			path = fleet.S(s, "cwd")
		}
		if fleet.CanonPath(path) != want || fleet.F(s, "last_event_at") < fleet.F(last, "at") || fleet.F(s, "last_event_at") <= fleet.F(row, "last_event_at") {
			continue
		}
		for _, k := range []string{"session", "last_event", "last_event_at", "last_tool", "turn_open", "transcript_path", "branch"} {
			row[k] = s[k]
		}
	}
}

// RuntimeText is the operator's view; --json exposes the same evidence to a UI.
func RuntimeText() string {
	status := RuntimeStatus()
	now := fleet.F(status, "at")
	var b strings.Builder
	fmt.Fprintf(&b, "Watcher: %s", fleet.S(status, "watcher"))
	if hb := fleet.M(status, "heartbeat"); hb != nil {
		fmt.Fprintf(&b, "; last tick %s ago", fleet.FmtAge(now-fleet.F(hb, "at")))
	}
	if err := fleet.S(status, "configuration_error"); err != "" {
		fmt.Fprintf(&b, "\nConfiguration: %s", err)
	}
	fmt.Fprintln(&b, "\nWorker observations (process exits do not establish task completion):")
	for _, row := range status["workers"].([]fleet.Rec) {
		renderRuntimeRow(&b, row, now)
	}
	fmt.Fprintf(&b, "Trace: %s\n", fleet.S(status, "observations"))
	return b.String()
}

func renderRuntimeRow(b *strings.Builder, row fleet.Rec, now float64) {
	fmt.Fprintf(b, "\n%s: %s", fleet.S(row, "address"), fleet.S(row, "state"))
	if pid := fleet.F(row, "pid"); pid > 0 {
		fmt.Fprintf(b, " · pid %.0f", pid)
	}
	if started := fleet.F(row, "started_at"); started > 0 {
		fmt.Fprintf(b, " · started %s ago", fleet.FmtAge(now-started))
	}
	if fleet.Has(row, "exit_code") {
		fmt.Fprintf(b, " · exit %.0f", fleet.F(row, "exit_code"))
	}
	fmt.Fprintf(b, "\n  %s · branch %s\n", fleet.S(row, "cwd"), fleet.S(row, "branch"))
	if result := fleet.M(row, "result"); result != nil {
		fmt.Fprintf(b, "  %s\n", fleet.TranscriptLine(result))
	}
	lastHook := "none observed for this launch"
	if at := fleet.F(row, "last_event_at"); at > 0 {
		lastHook = fmt.Sprintf("%s %s · %s ago · session %s", fleet.S(row, "last_event"), fleet.S(row, "last_tool"), fleet.FmtAge(now-at), fleet.S(row, "session"))
	}
	fmt.Fprintf(b, "  Last hook: %s\n", lastHook)
	if fleet.S(row, "provider") == "codex" {
		fmt.Fprintf(b, "  Last thread permissions: sandbox=%s approval=%s (null = unknown)\n", fleet.DumpJSON(row["sandbox_policy"]), fleet.DumpJSON(row["approval_policy"]))
	}
	for _, k := range []string{"error", "configuration_error", "binding_error"} {
		if message := fleet.S(row, k); message != "" {
			fmt.Fprintf(b, "  %s: %s\n", k, message)
		}
	}
	if fleet.B(row, "starts_paused") {
		fmt.Fprintln(b, "  Further starts paused by an explicit stop")
	}
	fmt.Fprintf(b, "  Output: %s", fleet.S(row, "output"))
	if at := fleet.F(row, "output_at"); at > 0 {
		fmt.Fprintf(b, " · %.0f bytes · modified %s ago", fleet.F(row, "output_bytes"), fleet.FmtAge(now-at))
	}
	fmt.Fprintln(b)
}

// WatcherHealth reads the watcher process and heartbeat without inspecting workers.
func WatcherHealth() (string, fleet.Rec) {
	hb := Heartbeat()
	watcher := "never_seen"
	if hb != nil {
		watcher = "running"
		switch {
		case fleet.PidGone(int(fleet.F(hb, "pid"))):
			watcher = "stopped"
		case Stale(3):
			watcher = "stale"
		}
	}
	return watcher, hb
}

func providerActivity(row, last fleet.Rec) {
	if last == nil || fleet.S(last, "state_file") == "" {
		return
	}
	if last != nil && fleet.S(last, "provider") != "" && (fleet.S(row, "state") == "exited" || fleet.S(row, "state") == "failed") && !providerTerminal(last) {
		row["provider_cleanup_pending"] = true
		row["error"] = "bridge exited without safe provider cleanup evidence; directory remains reserved"
	}
	row["state_file"] = last["state_file"]
	state := fleet.ReadJSON(fleet.S(last, "state_file"))
	if fleet.S(state, "attempt") != fleet.S(last, "attempt") || fleet.S(state, "provider") != fleet.S(last, "provider") {
		row["provider_error"] = "provider state missing or belongs to another attempt"
		return
	}
	for _, key := range []string{"sandbox_policy", "approval_policy", "trace", "provider", "attempt", "provider_session", "provider_turn", "provider_state", "provider_started", "provider_terminal", "provider_quiescent", "turn_may_have_been_sent", "pre_turn_rejection", "process_proof", "provider_executable", "provider_exit_code", "provider_exit_signal", "last_provider_event", "last_provider_event_at", "reason", "error", "earlier_error"} {
		if value, ok := state[key]; ok {
			row[key] = value
		}
	}
}
