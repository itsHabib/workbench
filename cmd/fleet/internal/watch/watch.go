// Package watch is Fleet's Go scheduler, delivery launcher and observer.
//
// Board facts are folded from records the hooks already wrote — sessions,
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
//	watch/late.json        the deadlines already said out loud — so each is said once
//	watch/delivery/        launch records, per-attempt output and collected process exits
//
// It also stamps delivered_at/delivered_by on the mail records it hands to a process.
// That is the whole of what it writes outside watch/, and it is a stamp, not an ack.
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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/report"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
	"github.com/itsHabib/workbench/filelock"
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
// A diagnostic fold must not replace a persistent watcher's heartbeat.
func Tick(interval time.Duration) (string, error) {
	var md string
	err := withOwner(func() error {
		var err error
		md, err = tick(interval)
		return err
	})
	return md, err
}

func tick(interval time.Duration) (string, error) {
	now := fleet.Now()
	prev := Heartbeat()
	prevAt := fleet.F(prev, "at")
	slept := prev != nil && prevAt > 0 && now-prevAt > 3*interval.Seconds()
	prevRows := map[string]fleet.Rec{}
	for _, r := range readRows(filepath.Join(dir(), "board.json")) {
		prevRows[fleet.S(r, "path")] = r
	}
	ticks := fleet.F(prev, "ticks")
	maybeSync(ticks)
	prevStates := map[string]string{}
	for p, r := range prevRows {
		prevStates[p] = fleet.S(r, "state")
	}
	rows := fold(prevAt, slept, prevStates)
	transitions := seatTransitions(rows, prevRows, now)
	work, wt := workTransitions(now, prevAt, slept)
	transitions = append(transitions, wt...)
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return "", err
	}
	for _, t := range transitions {
		_ = fleet.AppendJSONL(filepath.Join(dir(), "observed.jsonl"), t)
	}
	// Seed existing rows on upgrade and retain one daily observation even when a
	// row stays unchanged. This supplies daily counts without an agent declaration.
	if fleet.F(prev, "report_schema") == 0 || int64(now)/86400 != int64(prevAt)/86400 {
		for _, r := range work {
			_ = fleet.AppendJSONL(filepath.Join(dir(), "observed.jsonl"), fleet.Rec{"at": now, "repo": r["repo"], "change": r["change"], "relationship": r["relationship"], "row": r, "what": "snapshot"})
		}
	}
	md := render(rows, work, now, prev, slept, transitions)
	hb := fleet.Rec{"at": now, "pid": float64(os.Getpid()), "interval": interval.Seconds(), "slept": slept, "rows": float64(len(rows)), "work": float64(len(work)), "transitions": float64(len(transitions)), "ticks": ticks + 1, "report_schema": 1}
	if slept {
		hb["gap_from"] = prevAt
	}
	if err := publish(rows, work, md, hb); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir(), "report.md"), []byte(report.Render(fleet.Path(), now-86400, now)), 0o644); err != nil {
		_ = fleet.AppendJSONL(fleet.Path("hook-errors.jsonl"), fleet.Rec{"at": fleet.Now(), "error": "watch report: " + err.Error()})
	}
	// Lateness first, then delivery: a report derived this fold is carried by the same
	// fold rather than waiting for the next one. Both run AFTER publication, for the
	// reason the notifier does — neither may hold the board back.
	for _, o := range append(lateMail(now, work), deliver(fleet.Now())...) {
		_ = fleet.AppendJSONL(filepath.Join(dir(), "observed.jsonl"), o)
	}
	// Notification AFTER publication: a slow notifier must not hold the board or the
	// heartbeat back, and never widens the window in which a second watcher could start.
	for _, t := range transitions {
		if fleet.S(t, "what") != "" {
			continue
		}
		if attention[fleet.S(t, "to")] || (fleet.S(t, "change") != "" && (verbs.WorkAttention[fleet.S(t, "to")] || fleet.S(t, "to") == "done")) {
			notify(t)
		}
	}
	return md, nil
}

// maybeSync is the read side of the remote record: every tenth tick, when gh is here.
func maybeSync(ticks float64) {
	if os.Getenv("FLEET_GITHUB") == "off" || int(ticks)%10 != 0 {
		return
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return
	}
	var sink strings.Builder
	prevOut := verbs.Out
	verbs.Out = &sink
	_ = verbs.CmdSync("")
	verbs.Out = prevOut
}

// seatTransitions is every seat whose state differs from the previous board.
func seatTransitions(rows []fleet.Rec, prevRows map[string]fleet.Rec, now float64) []fleet.Rec {
	var transitions []fleet.Rec
	for _, r := range rows {
		from := fleet.S(prevRows[fleet.S(r, "path")], "state")
		to := fleet.S(r, "state")
		if from == to {
			continue
		}
		transitions = append(transitions, fleet.Rec{"at": now, "path": r["path"], "role": r["role"], "slot": r["slot"], "from": nilIfEmpty(from), "to": to,
			"session": r["session"], "branch": r["branch"], "repo": fleet.RepoID(fleet.S(r, "path"))})
	}
	return transitions
}

// workTransitions folds the ownership rows and diffs them by (change, relationship)
// against the previous work.json: state changes, changes of accountable / hands /
// due with the state unchanged, and rows that disappeared ("gone").
func workTransitions(now, prevAt float64, slept bool) ([]fleet.Rec, []fleet.Rec) {
	prevWork := map[string]fleet.Rec{}
	for _, r := range readRows(filepath.Join(dir(), "work.json")) {
		prevWork[workKey(r)] = r
	}
	work := make([]fleet.Rec, 0)
	var transitions []fleet.Rec
	seen := map[string]bool{}
	for _, w := range verbs.WorkRows("") {
		r := fleet.Rec(w)
		k := workKey(r)
		seen[k] = true
		work = append(work, r)
		transitions = append(transitions, rowTransitions(r, prevWork[k], now, prevAt, slept)...)
	}
	for k, r := range prevWork {
		if seen[k] || fleet.S(r, "state") == "gone" {
			continue
		}
		transitions = append(transitions, fleet.Rec{"at": now, "repo": r["repo"], "row": r, "change": r["change"], "relationship": r["relationship"], "for": r["for"],
			"from": nilIfEmpty(fleet.S(r, "state")), "to": "gone", "session": r["hands"], "branch": r["change"]})
	}
	return work, transitions
}

// rowTransitions is what changed on one row since the previous tick. The sleep rule
// applies here too: a NEW attention classification after a gap may be the gap's
// silence, not an observation, and reads unknown.
func rowTransitions(r, prev fleet.Rec, now, prevAt float64, slept bool) []fleet.Rec {
	from, to := fleet.S(prev, "state"), fleet.S(r, "state")
	if slept && verbs.WorkAttention[to] && from != to {
		r["unknown_since"] = prevAt
		r["state"] = "unknown"
		to = "unknown"
	}
	if from != to {
		return []fleet.Rec{{"at": now, "repo": r["repo"], "row": r, "change": r["change"], "relationship": r["relationship"], "for": r["for"],
			"from": nilIfEmpty(from), "to": to, "session": r["hands"], "branch": r["change"]}}
	}
	if prev == nil {
		return nil
	}
	// Responsibility can change with the state unchanged: who is accountable, who
	// has hands, when it is due. Each is a transition the hub must see.
	var out []fleet.Rec
	for _, col := range []string{"for", "hands", "due"} {
		a, b := colText(prev[col]), colText(r[col])
		if a == b {
			continue
		}
		out = append(out, fleet.Rec{"at": now, "repo": r["repo"], "row": r, "change": r["change"], "relationship": r["relationship"], "for": r["for"],
			"what": col, "from": nilIfEmpty(a), "to": b, "session": r["hands"], "branch": r["change"]})
	}
	return out
}

// publish writes the board, the work rows, the rendered markdown and the heartbeat.
func publish(rows, work []fleet.Rec, md string, hb fleet.Rec) error {
	if err := fleet.WriteJSON(filepath.Join(dir(), "board.json"), rowsAny(rows)); err != nil {
		return err
	}
	if err := fleet.WriteJSON(filepath.Join(dir(), "work.json"), rowsAny(work)); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir(), "board.md"), []byte(md), 0o644); err != nil {
		return err
	}
	return fleet.WriteJSON(filepath.Join(dir(), "heartbeat.json"), hb)
}

// colText is a column's value as the text a transition records.
func colText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	}
	return fmt.Sprint(v)
}

// fold is the join, classified for the board: the roled paths from `fleet board`,
// plus one state the board cannot see alone — a seat with an assignment and no one
// in it — and the sleep rule applied over both.
func fold(prevAt float64, slept bool, prevStates map[string]string) []fleet.Rec {
	var rows []fleet.Rec
	for _, r := range verbs.BoardRows() {
		row := fleet.Rec(r)
		state := fleet.S(row, "state")
		holds := holdsOf(row)
		if state == "dead" && len(holds) > 0 {
			state = "dead-holding-work"
		}
		// A dead session's work is reported whichever occupant won the row.
		if dh := strsOf(row["dead_holds"]); len(dh) > 0 {
			state = "dead-holding-work"
			if len(holds) == 0 {
				row["holds"] = dh
			}
		}
		if slept && (state == "dead" || state == "dead-holding-work" || state == "busy-and-overdue") {
			// The evidence for a classification the previous tick did not make may be
			// the silence of a sleeping machine — a deadline that "expired" or a pid
			// that "died" while nothing was running — as may a last event inside the gap.
			last := fleet.F(row, "last_event_at")
			if prevStates[fleet.S(row, "path")] != state || last == 0 || last > prevAt {
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

// workKey identifies an ownership row across ticks.
func workKey(r fleet.Rec) string {
	return fleet.S(r, "repo") + "|" + fleet.S(r, "change") + "|" + fleet.S(r, "relationship")
}

func strsOf(v any) []string {
	switch h := v.(type) {
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
func render(rows, work []fleet.Rec, now float64, prev fleet.Rec, slept bool, transitions []fleet.Rec) string {
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
	need, unknown, fine := triage(rows)
	if len(need) > 0 {
		fmt.Fprintf(&b, "\n## Needs a decision (%d)\n", len(need))
		for _, r := range need {
			b.WriteString(line(r, now))
		}
	}
	// Work first by accountable role: the rows a hub must decide something about.
	needWork, fineWork := triageWork(work)
	if len(needWork) > 0 {
		fmt.Fprintf(&b, "\n## Work needing a decision (%d)\n", len(needWork))
		for _, w := range needWork {
			fmt.Fprintf(&b, "- %s\n", verbs.WorkLine(w, now))
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
			b.WriteString(transitionLine(t))
		}
	}
	b.WriteString(fineLine(rows, fine, now))
	b.WriteString(fineWorkLine(fineWork))
	return b.String()
}

// triage splits seat rows into needing a decision (by role), unknown, and fine.
func triage(rows []fleet.Rec) (need, unknown, fine []fleet.Rec) {
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
	return need, unknown, fine
}

// triageWork splits ownership rows into needing a decision (by accountable role) and fine.
func triageWork(work []fleet.Rec) (need, fine []fleet.Rec) {
	for _, w := range work {
		if verbs.WorkAttention[fleet.S(w, "state")] {
			need = append(need, w)
			continue
		}
		fine = append(fine, w)
	}
	sort.SliceStable(need, func(i, j int) bool { return fleet.S(need[i], "for") < fleet.S(need[j], "for") })
	return need, fine
}

func transitionLine(t fleet.Rec) string {
	from := fleet.S(t, "from")
	if from == "" {
		from = "—"
	}
	if c := fleet.S(t, "change"); c != "" {
		if what := fleet.S(t, "what"); what != "" {
			return fmt.Sprintf("- %s %s %s: %s → %s\n", orDash(fleet.S(t, "for")), workName(t), what, from, fleet.S(t, "to"))
		}
		return fmt.Sprintf("- %s %s: %s → %s\n", orDash(fleet.S(t, "for")), workName(t), from, fleet.S(t, "to"))
	}
	return fmt.Sprintf("- %s %s: %s → %s\n", fleet.S(t, "role"), nameOf(t), from, fleet.S(t, "to"))
}

// fineLine: everything fine is one line, with what it hides and how old the oldest
// observation is.
func fineLine(rows, fine []fleet.Rec, now float64) string {
	if len(fine) == 0 {
		if len(rows) == 0 {
			return "\nno roled paths\n"
		}
		return ""
	}
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
	return fmt.Sprintf("\n%d fine (%s); oldest observation %s ago\n", len(fine), strings.Join(parts, ", "), fleet.FmtAge(now-oldest))
}

func fineWorkLine(fineWork []fleet.Rec) string {
	if len(fineWork) == 0 {
		return ""
	}
	wc := map[string]int{}
	for _, w := range fineWork {
		wc[fleet.S(w, "state")]++
	}
	var wp []string
	for _, k := range []string{"working", "idle", "dispatched", "done"} {
		if wc[k] > 0 {
			wp = append(wp, fmt.Sprintf("%d %s", wc[k], k))
		}
	}
	return fmt.Sprintf("%d work rows fine (%s)\n", len(fineWork), strings.Join(wp, ", "))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// workName is change/relationship, or the change alone for an undeclared row.
func workName(r fleet.Rec) string {
	n := fleet.S(r, "change")
	if rel := fleet.S(r, "relationship"); rel != "" {
		n += "/" + rel
	}
	return n
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, interval)
}

func serve(ctx context.Context, interval time.Duration) error {
	return withOwner(func() error { return serveOwned(ctx, interval) })
}

func withOwner(run func() error) error {
	// One writer of one board: an advisory lock held for the process's lifetime, so a
	// second watcher started inside the first's tick — before any heartbeat exists —
	// is refused too. Kernel-released on death, like every lock here.
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	owner, err := os.OpenFile(filepath.Join(dir(), "owner.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = owner.Close() }()
	if err := filelock.TryLock(owner); err != nil {
		hb := Heartbeat()
		if fleet.F(hb, "pid") <= 0 || fleet.F(hb, "at") <= 0 {
			return fmt.Errorf("watcher lock unavailable; heartbeat unknown (missing or incomplete): %w", err)
		}
		return fmt.Errorf("watcher lock unavailable; last recorded heartbeat pid %d, %s ago: %w", int(fleet.F(hb, "pid")), fleet.FmtAge(fleet.Now()-fleet.F(hb, "at")), err)
	}
	defer func() { _ = filelock.Unlock(owner) }()
	return run()
}

func serveOwned(ctx context.Context, interval time.Duration) error {
	defer func() {
		_ = fleet.AppendJSONL(filepath.Join(dir(), "observed.jsonl"), fleet.Rec{"at": fleet.Now(), "what": "watcher-stopped", "pid": os.Getpid()})
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if _, err := tick(interval); err != nil {
			_ = fleet.AppendJSONL(fleet.Path("hook-errors.jsonl"), fleet.Rec{"at": fleet.Now(), "error": "watch tick: " + err.Error()})
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
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
