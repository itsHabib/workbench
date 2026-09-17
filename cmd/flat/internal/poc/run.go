package poc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/itsHabib/workbench/cmd/flat/internal/flat"
)

// RunOptions configures one run of the workload.
type RunOptions struct {
	Dir           string // sandbox directory from Init
	Mode          string // flat | tree
	Model         string
	Seats         int
	Only          []string // subset of branches, for a smoke run
	BuilderWall   time.Duration
	MaxTurns      int
	LeadEvery     time.Duration
	OperatorEvery time.Duration
	WatchEvery    time.Duration
	KillT2After   time.Duration // fault B
	DiskFaultFor  time.Duration // fault D
	DiskMin       uint64
	Out           string // run directory for evidence
	FlatBin       string // path to the flat binary the builders call
	Consolidate   bool   // after the builders, one consolidator merges landed branches in ledger order
}

// Session is one provider session's accounting, read from the CLI's JSON.
type Session struct {
	Seat       string    `json:"seat"`
	Kind       string    `json:"kind"` // builder | resume | lead
	Mode       string    `json:"mode"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended"`
	DurationS  float64   `json:"duration_s"`
	Exit       int       `json:"exit"`
	Killed     bool      `json:"killed,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	NumTurns   int       `json:"num_turns"`
	CostUSD    float64   `json:"cost_usd"`
	InputTok   int       `json:"input_tokens"`
	OutputTok  int       `json:"output_tokens"`
	CacheRead  int       `json:"cache_read_tokens"`
	CacheWrite int       `json:"cache_write_tokens"`
	IsError    bool      `json:"is_error"`
	ResultTail string    `json:"result_tail,omitempty"`
}

type cliResult struct {
	SessionID  string  `json:"session_id"`
	NumTurns   int     `json:"num_turns"`
	IsError    bool    `json:"is_error"`
	Result     string  `json:"result"`
	TotalCost  float64 `json:"total_cost_usd"`
	DurationMS int     `json:"duration_ms"`
	Usage      struct {
		Input      int `json:"input_tokens"`
		Output     int `json:"output_tokens"`
		CacheRead  int `json:"cache_read_input_tokens"`
		CacheWrite int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type runner struct {
	o       RunOptions
	main    string
	state   *flat.State
	baseSHA string
	env     []string
	mu      sync.Mutex
	running int
	alive   int
	logf    func(format string, args ...any)
}

// prepare validates the options against the sandbox and builds the runner.
func prepare(o RunOptions) (*runner, error) {
	if o.Mode != "flat" && o.Mode != "tree" {
		return nil, errors.New("--mode must be flat or tree")
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	main := filepath.Join(dir, "main")
	s, err := flat.Open(main)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(main, "RULES.md")); err != nil {
		return nil, fmt.Errorf("%s is not a sandbox from `flat poc init`", main)
	}
	heads, _ := flat.Git(main, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if strings.TrimSpace(heads) != "main" {
		return nil, fmt.Errorf("sandbox already has branches (%s); init a fresh directory per run", strings.ReplaceAll(heads, "\n", ","))
	}
	if o.Out == "" {
		o.Out = filepath.Join(dir, "runs", time.Now().UTC().Format("20060102-150405")+"-"+o.Mode)
	}
	if err := os.MkdirAll(filepath.Join(o.Out, "logs"), 0o755); err != nil {
		return nil, err
	}
	if o.FlatBin == "" {
		if o.FlatBin, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	baseSHA, err := flat.Git(main, "rev-parse", "main")
	if err != nil {
		return nil, err
	}
	return &runner{o: o, main: main, state: s, baseSHA: baseSHA}, nil
}

// Run executes the workload in one mode and leaves evidence under Out.
func Run(o RunOptions) error {
	r, err := prepare(o)
	if err != nil {
		return err
	}
	r.env = childEnv(filepath.Dir(r.o.FlatBin), filepath.Join(filepath.Dir(r.main), "fleet-state"))
	// Wakes spawn from this process, so its own environment must carry the
	// same isolation the builders get.
	for _, kv := range r.env {
		if k, v, ok := strings.Cut(kv, "="); ok && (k == "FLEET_STATE" || k == "FLEET_WATCH" || strings.EqualFold(k, "PATH")) {
			os.Setenv(k, v)
		}
	}
	os.Unsetenv("CLAUDECODE")
	logFile, err := os.OpenFile(filepath.Join(r.o.Out, "run.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	r.logf = func(format string, args ...any) {
		line := fmt.Sprintf("%s "+format+"\n", append([]any{time.Now().UTC().Format("15:04:05")}, args...)...)
		fmt.Print(line)
		_, _ = logFile.WriteString(line)
	}
	_ = writeJSONFile(filepath.Join(r.o.Out, "options.json"), r.o)
	r.logf("run %s mode=%s model=%s seats=%d out=%s", filepath.Base(r.o.Out), r.o.Mode, r.o.Model, r.o.Seats, r.o.Out)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var bg sync.WaitGroup
	bg.Add(2)
	go func() { defer bg.Done(); r.watch(ctx) }()
	go func() { defer bg.Done(); r.operator(ctx) }()
	if r.o.Mode == "tree" {
		bg.Add(1)
		go func() { defer bg.Done(); r.lead(ctx) }()
	}

	tasks, err := LoadTasks(r.main)
	if err != nil {
		return err
	}
	r.runBuilders(selectTasks(tasks, r.o.Only))
	if r.o.Consolidate {
		r.consolidate()
	}
	r.logf("all builders finished; final pass")
	cancel()
	bg.Wait()
	if left := flat.WaitWakes(7 * time.Minute); len(left) > 0 {
		r.logf("wakes still running past their wall: %s", strings.Join(left, ", "))
	}
	if _, _, err := r.state.WatchOnce(r.watchOptions()); err != nil {
		r.logf("final watch: %v", err)
	}
	return r.collect()
}

// selectTasks narrows the workload to only, when given.
func selectTasks(tasks []Task, only []string) []Task {
	if len(only) == 0 {
		return tasks
	}
	var sub []Task
	for _, t := range tasks {
		for _, want := range only {
			if t.Branch == want {
				sub = append(sub, t)
			}
		}
	}
	return sub
}

// runBuilders admits and runs every task, in order, and waits for all.
func (r *runner) runBuilders(tasks []Task) {
	var builders sync.WaitGroup
	for i, t := range tasks {
		r.admit(i, t)
		builders.Add(1)
		r.mu.Lock()
		r.running++
		r.alive++
		r.mu.Unlock()
		go func(t Task) {
			defer builders.Done()
			r.build(t)
			r.mu.Lock()
			r.running--
			r.alive--
			r.mu.Unlock()
		}(t)
	}
	builders.Wait()
}

func (r *runner) watchOptions() flat.WatchOptions {
	return flat.WatchOptions{Base: "main", Idle: 10 * time.Minute, UnclaimedAfter: 5 * time.Minute, Renudge: 5 * time.Minute, DiskMin: r.o.DiskMin,
		Wake: true, WakeOpts: flat.WakeOptions{Model: r.o.Model, Max: 2, Wall: 6 * time.Minute}, Verify: "go test ./..."}
}

func (r *runner) watch(ctx context.Context) {
	t := time.NewTicker(r.o.WatchEvery)
	defer t.Stop()
	for {
		if _, alerts, err := r.state.WatchOnce(r.watchOptions()); err != nil {
			r.logf("watch: %v", err)
		} else if len(alerts) > 0 {
			var keys []string
			for _, a := range alerts {
				keys = append(keys, a.Key)
			}
			r.logf("watch alerts: %s", strings.Join(keys, " "))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// intent is what only the operator knows. Requests that match get the
// product answer; anything else that reached the operator was noise the
// fleet should have settled itself, and is counted as such.
var intent = []struct{ scope, ruling string }{
	{"pkg/export", "Export format is JSON Lines: one JSON object per row, one row per line, on stdout. Keys are the fixture field names."},
	{"cmd/app", "Export format is JSON Lines: one JSON object per row, one row per line, on stdout. Keys are the fixture field names."},
}

const noIntent = "No product preference here. This is an engineering question; rule it on evidence: the branch whose start commit is earlier lands first, the later one rebases after it lands."

type operatorRow struct {
	At       time.Time `json:"at"`
	Request  string    `json:"request"`
	From     string    `json:"from"`
	Question string    `json:"question"`
	Matched  bool      `json:"matched"`
	Ruling   string    `json:"ruling"`
	Err      string    `json:"err,omitempty"`
}

func (r *runner) operator(ctx context.Context) {
	t := time.NewTicker(r.o.OperatorEvery)
	defer t.Stop()
	for {
		reqs, _ := r.state.Requests(true)
		for _, q := range reqs {
			if q.Needs != flat.TierOperator || q.Status == "ruled" {
				continue
			}
			row := operatorRow{At: time.Now().UTC(), Request: q.ID, From: q.From, Question: q.Question}
			row.Ruling = noIntent
			for _, in := range intent {
				for _, sc := range q.Scope {
					if strings.HasPrefix(sc, in.scope) {
						row.Matched, row.Ruling = true, in.ruling
					}
				}
			}
			os.Setenv("FLAT_SEAT", "operator")
			if _, err := r.state.Rule(q.ID, "operator", 0, row.Ruling, "operator intent", "", nil); err != nil {
				row.Err = err.Error()
			}
			r.logf("operator ruled %s from %s matched=%v", q.ID, q.From, row.Matched)
			_ = appendJSONL(filepath.Join(r.o.Out, "operator.jsonl"), row)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

const leadPrompt = `You are the lead for this fleet. You never edit code. Each tick you:
1. Run: flat board --md   and   flat requests   and   flat digest
2. For every open request that needs lead: rule it from evidence (the board, git log of the
   branches involved, briefs/BRIEF.md, the code) with
   flat rule <id> --ruling "..." --evidence "..."
   When the ruling is about landing order, record it with --order "<first>,<second>".
   If the question is a product decision the brief does not settle, escalate:
   flat escalate <id> --to operator --why "..."
3. For every silent builder or unruled overlap on the board, nudge the seat:
   flat nudge <seat> "<what to do>"
4. Write docs/experiments/BOARD.md: a delta table (branch, state, age, blocker) with the
   wall-clock time from the system, plus a "decided" section listing every ruling id.
   Read the previous BOARD.md first; never re-open a ruling already listed there.
Then stop. One shell command per tool call; no && and no ;.`

func (r *runner) lead(ctx context.Context) {
	n := 0
	for {
		r.mu.Lock()
		alive := r.alive
		r.mu.Unlock()
		if alive > 0 || n == 0 {
			n++
			sess := r.claude(ctx, "lead", "lead", r.main, leadPrompt, 25, 6*time.Minute)
			r.logf("lead tick %d: turns=%d out=%d exit=%d", n, sess.NumTurns, sess.OutputTok, sess.Exit)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.o.LeadEvery):
		}
	}
}

// admit waits for a seat and the disk floor before builder i starts. Fault
// D: the fifth builder meets a reported disk of one byte for DiskFaultFor.
func (r *runner) admit(i int, t Task) {
	faultUntil := time.Time{}
	if i == 4 && r.o.DiskFaultFor > 0 {
		faultUntil = time.Now().Add(r.o.DiskFaultFor)
		os.Setenv("FLAT_DISK_FREE_BYTES", "1")
		r.logf("fault D: disk reported as 1 byte free for %s", r.o.DiskFaultFor)
		_ = appendJSONL(filepath.Join(r.o.Out, "faults.jsonl"), map[string]any{"at": time.Now().UTC(), "fault": "D", "seat": t.Branch, "for_s": r.o.DiskFaultFor.Seconds()})
	}
	for {
		if !faultUntil.IsZero() && time.Now().After(faultUntil) {
			os.Unsetenv("FLAT_DISK_FREE_BYTES")
			faultUntil = time.Time{}
			r.logf("fault D: disk floor met again")
		}
		r.mu.Lock()
		running := r.running
		r.mu.Unlock()
		a, err := r.state.Admit(flat.AdmitOptions{Seats: r.o.Seats, DiskMin: r.o.DiskMin, Base: "main"})
		if err != nil {
			r.logf("admit %s: %v", t.Branch, err)
			time.Sleep(5 * time.Second)
			continue
		}
		if a.Admitted && running < r.o.Seats {
			r.logf("admitted %s (%d working)", t.Branch, a.Active)
			return
		}
		reason := strings.Join(a.Reasons, "; ")
		if reason == "" {
			reason = fmt.Sprintf("runner: %d running", running)
		}
		r.logf("waiting to admit %s: %s", t.Branch, reason)
		time.Sleep(10 * time.Second)
	}
}

func (r *runner) build(t Task) {
	wt := filepath.Join(filepath.Dir(r.main), "wt", t.Branch)
	if _, err := flat.Git(r.main, "worktree", "add", "-q", wt, "-b", t.Branch, "main"); err != nil {
		r.logf("worktree %s: %v", t.Branch, err)
		return
	}
	start := filepath.Join(wt, "briefs", "out", t.Branch, "START.md")
	_ = os.MkdirAll(filepath.Dir(start), 0o755)
	_ = os.WriteFile(start, []byte(fmt.Sprintf("# %s\n\nstarted %s\nbase %s\n\n%s\n", t.Branch, time.Now().UTC().Format(time.RFC3339), r.baseSHA, t.Card)), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "start " + t.Branch}, {"push", "-q", "-u", "origin", t.Branch}} {
		if _, err := flat.Git(wt, args...); err != nil {
			r.logf("start %s: %v", t.Branch, err)
			return
		}
	}
	prompt := r.builderPrompt(t, wt, false)
	ctx := context.Background()
	if t.Fault == "B" && r.o.KillT2After > 0 {
		kctx, cancel := context.WithTimeout(ctx, r.o.KillT2After)
		sess := r.claude(kctx, t.Branch, "builder", wt, prompt, r.o.MaxTurns, r.o.BuilderWall)
		cancel()
		r.logf("fault B: killed %s after %s (turns=%d); resuming the seat in a fresh session", t.Branch, r.o.KillT2After, sess.NumTurns)
		_ = appendJSONL(filepath.Join(r.o.Out, "faults.jsonl"), map[string]any{"at": time.Now().UTC(), "fault": "B", "seat": t.Branch, "killed_after_s": r.o.KillT2After.Seconds()})
		prompt = r.builderPrompt(t, wt, true)
		sess = r.claude(ctx, t.Branch, "resume", wt, prompt, r.o.MaxTurns, r.o.BuilderWall)
		r.logf("builder %s (resumed) done: turns=%d out=%d exit=%d", t.Branch, sess.NumTurns, sess.OutputTok, sess.Exit)
		return
	}
	sess := r.claude(ctx, t.Branch, "builder", wt, prompt, r.o.MaxTurns, r.o.BuilderWall)
	r.logf("builder %s done: turns=%d out=%d exit=%d err=%v", t.Branch, sess.NumTurns, sess.OutputTok, sess.Exit, sess.IsError)
}

func (r *runner) builderPrompt(t Task, wt string, resumed bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "You are seat %s, a builder in a fleet of coding agents working on this repository.\n", t.Branch)
	fmt.Fprintf(&sb, "worktree: %s (you are in it), branch %s, base %s.\n", wt, t.Branch, r.baseSHA[:12])
	sb.WriteString("The flat command is on your PATH; `flat` alone prints its verbs.\n\n")
	if resumed {
		sb.WriteString("You are RESUMING this seat: a previous session on it was interrupted. Run `git log --oneline main..HEAD`, `git status`, `flat inbox`, and `flat requests` first to see what it did and whether it was waiting on a ruling; continue from there rather than starting over.\n\n")
	}
	switch r.o.Mode {
	case "flat":
		sb.WriteString("There is no lead. Peers rule for peers. When you ask, `--needs peer` is the default. Before you write RESULT.json, run `flat requests`: for any open request about ordering or mechanics that you can answer from the board (`flat board --md`), git log and the code, rule it: `flat rule <id> --order \"<first>,<second>\" --ruling \"...\" --evidence \"...\"`. If a request needs product intent the brief does not settle, `flat escalate <id> --to operator --why \"...\"`. Nobody else will do this.\n\n")
	case "tree":
		sb.WriteString("A lead session rules on requests. When you ask for a ruling on ordering or mechanics, pass `--needs lead`. Do not rule on other builders' requests and do not escalate to the operator yourself; the lead does.\n\n")
	}
	sb.WriteString("# Your task\n\n")
	sb.WriteString(t.Card)
	sb.WriteString("\n\n")
	sb.WriteString(Rules)
	sb.WriteString("\nbriefs/BRIEF.md is the topic brief. Start now.\n")
	return sb.String()
}

const consolidatorPrompt = `You are seat theme, the consolidator. Run: flat order
Merge every branch it lists, one at a time, in that order, into this branch with
git merge --no-ff <branch> -m "merge <branch>". After each merge run: go test ./...
Resolve conflicts so every merged branch's behavior survives (every key, every field, every
test). If flat order reports an unruled pair, take the order it printed. Record anything you
could not reconcile in CONFLICTS.md. Then write DEMO.md with one line per merged branch and the
demo command, run git rev-parse HEAD, write briefs/out/theme/RESULT.json with that head_sha and
the list of merged branches as claims, commit only that file, and stop. One shell command per
tool call; no && and no ;.`

// consolidate runs the theme merge: the lead's theme map is now the
// ledger's order, and the consolidator is a seat like any other.
func (r *runner) consolidate() {
	wt := filepath.Join(filepath.Dir(r.main), "wt", "theme")
	if _, err := flat.Git(r.main, "worktree", "add", "-q", wt, "-b", "theme", "main"); err != nil {
		r.logf("theme worktree: %v", err)
		return
	}
	r.mu.Lock()
	r.alive++
	r.mu.Unlock()
	sess := r.claude(context.Background(), "theme", "consolidator", wt, consolidatorPrompt, r.o.MaxTurns, r.o.BuilderWall)
	r.mu.Lock()
	r.alive--
	r.mu.Unlock()
	r.logf("consolidator done: turns=%d out=%d exit=%d", sess.NumTurns, sess.OutputTok, sess.Exit)
}

// claude runs one provider session and records it.
func (r *runner) claude(ctx context.Context, seat, kind, cwd, prompt string, maxTurns int, wall time.Duration) Session {
	sess := Session{Seat: seat, Kind: kind, Mode: r.o.Mode, Started: time.Now().UTC()}
	wctx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	args := []string{"-p", prompt, "--output-format", "json", "--permission-mode", "acceptEdits",
		"--max-turns", fmt.Sprint(maxTurns),
		"--allowedTools", "Read,Edit,Write,MultiEdit,Glob,Grep,Bash(git:*),Bash(go:*),Bash(flat:*),Bash(cat:*),Bash(ls:*),Bash(mkdir:*),Bash(sleep:*),Bash(gofmt:*)"}
	if r.o.Model != "" {
		args = append(args, "--model", r.o.Model)
	}
	cmd := exec.CommandContext(wctx, "claude", args...)
	cmd.Dir = cwd
	cmd.Env = append(append([]string{}, r.env...), "FLAT_SEAT="+seat)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	logName := fmt.Sprintf("%s-%s-%s.log", seat, kind, sess.Started.Format("150405"))
	stderr, err := os.Create(filepath.Join(r.o.Out, "logs", logName))
	if err == nil {
		cmd.Stderr = stderr
		defer stderr.Close()
	}
	runErr := cmd.Run()
	sess.Ended = time.Now().UTC()
	sess.DurationS = sess.Ended.Sub(sess.Started).Seconds()
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			sess.Exit = ee.ExitCode()
		} else {
			sess.Exit = -1
		}
		if wctx.Err() != nil {
			sess.Killed = true
		}
	}
	var res cliResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err == nil {
		sess.SessionID, sess.NumTurns, sess.IsError, sess.CostUSD = res.SessionID, res.NumTurns, res.IsError, res.TotalCost
		sess.InputTok, sess.OutputTok, sess.CacheRead, sess.CacheWrite = res.Usage.Input, res.Usage.Output, res.Usage.CacheRead, res.Usage.CacheWrite
		if n := len(res.Result); n > 400 {
			sess.ResultTail = res.Result[n-400:]
		} else {
			sess.ResultTail = res.Result
		}
	} else {
		sess.ResultTail = strings.TrimSpace(tail(stdout.String(), 400))
	}
	_ = os.WriteFile(filepath.Join(r.o.Out, "logs", strings.TrimSuffix(logName, ".log")+".out.json"), stdout.Bytes(), 0o644)
	_ = appendJSONL(filepath.Join(r.o.Out, "sessions.jsonl"), sess)
	return sess
}

// collect copies the substrate's state into the run directory as evidence.
func (r *runner) collect() error {
	evidence := filepath.Join(r.o.Out, "state")
	if err := os.MkdirAll(evidence, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"events.jsonl", "decisions.jsonl", "tiers.json"} {
		_ = copyFile(filepath.Join(r.state.Dir, name), filepath.Join(evidence, name))
	}
	for _, sub := range []string{"requests", "watch", "resources"} {
		_ = copyTree(filepath.Join(r.state.Dir, sub), filepath.Join(evidence, sub))
	}
	branches, _ := flat.Git(r.main, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	_ = os.WriteFile(filepath.Join(evidence, "branches.txt"), []byte(branches+"\n"), 0o644)
	log, _ := flat.Git(r.main, "log", "--all", "--format=%H %ct %an %s", "--date-order")
	_ = os.WriteFile(filepath.Join(evidence, "log.txt"), []byte(log+"\n"), 0o644)
	r.logf("evidence under %s", r.o.Out)
	return nil
}

// childEnv is the builders' environment: the flat binary first on PATH,
// no trace of the parent harness, and the machine's fleet hook pointed at
// an empty state so its slow-command gate and role bindings stay out of the
// sandbox.
func childEnv(binDir, fleetState string) []string {
	_ = os.MkdirAll(fleetState, 0o755)
	env := []string{"FLEET_STATE=" + fleetState, "FLEET_WATCH=off"}
	pathSet := false
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		switch {
		case key == "CLAUDECODE", key == "FLAT_SEAT", key == "FLAT_DISK_FREE_BYTES", key == "FLEET_STATE", key == "FLEET_WATCH":
			continue
		case strings.EqualFold(key, "PATH"):
			env = append(env, key+"="+binDir+string(os.PathListSeparator)+kv[len(key)+1:])
			pathSet = true
		default:
			env = append(env, kv)
		}
	}
	if !pathSet {
		env = append(env, "PATH="+binDir)
	}
	if runtime.GOOS == "windows" {
		env = append(env, "PATHEXT=.COM;.EXE;.BAT;.CMD")
	}
	return env
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func appendJSONL(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}
