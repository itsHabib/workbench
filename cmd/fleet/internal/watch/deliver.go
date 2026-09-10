package watch

// Delivery: the fold's third duty, and the first one that starts anything.
//
// Mail waits for a session. When nobody ever starts one, it waits forever — the
// rehearsal watched three unread messages sit for fifteen minutes behind a chip that
// had been open and untouched for two hours, because a live process is not the same
// as a process that will read anything. So: per fold, per configured address, if
// there is mail nobody has been handed and no one is there to read it, run the
// operator's command once, carrying every eligible message.
//
// The rules the rehearsal paid for:
//
//   - One launch per address per fold, carrying all of its eligible mail. Launching
//     per message started four sessions of one role in a single fold, each checking
//     liveness before the previous had written a session record.
//   - A launch counts as present for the rest of the fold, for the same reason.
//   - Live, no open turn, and untouched past FLEET_IDLE_GRACE reads absent. An idle
//     holder is worse than a dead one: it blocks delivery and answers nothing.
//   - A message is handed over once. Each is stamped delivered_at/delivered_by before
//     the fold ends, and a stamped message is never carried again — not on the next
//     fold, not by the next watcher.
//
// Latency is bounded by the fold interval: a message that arrives just after a fold
// waits for the next one, so worst-case delivery is one interval plus the grace.
// FLEET_MAIL_GRACE holds a message back briefly so a burst arrives together.
//
// Delivery is configuration, not policy: $FLEET_STATE/deliver.json says what to run.
//
//	{"hub:b": {"cwd": "/path/to/dir", "cmd": ["harness", "-p", "{{prompt}}"]}}
//
// `{{prompt}}` is substituted wherever the operator put it. The substrate never
// learns the harness's flags, and never invents a command for an unconfigured
// address — an address with no entry keeps its mail until a session starts.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Grace defaults. Both are durations, read once per fold.
const (
	defaultMailGrace  = 10.0
	defaultIdleGrace  = 300.0
	defaultReplyGrace = 900.0
)

// deliverTarget is one configured address.
type deliverTarget struct {
	address string
	cwd     string
	cmd     []string
	lateTo  string
}

// grace reads a duration from the environment, falling back to the default. An
// explicit zero is a choice — no grace at all — and is not the same as unparseable.
func grace(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if s := fleet.ParseDuration(v); s > 0 {
		return s
	}
	if v == "0" || v == "0s" {
		return 0
	}
	return fallback
}

// deliverTargets is the configured addresses, ordered so a fold is reproducible.
// An unreadable or malformed entry is skipped: delivery configuration is the
// operator's, and a typo in it must not stop the fold.
func deliverTargets() []deliverTarget {
	cfg := fleet.ReadJSON(fleet.Path("deliver.json"))
	if cfg == nil {
		return nil
	}
	var out []deliverTarget
	for address := range cfg {
		entry := fleet.M(cfg, address)
		cmd := fleet.Strs(entry, "cmd")
		if entry == nil || fleet.S(entry, "cwd") == "" || len(cmd) == 0 {
			continue
		}
		if err := fleet.MailAddress(address, "address"); err != nil {
			continue
		}
		out = append(out, deliverTarget{address: address, cwd: fleet.S(entry, "cwd"), cmd: cmd, lateTo: fleet.S(entry, "LATE_TO")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].address < out[j].address })
	return out
}

// deliver hands each configured address's waiting mail to one new process, and
// returns what it observed. Nothing here is fatal to a fold.
func deliver(now float64) []fleet.Rec {
	targets := deliverTargets()
	if len(targets) == 0 {
		return nil
	}
	mailGrace, idleGrace := grace("FLEET_MAIL_GRACE", defaultMailGrace), grace("FLEET_IDLE_GRACE", defaultIdleGrace)
	sessions := sessionRecords()
	started := map[string]bool{} // a launch counts as present for the rest of the fold
	var observed []fleet.Rec
	for _, t := range targets {
		rows, err := eligibleMail(t.address, now, mailGrace)
		if err != nil {
			observed = append(observed, fleet.Rec{"at": now, "what": "mail-delivery-failed", "address": t.address, "error": err.Error()})
			continue
		}
		if len(rows) == 0 || started[fleet.CanonPath(t.cwd)] || present(sessions, t.cwd, now, idleGrace) {
			continue
		}
		started[fleet.CanonPath(t.cwd)] = true
		observed = append(observed, launch(t, rows, now)...)
	}
	return observed
}

// eligibleMail is one address's unacked, never-delivered mail, older than the grace.
func eligibleMail(address string, now, mailGrace float64) ([]fleet.Rec, error) {
	tenant, err := fleet.MailAddressTenant(address)
	if err != nil {
		return nil, err
	}
	rows, err := fleet.MailboxRecords(tenant, address)
	if err != nil {
		return nil, err
	}
	var out []fleet.Rec
	for _, r := range rows {
		if fleet.Has(r, "acked_at") || fleet.Has(r, "delivered_at") || now-fleet.F(r, "at") < mailGrace {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// launch starts the configured command once, then stamps every message it carried.
// The stamp follows the start so a command that cannot run keeps its mail for the
// next fold, and precedes anything else so a started session is never sent twice.
func launch(t deliverTarget, rows []fleet.Rec, now float64) []fleet.Rec {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = fleet.S(r, "id")
	}
	attempt := fleet.Rec{"at": now, "what": "mail-delivery-attempt", "address": t.address, "cwd": t.cwd, "ids": ids}
	pid, err := run(t, prompt(t.address, rows))
	if err != nil {
		return []fleet.Rec{attempt, {"at": fleet.Now(), "what": "mail-delivery-failed", "address": t.address, "cwd": t.cwd, "ids": ids, "error": err.Error()}}
	}
	stamped := stamp(t.address, rows)
	return []fleet.Rec{attempt, {"at": fleet.Now(), "what": "mail-delivery-started", "address": t.address, "cwd": t.cwd, "ids": ids,
		"pid": float64(pid), "stamped": float64(stamped)}}
}

// stamp marks each carried message delivered. A message the stamp cannot reach is
// counted, not retried: the launch already happened, and the count says so.
func stamp(address string, rows []fleet.Rec) int {
	tenant, err := fleet.MailAddressTenant(address)
	if err != nil {
		return 0
	}
	n := 0
	for _, r := range rows {
		fields := fleet.Rec{"delivered_at": fleet.Now(), "delivered_by": watchAddress}
		if _, err := fleet.StampMail(tenant, address, fleet.S(r, "id"), fields); err == nil {
			n++
		}
	}
	return n
}

// prompt is what the started process is told: which address it is answering for, and
// the same bounded summary a session's own hook would have been given.
func prompt(address string, rows []fleet.Rec) string {
	lines := append([]string{"[fleet] mail for " + address + "; read the bodies with `fleet mail --unacked` and `fleet ack <id>` once read."},
		fleet.MailSummary(rows)...)
	return strings.Join(lines, "\n")
}

// run starts the operator's command detached, with its output in the watcher's own
// directory. The watcher does not wait for it: the process it starts outlives the fold.
func run(t deliverTarget, text string) (int, error) {
	argv := make([]string, len(t.cmd))
	for i, a := range t.cmd {
		argv[i] = strings.ReplaceAll(a, "{{prompt}}", text)
	}
	logs := filepath.Join(dir(), "delivery")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return 0, err
	}
	log, err := os.OpenFile(filepath.Join(logs, fleet.Safe(t.address)+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = log.Close() }()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.Stdin = t.cwd, log, log, nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// present is whether someone in this directory would read the mail. A session that
// has ended is gone; one with an open turn is working; one that is neither, and
// untouched past the idle grace, is a window nobody is looking at.
func present(sessions []fleet.Rec, cwd string, now, idleGrace float64) bool {
	want := fleet.CanonPath(cwd)
	for _, s := range sessions {
		if fleet.B(s, "ended") {
			continue
		}
		if fleet.CanonPath(fleet.S(s, "cwd")) != want && fleet.CanonPath(fleet.S(s, "launch_dir")) != want {
			continue
		}
		if fleet.B(s, "turn_open") || now-fleet.F(s, "last_event_at") < idleGrace {
			return true
		}
	}
	return false
}

// sessionRecords is every session record on this machine.
func sessionRecords() []fleet.Rec {
	entries, _ := os.ReadDir(fleet.Path("sessions"))
	var out []fleet.Rec
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if r := fleet.ReadJSON(filepath.Join(fleet.Path("sessions"), e.Name())); fleet.S(r, "session") != "" {
			out = append(out, r)
		}
	}
	return out
}
