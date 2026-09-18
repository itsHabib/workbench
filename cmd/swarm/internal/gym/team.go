package gym

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The team experiment: the same goal, with no task cards, given to four
// shapes of team. solo is one session that may fan out inside itself. flat is
// n equal peers. swat starts with two peers who agree a plan first, and the
// harness adds a peer while unclaimed work outnumbers idle hands. tree is one
// lead who does not write code and n-1 builders who take what the lead hands
// them. Every seat has its own clone and shares only the origin remote and
// the coordination store. Grading is hidden tests against origin's main.

//go:embed all:testdata
var goals embed.FS

// TeamResult is one run of one shape.
type TeamResult struct {
	At        time.Time      `json:"at"`
	Shape     string         `json:"shape"`
	Model     string         `json:"model"`
	Store     string         `json:"store"`
	Seats     []SeatResult   `json:"seats"`
	Passed    int            `json:"hidden_passed"`
	Total     int            `json:"hidden_total"`
	Builds    bool           `json:"builds"`
	OwnTests  int            `json:"own_test_files"`
	WallS     float64        `json:"wall_s"`
	CostUSD   float64        `json:"cost_usd"`
	Turns     int            `json:"turns"`
	Work      []workRow      `json:"work"`
	Requests  int            `json:"requests"`
	Commits   map[string]int `json:"commits_by_author"`
	Failed    []string       `json:"failed_tests,omitempty"`
	GradeNote string         `json:"grade_note,omitempty"`
}

// SeatResult is one session's bill.
type SeatResult struct {
	Seat      string  `json:"seat"`
	Role      string  `json:"role"`
	JoinedS   float64 `json:"joined_s"`
	WallS     float64 `json:"wall_s"`
	Turns     int     `json:"turns"`
	CostUSD   float64 `json:"cost_usd"`
	CostKnown bool    `json:"cost_known"`
	Wakes     int     `json:"wakes"`
	Killed    bool    `json:"killed,omitempty"`
}

type workRow struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	AddedBy string `json:"added_by"`
	DoneBy  string `json:"done_by"`
	Holder  string `json:"holder"`
	Title   string `json:"title"`
}

const verbsCard = `
How the team coordinates. The only shared things are the git remote "origin" and the swarm commands below. There is no chat.
  swarm work list                                    every unit of work and who holds it
  swarm work add <id> --title "..." --files a,b      add a unit (short kebab-case id). Refused "exists" means someone already added it.
  swarm work claim <id>                              take a unit. Refused "held_by_other" means it is taken: pick another.
  swarm work done <id> --head <sha> --result "..."   after your commit for it is on origin main
  swarm work drop <id>                               give a unit back if you cannot finish it
  swarm ask --scope <pkg> --question "..." --options "a|b"    a decision more than one seat depends on
  swarm requests                                     open questions. Answer one with: swarm claim <req-id>  then  swarm rule <req-id> --ruling "..."
  swarm decisions                                    what has been decided. Decisions bind everyone.
  swarm nudge <seat> "..."                           a note to one seat
  swarm inbox                                        your notes
  swarm idle --timeout 4m                            block until there is something for you (a note, a question, open work, or all done)
Rules: claim before you touch files. Small commits. To land: git pull --rebase origin main, run go build ./... and go test ./..., then git push origin HEAD:main. If the push is rejected, pull --rebase and try again. Never force-push.
Your session ends when your turn ends. Never leave a command running in the background and stop: wait with swarm idle in the foreground, and if it times out run it again.
One shell command per tool call; no && and no ;. Do not look for files named zz_hidden_test.go.`

func teamPrompt(shape, _, role, seat string, roster []string) string {
	others := strings.Join(roster, ", ")
	switch {
	case shape == "solo":
		return "Build what SPEC.md describes, all of it, and land it on origin main. You may use subagents. To land: git pull --rebase origin main, go build ./... and go test ./..., git push origin HEAD:main. One shell command per tool call. Do not look for files named zz_hidden_test.go. Stop when the spec's definition of done holds on origin main."
	case role == "lead":
		return "You are " + seat + ", the lead of a team building what SPEC.md describes. Your builders are: " + others + ". You do not write product code. You break the goal into units with swarm work add, hand each unit to a builder with swarm nudge <seat> \"take <id>: ...\", answer their questions in swarm requests, watch swarm work list and git log origin/main, and check the result builds and tests pass on origin main. Hand out the first units straight away so nobody waits. Use swarm idle between checks. Stop when the spec's definition of done holds on origin main and every unit is done." + verbsCard
	case role == "builder":
		return "You are " + seat + ", a builder on a team building what SPEC.md describes. Your lead is seat lead. Start with swarm inbox: the lead hands you units by note. Work only units the lead handed you: claim, build with tests, land, mark done, then run swarm idle for the next. If something is unclear or touches another package, swarm ask and wait for the ruling rather than guessing. Stop when swarm idle says every unit of work is done." + verbsCard
	case role == "joiner":
		return "You are " + seat + ", joining a team already building what SPEC.md describes. The others are: " + others + ". Nobody is in charge. Read swarm decisions and swarm work list, git pull origin main, then claim an open unit and build it with tests. When nothing is open, run swarm idle. Stop when it says every unit of work is done and origin main builds and passes." + verbsCard
	case shape == "swat":
		return "You are " + seat + ", one of two peers starting a build of what SPEC.md describes. The other is: " + others + ". Nobody is in charge. Agree the plan before writing code: the seat whose name sorts first posts the breakdown and the shared interfaces as one swarm ask; the other claims and rules it, amending if needed. Then add the units with swarm work add and get to work: claim, build with tests, land, mark done. More peers may join when there is open work, so keep units small and independent and keep the list honest. When nothing is open, run swarm idle. Stop when it says every unit of work is done and origin main builds and passes." + verbsCard
	}
	return "You are " + seat + ", one of " + fmt.Sprint(len(roster)+1) + " equal peers building what SPEC.md describes. The others are: " + others + ". Nobody is in charge and nobody will hand you a task. Look at swarm work list first: if it is empty, break the goal into units and add them; if someone beat you to it, use theirs. Then claim, build with tests, land, mark done, repeat. Raise anything two packages must agree on with swarm ask. When nothing is open, run swarm idle. Stop when it says every unit of work is done and origin main builds and passes." + verbsCard
}

type teamRun struct {
	shape, model, store, out string
	goal, self, seatCmd      string
	wakes                    int
	turns                    int
	wall                     time.Duration
	env                      []string
	start                    time.Time
	mu                       sync.Mutex
	seats                    []SeatResult
	wg                       sync.WaitGroup
	ctx                      context.Context
}

func teamCmd(args []string) int {
	fl := flag.NewFlagSet("gym team", flag.ContinueOnError)
	shape := fl.String("shape", "flat", "solo|flat|swat|tree")
	n := fl.Int("n", 3, "seats (swat: the most it may grow to)")
	model := fl.String("model", "claude-sonnet-5", "model for every seat")
	store := fl.String("store", "", "coordination store (default file:<out>/plane)")
	out := fl.String("out", "", "results directory")
	wall := fl.Duration("wall", 30*time.Minute, "wall cap for the whole run")
	turns := fl.Int("turns", 150, "turn cap per session")
	goal := fl.String("goal", "kvlab", "which sandbox goal: a directory under testdata")
	wakes := fl.Int("wakes", 8, "most times a stopped seat is resumed")
	seatCmd := fl.String("seat-cmd", "", "shell that runs one seat turn somewhere else (see docs/SEAT.md); empty runs claude here")
	if fl.Parse(args) != nil || *out == "" {
		fmt.Fprint(os.Stderr, usage)
		return 3
	}
	abs, _ := filepath.Abs(*out)
	if err := os.MkdirAll(filepath.Join(abs, "logs"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	if *store == "" {
		*store = "file:" + filepath.Join(abs, "plane")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *wall)
	defer cancel()
	self, _ := os.Executable()
	r := &teamRun{seatCmd: *seatCmd, goal: *goal, self: self, wakes: *wakes, shape: *shape, model: *model, store: *store, out: abs, turns: *turns, wall: *wall, ctx: ctx, start: time.Now()}
	r.env = append(childEnv(filepath.Dir(self), filepath.Join(abs, "fleet-state")), "SWARM_STORE="+*store)
	if err := r.seed(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		return 3
	}
	r.launch(*n)
	r.wg.Wait()
	res := r.result()
	data, _ := json.MarshalIndent(res, "", "  ")
	_ = os.WriteFile(filepath.Join(abs, "result.json"), data, 0o644)
	fmt.Print(TeamTable([]TeamResult{res}))
	return 0
}

func (r *teamRun) launch(n int) {
	switch r.shape {
	case "solo":
		r.spawn("solo", "solo", nil)
	case "tree":
		var builders []string
		for i := 1; i < n; i++ {
			builders = append(builders, fmt.Sprintf("b%d", i))
		}
		_ = r.swarm("lead", "init", "--operator", "operator", "--lead", "lead")
		r.spawn("lead", "lead", builders)
		for _, b := range builders {
			r.spawn(b, "builder", nil)
		}
	case "swat":
		r.spawn("p1", "peer", []string{"p2"})
		r.spawn("p2", "peer", []string{"p1"})
		r.wg.Add(1)
		go r.grow(n)
	default:
		var all []string
		for i := 1; i <= n; i++ {
			all = append(all, fmt.Sprintf("p%d", i))
		}
		for i, s := range all {
			others := append(append([]string{}, all[:i]...), all[i+1:]...)
			r.spawn(s, "peer", others)
		}
	}
}

// grow adds a peer while open work outnumbers the seats not holding any. It
// reads nothing but the work list, which is the point: the list is the load
// signal, and nobody has to ask for help.
func (r *teamRun) grow(limit int) {
	defer r.wg.Done()
	live := []string{"p1", "p2"}
	for len(live) < limit {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
		open, held, done, total := r.workCounts()
		if total > 0 && done == total {
			return
		}
		idle := len(live) - held
		if open < 2 || open <= idle {
			continue
		}
		seat := fmt.Sprintf("p%d", len(live)+1)
		fmt.Printf("[%4.0fs] grow: %d open, %d held, %d seats -> adding %s\n", time.Since(r.start).Seconds(), open, held, len(live), seat)
		r.spawn(seat, "joiner", live)
		live = append(live, seat)
	}
}

func (r *teamRun) workCounts() (open, held, done, total int) {
	for _, w := range r.workList() {
		total++
		switch w.State {
		case "open":
			open++
		case "claimed":
			held++
		case "done":
			done++
		}
	}
	return
}

func (r *teamRun) workList() []workRow {
	cmd := exec.Command(r.self, "work", "list", "--json", "--as", "harness")
	cmd.Dir, cmd.Env = filepath.Join(r.out, "seed"), r.env
	data, err := cmd.Output()
	if err != nil {
		return nil
	}
	var rows []workRow
	_ = json.Unmarshal(data, &rows)
	return rows
}

func (r *teamRun) swarm(seat string, args ...string) error {
	cmd := exec.Command(r.self, append(args, "--as", seat)...)
	cmd.Dir, cmd.Env = filepath.Join(r.out, "seed"), r.env
	return cmd.Run()
}

// seed builds origin with the spec and an empty module, and nothing else.
func (r *teamRun) seed() error {
	origin := filepath.Join(r.out, "origin.git")
	seed := filepath.Join(r.out, "seed")
	if err := hgit("", "init", "-q", "--bare", "-b", "main", origin); err != nil {
		return err
	}
	if err := hgit("", "clone", "-q", origin, seed); err != nil {
		return err
	}
	spec, _ := goals.ReadFile("testdata/" + r.goal + "/SPEC.md")
	_ = os.WriteFile(filepath.Join(seed, "SPEC.md"), spec, 0o644)
	_ = os.WriteFile(filepath.Join(seed, "go.mod"), []byte("module "+r.goal+"\n\ngo 1.22\n"), 0o644)
	for _, step := range [][]string{{"checkout", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "spec"}, {"push", "-q", "origin", "main"}} {
		if err := hgit(seed, step...); err != nil {
			return err
		}
	}
	return nil
}

func hgit(dir string, args ...string) error {
	full := append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "-c", "user.name=harness", "-c", "user.email=harness@example.invalid"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, tail(string(out), 300))
	}
	return nil
}

func (r *teamRun) spawn(seat, role string, roster []string) {
	dir := filepath.Join(r.out, "seats", seat)
	_ = os.MkdirAll(filepath.Dir(dir), 0o755)
	if err := hgit("", "clone", "-q", filepath.Join(r.out, "origin.git"), dir); err != nil {
		fmt.Fprintln(os.Stderr, "clone:", err)
		return
	}
	for _, kv := range [][]string{{"user.name", seat}, {"user.email", seat + "@example.invalid"}, {"commit.gpgsign", "false"}, {"core.hooksPath", "/dev/null"}} {
		_ = hgit(dir, "config", kv[0], kv[1])
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		sr := SeatResult{Seat: seat, Role: role, JoinedS: time.Since(r.start).Seconds(), CostKnown: true}
		t0 := time.Now()
		session, msg := "", teamPrompt(r.shape, r.goal, role, seat, roster)
		for {
			session = r.turn(&sr, dir, session, msg)
			if session == "" || r.ctx.Err() != nil || sr.Wakes >= r.wakes {
				break
			}
			if msg = r.wakeReason(seat); msg == "" {
				break
			}
			sr.Wakes++
			fmt.Printf("[%4.0fs] wake %s: %s\n", time.Since(r.start).Seconds(), seat, tailLine(msg))
		}
		sr.WallS, sr.Killed = time.Since(t0).Seconds(), r.ctx.Err() != nil
		fmt.Printf("[%4.0fs] %s (%s) stopped: %d turns $%.2f, %d wakes\n", time.Since(r.start).Seconds(), seat, role, sr.Turns, sr.CostUSD, sr.Wakes)
		r.mu.Lock()
		r.seats = append(r.seats, sr)
		r.mu.Unlock()
	}()
}

// turn runs one headless session to the end of its turn: a fresh one, or a
// resume of the seat's last with a wake message. It returns the session id.
func (r *teamRun) turn(sr *SeatResult, dir, session, msg string) string {
	tools := "Read,Edit,Write,MultiEdit,Glob,Grep,Bash(git:*),Bash(go:*),Bash(swarm:*),Bash(cat:*),Bash(ls:*),Bash(gofmt:*),Bash(mkdir:*)"
	if r.shape == "solo" {
		tools += ",Task,Agent"
	}
	args := []string{"-p", msg, "--output-format", "json", "--permission-mode", "acceptEdits", "--max-turns", fmt.Sprint(r.turns), "--model", r.model, "--allowedTools", tools}
	if session != "" {
		args = append(args, "--resume", session)
	}
	cmd := exec.CommandContext(r.ctx, "claude", args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, r.env...), "SWARM_SEAT="+sr.Seat)
	if r.seatCmd != "" {
		cmd = r.remoteTurn(sr, dir, session, msg)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	var res struct {
		NumTurns  int     `json:"num_turns"`
		Cost      float64 `json:"total_cost_usd"`
		SessionID string  `json:"session_id"`
	}
	if json.Unmarshal(lastJSONLine(stdout.Bytes()), &res) != nil {
		sr.CostKnown = false
	}
	sr.Turns += res.NumTurns
	sr.CostUSD += res.Cost
	name := fmt.Sprintf("%s.%d", sr.Seat, sr.Wakes)
	_ = os.WriteFile(filepath.Join(r.out, "logs", name+".out.json"), stdout.Bytes(), 0o644)
	_ = os.WriteFile(filepath.Join(r.out, "logs", name+".err.txt"), stderr.Bytes(), 0o644)
	return res.SessionID
}

// remoteTurn hands one turn to the substrate named by --seat-cmd. The shell
// gets the seat, the prompt as a file, the session to resume, the checkout,
// the origin, the model and the turn cap in its environment, and must print
// the seat runner's JSON line last. Which machine it runs on is its business.
func (r *teamRun) remoteTurn(sr *SeatResult, dir, session, msg string) *exec.Cmd {
	promptFile := filepath.Join(r.out, "logs", fmt.Sprintf("%s.%d.prompt", sr.Seat, sr.Wakes))
	_ = os.WriteFile(promptFile, []byte(msg), 0o644)
	cmd := exec.CommandContext(r.ctx, "sh", "-c", r.seatCmd)
	cmd.Dir = r.out
	cmd.Env = append(append([]string{}, r.env...),
		"SEAT="+sr.Seat, "PROMPT_FILE="+promptFile, "RESUME="+session, "DIR="+dir,
		"REMOTE="+filepath.Join(r.out, "origin.git"), "MODEL="+r.model, "TURNS="+fmt.Sprint(r.turns), "SWARM_SEAT="+sr.Seat)
	return cmd
}

// wakeReason blocks until a stopped seat has something to do and says what,
// or returns "" when the team is finished. A headless session is gone once
// its turn ends, so a seat that stopped holding a unit is told so at once.
func (r *teamRun) wakeReason(seat string) string {
	if r.shape == "solo" {
		return ""
	}
	for r.ctx.Err() == nil {
		rows := r.workList()
		done := 0
		for _, w := range rows {
			if w.State == "done" {
				done++
			}
			if w.State == "claimed" && w.Holder == seat {
				return "You stopped while holding unit " + w.ID + ". Your session ends when your turn ends: nothing you left running in the background survives. Finish " + w.ID + " now, or run swarm work drop " + w.ID + " so someone else can. To wait for others, run swarm idle in the foreground."
			}
		}
		if len(rows) == 0 || done == len(rows) {
			return ""
		}
		cmd := exec.CommandContext(r.ctx, r.self, "idle", "--timeout", "45s", "--as", seat)
		cmd.Dir, cmd.Env = filepath.Join(r.out, "seed"), r.env
		if out, err := cmd.Output(); err == nil && !strings.Contains(string(out), "every unit") {
			return "Wake: " + strings.TrimSpace(string(out)) + ". The team is not finished."
		}
	}
	return ""
}

func (r *teamRun) result() TeamResult {
	res := TeamResult{At: r.start.UTC(), Shape: r.shape, Model: r.model, Store: r.store, Seats: r.seats,
		WallS: time.Since(r.start).Seconds(), Work: r.workList()}
	sort.Slice(res.Seats, func(i, j int) bool { return res.Seats[i].Seat < res.Seats[j].Seat })
	for _, s := range res.Seats {
		res.CostUSD += s.CostUSD
		res.Turns += s.Turns
	}
	res.Requests = r.countRequests()
	g := Grade(r.goal, filepath.Join(r.out, "origin.git"), filepath.Join(r.out, "grade"))
	res.Total = g.Passed + len(g.Failed)
	res.Passed, res.Builds, res.OwnTests, res.Failed, res.Commits, res.GradeNote = g.Passed, g.Builds, g.OwnTests, g.Failed, g.Commits, g.Note
	return res
}

func (r *teamRun) countRequests() int {
	cmd := exec.Command(r.self, "requests", "--all", "--json", "--as", "harness")
	cmd.Dir, cmd.Env = filepath.Join(r.out, "seed"), r.env
	data, err := cmd.Output()
	if err != nil {
		return -1
	}
	var rows []json.RawMessage
	_ = json.Unmarshal(data, &rows)
	return len(rows)
}

// Graded is what the hidden tests say about a repository's main.
type Graded struct {
	Passed   int
	Builds   bool
	OwnTests int
	Failed   []string
	Commits  map[string]int
	Note     string
}

// Grade clones origin's main, drops the hidden tests in, and counts which of
// them pass. A package that does not compile fails all of its tests.
func Grade(goal, origin, into string) Graded {
	g := Graded{Commits: map[string]int{}}
	_ = os.RemoveAll(into)
	if err := hgit("", "clone", "-q", origin, into); err != nil {
		g.Note = err.Error()
		return g
	}
	if out, err := exec.Command("git", "-C", into, "log", "--format=%an").Output(); err == nil {
		for _, a := range strings.Fields(string(out)) {
			g.Commits[a]++
		}
	}
	_ = filepath.WalkDir(into, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, "_test.go") {
			g.OwnTests++
		}
		return nil
	})
	build := exec.Command("go", "build", "./...")
	build.Dir = into
	g.Builds = build.Run() == nil
	want, err := plantHidden(goal, into)
	if err != nil {
		g.Note = err.Error()
		return g
	}
	test := exec.Command("go", "test", "-json", "-count=1", "-run", "TestHidden", "./...")
	test.Dir = into
	out, _ := test.Output()
	passed := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		var ev struct{ Action, Package, Test string }
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Action == "pass" && ev.Test != "" {
			passed[ev.Package+"."+ev.Test] = true
		}
	}
	for _, name := range want {
		if passed[name] {
			g.Passed++
			continue
		}
		g.Failed = append(g.Failed, name)
	}
	return g
}

// plantHidden copies the hidden tests into a checkout and returns the
// package-qualified names the grader expects to see pass.
func plantHidden(goal, into string) ([]string, error) {
	var want []string
	root := "testdata/" + goal + "/hidden"
	prefix := layoutPrefix(into)
	err := fs.WalkDir(goals, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := goals.ReadFile(p)
		if err != nil {
			return err
		}
		pkg := filepath.Base(filepath.Dir(p))
		for _, line := range strings.Split(string(data), "\n") {
			if name, ok := strings.CutPrefix(line, "func TestHidden"); ok {
				want = append(want, goal+"/"+prefix+pkg+".TestHidden"+name[:strings.Index(name, "(")])
			}
		}
		if prefix != "" {
			data = bytes.ReplaceAll(data, []byte(`"`+goal+`/`), []byte(`"`+goal+`/`+prefix))
		}
		dst := filepath.Join(into, prefix, pkg)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, "zz_hidden_test.go"), data, 0o644)
	})
	sort.Strings(want)
	return want, err
}

// lastJSONLine is the seat's result: claude prints one JSON document, a
// seat runner prints one JSON line last, after whatever the substrate said.
func lastJSONLine(out []byte) []byte {
	trimmed := bytes.TrimSpace(out)
	if bytes.HasPrefix(trimmed, []byte("{")) && json.Valid(trimmed) {
		return trimmed
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := bytes.TrimSpace(lines[i]); bytes.HasPrefix(l, []byte("{")) {
			return l
		}
	}
	return trimmed
}

// layoutPrefix finds where a team put its packages. The specs name packages,
// not directories, so a team that agreed on pkg/ or internal/ is graded
// there rather than scored zero for a layout choice.
func layoutPrefix(into string) string {
	for _, prefix := range []string{"", "pkg/", "internal/", "src/"} {
		found := 0
		entries, _ := os.ReadDir(filepath.Join(into, prefix))
		for _, e := range entries {
			if files, _ := filepath.Glob(filepath.Join(into, prefix, e.Name(), "*.go")); e.IsDir() && len(files) > 0 {
				found++
			}
		}
		if found >= 2 {
			return prefix
		}
	}
	return ""
}

// TeamTable renders runs side by side.
func TeamTable(rs []TeamResult) string {
	var sb strings.Builder
	sb.WriteString("| shape | seats | hidden tests | builds | wall | cost | turns | units (added by) | questions |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rs {
		adders := map[string]int{}
		done := 0
		for _, w := range r.Work {
			adders[w.AddedBy]++
			if w.State == "done" {
				done++
			}
		}
		var who []string
		for a, n := range adders {
			who = append(who, fmt.Sprintf("%s:%d", a, n))
		}
		sort.Strings(who)
		fmt.Fprintf(&sb, "| %s | %d | %d/%d | %v | %.0fs | $%.2f | %d | %d, %d done (%s) | %d |\n", r.Shape, len(r.Seats), r.Passed, r.Total, r.Builds,
			r.WallS, r.CostUSD, r.Turns, len(r.Work), done, strings.Join(who, " "), r.Requests)
	}
	return sb.String()
}

// teamTableCmd renders saved runs. With --regrade GOAL it grades each run's
// origin again first, for when the grader changed and the sessions did not.
func teamTableCmd(args []string) int {
	fl := flag.NewFlagSet("gym team-table", flag.ContinueOnError)
	regrade := fl.String("regrade", "", "grade each run's origin.git again against this goal")
	if fl.Parse(args) != nil {
		return 3
	}
	var rs []TeamResult
	for _, d := range fl.Args() {
		data, err := os.ReadFile(filepath.Join(d, "result.json"))
		var r TeamResult
		if err != nil || json.Unmarshal(data, &r) != nil {
			fmt.Fprintln(os.Stderr, "skipping", d)
			continue
		}
		if *regrade != "" {
			g := Grade(*regrade, filepath.Join(d, "origin.git"), filepath.Join(d, "grade"))
			r.Passed, r.Total, r.Builds, r.Failed, r.GradeNote = g.Passed, g.Passed+len(g.Failed), g.Builds, g.Failed, g.Note
			out, _ := json.MarshalIndent(r, "", "  ")
			_ = os.WriteFile(filepath.Join(d, "result.json"), out, 0o644)
		}
		rs = append(rs, r)
	}
	fmt.Print(TeamTable(rs))
	return 0
}
