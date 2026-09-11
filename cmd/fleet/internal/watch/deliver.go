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
//   - A live occupant remains present while idle. End it before starting another
//     session in that directory; silence does not authorize a competing launch.
//   - A message is handed over once. Each is stamped delivered_at/delivered_by before
//     the process starts — the stamp is the reservation, and a start that does not
//     happen gives it back — and a stamped message is never carried again, not on the
//     next fold and not by the next watcher.
//   - The launch directory must be the address's own, in roles.map, in the same tenant.
//     Mail identity comes from the exact directory, so a wrong one delivers to nobody.
//   - Deciding and launching happen inside one delivery lock. The stamps are the
//     reservation, and the next holder of the lock re-reads them. Board ownership
//     separately excludes overlapping persistent and one-shot watcher processes.
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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Grace defaults. Both are durations, read once per fold.
const (
	defaultMailGrace  = 10.0
	defaultReplyGrace = 900.0
)

// deliverTarget is one configured address.
type deliverTarget struct {
	address     string
	cwd         string
	cmd         []string
	lateTo      string
	every       time.Duration
	instruction string
	configError string
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
	targets, _ := readDeliverTargets()
	return targets
}

// Read once so status describes the same configuration it used for worker rows.
func readDeliverTargets() ([]deliverTarget, string) {
	path := fleet.Path("deliver.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, "no deliver.json; no headless commands configured"
	}
	if err != nil {
		return nil, err.Error()
	}
	var cfg fleet.Rec
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err.Error()
	}
	if cfg == nil {
		return nil, "deliver.json must be an object"
	}
	targets := parseDeliverTargets(cfg)
	if len(cfg) != len(targets) {
		return targets, fmt.Sprintf("%d entries omitted due to an invalid address or command; inspect %s", len(cfg)-len(targets), path)
	}
	return targets, ""
}

func parseDeliverTargets(cfg fleet.Rec) []deliverTarget {
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
		t := deliverTarget{address: address, cwd: fleet.S(entry, "cwd"), cmd: cmd, lateTo: fleet.S(entry, "LATE_TO"), instruction: fleet.S(entry, "prompt")}
		if raw := fleet.S(entry, "every"); raw != "" {
			var err error
			t.every, err = time.ParseDuration(raw)
			if err != nil || t.every <= 0 || strings.TrimSpace(t.instruction) == "" {
				t.configError = "every requires a positive duration and a nonempty prompt"
			}
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].address < out[j].address })
	return out
}

// deliverLockKey is the one serialisation point for delivery, taken by every fold in
// every process — the scheduled watcher and each `fleet watch --once` alike.
const deliverLockKey = "watch-delivery"

// deliver hands each configured address's waiting mail to one new process, and
// returns what it observed. Nothing here is fatal to a fold.
func deliver(now float64) []fleet.Rec {
	targets := deliverTargets()
	if len(targets) == 0 {
		return nil
	}
	mailGrace := grace("FLEET_MAIL_GRACE", defaultMailGrace)
	started := map[string]bool{} // a launch counts as present for the rest of the fold
	var observed []fleet.Rec
	for _, t := range targets {
		if started[fleet.CanonPath(t.cwd)] {
			continue
		}
		obs, launched := deliverOne(t, now, mailGrace)
		observed = append(observed, obs...)
		if launched {
			started[fleet.CanonPath(t.cwd)] = true
		}
	}
	return observed
}

// deliverOne decides and launches for one address inside the delivery lock.
//
// The decision and the act have to be one step, independently of board ownership.
// Under this lock the stamps a launch writes are the reservation: the next delivery
// attempt re-reads them before deciding whether another process is needed.
//
// A lock it cannot take in time is a refusal to deliver this fold, recorded and left
// for the next one. Mail is never lost by waiting; it is lost by being delivered twice.
func deliverOne(t deliverTarget, now, mailGrace float64) ([]fleet.Rec, bool) {
	var observed []fleet.Rec
	launched := false
	err := fleet.KeyLock(deliverLockKey, func() error {
		_, _, slot := fleet.MapRowsFor(t.cwd)
		if slot != "" {
			return fleet.KeyLock("slot:"+slot, func() error {
				var err error
				observed, launched, err = deliverLocked(t, now, mailGrace)
				return err
			})
		}
		var err error
		observed, launched, err = deliverLocked(t, now, mailGrace)
		return err
	})
	if err != nil {
		observed = append(observed, fleet.Rec{"at": fleet.Now(), "what": "mail-delivery-deferred", "address": t.address, "cwd": t.cwd, "error": err.Error()})
	}
	return observed, launched
}

func deliverLocked(t deliverTarget, now, mailGrace float64) ([]fleet.Rec, bool, error) {
	var observed []fleet.Rec
	launched := false

	if err := bound(t); err != nil {
		observed = append(observed, fleet.Rec{"at": now, "what": "mail-delivery-unbound", "address": t.address, "cwd": t.cwd, "error": err.Error()})
		return observed, launched, nil
	}
	_, _, slot := fleet.MapRowsFor(t.cwd)
	branch := fleet.BranchOf(t.cwd)
	if addressStopped(t.address) || (slot != "" && fleet.StopFlag("slot:"+slot) != nil) || (branch != "" && fleet.StopFlag(fleet.Scope(t.cwd, branch)) != nil) {
		return observed, launched, nil
	}
	rows, err := eligibleMail(t.address, now, mailGrace)
	if err != nil {
		observed = append(observed, fleet.Rec{"at": now, "what": "mail-delivery-failed", "address": t.address, "error": err.Error()})
		return observed, launched, nil
	}
	if t.configError != "" {
		observed = append(observed, fleet.Rec{"at": now, "what": "delivery-config-invalid", "address": t.address, "error": t.configError})
		return observed, launched, nil
	}
	last, err := readLaunch(t)
	if err != nil {
		return observed, launched, err
	}
	if launchPresent(last) {
		if state, reason := processState(last); state == "unknown" {
			observed = append(observed, fleet.Rec{"at": now, "what": "launch-unresolved", "address": t.address, "error": reason})
		}
		return observed, launched, nil
	}
	if present(sessionRecords(), t.cwd) {
		return observed, launched, nil
	}
	assignment := pendingAssignment(t, last)
	periodic := t.every > 0 && now-fleet.F(last, "at") >= t.every.Seconds()
	if len(rows) == 0 && assignment == "" && !periodic {
		return observed, launched, nil
	}
	launched = true
	observed = append(observed, launch(t, rows, now, assignment)...)
	return observed, launched, nil
}

// bound is whether the configured launch directory is the mailbox address's own
// checkout, in the same tenant.
//
// Mail identity is resolved from the exact launch directory, so a stale or wrong
// `cwd` starts a process wearing a different role — or none — while the fold stamps
// the intended mailbox delivered. That process cannot read or acknowledge what it was
// handed, and no later fold retries it: the messages are gone. The binding is cheap
// to check and the only thing that makes the stamp true, so it is checked first.
func bound(t deliverTarget) error {
	tenant, err := fleet.MailAddressTenant(t.address)
	if err != nil {
		return err
	}
	role, cwdTenant, slot := fleet.MapRowsFor(t.cwd)
	if cwdTenant != tenant {
		return fmt.Errorf("cwd %s is in tenant %q, address %s is in tenant %q", t.cwd, cwdTenant, t.address, tenant)
	}
	if role != t.address && slot != t.address {
		return fmt.Errorf("cwd %s is bound to role %q seat %q, not to %s", t.cwd, role, slot, t.address)
	}
	return nil
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

// launch reserves every message it is about to carry, starts the configured command
// once, and reconciles the reservation against what actually happened.
//
// The reservation is the stamp, and it has to be durable before the start. Stamping
// afterwards loses a message the other way round: the process has already been handed
// it, and a stamp that fails — an unwritable mailbox, a mid-fold crash — leaves the
// record eligible, so the next fold launches a second process carrying the same mail.
// Taking the reservation first inverts the failure: nothing starts until every message
// is marked, and a start that never happens gives the marks back. A give-back that
// itself fails is the one remaining hole, and it is recorded rather than left silent.
func launch(t deliverTarget, rows []fleet.Rec, now float64, assignment string) []fleet.Rec {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = fleet.S(r, "id")
	}
	observed := []fleet.Rec{{"at": now, "what": "mail-delivery-attempt", "address": t.address, "cwd": t.cwd, "ids": ids}}
	reserved, err := reserve(t.address, rows)
	if err != nil {
		observed = append(observed, fleet.Rec{"at": fleet.Now(), "what": "mail-delivery-failed", "address": t.address, "cwd": t.cwd, "ids": ids, "error": err.Error()})
		return append(observed, release(t.address, reserved)...)
	}
	pid, err := run(t, wakePrompt(t, rows, assignment), assignment, now)
	if err != nil {
		observed = append(observed, fleet.Rec{"at": fleet.Now(), "what": "mail-delivery-failed", "address": t.address, "cwd": t.cwd, "ids": ids, "error": err.Error()})
		return append(observed, release(t.address, reserved)...)
	}
	return append(observed, fleet.Rec{"at": fleet.Now(), "what": "mail-delivery-started", "address": t.address, "cwd": t.cwd, "ids": ids,
		"pid": float64(pid), "stamped": float64(len(reserved))})
}

// reserve stamps every message delivered before anything starts, and stops at the
// first one it cannot reach — returning what it did take so the caller can give it
// back. A partial reservation is never launched against: all of the mail travels, or
// none of it does.
func reserve(address string, rows []fleet.Rec) ([]string, error) {
	tenant, err := fleet.MailAddressTenant(address)
	if err != nil {
		return nil, err
	}
	var taken []string
	for _, r := range rows {
		id := fleet.S(r, "id")
		fields := fleet.Rec{"delivered_at": fleet.Now(), "delivered_by": watchAddress}
		if _, err := fleet.StampMail(tenant, address, id, fields); err != nil {
			return taken, fmt.Errorf("reserve %s: %w", id, err)
		}
		taken = append(taken, id)
	}
	return taken, nil
}

// release gives back reservations no process received. A message it cannot reach is
// stranded — stamped delivered with nothing running — and says so, once per message,
// because that is the only trace an operator has to go on.
func release(address string, ids []string) []fleet.Rec {
	if len(ids) == 0 {
		return nil
	}
	tenant, err := fleet.MailAddressTenant(address)
	if err != nil {
		return []fleet.Rec{{"at": fleet.Now(), "what": "mail-reservation-stranded", "address": address, "ids": ids, "error": err.Error()}}
	}
	var out []fleet.Rec
	for _, id := range ids {
		if err := fleet.UnstampMail(tenant, address, id, "delivered_at", "delivered_by"); err != nil {
			out = append(out, fleet.Rec{"at": fleet.Now(), "what": "mail-reservation-stranded", "address": address, "id": id, "error": err.Error()})
		}
	}
	return out
}

// prompt is what the started process is told: which address it is answering for, and
// the same bounded summary a session's own hook would have been given.
func prompt(address string, rows []fleet.Rec) string {
	lines := append([]string{"[fleet] mail for " + address + "; read the bodies with `fleet mail --unacked` and `fleet ack <id>` once read."},
		fleet.MailSummary(rows)...)
	return strings.Join(lines, "\n")
}

// present protects an existing occupant even while it is idle. Silence is not exit.
func present(sessions []fleet.Rec, cwd string) bool {
	want := fleet.CanonPath(cwd)
	for _, s := range sessions {
		if fleet.B(s, "ended") {
			continue
		}
		if fleet.S(s, "pid_kind") == "harness" && !processPresent(int(fleet.F(s, "pid"))) {
			continue
		}
		path := fleet.S(s, "launch_dir")
		if path == "" {
			path = fleet.S(s, "cwd")
		}
		if fleet.CanonPath(path) == want {
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

func addressStopped(address string) bool {
	key, err := fleet.MailStopKey(address)
	return err == nil && fleet.StopFlag(key) != nil
}
