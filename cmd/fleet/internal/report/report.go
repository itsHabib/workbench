// Package report folds passive local telemetry without mutating the substrate.
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

type records = []fleet.Rec

type source struct {
	rows    records
	problem string
}

func readLog(root, name string, now float64) source {
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return source{problem: name + ": " + err.Error()}
	}
	defer f.Close()
	var out source
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 4*1024*1024)
	bad := 0
	for scan.Scan() {
		r := fleet.ReadJSONBytes(scan.Bytes())
		if r == nil || fleet.F(r, "at") <= 0 {
			bad++
			continue
		}
		if fleet.F(r, "at") <= now {
			out.rows = append(out.rows, r)
		}
	}
	if bad > 0 {
		out.problem = fmt.Sprintf("%s: %d malformed records skipped", name, bad)
	}
	if err := scan.Err(); err != nil {
		out.problem += " " + name + ": " + err.Error()
	}
	sort.SliceStable(out.rows, func(i, j int) bool { return fleet.F(out.rows[i], "at") < fleet.F(out.rows[j], "at") })
	return out
}

func stamp(at float64) string {
	if at <= 0 {
		return "unknown"
	}
	return time.UnixMilli(int64(at * 1000)).UTC().Format(time.RFC3339)
}
func cell(s string) string {
	r := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
func rowID(r fleet.Rec) string {
	return fleet.S(r, "repo") + " / " + fleet.S(r, "change") + " / " + fleet.S(r, "relationship")
}
func key(r fleet.Rec) string {
	return string(fleet.DumpJSON([]string{fleet.S(r, "repo"), fleet.S(r, "change"), fleet.S(r, "relationship")}))
}
func inWindow(r fleet.Rec, since float64) bool { return fleet.F(r, "at") >= since }

// Render returns a deterministic Markdown view as of now. Missing telemetry is
// stated explicitly; historical shadow verdicts are never counted as real ones.
func Render(root string, since, now float64) string {
	events := readLog(root, "events.jsonl", now)
	observed := readLog(root, "watch/observed.jsonl", now)
	actions := readLog(root, "actions.jsonl", now)
	var b strings.Builder
	fmt.Fprintf(&b, "# Fleet report\n\n%s → %s (UTC)\n\n", stamp(since), stamp(now))
	b.WriteString("Derived local observations. Missing records are unknown; zero recorded events does not prove zero activity. Watcher timestamps measure observation, not the exact time a state changed.\n\n")
	for _, s := range []source{events, observed, actions} {
		if s.problem != "" {
			fmt.Fprintf(&b, "- Coverage: %s\n", cell(s.problem))
		}
	}
	b.WriteString("\n")
	actual := records{}
	for _, e := range events.rows {
		if !fleet.B(e, "shadow") {
			actual = append(actual, e)
		}
	}
	if len(actual) > 0 {
		fmt.Fprintf(&b, "First retained real hook event: %s. Earlier shadow history is excluded.\n\n", stamp(fleet.F(actual[0], "at")))
	}
	refusals(&b, actual, since, now)
	attention(&b, observed.rows, actions.rows, since, now)
	lifecycle(&b, observed.rows, actions.rows, since, now)
	hookStats(&b, actual, since)
	return b.String()
}

func nextOutcome(d, n fleet.Rec) string {
	if n == nil {
		return "no next event recorded (idle unknown)"
	}
	ev := fleet.S(n, "event")
	if ev == "Stop" || ev == "SessionEnd" {
		return "went idle / ended"
	}
	if ev != "PreToolUse" {
		return "next event " + ev + "; work outcome unknown"
	}
	fp := fleet.S(d, "fingerprint")
	if fp != "" && fp == fleet.S(n, "fingerprint") {
		return "retried the same command"
	}
	if fleet.S(d, "cwd") != "" && fleet.S(n, "cwd") != "" && fleet.S(d, "cwd") != fleet.S(n, "cwd") && fleet.F(n, "code") == 0 {
		return "worked elsewhere (different cwd; allowed)"
	}
	return "different or unidentifiable tool call; destination unknown"
}
func refusals(b *strings.Builder, es records, since, now float64) {
	b.WriteString("## Refusals and the session's next event\n\n| At | Session | Reason | Next event | Wait |\n|---|---|---|---|---|\n")
	next := map[string]fleet.Rec{}
	lines := []string{}
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		sid := fleet.S(e, "harness") + "/" + fleet.S(e, "session")
		if inWindow(e, since) && fleet.F(e, "code") != 0 {
			n := next[sid]
			until := now
			if n != nil {
				until = fleet.F(n, "at")
			}
			lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %.1fs |\n", stamp(fleet.F(e, "at")), cell(sid), cell(fleet.S(e, "reason")), cell(nextOutcome(e, n)), until-fleet.F(e, "at")))
		}
		if fleet.S(e, "session") != "" {
			next[sid] = e
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		b.WriteString(lines[i])
	}
	fmt.Fprintf(b, "\n%d recorded refusals. A retry is evidence of repetition, not proof of the session's understanding.\n\n", len(lines))
}

var needs = map[string]bool{"busy-and-overdue": true, "dead-holding-work": true, "assigned-no-occupant": true, "dead": true, "late": true, "undeclared": true, "abandoned": true, "failed": true, "unknown": true}

func matches(t, r fleet.Rec) bool {
	if slot := fleet.S(t, "slot"); slot != "" && slot == fleet.S(r, "slot") {
		return true
	}
	change := fleet.S(t, "change")
	if change == "" {
		change = fleet.S(t, "branch")
	}
	if fleet.S(t, "repo") == "" || change == "" {
		return false
	}
	if fleet.S(t, "repo") != fleet.S(r, "repo") || change != fleet.S(r, "change") {
		return false
	}
	return fleet.S(t, "relationship") == "" || fleet.S(r, "relationship") == "" || fleet.S(r, "relationship") == fleet.S(t, "relationship")
}
func nextAction(t fleet.Rec, acts records, until float64) fleet.Rec {
	for _, a := range acts {
		if fleet.F(a, "at") < fleet.F(t, "at") || fleet.F(a, "at") > until {
			continue
		}
		if matches(t, fleet.M(a, "row")) {
			return a
		}
	}
	return nil
}
func attentionEnd(t fleet.Rec, obs records, now float64) float64 {
	for _, n := range obs {
		if fleet.F(n, "at") <= fleet.F(t, "at") || fleet.S(n, "what") != "" || needs[fleet.S(n, "to")] {
			continue
		}
		same := fleet.S(t, "path") != "" && fleet.S(t, "path") == fleet.S(n, "path")
		if same || (fleet.S(t, "repo") != "" && key(t) == key(n)) {
			return fleet.F(n, "at")
		}
	}
	return now
}
func attention(b *strings.Builder, obs, acts records, since, now float64) {
	b.WriteString("## Attention latency\n\n| Observed | Row / seat | State | Next recorded action | Latency / still waiting |\n|---|---|---|---|---|\n")
	n := 0
	for _, t := range obs {
		if !inWindow(t, since) || fleet.S(t, "what") != "" || !needs[fleet.S(t, "to")] {
			continue
		}
		end := attentionEnd(t, obs, now)
		a := nextAction(t, acts, end)
		action := "none recorded"
		if end < now {
			action = "none before attention cleared"
		}
		if a != nil {
			end = fleet.F(a, "at")
			action = fleet.S(a, "action")
		}
		id := rowID(t)
		if fleet.S(t, "path") != "" {
			id = fleet.S(t, "path")
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %.1fs |\n", stamp(fleet.F(t, "at")), cell(id), cell(fleet.S(t, "to")), action, end-fleet.F(t, "at"))
		n++
	}
	fmt.Fprintf(b, "\n%d transitions. Actions are committed dispatch / revoke / reassign operations; actor identity is not inferred. Legacy rows without repository identity cannot be correlated.\n\n", n)
}

type life struct {
	row                fleet.Rec
	first, hands, done float64
	seen, closed       float64
}

func epoch(r fleet.Rec) string { return key(r) + fmt.Sprintf("@%.6f", fleet.F(r, "at")) }
func addLife(ls map[string]*life, r fleet.Rec, at float64) {
	if r == nil || fleet.S(r, "repo") == "" || fleet.S(r, "relationship") == "" || fleet.F(r, "at") <= 0 {
		return
	}
	k := epoch(r)
	l := ls[k]
	if l == nil {
		for _, previous := range ls {
			if key(previous.row) == key(r) && previous.closed == 0 {
				previous.closed = at
			}
		}
		l = &life{first: fleet.F(r, "at")}
		ls[k] = l
	}
	l.row = r
	l.seen = at
	if fleet.S(r, "hands") != "" && l.hands == 0 {
		l.hands = at
	}
	if fleet.F(r, "done_at") > 0 {
		l.done = fleet.F(r, "done_at")
	}
}
func lifecycle(b *strings.Builder, obs, acts records, since, now float64) {
	ls := map[string]*life{}
	days := map[string]map[string]bool{}
	// Sort actions and snapshots together so accountable roles follow chronology.
	timeline := append(records{}, obs...)
	for _, a := range acts {
		timeline = append(timeline, fleet.Rec{"at": a["at"], "row": a["row"]})
	}
	sort.SliceStable(timeline, func(i, j int) bool { return fleet.F(timeline[i], "at") < fleet.F(timeline[j], "at") })
	for _, t := range timeline {
		r := fleet.M(t, "row")
		addLife(ls, r, fleet.F(t, "at"))
		if fleet.S(t, "to") == "gone" && ls[epoch(r)] != nil {
			ls[epoch(r)].closed = fleet.F(t, "at")
		}
		if !inWindow(t, since) || fleet.S(r, "state") != "undeclared" || fleet.S(t, "to") == "gone" {
			continue
		}
		day := stamp(fleet.F(t, "at"))[:10]
		if days[day] == nil {
			days[day] = map[string]bool{}
		}
		days[day][key(r)] = true
	}
	b.WriteString("## Undeclared rows per day\n\nUnique rows observed undeclared each UTC day (not an estimate for unobserved days).\n\n")
	for _, day := range sortedKeys(days) {
		fmt.Fprintf(b, "- %s: %d\n", day, len(days[day]))
	}
	if len(days) == 0 {
		b.WriteString("No undeclared row snapshots recorded in this window.\n")
	}
	b.WriteString("\n## Dispatch → hands → done\n\nHands is first observed occupancy, an upper bound on acquisition time. Rows include those dispatched or observed in the window.\n\n| Row | Accountable | Dispatch | Hands observed | Done receipt | Late |\n|---|---|---|---|---|---|\n")
	late := map[string]int{}
	for _, k := range sortedKeys(ls) {
		l := ls[k]
		if l.first < since && l.seen < since {
			continue
		}
		role := fleet.S(l.row, "for")
		lag := "no recorded lateness"
		due := fleet.F(l.row, "due")
		end := now
		if l.closed > 0 {
			end = l.closed
		}
		if l.done > 0 {
			end = l.done
		}
		if due > 0 && end > due {
			lag = fmt.Sprintf("%.1fs", end-due)
			late[role]++
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n", cell(rowID(l.row)), cell(role), stamp(l.first), stamp(l.hands), stamp(l.done), lag)
	}
	b.WriteString("\nLate rows by last recorded accountable role (not historical blame):\n")
	for _, role := range sortedKeys(late) {
		fmt.Fprintf(b, "- %s: %d\n", cell(role), late[role])
	}
	b.WriteString("\nLifecycle history starts with retained row snapshots / action records; deleted rows and earlier hand changes may be unknown.\n\n")
}
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return xs[int(math.Ceil(p*float64(len(xs))))-1]
}
func hookStats(b *strings.Builder, es records, since float64) {
	groups := map[string][]float64{}
	prompts, capped, lines := 0, 0, 0
	b.WriteString("## Silent takeovers of dead holders' branches\n\n")
	takeovers := 0
	for _, e := range es {
		if !inWindow(e, since) {
			continue
		}
		group := fleet.S(e, "harness") + "/" + fleet.S(e, "event")
		if ms, ok := e["ms"].(float64); ok && ms >= 0 {
			groups[group] = append(groups[group], ms)
		}
		if fleet.S(e, "event") == "UserPromptSubmit" {
			prompts++
			lines += int(fleet.F(e, "prompt_truncated"))
			if fleet.F(e, "prompt_truncated") > 0 {
				capped++
			}
		}
		raw, _ := json.Marshal(e["takeovers"])
		var ts records
		_ = json.Unmarshal(raw, &ts)
		for _, t := range ts {
			fmt.Fprintf(b, "- %s: %s, %s → %s\n", stamp(fleet.F(e, "at")), cell(fleet.S(t, "key")), cell(fleet.S(t, "from")), cell(fleet.S(t, "to")))
			takeovers++
		}
	}
	fmt.Fprintf(b, "\n%d recorded automatic replacements, excluding successful adapter rollbacks. A later tool refusal does not itself undo a lease.\n\n## Prompt truncation\n\n%d / %d prompt events capped at least one board line; %d lines exceeded the 700-byte cap.\n\n## Hook latency tails\n\n| Harness / event | n | p50 ms | p95 ms | p99 ms | max ms |\n|---|---:|---:|---:|---:|---:|\n", takeovers, capped, prompts, lines)
	for _, k := range sortedKeys(groups) {
		xs := groups[k]
		sort.Float64s(xs)
		fmt.Fprintf(b, "| %s | %d | %.3f | %.3f | %.3f | %.3f |\n", cell(k), len(xs), percentile(xs, .5), percentile(xs, .95), percentile(xs, .99), xs[len(xs)-1])
	}
	b.WriteString("\nLatency covers verdict evaluation, excluding telemetry append and watcher revival.\n")
}
