package gym

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Row is one attempt: one model, one task, graded.
type Row struct {
	At        time.Time `json:"at"`
	Task      string    `json:"task"`
	Seat      string    `json:"seat"`
	Level     string    `json:"level"`
	Model     string    `json:"model"`
	Rep       int       `json:"rep"`
	Pass      bool      `json:"pass"`
	Detail    string    `json:"detail"`
	Turns     int       `json:"turns"`
	OutputTok int       `json:"output_tokens"`
	CostUSD   float64   `json:"cost_usd"`
	CostKnown bool      `json:"cost_known"` // false when the session was killed before it reported
	WallS     float64   `json:"wall_s"`
	Killed    bool      `json:"killed,omitempty"`
}

const rules = `
Standing rules: work only in this directory. One shell command per tool call; no && and no ;.
Do not look for or modify files named zz_hidden_test.go. When you are done, stop.`

const usage = `swarm gym: which model is good enough for which seat

  swarm gym list
  swarm gym run --models claude-haiku-4-5-20251001,claude-sonnet-5 [--tasks id,id] [--seat author]
                [--reps 1] [--parallel 3] --out DIR
  swarm gym table --out DIR
  swarm gym capacity [--threads 2,4,8,16] [--notes both] [--reps 1] --out DIR
                how many interleaved threads one agent keeps straight, in its head and with a notes file
`

// Main is the gym subcommand.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 3
	}
	switch args[0] {
	case "list":
		for _, t := range Tasks() {
			fmt.Printf("%-20s %-13s %-5s %s\n", t.ID, t.Seat, t.Level, t.Grader)
		}
		return 0
	case "run":
		return runCmd(args[1:])
	case "capacity":
		return capacityCmd(args[1:])
	case "mail":
		return mailMain(args[1:])
	case "table":
		fs := flag.NewFlagSet("gym table", flag.ContinueOnError)
		out := fs.String("out", "", "results directory")
		if fs.Parse(args[1:]) != nil || *out == "" {
			return 3
		}
		rows, err := readRows(filepath.Join(*out, "rows.jsonl"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		fmt.Print(Table(rows))
		return 0
	}
	fmt.Fprint(os.Stderr, usage)
	return 3
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("gym run", flag.ContinueOnError)
	models := fs.String("models", "", "comma-separated model ids")
	only := fs.String("tasks", "", "comma-separated task ids (default all)")
	seat := fs.String("seat", "", "only tasks for this seat")
	reps := fs.Int("reps", 1, "attempts per model per task")
	parallel := fs.Int("parallel", 3, "attempts at once")
	out := fs.String("out", "", "results directory")
	if fs.Parse(args) != nil || *models == "" || *out == "" {
		fmt.Fprint(os.Stderr, usage)
		return 3
	}
	if err := os.MkdirAll(filepath.Join(*out, "logs"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	want := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id != "" {
			want[id] = true
		}
	}
	type job struct {
		t     Task
		model string
		rep   int
	}
	var jobs []job
	for _, t := range Tasks() {
		if (len(want) > 0 && !want[t.ID]) || (*seat != "" && t.Seat != *seat) {
			continue
		}
		for _, m := range strings.Split(*models, ",") {
			for r := 1; r <= *reps; r++ {
				jobs = append(jobs, job{t, strings.TrimSpace(m), r})
			}
		}
	}
	self, _ := os.Executable()
	env := childEnv(filepath.Dir(self), filepath.Join(*out, "fleet-state"))
	var mu sync.Mutex
	sem := make(chan struct{}, *parallel)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			row := attempt(j.t, j.model, j.rep, *out, env)
			mu.Lock()
			defer mu.Unlock()
			appendRow(filepath.Join(*out, "rows.jsonl"), row)
			verdict := "FAIL"
			if row.Pass {
				verdict = "pass"
			}
			fmt.Printf("%s %-20s %-28s rep%d turns=%-3d $%.2f %4.0fs  %s\n", verdict, j.t.ID, j.model, j.rep, row.Turns, row.CostUSD, row.WallS, tailLine(row.Detail))
		}(j)
	}
	wg.Wait()
	rows, _ := readRows(filepath.Join(*out, "rows.jsonl"))
	fmt.Print("\n" + Table(rows))
	return 0
}

// attempt builds a fresh fixture, runs one session as the seat, and grades.
func attempt(t Task, model string, rep int, out string, env []string) Row {
	row := Row{At: time.Now().UTC(), Task: t.ID, Seat: t.Seat, Level: t.Level, Model: model, Rep: rep}
	root, err := os.MkdirTemp("", "gym-"+t.ID+"-")
	if err != nil {
		row.Detail = err.Error()
		return row
	}
	defer os.RemoveAll(root)
	fx, err := build(t, root)
	if err != nil {
		row.Detail = "fixture: " + err.Error()
		return row
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(t.WallS)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", t.Goal+rules, "--output-format", "json", "--permission-mode", "acceptEdits",
		"--max-turns", fmt.Sprint(t.Turns), "--model", model,
		"--allowedTools", "Read,Edit,Write,MultiEdit,Glob,Grep,Bash(git:*),Bash(go:*),Bash(swarm:*),Bash(cat:*),Bash(ls:*),Bash(gofmt:*)")
	cmd.Dir = fx.cwd
	cmd.Env = append(append([]string{}, env...), "SWARM_SEAT="+fx.seat)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	row.WallS = time.Since(start).Seconds()
	row.Killed = ctx.Err() != nil
	var res struct {
		NumTurns int     `json:"num_turns"`
		Cost     float64 `json:"total_cost_usd"`
		Usage    struct {
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(stdout.Bytes(), &res) == nil {
		row.Turns, row.CostUSD, row.OutputTok, row.CostKnown = res.NumTurns, res.Cost, res.Usage.Output, true
	}
	name := fmt.Sprintf("%s-%s-rep%d", t.ID, strings.ReplaceAll(model, "/", "_"), rep)
	_ = os.WriteFile(filepath.Join(out, "logs", name+".out.json"), stdout.Bytes(), 0o644)
	if runErr != nil && row.Turns == 0 {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			row.Detail = "session did not run: " + runErr.Error() + " " + tail(stderr.String(), 200)
			return row
		}
	}
	// Graded whatever the session said about itself.
	row.Pass, row.Detail = fx.grade()
	return row
}

func childEnv(binDir, fleetState string) []string {
	_ = os.MkdirAll(fleetState, 0o755)
	env := []string{"FLEET_STATE=" + fleetState, "FLEET_WATCH=off"}
	for _, kv := range os.Environ() {
		key, val, _ := strings.Cut(kv, "=")
		switch {
		case key == "CLAUDECODE", key == "SWARM_SEAT", key == "SWARM_STATE", key == "SWARM_STORE", key == "FLEET_STATE", key == "FLEET_WATCH":
		case strings.EqualFold(key, "PATH"):
			env = append(env, key+"="+binDir+string(os.PathListSeparator)+val)
		default:
			env = append(env, kv)
		}
	}
	return env
}

func appendRow(path string, r Row) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(r)
	_, _ = f.Write(append(data, '\n'))
}

func readRows(path string) ([]Row, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r Row
		if json.Unmarshal([]byte(line), &r) == nil && r.Task != "" {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

func tailLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > 110 {
		return "…" + s[len(s)-110:]
	}
	return s
}

// Table renders pass counts and median cost, turns and wall per task and
// model. With one attempt per cell it is a smoke test, not a finding, and
// it says so.
func Table(rows []Row) string {
	type cell struct {
		pass, n            int
		cost, turns, walls []float64
	}
	cells := map[string]*cell{}
	models := map[string]bool{}
	var tasks []string
	seen := map[string]bool{}
	minN := 1 << 30
	for _, r := range rows {
		k := r.Task + "|" + r.Model
		c := cells[k]
		if c == nil {
			c = &cell{}
			cells[k] = c
		}
		c.n++
		if r.Pass {
			c.pass++
		}
		if r.CostKnown {
			c.cost = append(c.cost, r.CostUSD)
		}
		c.turns = append(c.turns, float64(r.Turns))
		c.walls = append(c.walls, r.WallS)
		models[r.Model] = true
		if !seen[r.Task] {
			seen[r.Task] = true
			tasks = append(tasks, r.Task+"|"+r.Seat+"|"+r.Level)
		}
	}
	var ms []string
	for m := range models {
		ms = append(ms, m)
	}
	sort.Strings(ms)
	sort.Strings(tasks)
	var sb strings.Builder
	sb.WriteString("| task | seat | level |")
	for _, m := range ms {
		sb.WriteString(" " + m + " |")
	}
	sb.WriteString("\n|---|---|---|" + strings.Repeat("---|", len(ms)) + "\n")
	for _, t := range tasks {
		parts := strings.Split(t, "|")
		fmt.Fprintf(&sb, "| %s | %s | %s |", parts[0], parts[1], parts[2])
		for _, m := range ms {
			c := cells[parts[0]+"|"+m]
			if c == nil {
				sb.WriteString(" - |")
				continue
			}
			if c.n < minN {
				minN = c.n
			}
			fmt.Fprintf(&sb, " %d/%d · $%.2f · %.0f turns · %.0fs |", c.pass, c.n, median(c.cost), median(c.turns), median(c.walls))
		}
		sb.WriteString("\n")
	}
	if minN < 3 {
		fmt.Fprintf(&sb, "\nFewest attempts in a cell: %d. Below three this is a smoke test of the gym, not a finding about a model.\n", minN)
	}
	return sb.String()
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	return xs[len(xs)/2]
}
