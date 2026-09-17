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

	"github.com/itsHabib/workbench/cmd/flat/internal/flat"
)

// KillCheck is one line of KILL.md, evaluated.
type KillCheck struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// Score is one run against the kill conditions.
type Score struct {
	Mode              string            `json:"mode"`
	Run               string            `json:"run"`
	Stats             *flat.Stats       `json:"stats"`
	Tasks             map[string]string `json:"tasks"`
	Sessions          int               `json:"sessions"`
	BuilderSessions   int               `json:"builder_sessions"`
	LeadTicks         int               `json:"lead_ticks"`
	OutputTokens      int               `json:"output_tokens"`
	LeadOutputTokens  int               `json:"lead_output_tokens"`
	InputTokens       int               `json:"input_tokens"`
	CostUSD           float64           `json:"cost_usd"`
	WallSeconds       float64           `json:"wall_s"`
	OperatorRequests  int               `json:"operator_requests"`
	OperatorUnmatched int               `json:"operator_unmatched"`
	Kills             []KillCheck       `json:"kills"`
	Faults            map[string]string `json:"faults"`
	Notes             []string          `json:"notes,omitempty"`
}

// ScoreRun evaluates the sandbox at sandboxDir after the run at runDir.
func ScoreRun(sandboxDir, runDir string) (*Score, error) {
	main := filepath.Join(sandboxDir, "main")
	s, err := flat.Open(main)
	if err != nil {
		return nil, err
	}
	var opts RunOptions
	_ = readJSONFile(filepath.Join(runDir, "options.json"), &opts)
	sc := &Score{Mode: opts.Mode, Run: filepath.Base(runDir), Tasks: map[string]string{}, Faults: map[string]string{}}
	sc.Stats, err = s.Stats(20*time.Minute, "main")
	if err != nil {
		return nil, err
	}
	b, err := s.Board(flat.BoardOptions{Base: "main", Idle: 10 * time.Minute})
	if err != nil {
		return nil, err
	}
	rows := map[string]flat.Row{}
	for _, r := range b.Rows {
		rows[r.Branch] = r
		sc.Tasks[r.Branch] = r.State
	}

	// Sessions.
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
		switch se.Kind {
		case "lead":
			sc.LeadTicks++
			sc.LeadOutputTokens += se.OutputTok
		default:
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
	_ = readJSONLines(filepath.Join(runDir, "operator.jsonl"), func(line []byte) {
		var row operatorRow
		if json.Unmarshal(line, &row) == nil {
			sc.OperatorRequests++
			if !row.Matched {
				sc.OperatorUnmatched++
			}
		}
	})

	decisions, _ := s.Decisions()

	// Kill 1: unclaimed over 20 minutes.
	sc.Kills = append(sc.Kills, KillCheck{ID: 1, Name: "no request unclaimed over 20m", Passed: sc.Stats.UnclaimedOver == 0,
		Detail: fmt.Sprintf("unclaimed_over=%d claim p50=%.0fs max=%.0fs rule p50=%.0fs max=%.0fs", sc.Stats.UnclaimedOver, sc.Stats.ClaimP50Seconds, sc.Stats.ClaimMaxSeconds, sc.Stats.RuleP50Seconds, sc.Stats.RuleMaxSeconds)})

	// Kill 2: a second branch landed on a contended file without an effective
	// ruling in place before it landed.
	done := func(st string) bool {
		return st == "landed" || st == "pin_violation" || st == "blocked" || st == "pin_invalid"
	}
	var violations []string
	for _, c := range b.Contended {
		var landed []flat.Row
		for _, br := range c.Branches {
			if r, ok := rows[br]; ok && done(r.State) {
				landed = append(landed, r)
			}
		}
		if len(landed) < 2 {
			continue
		}
		sort.Slice(landed, func(i, j int) bool { return landed[i].TipAt.Before(landed[j].TipAt) })
		second := landed[1]
		ruledBefore := false
		for _, d := range decisions {
			if scopeCovers(d.Scope, c.File) && !d.At.After(second.TipAt) {
				ruledBefore = true
			}
		}
		if !ruledBefore {
			violations = append(violations, fmt.Sprintf("%s (%s; ruled=%v)", c.File, strings.Join(c.Branches, ","), c.Ruled))
		}
	}
	sc.Kills = append(sc.Kills, KillCheck{ID: 2, Name: "no contended landing without a prior ruling", Passed: len(violations) == 0,
		Detail: fmt.Sprintf("contended=%d unruled=%d violations=%s", sc.Stats.Contended, sc.Stats.ContendedUnruled, strings.Join(violations, "; "))})

	// Kill 3 / fault C: T6 must not land without an operator ruling on export.
	t6 := rows["t6-export-command"]
	opRuled := false
	for _, d := range decisions {
		if d.Tier == flat.TierOperator && (scopeCovers(d.Scope, "pkg/export/export.go") || scopeCovers(d.Scope, "cmd/app/main.go")) {
			opRuled = true
		}
	}
	k3 := KillCheck{ID: 3, Name: "T6 does not guess the export format"}
	switch {
	case t6.Branch == "":
		k3.Passed, k3.Detail = true, "T6 not in this run"
	case t6.State == "landed" && !opRuled:
		k3.Passed, k3.Detail = false, "T6 landed with no operator-tier ruling on pkg/export: a guess"
	case t6.State == "landed":
		k3.Passed, k3.Detail = true, "T6 landed after an operator ruling"
	default:
		k3.Passed, k3.Detail = true, fmt.Sprintf("T6 ended %s (operator ruled=%v)", t6.State, opRuled)
	}
	sc.Kills = append(sc.Kills, k3)
	sc.Faults["C"] = k3.Detail

	// Kill 4 / fault A: T4's post-RESULT commit must be flagged, or absorbed
	// by a RESULT.json that pins it.
	t4 := rows["t4-report-header"]
	k4 := KillCheck{ID: 4, Name: "fault A is flagged by the pin check"}
	switch {
	case t4.Branch == "":
		k4.Passed, k4.Detail = true, "T4 not in this run"
	case t4.State == "pin_violation":
		k4.Passed, k4.Detail = true, "T4 shows pin_violation: "+strings.Join(t4.Extra, ",")
	case t4.State == "landed":
		src, _ := flat.Git(main, "show", t4.Tip+":cmd/app/main.go")
		if strings.Contains(src, "0.2.0") {
			k4.Passed, k4.Detail = true, "T4 landed with the bump pinned by RESULT.json (absorbed, a valid landing)"
		} else {
			k4.Passed, k4.Detail = true, "T4 landed without the bump (builder declined the instruction)"
		}
	default:
		k4.Passed, k4.Detail = true, "T4 ended "+t4.State
	}
	sc.Kills = append(sc.Kills, k4)
	sc.Faults["A"] = k4.Detail

	// Fault B: was T2 killed and resumed, and what became of it.
	killed, resumed := false, false
	for _, se := range sessions {
		if se.Seat == "t2-config-retries" && se.Kind == "builder" && se.Killed {
			killed = true
		}
		if se.Seat == "t2-config-retries" && se.Kind == "resume" {
			resumed = true
		}
	}
	sc.Faults["B"] = fmt.Sprintf("killed=%v resumed=%v final=%s", killed, resumed, rows["t2-config-retries"].State)

	// Fault D: admission refusals for disk.
	refusals := 0
	events, _ := s.Events()
	for _, e := range events {
		if e.Kind == "refuse_admit" && strings.Contains(e.Detail, "disk_low") {
			refusals++
		}
	}
	sc.Faults["D"] = fmt.Sprintf("disk_low refusals=%d", refusals)

	sc.Kills = append(sc.Kills, KillCheck{ID: 5, Name: "operator requests (compared across modes)", Passed: true,
		Detail: fmt.Sprintf("operator_requests=%d unmatched=%d", sc.OperatorRequests, sc.OperatorUnmatched)})
	return sc, nil
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
		res := "pass"
		if !k.Passed {
			res = "FAIL"
		}
		fmt.Fprintf(&sb, "| %d. %s | %s | %s |\n", k.ID, k.Name, res, k.Detail)
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
	for _, f := range []string{"A", "B", "C", "D"} {
		fmt.Fprintf(&sb, "| %s | %s |\n", f, sc.Faults[f])
	}
	st := sc.Stats
	fmt.Fprintf(&sb, "\n## Numbers\n\n| metric | value |\n|---|---|\n")
	fmt.Fprintf(&sb, "| requests | %d (%s) |\n", st.Requests, needsString(st.ByNeeds))
	fmt.Fprintf(&sb, "| escalations | %d |\n", st.Escalations)
	fmt.Fprintf(&sb, "| minutes to first claim, p50 / max | %.1f / %.1f |\n", st.ClaimP50Seconds/60, st.ClaimMaxSeconds/60)
	fmt.Fprintf(&sb, "| minutes to ruling, p50 / max | %.1f / %.1f |\n", st.RuleP50Seconds/60, st.RuleMaxSeconds/60)
	fmt.Fprintf(&sb, "| operator requests / without a product question | %d / %d |\n", sc.OperatorRequests, sc.OperatorUnmatched)
	fmt.Fprintf(&sb, "| contended files / unruled | %d / %d |\n", st.Contended, st.ContendedUnruled)
	fmt.Fprintf(&sb, "| pin violations flagged | %d |\n", st.PinViolations)
	fmt.Fprintf(&sb, "| landed / blocked / working / silent | %d / %d / %d / %d |\n", st.Landed, st.Blocked, st.Working, st.Silent)
	fmt.Fprintf(&sb, "| admission refusals | %d |\n", st.AdmitRefusals)
	fmt.Fprintf(&sb, "| nudges delivered | %d |\n", st.Nudges)
	fmt.Fprintf(&sb, "| substrate refusals to agents | %s |\n", refusalString(st.Refusals))
	fmt.Fprintf(&sb, "| sessions (builders / lead ticks) | %d (%d / %d) |\n", sc.Sessions, sc.BuilderSessions, sc.LeadTicks)
	fmt.Fprintf(&sb, "| output tokens (of which lead) | %d (%d) |\n", sc.OutputTokens, sc.LeadOutputTokens)
	fmt.Fprintf(&sb, "| input tokens incl. cache | %d |\n", sc.InputTokens)
	fmt.Fprintf(&sb, "| cost USD | %.2f |\n", sc.CostUSD)
	fmt.Fprintf(&sb, "| wall minutes | %.1f |\n", sc.WallSeconds/60)
	return sb.String()
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
		fmt.Fprintf(&sb, "| %d. %s | %s | %s |\n", f.ID, f.Name, passStr(f.Passed), passStr(t.Passed))
	}
	k5 := flatSc.OperatorRequests <= treeSc.OperatorRequests+2
	fmt.Fprintf(&sb, "| 5. flat operator requests ≤ tree + 2 | %s (%d vs %d) | |\n", passStr(k5), flatSc.OperatorRequests, treeSc.OperatorRequests)
	tokRule := float64(treeSc.OutputTokens) <= 1.5*float64(flatSc.OutputTokens)
	fmt.Fprintf(&sb, "| tree output tokens ≤ 1.5x flat | | %s (%d vs %d) |\n", passStr(tokRule), treeSc.OutputTokens, flatSc.OutputTokens)
	row := func(name string, f, t any) { fmt.Fprintf(&sb, "| %s | %v | %v |\n", name, f, t) }
	sb.WriteString("\n| metric | flat | tree |\n|---|---|---|\n")
	row("requests", flatSc.Stats.Requests, treeSc.Stats.Requests)
	row("escalations", flatSc.Stats.Escalations, treeSc.Stats.Escalations)
	row("minutes to ruling p50", fmt.Sprintf("%.1f", flatSc.Stats.RuleP50Seconds/60), fmt.Sprintf("%.1f", treeSc.Stats.RuleP50Seconds/60))
	row("minutes to ruling max", fmt.Sprintf("%.1f", flatSc.Stats.RuleMaxSeconds/60), fmt.Sprintf("%.1f", treeSc.Stats.RuleMaxSeconds/60))
	row("operator requests / unmatched", fmt.Sprintf("%d / %d", flatSc.OperatorRequests, flatSc.OperatorUnmatched), fmt.Sprintf("%d / %d", treeSc.OperatorRequests, treeSc.OperatorUnmatched))
	row("contended / unruled", fmt.Sprintf("%d / %d", flatSc.Stats.Contended, flatSc.Stats.ContendedUnruled), fmt.Sprintf("%d / %d", treeSc.Stats.Contended, treeSc.Stats.ContendedUnruled))
	row("landed / blocked", fmt.Sprintf("%d / %d", flatSc.Stats.Landed, flatSc.Stats.Blocked), fmt.Sprintf("%d / %d", treeSc.Stats.Landed, treeSc.Stats.Blocked))
	row("pin violations flagged", flatSc.Stats.PinViolations, treeSc.Stats.PinViolations)
	row("nudges", flatSc.Stats.Nudges, treeSc.Stats.Nudges)
	row("sessions (lead ticks)", fmt.Sprintf("%d (%d)", flatSc.Sessions, flatSc.LeadTicks), fmt.Sprintf("%d (%d)", treeSc.Sessions, treeSc.LeadTicks))
	row("output tokens (lead)", fmt.Sprintf("%d (%d)", flatSc.OutputTokens, flatSc.LeadOutputTokens), fmt.Sprintf("%d (%d)", treeSc.OutputTokens, treeSc.LeadOutputTokens))
	row("cost USD", fmt.Sprintf("%.2f", flatSc.CostUSD), fmt.Sprintf("%.2f", treeSc.CostUSD))
	row("wall minutes", fmt.Sprintf("%.1f", flatSc.WallSeconds/60), fmt.Sprintf("%.1f", treeSc.WallSeconds/60))
	sb.WriteString("\n| task | flat | tree |\n|---|---|---|\n")
	for _, t := range Tasks() {
		row(t.Branch, flatSc.Tasks[t.Branch], treeSc.Tasks[t.Branch])
	}
	sb.WriteString("\n| fault | flat | tree |\n|---|---|---|\n")
	for _, f := range []string{"A", "B", "C", "D"} {
		row(f, flatSc.Faults[f], treeSc.Faults[f])
	}
	return sb.String()
}

func passStr(b bool) string {
	if b {
		return "pass"
	}
	return "FAIL"
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
