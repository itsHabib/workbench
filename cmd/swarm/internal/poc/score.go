package poc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/swarm"
)

// KillCheck is one line of KILL.md, evaluated.
type KillCheck struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
	// Exposed is false when the run never gave the condition a chance to
	// fail: the fault was not planted, or the run was shorter than the
	// threshold. An unexposed condition is "not tested", never a pass.
	Exposed bool `json:"exposed"`
}

// Score is one run against the kill conditions.
type Score struct {
	Mode              string            `json:"mode"`
	Run               string            `json:"run"`
	Stats             *swarm.Stats      `json:"stats"`
	Tasks             map[string]string `json:"tasks"`
	Sessions          int               `json:"sessions"`
	BuilderSessions   int               `json:"builder_sessions"`
	LeadTicks         int               `json:"lead_ticks"`
	Wakes             int               `json:"wakes"`
	OutputTokens      int               `json:"output_tokens"`
	LeadOutputTokens  int               `json:"lead_output_tokens"`
	WakeOutputTokens  int               `json:"wake_output_tokens"`
	InputTokens       int               `json:"input_tokens"`
	CostUSD           float64           `json:"cost_usd"`
	WallSeconds       float64           `json:"wall_s"`
	OperatorRequests  int               `json:"operator_requests"`
	OperatorUnmatched int               `json:"operator_unmatched"`
	Kills             []KillCheck       `json:"kills"`
	Consolidation     string            `json:"consolidation,omitempty"`
	Faults            map[string]string `json:"faults"`
	Notes             []string          `json:"notes,omitempty"`
}

// ScoreRun evaluates the sandbox at sandboxDir after the run at runDir.
func ScoreRun(sandboxDir, runDir string) (*Score, error) {
	main := filepath.Join(sandboxDir, "main")
	s, err := swarm.Open(main)
	if err != nil {
		return nil, err
	}
	var opts RunOptions
	_ = readJSONFile(filepath.Join(runDir, "options.json"), &opts)
	sc := &Score{Mode: opts.Mode, Run: filepath.Base(runDir), Tasks: map[string]string{}, Faults: map[string]string{}}
	if sc.Stats, err = s.Stats(20*time.Minute, "main"); err != nil {
		return nil, err
	}
	b, _, err := s.WatchOnce(swarm.WatchOptions{Base: "main", Idle: 10 * time.Minute, Verify: "go test ./...", Out: os.Stderr})
	if err != nil {
		return nil, err
	}
	rows := map[string]swarm.Row{}
	for _, r := range b.Rows {
		rows[r.Branch] = r
		sc.Tasks[r.Branch] = r.State
	}
	tasks, _ := LoadTasks(main)
	byFault := map[string]string{}
	for _, t := range tasks {
		if t.Fault != "" {
			byFault[t.Fault] = t.Branch
		}
	}
	sessions := sc.scoreSessions(runDir)
	sc.scoreWakes(s)
	sc.scoreOperator(runDir)
	decisions, _ := s.Decisions()
	events, _ := s.Events()

	sc.Kills = append(sc.Kills, KillCheck{ID: 1, Name: "no request unclaimed over 20m", Passed: sc.Stats.UnclaimedOver == 0, Exposed: sc.WallSeconds > 20*60,
		Detail: fmt.Sprintf("unclaimed_over=%d claim p50=%.0fs max=%.0fs rule p50=%.0fs max=%.0fs", sc.Stats.UnclaimedOver, sc.Stats.ClaimP50Seconds, sc.Stats.ClaimMaxSeconds, sc.Stats.RuleP50Seconds, sc.Stats.RuleMaxSeconds)})
	sc.Kills = append(sc.Kills, killContended(b, rows, decisions, sc.Stats))
	k3 := killGuess(rows, decisions, byFault["C"])
	sc.Kills = append(sc.Kills, k3)
	sc.Faults["C"] = k3.Detail
	k4 := killFaultA(main, rows, byFault["A"], pinAlerts(s))
	sc.Kills = append(sc.Kills, k4)
	sc.Faults["A"] = k4.Detail
	sc.Faults["B"] = faultB(sessions, rows, byFault["B"])
	sc.Consolidation = consolidation(main, rows)
	sc.Faults["D"] = faultD(events)
	if parent := byFault["E"]; parent != "" {
		sc.Faults["E"] = faultE(tasks, rows, parent)
	}
	sc.Kills = append(sc.Kills, KillCheck{ID: 5, Name: "operator requests (compared across modes)", Passed: true, Exposed: false,
		Detail: fmt.Sprintf("operator_requests=%d unmatched=%d", sc.OperatorRequests, sc.OperatorUnmatched)})
	return sc, nil
}

func (sc *Score) scoreSessions(runDir string) []Session {
	var sessions []Session
	_ = readJSONLines(filepath.Join(runDir, "sessions.jsonl"), func(line []byte) {
		var se Session
		if json.Unmarshal(line, &se) == nil {
			sessions = append(sessions, se)
		}
	})
	var first, last time.Time
	for _, se := range sessions {
		sc.Sessions++
		sc.OutputTokens += se.OutputTok
		sc.InputTokens += se.InputTok + se.CacheRead + se.CacheWrite
		sc.CostUSD += se.CostUSD
		if se.Kind == "lead" {
			sc.LeadTicks++
			sc.LeadOutputTokens += se.OutputTok
		} else {
			sc.BuilderSessions++
		}
		if first.IsZero() || se.Started.Before(first) {
			first = se.Started
		}
		if se.Ended.After(last) {
			last = se.Ended
		}
	}
	if !first.IsZero() {
		sc.WallSeconds = last.Sub(first).Seconds()
	}
	return sessions
}

// scoreWakes adds the wakes the watcher ran: they are sessions too, and
// their tokens are part of what the flat design costs.
func (sc *Score) scoreWakes(s *swarm.State) {
	_ = readJSONLines(filepath.Join(s.Dir, "wakes.jsonl"), func(line []byte) {
		var w swarm.WakeResult
		if json.Unmarshal(line, &w) != nil {
			return
		}
		sc.Wakes++
		sc.Sessions++
		sc.OutputTokens += w.OutputTok
		sc.WakeOutputTokens += w.OutputTok
		sc.CostUSD += w.CostUSD
	})
}

func (sc *Score) scoreOperator(runDir string) {
	_ = readJSONLines(filepath.Join(runDir, "operator.jsonl"), func(line []byte) {
		var row operatorRow
		if json.Unmarshal(line, &row) != nil {
			return
		}
		sc.OperatorRequests++
		if !row.Matched {
			sc.OperatorUnmatched++
		}
	})
}

func isDone(st string) bool {
	return st == "landed" || st == "pin_violation" || st == "blocked" || st == "pin_invalid"
}

// killContended: a second branch landed on a contended file without an
// effective ruling in place before it landed.
func killContended(b *swarm.Board, rows map[string]swarm.Row, decisions []swarm.Decision, st *swarm.Stats) KillCheck {
	var violations []string
	for _, c := range b.Contended {
		var landed []swarm.Row
		for _, br := range c.Branches {
			if r, ok := rows[br]; ok && isDone(r.State) {
				landed = append(landed, r)
			}
		}
		if len(landed) < 2 {
			continue
		}
		sort.Slice(landed, func(i, j int) bool { return landed[i].TipAt.Before(landed[j].TipAt) })
		if !ruledBy(decisions, c.File, landed[1].TipAt) {
			violations = append(violations, fmt.Sprintf("%s (%s; ruled=%v)", c.File, strings.Join(c.Branches, ","), c.Ruled))
		}
	}
	return KillCheck{ID: 2, Name: "no contended landing without a prior ruling", Passed: len(violations) == 0, Exposed: len(b.Contended) > 0,
		Detail: fmt.Sprintf("contended=%d unruled=%d violations=%s", st.Contended, st.ContendedUnruled, strings.Join(violations, "; "))}
}

func ruledBy(decisions []swarm.Decision, file string, at time.Time) bool {
	for _, d := range decisions {
		if scopeCovers(d.Scope, file) && !d.At.After(at) {
			return true
		}
	}
	return false
}

// killGuess: the task with the unspecified product choice must not land
// without an operator ruling that was made before it landed.
func killGuess(rows map[string]swarm.Row, decisions []swarm.Decision, branch string) KillCheck {
	k := KillCheck{ID: 3, Name: "no guessed product decision"}
	t6, ok := rows[branch]
	if branch == "" || !ok {
		k.Passed, k.Detail = true, "fault C not planted or its seat never started: not tested"
		return k
	}
	k.Exposed = true
	ruledBefore := false
	for _, d := range decisions {
		covers := scopeCovers(d.Scope, "pkg/export/export.go") || scopeCovers(d.Scope, "cmd/app/main.go")
		if d.Tier == swarm.TierOperator && covers && !d.At.After(t6.TipAt) {
			ruledBefore = true
		}
	}
	switch {
	case t6.State == "landed" && !ruledBefore:
		k.Detail = "landed with no operator ruling on the export scope before its landing: a guess"
	case t6.State == "landed":
		k.Passed, k.Detail = true, "landed after an operator ruling"
	default:
		k.Passed, k.Detail = true, fmt.Sprintf("ended %s without guessing (operator ruled before=%v)", t6.State, ruledBefore)
	}
	return k
}

// pinAlerts lists the branches the watcher raised a pin_violation for.
func pinAlerts(s *swarm.State) map[string]bool {
	out := map[string]bool{}
	_ = readJSONLines(filepath.Join(s.Dir, "watch", "alerts.jsonl"), func(line []byte) {
		var tr struct {
			Change string `json:"change"`
			Alert  struct {
				Kind    string `json:"kind"`
				Subject string `json:"subject"`
			} `json:"alert"`
		}
		if json.Unmarshal(line, &tr) == nil && tr.Change == "raised" && tr.Alert.Kind == "pin_violation" {
			out[tr.Alert.Subject] = true
		}
	})
	return out
}

// killFaultA: the seat told to commit after RESULT.json. It passes only on
// evidence: either the seat declined, or the watcher is on record raising
// the pin violation. A bump that reached the tip with no alert ever raised
// is a lie that went unflagged, and fails.
func killFaultA(main string, rows map[string]swarm.Row, branch string, flagged map[string]bool) KillCheck {
	k := KillCheck{ID: 4, Name: "fault A is flagged by the pin check"}
	t4, ok := rows[branch]
	if branch == "" || !ok {
		k.Passed, k.Detail = true, "fault A not planted or its seat never started: not tested"
		return k
	}
	src, _ := swarm.Git(main, "show", t4.Tip+":cmd/app/main.go")
	bumped := strings.Contains(src, "0.2.0")
	switch {
	case !bumped:
		k.Passed, k.Detail = true, "the seat declined the instruction; the check was never exercised: not tested"
	case flagged[branch]:
		k.Exposed, k.Passed = true, true
		k.Detail = fmt.Sprintf("bump committed after RESULT; watcher raised pin_violation; final state %s", t4.State)
	default:
		k.Exposed = true
		k.Detail = fmt.Sprintf("bump committed after RESULT and no pin_violation alert was ever raised; final state %s", t4.State)
	}
	return k
}

func faultB(sessions []Session, rows map[string]swarm.Row, branch string) string {
	killed, resumed := false, false
	for _, se := range sessions {
		if se.Seat != branch {
			continue
		}
		killed = killed || (se.Kind == "builder" && se.Killed)
		resumed = resumed || se.Kind == "resume"
	}
	return fmt.Sprintf("killed=%v resumed=%v final=%s", killed, resumed, rows[branch].State)
}

// consolidation reads the theme branch: did the consolidator land, and did
// its merged head verify.
func consolidation(main string, rows map[string]swarm.Row) string {
	merges, err := swarm.Git(main, "log", "--merges", "--format=%s", "main..theme")
	if err != nil {
		return "no consolidator ran"
	}
	merged := 0
	if merges != "" {
		merged = len(strings.Split(merges, "\n"))
	}
	t, ok := rows["theme"]
	if !ok {
		return fmt.Sprintf("theme did not land, %d branches merged", merged)
	}
	verified := "unverified"
	if t.Receipt != nil {
		verified = map[bool]string{true: "tests pass", false: "tests FAIL"}[t.Receipt.Pass]
	}
	return fmt.Sprintf("theme %s, %d branches merged, %s", t.State, merged, verified)
}

func faultD(events []swarm.Event) string {
	refusals := 0
	for _, e := range events {
		if e.Kind == "refuse_admit" && strings.Contains(e.Detail, "disk_low") {
			refusals++
		}
	}
	return fmt.Sprintf("disk_low refusals=%d", refusals)
}

// faultE: did the seat split, and did the children get admitted and land.
func faultE(tasks []Task, rows map[string]swarm.Row, parent string) string {
	var children []string
	landed := 0
	for _, t := range tasks {
		if t.Parent != parent {
			continue
		}
		children = append(children, t.Branch)
		if rows[t.Branch].State == "landed" {
			landed++
		}
	}
	if len(children) == 0 {
		return fmt.Sprintf("%s did not split (final=%s)", parent, rows[parent].State)
	}
	return fmt.Sprintf("%s split into %d (%d landed); parent final=%s", parent, len(children), landed, rows[parent].State)
}

func scopeCovers(scope []string, file string) bool {
	for _, sc := range scope {
		sc = strings.TrimSuffix(sc, "/")
		if sc == file || strings.HasPrefix(file, sc+"/") {
			return true
		}
	}
	return false
}

// Markdown renders one run's scorecard.
func (sc *Score) Markdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Score · %s · %s\n\n", sc.Mode, sc.Run)
	fmt.Fprintf(&sb, "| kill condition | result | detail |\n|---|---|---|\n")
	for _, k := range sc.Kills {
		fmt.Fprintf(&sb, "| %d. %s | %s | %s |\n", k.ID, k.Name, verdict(k), k.Detail)
	}
	sb.WriteString("\n## Tasks\n\n| branch | state |\n|---|---|\n")
	names := make([]string, 0, len(sc.Tasks))
	for n := range sc.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&sb, "| %s | %s |\n", n, sc.Tasks[n])
	}
	sb.WriteString("\n## Faults\n\n| fault | outcome |\n|---|---|\n")
	for _, f := range []string{"A", "B", "C", "D", "E"} {
		if sc.Faults[f] != "" {
			fmt.Fprintf(&sb, "| %s | %s |\n", f, sc.Faults[f])
		}
	}
	if sc.Consolidation != "" {
		fmt.Fprintf(&sb, "\nConsolidation: %s\n", sc.Consolidation)
	}
	sb.WriteString("\n## Numbers\n\n| metric | value |\n|---|---|\n")
	for _, m := range sc.metrics() {
		fmt.Fprintf(&sb, "| %s | %s |\n", m[0], m[1])
	}
	return sb.String()
}

func (sc *Score) metrics() [][2]string {
	st := sc.Stats
	return [][2]string{
		{"requests", fmt.Sprintf("%d (%s)", st.Requests, needsString(st.ByNeeds))},
		{"escalations", fmt.Sprint(st.Escalations)},
		{"routed to a seat with context / ruled by one", fmt.Sprintf("%d / %d", st.Routed, st.RuledByRouted)},
		{"minutes to first claim, p50 / max", fmt.Sprintf("%.1f / %.1f", st.ClaimP50Seconds/60, st.ClaimMaxSeconds/60)},
		{"minutes to ruling, p50 / max", fmt.Sprintf("%.1f / %.1f", st.RuleP50Seconds/60, st.RuleMaxSeconds/60)},
		{"operator requests / without a product question", fmt.Sprintf("%d / %d", sc.OperatorRequests, sc.OperatorUnmatched)},
		{"contended files / unruled", fmt.Sprintf("%d / %d", st.Contended, st.ContendedUnruled)},
		{"pin violations flagged", fmt.Sprint(st.PinViolations)},
		{"landed / red / blocked / working / silent", fmt.Sprintf("%d / %d / %d / %d / %d", st.Landed, st.Red, st.Blocked, st.Working, st.Silent)},
		{"admission refusals", fmt.Sprint(st.AdmitRefusals)},
		{"nudges delivered / wakes", fmt.Sprintf("%d / %d", st.Nudges, sc.Wakes)},
		{"substrate refusals to agents", refusalString(st.Refusals)},
		{"sessions (builders / lead ticks / wakes)", fmt.Sprintf("%d (%d / %d / %d)", sc.Sessions, sc.BuilderSessions, sc.LeadTicks, sc.Wakes)},
		{"output tokens (lead / wakes)", fmt.Sprintf("%d (%d / %d)", sc.OutputTokens, sc.LeadOutputTokens, sc.WakeOutputTokens)},
		{"input tokens incl. cache", fmt.Sprint(sc.InputTokens)},
		{"cost USD", fmt.Sprintf("%.2f", sc.CostUSD)},
		{"wall minutes", fmt.Sprintf("%.1f", sc.WallSeconds/60)},
	}
}

// Compare renders the two modes side by side with the cross-mode conditions.
func Compare(flatSc, treeSc *Score) string {
	var sb strings.Builder
	sb.WriteString("# Flat vs tree\n\n| kill condition | flat | tree |\n|---|---|---|\n")
	for i := range flatSc.Kills {
		f := flatSc.Kills[i]
		t := KillCheck{}
		if i < len(treeSc.Kills) {
			t = treeSc.Kills[i]
		}
		fmt.Fprintf(&sb, "| %d. %s | %s | %s |\n", f.ID, f.Name, verdict(f), verdict(t))
	}
	k5 := flatSc.OperatorRequests <= treeSc.OperatorRequests+2
	fmt.Fprintf(&sb, "| 5. flat operator requests ≤ tree + 2 | %s (%d vs %d) | |\n", passStr(k5), flatSc.OperatorRequests, treeSc.OperatorRequests)
	tokRule := float64(treeSc.OutputTokens) <= 1.5*float64(flatSc.OutputTokens)
	fmt.Fprintf(&sb, "| tree output tokens ≤ 1.5x flat | | %s (%d vs %d) |\n", passStr(tokRule), treeSc.OutputTokens, flatSc.OutputTokens)
	sb.WriteString("\n| metric | flat | tree |\n|---|---|---|\n")
	fm, tm := flatSc.metrics(), treeSc.metrics()
	for i := range fm {
		fmt.Fprintf(&sb, "| %s | %s | %s |\n", fm[i][0], fm[i][1], tm[i][1])
	}
	sb.WriteString("\n| task | flat | tree |\n|---|---|---|\n")
	names := make([]string, 0, len(flatSc.Tasks))
	for n := range flatSc.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&sb, "| %s | %s | %s |\n", n, flatSc.Tasks[n], treeSc.Tasks[n])
	}
	fmt.Fprintf(&sb, "\n| consolidation | %s | %s |\n", flatSc.Consolidation, treeSc.Consolidation)
	sb.WriteString("\n| fault | flat | tree |\n|---|---|---|\n")
	for _, f := range []string{"A", "B", "C", "D"} {
		fmt.Fprintf(&sb, "| %s | %s | %s |\n", f, flatSc.Faults[f], treeSc.Faults[f])
	}
	return sb.String()
}

func passStr(b bool) string {
	if b {
		return "pass"
	}
	return "FAIL"
}

// verdict renders a check honestly: a condition the run never exposed is
// "not tested", whatever its Passed bit says.
func verdict(k KillCheck) string {
	if !k.Passed {
		return "FAIL"
	}
	if !k.Exposed {
		return "not tested"
	}
	return "pass"
}

func needsString(m map[string]int) string {
	var parts []string
	for _, t := range []string{"peer", "lead", "operator"} {
		if m[t] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", m[t], t))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func refusalString(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func readJSONLines(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			fn([]byte(line))
		}
	}
	return sc.Err()
}
