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
	return state == "running" || state == "unknown"
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
	argv := make([]string, len(t.cmd))
	for i, a := range t.cmd {
		argv[i] = strings.ReplaceAll(a, "{{prompt}}", text)
	}
	path := launchPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	attempt := strings.TrimSuffix(path, ".json") + fmt.Sprintf("-%d-%d", os.Getpid(), time.Now().UnixNano())
	logPath := attempt + ".log"
	log, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = log.Close() }()
	exitFile := attempt + ".exit.json"
	if err := fleet.WriteJSON(attempt+".meta.json", fleet.Rec{"at": now, "address": t.address, "cwd": t.cwd, "output": logPath}); err != nil {
		return 0, err
	}
	if assignment == "" {
		last, err := readLaunch(t)
		if err != nil {
			return 0, err
		}
		assignment = fleet.S(last, "assignment")
	}
	r := fleet.Rec{"at": now, "address": t.address, "cwd": t.cwd, "status": "starting", "assignment": assignment, "exit_file": exitFile, "output": logPath}
	if err := fleet.WriteJSON(path, r); err != nil {
		return 0, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
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
