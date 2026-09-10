package watch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Delivery. Mail is addressed to a role; a role with a live session reads its
// mailbox at its next prompt and the watcher does nothing. A role with NO live
// session would never read it, so the watcher launches one from the operator's
// deliver.json in the state root:
//
//	{"<role>": {"cwd": "<dir>", "cmd": ["claude", "-p", "--model", "opus", "{{prompt}}"]}}
//
// `{{prompt}}` is replaced by the mail lines the hook would have injected. Each
// launch is recorded in watch/observed.jsonl and stamped on the message
// (`delivered_at`, `delivered_by`), so a message is launched for once, whatever
// becomes of the session. A message younger than the grace period is left alone: the
// role's session may be starting.

// MailGrace is how long an unread message waits for a live session before delivery.
var MailGrace = 10 * time.Second

// DeliverFile is the operator's per-role launch command.
func DeliverFile() string { return fleet.Path("deliver.json") }

// deliverMail launches one session per role that has unread, undelivered mail past
// the grace and nobody live to read it.
func deliverMail(now float64) {
	defer func() { _ = recover() }() // never a reason for a tick to fail
	config := fleet.ReadJSON(DeliverFile())
	if config == nil {
		return
	}
	live := liveRoles()
	for _, role := range fleet.MailRoles() {
		if live[role] {
			continue
		}
		pending := pendingMail(role, now)
		if len(pending) == 0 {
			continue
		}
		spec := fleet.M(config, role)
		if spec == nil {
			continue
		}
		launchFor(role, spec, pending, now)
	}
}

// liveRoles is every role some live session wears.
func liveRoles() map[string]bool {
	out := map[string]bool{}
	d := fleet.Path("sessions")
	ents, _ := os.ReadDir(d)
	for _, e := range ents {
		r := fleet.ReadJSON(filepath.Join(d, e.Name()))
		if role := fleet.S(r, "role"); role != "" && fleet.SessionAlive(r) {
			out[role] = true
		}
	}
	return out
}

// pendingMail is the unread, undelivered messages older than the grace.
func pendingMail(role string, now float64) []fleet.Rec {
	var out []fleet.Rec
	for _, m := range fleet.Unacked(role) {
		if fleet.S(m, "delivered_by") != "" || now-fleet.F(m, "at") < MailGrace.Seconds() {
			continue
		}
		out = append(out, m)
	}
	return out
}

// launchFor runs the role's deliver command once for this batch, stamps every
// message under the mailbox lock, and records the launch.
func launchFor(role string, spec fleet.Rec, pending []fleet.Rec, now float64) {
	words := fleet.Strs(spec, "cmd")
	if len(words) == 0 {
		return
	}
	var lines, ids []string
	for _, m := range pending {
		lines = append(lines, fleet.MailLine(m))
		ids = append(ids, fleet.S(m, "id"))
	}
	prompt := strings.Join(lines, "\n") + "\nRead each with `fleet mail`, act, and `fleet ack <id>`."
	argv := make([]string, len(words))
	for i, w := range words {
		argv[i] = strings.ReplaceAll(w, "{{prompt}}", prompt)
	}
	by := fmt.Sprintf("watch:%d", os.Getpid())
	// The stamp before the launch: a launch that then fails is recorded as such, and
	// a stamp that failed to write is a reason not to launch — twice would follow.
	stamped := 0
	err := fleet.KeyLock(fleet.MailLockKey(role), func() error {
		for _, m := range pending {
			cur := fleet.ReadJSON(fleet.MailFile(role, fleet.S(m, "id")))
			if cur == nil || fleet.S(cur, "delivered_by") != "" {
				continue
			}
			cur["delivered_at"], cur["delivered_by"] = now, by
			if err := fleet.WriteJSON(fleet.MailFile(role, fleet.S(m, "id")), cur); err != nil {
				return err
			}
			stamped++
		}
		return nil
	})
	rec := fleet.Rec{"at": now, "what": "deliver", "role": role, "mail": ids, "cmd": argv[0], "cwd": fleet.S(spec, "cwd"), "by": by}
	if err != nil || stamped == 0 {
		rec["error"] = "stamp: " + errText(err)
		_ = fleet.AppendJSONL(filepath.Join(dir(), "observed.jsonl"), rec)
		return
	}
	pid, err := start(argv, fleet.S(spec, "cwd"))
	if err != nil {
		rec["error"] = err.Error()
	}
	if pid > 0 {
		rec["pid"] = float64(pid)
	}
	_ = fleet.AppendJSONL(filepath.Join(dir(), "observed.jsonl"), rec)
}

func errText(err error) string {
	if err == nil {
		return "nothing to stamp"
	}
	return err.Error()
}

// start runs the deliver command detached, its output to watch/deliver.log.
func start(argv []string, cwd string) (int, error) {
	log, err := os.OpenFile(filepath.Join(dir(), "deliver.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer log.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Stdin = nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
