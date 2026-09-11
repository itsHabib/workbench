package watch

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/provider"
)

// A launch record bridges the interval before a harness emits SessionStart and
// survives watcher replacement. Exit files are per attempt: a late Wait cannot
// overwrite the state of a newer launch. No exit automatically replays work.
func launchPath(t deliverTarget) string {
	key := sha256.Sum256([]byte(fleet.CanonPath(t.cwd)))
	return filepath.Join(dir(), "delivery", fmt.Sprintf("%x.json", key))
}

func readLaunch(t deliverTarget) (fleet.Rec, error) {
	b, err := os.ReadFile(launchPath(t))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r fleet.Rec
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if fleet.F(r, "at") <= 0 || fleet.S(r, "status") == "" {
		return nil, fmt.Errorf("invalid launch record for %s; inspect %s", t.address, launchPath(t))
	}
	if fleet.S(r, "status") == "starting" && fleet.ReadJSON(fleet.S(r, "exit_file")) == nil {
		return nil, fmt.Errorf("process start is unresolved for %s; inspect %s before retrying", t.address, launchPath(t))
	}
	return r, nil
}

func processPresent(pid int) bool { return !fleet.PidGone(pid) }

func launchPresent(r fleet.Rec) bool {
	if r == nil {
		return false
	}
	state, _ := processState(r)
	if state == "running" || state == "unknown" {
		return true
	}
	if fleet.S(r, "provider") == "" || fleet.S(r, "status") == "failed" {
		return false
	}
	return !providerTerminal(r)
}

// Placement already carries the work. Waking its worker does not require a second
// order. Read under the placement lock, and never hand a reused seat stale work.
func pendingAssignment(t deliverTarget, last fleet.Rec) string {
	role, tenant, slot := fleet.MapRowsFor(t.cwd)
	if slot == "" {
		return ""
	}
	a := fleet.ReadJSON(fleet.Path("assign", fleet.Safe(slot)+".json"))
	if a == nil || fleet.S(a, "delivered_to") != "" || strings.TrimSpace(fleet.S(a, "brief")) == "" {
		return ""
	}
	if fleet.S(a, "slot") != slot || fleet.S(a, "role") != role || fleet.S(a, "tenant") != tenant ||
		fleet.CanonPath(fleet.S(a, "path")) != fleet.CanonPath(t.cwd) ||
		fleet.S(a, "repo") != fleet.RepoID(t.cwd) || fleet.S(a, "branch") != fleet.BranchOf(t.cwd) {
		return ""
	}
	sum := sha256.Sum256(fleet.DumpJSON(a))
	candidate := fmt.Sprintf("%x", sum)
	if candidate == fleet.S(last, "assignment") && fleet.S(last, "status") != "failed" {
		return ""
	}
	return candidate
}

func wakePrompt(t deliverTarget, rows []fleet.Rec, assignment string) string {
	parts := []string{t.instruction}
	if assignment != "" {
		parts = append(parts, "[fleet] new assignment in this seat. Read the current startup assignment and complete its authorized work; report a real blocker or the result.")
	}
	if len(rows) > 0 {
		parts = append(parts, prompt(t.address, rows))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func run(t deliverTarget, text, assignment string, now float64) (int, error) {
	if !filepath.IsAbs(fleet.State) || !filepath.IsAbs(fleet.OrgState) {
		return 0, fmt.Errorf("provider delivery requires absolute FLEET_STATE and ORG_STATE roots")
	}
	last, err := readLaunch(t)
	if err != nil {
		return 0, err
	}
	resume, err := resumeSession(t, last)
	if err != nil {
		return 0, err
	}
	path := launchPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	attempt := strings.TrimSuffix(path, ".json") + fmt.Sprintf("-%d-%d", os.Getpid(), time.Now().UnixNano())
	logPath := attempt + ".log"
	log, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() { _ = log.Close() }()
	exitFile := attempt + ".exit.json"
	if err := fleet.WriteJSON(attempt+".meta.json", fleet.Rec{"at": now, "address": t.address, "cwd": t.cwd, "output": logPath, "provider": t.provider, "attempt": attempt, "state_file": attempt + ".state.json"}); err != nil {
		return 0, err
	}
	if assignment == "" {
		assignment = fleet.S(last, "assignment")
	}
	r := fleet.Rec{"at": now, "address": t.address, "cwd": t.cwd, "status": "starting", "assignment": assignment, "exit_file": exitFile, "output": logPath, "provider": t.provider, "attempt": attempt, "state_file": attempt + ".state.json", "cancel_file": attempt + ".cancel", "work_identity": workIdentity(t), "resume": resume}
	if err := fleet.WriteJSON(path, r); err != nil {
		return 0, err
	}
	cmd, err := providerCommand(map[string]any{"provider": t.provider, "cwd": t.cwd, "model": t.model, "permission_mode": t.permissionMode, "resume": resume, "prompt": text, "attempt": attempt, "state_file": attempt + ".state.json", "cancel_file": attempt + ".cancel", "output": logPath, "trace": attempt + ".trace.jsonl"})
	if err != nil {
		return 0, err
	}
	cmd.Dir, cmd.Stdout, cmd.Stderr = t.cwd, log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		r["status"], r["error"] = "failed", err.Error()
		if writeErr := fleet.WriteJSON(path, r); writeErr != nil {
			return 0, fmt.Errorf("start: %v; record failure: %w", err, writeErr)
		}
		return 0, err
	}
	r["status"], r["pid"] = "running", cmd.Process.Pid
	r["process_identity"], _ = processIdentity(cmd.Process.Pid)
	observed := filepath.Join(dir(), "observed.jsonl")
	if err := fleet.WriteJSON(path, r); err != nil {
		_ = fleet.AppendJSONL(observed, fleet.Rec{"at": fleet.Now(), "what": "launch-state-unknown", "address": t.address, "pid": cmd.Process.Pid, "error": err.Error()})
	}
	go recordExit(cmd, exitFile, observed, t.address)
	return cmd.Process.Pid, nil
}

func recordExit(cmd *exec.Cmd, path, observed, address string) {
	err := cmd.Wait()
	r := fleet.Rec{"at": fleet.Now(), "what": "delivery-exited", "address": address, "pid": cmd.Process.Pid, "exit_code": cmd.ProcessState.ExitCode()}
	if err != nil {
		r["error"] = err.Error()
	}
	_ = fleet.AppendJSONL(observed, r)
	_ = fleet.WriteJSON(path, r)
}

// providerCommand is replaceable only by in-package process fixtures.
var providerCommand = provider.Command

func workIdentity(t deliverTarget) string {
	_, tenant, slot := fleet.MapRowsFor(t.cwd)
	a := fleet.ReadJSON(fleet.Path("assign", fleet.Safe(slot)+".json"))
	var detached string
	if fleet.BranchOf(t.cwd) == "" {
		gitdir, _ := fleet.GitDirs(t.cwd)
		b, _ := os.ReadFile(filepath.Join(gitdir, "HEAD"))
		detached = strings.TrimSpace(string(b))
	}
	sum := sha256.Sum256(fleet.DumpJSON(fleet.Rec{"tenant": tenant, "address": t.address, "branch": fleet.BranchOf(t.cwd), "repo": fleet.RepoID(t.cwd), "assigned_at": a["at"], "detached_head": detached}))
	return fmt.Sprintf("%x", sum)
}

// Never guess a transcript or silently fall back to a new session on bad evidence.
func resumeSession(t deliverTarget, last fleet.Rec) (string, error) {
	if t.fresh || last == nil || fleet.S(last, "provider") != t.provider || fleet.S(last, "work_identity") != workIdentity(t) {
		return "", nil
	}
	if fleet.S(last, "status") == "failed" {
		return fleet.S(last, "resume"), nil
	}
	raw, err := os.ReadFile(fleet.S(last, "state_file"))
	if err != nil {
		return "", fmt.Errorf("previous provider state is unavailable: %w", err)
	}
	var state fleet.Rec
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", err
	}
	if fleet.S(state, "attempt") != fleet.S(last, "attempt") || fleet.S(state, "provider") != t.provider {
		return "", fmt.Errorf("previous provider state belongs to another attempt")
	}
	if state["provider_started"] == false || providerQuiescent(last, state) {
		return fleet.S(last, "resume"), nil
	}
	session := fleet.S(state, "provider_session")
	if session == "" {
		return "", fmt.Errorf("previous attempt has no provider session; inspect it and explicitly set fresh to restart")
	}
	if requested := fleet.S(last, "resume"); requested != "" && requested != session {
		return "", fmt.Errorf("provider session does not match the recorded resume identity")
	}
	return session, nil
}

// A collected bridge exit alone cannot release a live provider's reservation.
func providerTerminal(last fleet.Rec) bool {
	state := fleet.ReadJSON(fleet.S(last, "state_file"))
	if state == nil || fleet.S(state, "attempt") != fleet.S(last, "attempt") || fleet.S(state, "provider") != fleet.S(last, "provider") {
		return false
	}
	return state["provider_terminal"] == true || state["provider_started"] == false || providerQuiescent(last, state)
}

// Quiescence is a separate proof, never a synthetic completed turn.
func providerQuiescent(last, state fleet.Rec) bool {
	if fleet.S(state, "provider") != "codex" || state["provider_quiescent"] != true || state["turn_may_have_been_sent"] != false {
		return false
	}
	switch fleet.S(state, "pre_turn_rejection") {
	case "initialize", "thread/start", "thread/resume":
	default:
		return false
	}
	proof := fleet.ReadJSON(fleet.S(last, "state_file") + ".process.json")
	if value, exists := proof["error"]; exists && value != "" {
		return false
	}
	return fleet.S(proof, "schema") == "fleet.process-proof.v1" && fleet.S(proof, "method") == "darwin-no-fork-v1" &&
		fleet.S(proof, "attempt") == fleet.S(last, "attempt") && fleet.S(proof, "provider") == "codex" &&
		proof["armed"] == true && proof["exec_observed"] == true && proof["exit_observed"] == true &&
		proof["fork_observed"] == false && fleet.F(proof, "pid") > 0 && proof["quiescent"] == true
}

// Cancel uses retained launches, even when their delivery configuration was removed.
func Cancel(address string) error {
	return fleet.KeyLock(deliverLockKey, func() error {
		records, err := os.ReadDir(filepath.Join(dir(), "delivery"))
		if err != nil {
			return err
		}
		var found fleet.Rec
		for _, entry := range records {
			r, err := cancelCandidate(entry.Name(), address)
			if err != nil {
				return err
			}
			if r == nil {
				continue
			}
			if found != nil {
				return fmt.Errorf("multiple active attempts for %s; inspect them before cancellation", address)
			}
			found = r
		}
		if found == nil {
			return fmt.Errorf("no running attempt for %s", address)
		}
		return fleet.WriteJSON(fleet.S(found, "cancel_file"), fleet.Rec{"at": fleet.Now(), "attempt": found["attempt"]})
	})
}

func cancelCandidate(name, address string) (fleet.Rec, error) {
	if len(name) != 64+len(".json") || !strings.HasSuffix(name, ".json") {
		return nil, nil
	}
	path := filepath.Join(dir(), "delivery", name)
	r := fleet.ReadJSON(path)
	if fleet.S(r, "address") != address {
		return nil, nil
	}
	if fleet.S(r, "cwd") == "" || launchPath(deliverTarget{cwd: fleet.S(r, "cwd")}) != path {
		return nil, fmt.Errorf("invalid retained launch binding")
	}
	state, reason := processState(r)
	if state == "exited" || state == "failed" || state == "launch_failed" {
		return nil, nil
	}
	if state != "running" {
		return nil, fmt.Errorf("cannot interrupt %s: %s %s", address, state, reason)
	}
	if fleet.S(r, "cancel_file") == "" {
		return nil, fmt.Errorf("attempt has no cancellation endpoint")
	}
	return r, nil
}
