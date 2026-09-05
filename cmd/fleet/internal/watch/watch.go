// Package watch is the one standing process the fleet has, and it owns nothing.
//
// Every fact it shows is a fold over records the hooks already wrote — sessions,
// leases, roles.map — plus what `unowned` can learn from the network. It ticks a
// clock nobody else has (a stopped worker is indistinguishable from a slow one until
// something measures time when no one is looking), classifies every roled path,
// writes the board, records each transition, and notifies. If it dies: no alerts and
// no live board, and `fleet board` folds the same files itself. Hooks never talk to
// it. Nothing fails closed that did not already.
//
// What it writes, and how each record is superseded:
//
//	watch/heartbeat.json   this process, its interval, and the last tick — replaced every tick
//	watch/board.json       the rows as of the last tick — replaced every tick
//	watch/board.md         the same rows, attention-budgeted for a person — replaced every tick
//	watch/observed.jsonl   one line per state transition — appended, the recording of a day
//
// The board is attention-budgeted. A board with forty green rows and two red should
// show the two; "everything is fine" is one line that carries the count it hides and
// the age of the oldest observation behind it, because hidden green hides stale green.
//
// Sleep. If the gap since the last tick exceeds three intervals the machine slept, and
// no row is called dead or overdue on evidence that falls entirely inside the gap:
// those rows read `unknown since <gap start>` until one fresh tick has seen them.
// This is the difference between a useful 3am and a hated one.
//
// Notification is an operator-configured command (FLEET_NOTIFY), given the transition
// as JSON on stdin. The substrate does not learn the notifier's vocabulary.
package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
)

// DefaultInterval is one tick.
const DefaultInterval = 60 * time.Second

// Attention are the states that need a decision. Everything else is fine or is waiting.
var attention = map[string]bool{"busy-and-overdue": true, "dead-holding-work": true, "assigned-no-occupant": true}

func dir() string { return fleet.Path("watch") }

// Heartbeat is the watcher's own record.
func Heartbeat() fleet.Rec { return fleet.ReadJSON(filepath.Join(dir(), "heartbeat.json")) }

// Stale reports whether no watcher has ticked within `intervals` intervals.
func Stale(intervals float64) bool {
	hb := Heartbeat()
	if hb == nil {
		return true
	}
	iv := fleet.F(hb, "interval")
	if iv <= 0 {
		iv = DefaultInterval.Seconds()
	}
	return fleet.Now()-fleet.F(hb, "at") > intervals*iv
}

// Tick folds once and writes the board. It returns the rendered board.
func Tick(interval time.Duration) (string, error) {
	now := fleet.Now()
	prev := Heartbeat()
	prevAt := fleet.F(prev, "at")
	slept := prev != nil && prevAt > 0 && now-prevAt > 3*interval.Seconds()
	prevRows := map[string]fleet.Rec{}
	if pb := readRows(filepath.Join(dir(), "board.json")); pb != nil {
		for _, r := range pb {
			prevRows[fleet.S(r, "path")] = r
		}
	}
	rows := fold(now, prevAt, slept)
	var transitions []fleet.Rec
	for _, r := range rows {
		from := fleet.S(prevRows[fleet.S(r, "path")], "state")
		to := fleet.S(r, "state")
		if from == to {
			continue
		}
		t := fleet.Rec{"at": now, "path": r["path"], "role": r["role"], "slot": r["slot"], "from": nilIfEmpty(from), "to": to,
			"session": r["session"], "branch": r["branch"]}
		transitions = append(transitions, t)
	}
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return "", err
	}
	for _, t := range transitions {
		_ = fleet.AppendJSONL(filepath.Join(dir(), "observed.jsonl"), t)
		if attention[fleet.S(t, "to")] {
			notify(t)
		}
	}
	md := render(rows, now, prev, slept, transitions)
	if err := fleet.WriteJSON(filepath.Join(dir(), "board.json"), rowsAny(rows)); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir(), "board.md"), []byte(md), 0o644); err != nil {
		return "", err
	}
	hb := fleet.Rec{"at": now, "pid": float64(os.Getpid()), "interval": interval.Seconds(), "slept": slept, "rows": float64(len(rows)), "transitions": float64(len(transitions))}
	if slept {
		hb["gap_from"] = prevAt
	}
	if err := fleet.WriteJSON(filepath.Join(dir(), "heartbeat.json"), hb); err != nil {
		return "", err
	}
	return md, nil
}

// fold is the join, classified for the board: the roled paths from `fleet board`,
// plus one state the board cannot see alone — a seat with an assignment and no one
// in it — and the sleep rule applied over both.
func fold(now, prevAt float64, slept bool) []fleet.Rec {
	var rows []fleet.Rec
	for _, r := range verbs.BoardRows() {
		row := fleet.Rec(r)
		state := fleet.S(row, "state")
		holds := holdsOf(row)
		if state == "dead" && len(holds) > 0 {
			state = "dead-holding-work"
		}
		if slept && (state == "dead" || state == "dead-holding-work" || state == "busy-and-overdue") {
			// The evidence for this classification may be the silence of a sleeping machine.
			if last := fleet.F(row, "last_event_at"); last == 0 || last > prevAt {
				row["unknown_since"] = prevAt
				state = "unknown"
			}
		}
		row["state"] = state
		rows = append(rows, row)
	}
	for _, s := range verbs.SlotRows("") {
		a, _ := s["assigned"].(fleet.Rec)
		if a == nil {
			continue
		}
		if st := fleet.S(s, "state"); st == "free" || st == "missing" || st == "dirty" {
			for _, row := range rows {
				if fleet.S(row, "slot") == fleet.S(s, "slot") {
					row["state"] = "assigned-no-occupant"
					row["assigned"] = fleet.Rec{"branch": a["branch"], "at": a["at"], "by": a["by"], "for": a["for"]}
				}
			}
		}
	}
	return rows
}

func holdsOf(r fleet.Rec) []string {
	switch h := r["holds"].(type) {
	case []string:
		return h
	case []any:
		var out []string
		for _, x := range h {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func rowsAny(rows []fleet.Rec) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = map[string]any(r)
	}
	return out
}

func readRows(p string) []fleet.Rec {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var v []map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	out := make([]fleet.Rec, len(v))
	for i, r := range v {
		out[i] = r
	}
	return out
}

// render is the attention-budgeted board: rows needing a decision first, grouped by
// role; then what changed this tick; then everything that is fine, as one line.
func render(rows []fleet.Rec, now float64, prev fleet.Rec, slept bool, transitions []fleet.Rec) string {
	host, _ := os.Hostname()
	var b strings.Builder
	hbAge := "first tick"
	if prev != nil {
		hbAge = fleet.FmtAge(now-fleet.F(prev, "at")) + " since last tick"
	}
	fmt.Fprintf(&b, "# fleet board · %s · %s · %s\n", host, time.Unix(int64(now), 0).Format("2006-01-02 15:04"), hbAge)
	if slept {
		fmt.Fprintf(&b, "machine slept: %s of silence; rows whose only evidence is that silence read `unknown`\n", fleet.FmtAge(now-fleet.F(prev, "at")))
	}
	var need, fine, unknown []fleet.Rec
	for _, r := range rows {
		switch st := fleet.S(r, "state"); {
		case attention[st]:
			need = append(need, r)
		case st == "unknown":
			unknown = append(unknown, r)
		default:
			fine = append(fine, r)
		}
	}
	sort.SliceStable(need, func(i, j int) bool { return fleet.S(need[i], "role") < fleet.S(need[j], "role") })
	if len(need) > 0 {
		fmt.Fprintf(&b, "\n## Needs a decision (%d)\n", len(need))
		for _, r := range need {
			b.WriteString(line(r, now))
		}
	}
	if len(unknown) > 0 {
		fmt.Fprintf(&b, "\n## Unknown (%d) — no evidence since the machine slept\n", len(unknown))
		for _, r := range unknown {
			b.WriteString(line(r, now))
		}
	}
	if len(transitions) > 0 {
		fmt.Fprintf(&b, "\n## Changed this tick (%d)\n", len(transitions))
		for _, t := range transitions {
			from := fleet.S(t, "from")
			if from == "" {
				from = "—"
			}
			fmt.Fprintf(&b, "- %s %s: %s → %s\n", fleet.S(t, "role"), nameOf(t), from, fleet.S(t, "to"))
		}
	}
	// Everything fine is one line, with what it hides and how old the oldest observation is.
	counts := map[string]int{}
	oldest := now
	for _, r := range fine {
		counts[fleet.S(r, "state")]++
		if at := fleet.F(r, "last_event_at"); at > 0 && at < oldest {
			oldest = at
		}
	}
	var parts []string
	for _, k := range []string{"busy", "idle-holding-work", "idle", "vacant", "dead"} {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	if len(fine) > 0 {
		fmt.Fprintf(&b, "\n%d fine (%s); oldest observation %s ago\n", len(fine), strings.Join(parts, ", "), fleet.FmtAge(now-oldest))
	} else if len(rows) == 0 {
		b.WriteString("\nno roled paths\n")
	}
	return b.String()
}

func nameOf(r fleet.Rec) string {
	if s := fleet.S(r, "slot"); s != "" {
		return s
	}
	return filepath.Base(strings.TrimRight(fleet.S(r, "path"), "/"))
}

func line(r fleet.Rec, now float64) string {
	who := "-"
	if s := fleet.S(r, "session"); s != "" {
		who = fleet.Short(s)
	}
	age := ""
	if at := fleet.F(r, "last_event_at"); at > 0 {
		age = fmt.Sprintf(", last %s ago", fleet.FmtAge(now-at))
	}
	detail := ""
	switch fleet.S(r, "state") {
	case "dead-holding-work":
		var labels []string
		for _, k := range holdsOf(r) {
			labels = append(labels, fleet.KeyLabel(k))
		}
		detail = " — holds " + strings.Join(labels, ", ")
	case "busy-and-overdue":
		detail = fmt.Sprintf(" — turn open %s, cadence %s", fleet.FmtAge(now-fleet.F(r, "turn_open_at")), fleet.FmtAge(fleet.F(r, "cadence")))
	case "assigned-no-occupant":
		a := fleet.M(r, "assigned")
		detail = fmt.Sprintf(" — assigned %s %s ago by %s for %s, nobody there", fleet.S(a, "branch"), fleet.FmtAge(now-fleet.F(a, "at")), fleet.S(a, "by"), fleet.S(a, "for"))
	case "unknown":
		detail = fmt.Sprintf(" — unknown since %s ago", fleet.FmtAge(now-fleet.F(r, "unknown_since")))
	}
	return fmt.Sprintf("- **%s** %s %s (%s%s)%s\n", fleet.S(r, "state"), fleet.S(r, "role"), nameOf(r), who, age, detail)
}

// notify hands one transition to the operator's notifier, if one is configured.
// Best effort, never blocking a tick for long, never a reason for the watcher to die.
func notify(t fleet.Rec) {
	cmdline := os.Getenv("FLEET_NOTIFY")
	if cmdline == "" {
		return
	}
	words := fleet.ShellWords(cmdline)
	if len(words) == 0 {
		return
	}
	cmd := exec.Command(words[0], words[1:]...)
	cmd.Stdin = strings.NewReader(string(fleet.DumpJSON(t)) + "\n")
	done := make(chan struct{})
	go func() { _ = cmd.Run(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

// Serve ticks forever. One per machine: a second watcher finding a fresh heartbeat
// from a live pid exits rather than compete.
func Serve(interval time.Duration) error {
	if hb := Heartbeat(); hb != nil && !Stale(2) && fleet.PidAlive(int(fleet.F(hb, "pid"))) && int(fleet.F(hb, "pid")) != os.Getpid() {
		return fmt.Errorf("a watcher is already ticking (pid %d, last tick %s ago)", int(fleet.F(hb, "pid")), fleet.FmtAge(fleet.Now()-fleet.F(hb, "at")))
	}
	for {
		if _, err := Tick(interval); err != nil {
			_ = fleet.AppendJSONL(fleet.Path("hook-errors.jsonl"), fleet.Rec{"at": fleet.Now(), "error": "watch tick: " + err.Error()})
		}
		time.Sleep(interval)
	}
}

// EnsureRunning starts a detached watcher if none has ticked recently. Called from
// SessionStart — the one event where a spawn is permitted — so there is no install
// step: any session on the machine revives a dead watcher. Off when FLEET_WATCH=off.
func EnsureRunning() {
	if os.Getenv("FLEET_WATCH") == "off" || !Stale(2) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return
	}
	log, err := os.OpenFile(filepath.Join(dir(), "watch.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer log.Close()
	cmd := exec.Command(exe, "watch")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Stdin = nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		_ = fleet.AppendJSONL(fleet.Path("hook-errors.jsonl"), fleet.Rec{"at": fleet.Now(), "error": "watch start: " + err.Error()})
		return
	}
	_ = cmd.Process.Release()
}
