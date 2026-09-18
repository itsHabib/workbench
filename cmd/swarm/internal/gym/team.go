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
	Receipts  []receipt      `json:"receipts,omitempty"`
	Children  []ChildResult  `json:"children,omitempty"`
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
	Team    bool      `json:"team"`
	Brief   string    `json:"brief"`
	After   []string  `json:"after"`
	DoneAt  time.Time `json:"done_at"`
	ID      string    `json:"id"`
	State   string    `json:"state"`
	AddedBy string    `json:"added_by"`
	DoneBy  string    `json:"done_by"`
	Holder  string    `json:"holder"`
	Title   string    `json:"title"`
}

const verbsCard = `
How the team coordinates. The only shared things are the git remote "origin" and the swarm commands below. There is no chat.
  swarm work list                                    every unit of work and who holds it
  swarm work add <id> --title "..." --files a,b      add a unit (short kebab-case id). Refused "exists" means someone already added it.
  swarm work next                                    open units, the ones nearest what you already touched first
  swarm work claim <id>                              take a unit (you may hold two: line up your follow-on while you land the first). Refused "held_by_other" means it is taken: pick another.
  swarm work done <id> --head <sha> --result "..."   after your commit for it is on origin BRANCH
  swarm work add <id> --title "..." --files dir1,dir2 --team --brief "..."   (founders only) a part big enough for a team of its own; --files names the directories that team owns, and a second team for the same directory is refused
  swarm work drop <id>                               give a unit back if you cannot finish it
  swarm ask --scope <pkg> --question "..." --options "a|b"    a decision more than one seat depends on
  swarm requests                                     open questions. Answer one with: swarm claim <req-id>  then  swarm rule <req-id> --ruling "..."
  swarm decisions                                    what has been decided. Decisions bind everyone.
  swarm nudge <seat> "..."                           a note to one seat
  swarm inbox                                        your notes
  swarm idle --timeout 4m                            block until there is something for you (a note, a question, open work, or all done)
Rules: claim before you touch files. Small commits. To land: git pull --rebase origin BRANCH, run go build ./... and go test ./..., then git push origin HEAD:BRANCH. If the push is rejected, pull --rebase and try again. Never force-push.
Your session ends when your turn ends. Never leave a command running in the background and stop: wait with swarm idle in the foreground, and if it times out run it again.
One shell command per tool call; no && and no ;. Do not look for files named zz_hidden_test.go.`

func teamPrompt(shape, branch, role, seat string, roster []string) string {
	text := teamPromptText(shape, role, seat, roster)
	if branch != "main" {
		text = strings.Replace(text, "what SPEC.md describes", "your team's part (TEAM.md) of what SPEC.md describes", 1)
	}
	return strings.ReplaceAll(text, "BRANCH", branch)
}

func teamPromptText(shape, role, seat string, roster []string) string {
	others := strings.Join(roster, ", ")
	switch {
	case shape == "solo":
		return "Build what SPEC.md describes, all of it, and land it on origin BRANCH. You may use subagents. To land: git pull --rebase origin BRANCH, go build ./... and go test ./..., git push origin HEAD:BRANCH. One shell command per tool call. Do not look for files named zz_hidden_test.go. Stop when the spec's definition of done holds on origin BRANCH."
	case role == "lead":
		return "You are " + seat + ", the lead of a team building what SPEC.md describes. Your builders are: " + others + ". You do not write product code. You break the goal into units with swarm work add, hand each unit to a builder with swarm nudge <seat> \"take <id>: ...\", answer their questions in swarm requests, watch swarm work list and git log origin/BRANCH, and check the result builds and tests pass on origin BRANCH. Hand out the first units straight away so nobody waits. Use swarm idle between checks. Stop when the spec's definition of done holds on origin BRANCH and every unit is done." + verbsCard
	case role == "builder":
		return "You are " + seat + ", a builder on a team building what SPEC.md describes. Your lead is seat lead. Start with swarm inbox: the lead hands you units by note. Work only units the lead handed you: claim, build with tests, land, mark done, then run swarm idle for the next. If something is unclear or touches another package, swarm ask and wait for the ruling rather than guessing. Stop when swarm idle says every unit of work is done." + verbsCard
	case role == "joiner":
		return "You are " + seat + ", joining a team already building what SPEC.md describes. The others are: " + others + ". Nobody is in charge. Read swarm decisions and swarm work list, git pull origin BRANCH, then claim an open unit and build it with tests. When nothing is open, run swarm idle. Stop when it says every unit of work is done and origin BRANCH builds and passes." + verbsCard
	case shape == "teams" && role == "peer":
		return "You are " + seat + ", one of two founders of a team of teams building what SPEC.md describes. The other founder is: " + others + ". Nobody is in charge. Agree the plan first: the seat whose name sorts first posts the breakdown and the shared interfaces as one swarm ask; the other claims and rules it. This goal is too big for one team, so cut it into a few large parts that can be built in parallel and the founder who posted the plan adds each part, after the ruling, with swarm work add <id> --title ... --files <the directories that team owns> --team --brief \"what that team must build, which packages, what it may assume from the others\"; the other founder does not add parts, it claims foundation units. A team of its own forms for every --team unit, works on its own branch, and its result is merged into BRANCH for you when it is green; you never build those parts. Keep the shared foundation (the lowest packages everyone imports) as ordinary units and build it yourselves first, so the teams can import it. Then claim, build, land, mark done. When nothing is open, run swarm idle. Stop when it says every unit of work is done and origin BRANCH builds and passes." + verbsCard
	case shape == "swat":
		return "You are " + seat + ", one of two peers starting a build of what SPEC.md describes. The other is: " + others + ". Nobody is in charge. Agree the plan before writing code: the seat whose name sorts first posts the breakdown and the shared interfaces as one swarm ask; the other claims and rules it, amending if needed. Then add the units with swarm work add and get to work: claim, build with tests, land, mark done. More peers may join when there is open work, so keep units small and independent and keep the list honest. When nothing is open, run swarm idle. Stop when it says every unit of work is done and origin BRANCH builds and passes." + verbsCard
	}
	return "You are " + seat + ", one of " + fmt.Sprint(len(roster)+1) + " equal peers building what SPEC.md describes. The others are: " + others + ". Nobody is in charge and nobody will hand you a task. Look at swarm work list first: if it is empty, break the goal into units and add them; if someone beat you to it, use theirs. Then claim, build with tests, land, mark done, repeat. Raise anything two packages must agree on with swarm ask. When nothing is open, run swarm idle. Stop when it says every unit of work is done and origin BRANCH builds and passes." + verbsCard
}

type receipt struct {
	Head   string  `json:"head"`
	Author string  `json:"author"`
	Green  bool    `json:"green"`
	At     float64 `json:"at_s"`
}

type teamRun struct {
	receipts                 []receipt
	children                 []ChildResult
	lives                    map[string]*liveSeat
	childRuns                map[string]*teamRun
	cancel                   context.CancelFunc
	decisions                []BrainDecision
	brainCost, costCap       float64
	brainOn                  bool
	brainEvery               time.Duration
	brainModel, metricsAddr  string
	deadline                 time.Duration
	escalated                bool
	live                     []string
	shape, model, store, out string
	branch, brief            string
	originPath               string
	childMax                 int
	goal, self, seatCmd      string
	goalFile, module         string
	reconcile                bool
	twist, twistTest         string
	twistAfter               time.Duration
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
	shape := fl.String("shape", "flat", "solo|flat|swat|tree|teams")
	n := fl.Int("n", 3, "seats (swat: the most it may grow to)")
	model := fl.String("model", "claude-sonnet-5", "model for every seat")
	store := fl.String("store", "", "coordination store (default file:<out>/plane)")
	out := fl.String("out", "", "results directory")
	wall := fl.Duration("wall", 30*time.Minute, "wall cap for the whole run")
	turns := fl.Int("turns", 150, "turn cap per session")
	goal := fl.String("goal", "kvlab", "which sandbox goal: a directory under testdata")
	goalFile := fl.String("goal-file", "", "a goal of your own (a markdown file); no hidden tests, graded on build and the team's own tests")
	twist := fl.String("twist", "", "a requirement change delivered by note to every live seat partway through")
	twistTest := fl.String("twist-test", "", "a Go test file the customer lands on main with the twist, under customer/; red until the team meets the change")
	twistAfter := fl.Duration("twist-after", 4*time.Minute, "when the twist is delivered")
	reconcile := fl.Bool("reconcile", false, "a controller verifies every landing on origin main and nudges its author when red")
	wakes := fl.Int("wakes", 8, "most times a stopped seat is resumed")
	childMax := fl.Int("team-max", 4, "teams: most seats a child team may grow to")
	deadline := fl.Duration("deadline", 0, "when the work should be done, from the start; growth and the brain read the projection against it")
	brainOn := fl.Bool("brain", false, "a model judges the telemetry every interval and may add, retire, form, disband, nudge or escalate; replaces the fixed growth rule")
	brainEvery := fl.Duration("brain-every", 60*time.Second, "how often the brain judges")
	brainModel := fl.String("brain-model", "claude-sonnet-5", "model the brain uses")
	costCap := fl.Float64("cost-cap", 0, "dollars the brain is told not to exceed (0: none)")
	metricsAddr := fl.String("metrics", "", "serve Prometheus text metrics on this address, e.g. :9109")
	seatCmd := fl.String("seat-cmd", "", "shell that runs one seat turn somewhere else (see docs/SEAT.md); empty runs claude here")
	if fl.Parse(args) != nil || *out == "" {
		fmt.Fprint(os.Stderr, usage)
		return 3
	}
	if _, err := exec.LookPath("go"); err != nil {
		// The seats may run anywhere, but the grader builds and tests here.
		fmt.Fprintln(os.Stderr, "gym team: go is not on this machine's PATH; the seats could succeed and the grader could not. Install Go here, or run team-table --regrade later on a machine that has it.")
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
	r := &teamRun{deadline: *deadline, brainOn: *brainOn, brainEvery: *brainEvery, brainModel: *brainModel, costCap: *costCap, metricsAddr: *metricsAddr, lives: map[string]*liveSeat{}, childRuns: map[string]*teamRun{}, branch: "main", childMax: *childMax, twistTest: *twistTest, twist: *twist, twistAfter: *twistAfter, goalFile: *goalFile, reconcile: *reconcile, module: "goal", seatCmd: *seatCmd, goal: *goal, self: self, wakes: *wakes, shape: *shape, model: *model, store: *store, out: abs, turns: *turns, wall: *wall, ctx: ctx, start: time.Now()}
	r.env = append(childEnv(filepath.Dir(self), filepath.Join(abs, "fleet-state")), "SWARM_STORE="+*store)
	if err := r.seed(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		return 3
	}
	res := r.run(*n)
	data, _ := json.MarshalIndent(res, "", "  ")
	_ = os.WriteFile(filepath.Join(abs, "result.json"), data, 0o644)
	fmt.Print(TeamTable([]TeamResult{res}))
	return 0
}

// run drives one team to its end: seats, controllers, and the result.
func (r *teamRun) run(n int) TeamResult {
	r.launch(n)
	if r.reconcile {
		r.wg.Add(1)
		go r.reconciler()
	}
	if r.twist != "" {
		r.wg.Add(1)
		go r.twister()
	}
	if r.shape == "teams" || r.brainOn {
		r.wg.Add(1)
		go r.teamsController()
	}
	if r.brainOn {
		r.wg.Add(1)
		go r.brain()
	}
	if r.metricsAddr != "" {
		go r.metrics(r.metricsAddr)
	}
	r.wg.Wait()
	return r.result()
}

func (r *teamRun) launch(n int) {
	switch r.shape {
	case "teams":
		r.spawn("p1", "peer", []string{"p2"})
		r.spawn("p2", "peer", []string{"p1"})
		if r.brainOn {
			return
		}
		r.wg.Add(1)
		go r.grow(n)
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
		if r.brainOn {
			return
		}
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

// twister is the customer changing their mind. After a delay it sends the
// change as a note to every seat that has joined and appends it to SPEC.md
// on origin, so a seat that reads the spec later sees it too. Work landed
// under the old requirement is now wrong; the ledger has to be superseded
// and the reconcile controller has to catch whatever goes red meanwhile.
func (r *teamRun) twister() {
	defer r.wg.Done()
	select {
	case <-r.ctx.Done():
		return
	case <-time.After(r.twistAfter):
	}
	seed := filepath.Join(r.out, "seed")
	_ = hgit(seed, "pull", "-q", "--rebase", "origin", "main")
	f, err := os.OpenFile(filepath.Join(seed, "SPEC.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString("\n\n## Change from the customer\n\n" + r.twist + "\n")
		_ = f.Close()
		if r.twistTest != "" {
			if data, err := os.ReadFile(r.twistTest); err == nil {
				_ = os.MkdirAll(filepath.Join(seed, "customer"), 0o755)
				_ = os.WriteFile(filepath.Join(seed, "customer", "customer_test.go"), data, 0o644)
				_ = hgit(seed, "add", "customer")
			}
		}
		_ = hgit(seed, "commit", "-q", "-am", "customer change")
		_ = hgit(seed, "push", "-q", "origin", "HEAD:main")
	}
	r.mu.Lock()
	seats := append([]string{}, r.live...)
	r.mu.Unlock()
	for _, seat := range seats {
		_ = r.swarm("customer", "nudge", seat, "CHANGE OF REQUIREMENT (also appended to SPEC.md on origin main): "+r.twist+" Decide with the team how existing decisions and landed work change; supersede rulings rather than ignoring them; fix what this makes wrong before taking new work.")
	}
	fmt.Printf("[%4.0fs] twist delivered to %s\n", time.Since(r.start).Seconds(), strings.Join(seats, ","))
}

// reconciler is a standing controller, not a seat: every new commit on
// origin main is built and tested in a fresh clone, and a red landing is
// sent back to the seat that made it by note. It reads git and the store
// and never writes code, which is what a reconcile role reduces to.
func (r *teamRun) reconciler() {
	defer r.wg.Done()
	seen := ""
	for r.ctx.Err() == nil {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
		head, author := r.originHead()
		if head == "" || head == seen {
			if r.finished() {
				return
			}
			continue
		}
		seen = head
		ok, out := r.verifyHead(head)
		r.mu.Lock()
		if ls := r.lives[author]; ls != nil {
			ls.mu.Lock()
			ls.lastLand = time.Now()
			ls.mu.Unlock()
		}
		r.mu.Unlock()
		verdict := map[bool]string{true: "pass", false: "fail"}[ok]
		_ = r.swarm("reconciler", "receipt", head, "--verdict", verdict, "--cmd", "go build+vet+test", "--tail", tail(out, 400))
		r.mu.Lock()
		r.receipts = append(r.receipts, receipt{Head: head, Author: author, Green: ok, At: time.Since(r.start).Seconds()})
		r.mu.Unlock()
		if ok {
			fmt.Printf("[%4.0fs] reconcile: %s by %s green\n", time.Since(r.start).Seconds(), head[:8], author)
			continue
		}
		fmt.Printf("[%4.0fs] reconcile: %s by %s RED, nudging\n", time.Since(r.start).Seconds(), head[:8], author)
		r.mu.Lock()
		targets := append([]string{}, r.live...)
		r.mu.Unlock()
		msg := "Landing " + head[:8] + " by " + author + " is red on a fresh clone of origin main. Whoever owns the failing area fixes it before taking new work; if it is the customer's acceptance test, add a unit for it:\n" + tail(out, 1500)
		if author != "" && author != "harness" {
			targets = []string{author}
			msg = "Your landing " + head[:8] + " is red on a fresh clone of origin main. Fix it before taking new work:\n" + tail(out, 1500)
		}
		for _, seat := range targets {
			_ = r.swarm("reconciler", "nudge", seat, msg)
		}
	}
}

func (r *teamRun) originHead() (head, author string) {
	out, err := exec.Command("git", "--git-dir", r.origin(), "log", "-1", "--format=%H %an", r.branch).Output()
	if err != nil {
		return "", ""
	}
	head, author, _ = strings.Cut(strings.TrimSpace(string(out)), " ")
	return head, author
}

func (r *teamRun) verifyHead(head string) (bool, string) {
	dir := filepath.Join(r.out, "verify", head[:12])
	_ = os.RemoveAll(dir)
	if err := hgit("", "clone", "-q", "-b", r.branch, r.origin(), dir); err != nil {
		return false, err.Error()
	}
	var log strings.Builder
	for _, step := range [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, {"go", "test", "-count=1", "./..."}} {
		cmd := exec.CommandContext(r.ctx, step[0], step[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		log.Write(out)
		if err != nil {
			return false, strings.Join(step, " ") + "\n" + log.String()
		}
	}
	_ = os.RemoveAll(dir)
	return true, ""
}

func (r *teamRun) finished() bool {
	open, held, done, total := r.workCounts()
	return total > 0 && open == 0 && held == 0 && done == total
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
		open, _, done, total := r.workCounts()
		if total > 0 && done == total {
			return
		}
		idle := len(live) - r.holders()
		if r.deadline > 0 {
			pr := project(r.workList(), len(live), idle, time.Since(r.start), r.deadline)
			if pr.Recommend == "escalate" && !r.escalated {
				r.escalated = true
				_ = os.WriteFile(filepath.Join(r.out, "ESCALATION.md"), []byte(fmt.Sprintf("projected finish %.0fs against deadline %.0fs: %s\n", pr.FinishS, pr.DeadlineS, pr.Why)), 0o644)
				fmt.Printf("[%4.0fs] ESCALATION: %s (finish %.0fs, deadline %.0fs)\n", time.Since(r.start).Seconds(), pr.Why, pr.FinishS, pr.DeadlineS)
			}
			if pr.Recommend != "add_seat" {
				continue
			}
			fmt.Printf("[%4.0fs] projection: finish %.0fs vs deadline %.0fs, one more seat gains %.0fs\n", time.Since(r.start).Seconds(), pr.FinishS, pr.DeadlineS, pr.SeatGainS)
		} else if open < 2 || open <= idle {
			continue
		}
		seat := fmt.Sprintf("p%d", len(live)+1)
		fmt.Printf("[%4.0fs] grow: %d open, %d idle, %d seats -> adding %s\n", time.Since(r.start).Seconds(), open, idle, len(live), seat)
		r.spawn(seat, "joiner", live)
		live = append(live, seat)
	}
}

// holders is how many distinct seats hold at least one unit.
func (r *teamRun) holders() int {
	seen := map[string]bool{}
	for _, w := range r.workList() {
		if w.State == "claimed" && w.Holder != "" {
			seen[w.Holder] = true
		}
	}
	return len(seen)
}

func (r *teamRun) workCounts() (open, held, done, total int) {
	for _, w := range r.workList() {
		total++
		switch {
		case w.State == "open" && w.Team:
			// a team's job: the harness claims it within a poll, not a seat
		case w.State == "open":
			open++
		case w.State == "claimed":
			held++
		case w.State == "done":
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
	spec, err := goalSpec(r.goal)
	if r.goalFile != "" {
		spec, err = os.ReadFile(r.goalFile)
		r.goal = ""
	} else {
		r.module = r.goal
	}
	if err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(seed, "SPEC.md"), spec, 0o644)
	_ = os.WriteFile(filepath.Join(seed, "go.mod"), []byte("module "+r.module+"\n\ngo 1.22\n"), 0o644)
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

func (r *teamRun) origin() string {
	if r.originPath != "" {
		return r.originPath
	}
	return filepath.Join(r.out, "origin.git")
}

func (r *teamRun) spawn(seat, role string, roster []string) {
	dir := filepath.Join(r.out, "seats", seat)
	_ = os.MkdirAll(filepath.Dir(dir), 0o755)
	if err := hgit("", "clone", "-q", r.origin(), dir); err != nil {
		fmt.Fprintln(os.Stderr, "clone:", err)
		return
	}
	for _, kv := range [][]string{{"user.name", seat}, {"user.email", seat + "@example.invalid"}, {"commit.gpgsign", "false"}, {"core.hooksPath", "/dev/null"}} {
		_ = hgit(dir, "config", kv[0], kv[1])
	}
	if r.branch != "main" {
		_ = hgit(dir, "checkout", "-q", "-b", r.branch, "origin/"+r.branch)
	}
	ls := &liveSeat{seat: seat, role: role, joined: time.Now()}
	r.mu.Lock()
	r.live = append(r.live, seat)
	if r.lives == nil {
		r.lives = map[string]*liveSeat{}
	}
	r.lives[seat] = ls
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		sr := SeatResult{Seat: seat, Role: role, JoinedS: time.Since(r.start).Seconds(), CostKnown: true}
		t0 := time.Now()
		session, msg := "", teamPrompt(r.shape, r.branch, role, seat, roster)
		for {
			ls.mu.Lock()
			ls.running = true
			ls.mu.Unlock()
			session = r.turn(&sr, dir, session, msg)
			ls.mu.Lock()
			ls.running, ls.stopped, ls.turns, ls.cost, ls.wakes = false, time.Now(), sr.Turns, sr.CostUSD, sr.Wakes
			retired := ls.retired
			ls.mu.Unlock()
			if session == "" || r.ctx.Err() != nil || sr.Wakes >= r.wakes || retired {
				break
			}
			if msg = r.wakeReason(seat); msg == "" {
				break
			}
			ls.mu.Lock()
			retired = ls.retired
			ls.mu.Unlock()
			if retired {
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
		"REMOTE="+r.origin(), "BRANCH="+r.branch, "MODEL="+r.model, "TURNS="+fmt.Sprint(r.turns), "SWARM_SEAT="+sr.Seat)
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
	res.Receipts = r.receipts
	res.Children = r.children
	for _, c := range r.children {
		// a team of teams is billed as a whole
		res.CostUSD += c.Result.CostUSD
		res.Turns += c.Result.Turns
		res.Seats = append(res.Seats, c.Result.Seats...)
	}
	g := Grade(r.goal, r.origin(), filepath.Join(r.out, "grade"))
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
	if goal == "" {
		own := exec.Command("go", "test", "-count=1", "./...")
		own.Dir = into
		if own.Run() == nil && g.Builds {
			g.Passed = 1
		} else {
			g.Failed = []string{"own test suite"}
		}
		g.Note = "no hidden tests: 1/1 means the team's own suite is green on origin main"
		return g
	}
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
	want, err := plantPart(goal, goal, "", into)
	if err != nil {
		return nil, err
	}
	for dir, part := range parts(goal) {
		more, err := plantPart(goal, part, dir+"/", into)
		if err != nil {
			return nil, err
		}
		want = append(want, more...)
	}
	sort.Strings(want)
	return want, nil
}

// parts reads a composite goal's parts.json: {"kv": "kvlab", ...} means the
// kvlab spec and hidden tests live under kv/ with imports goal/kv/pkg.
func parts(goal string) map[string]string {
	data, err := goals.ReadFile("testdata/" + goal + "/parts.json")
	if err != nil {
		return nil
	}
	var m map[string]string
	_ = json.Unmarshal(data, &m)
	return m
}

// goalSpec is the spec a team is handed: the goal's own SPEC.md, and for a
// composite goal each part's spec after it with its directory named.
func goalSpec(goal string) ([]byte, error) {
	spec, err := goals.ReadFile("testdata/" + goal + "/SPEC.md")
	if err != nil {
		return nil, err
	}
	ps := parts(goal)
	var dirs []string
	for d := range ps {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		part, err := goals.ReadFile("testdata/" + ps[d] + "/SPEC.md")
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(part), "`"+ps[d]+"/", "`"+goal+"/"+d+"/")
		text = strings.ReplaceAll(text, "module named `"+ps[d]+"`", "subsystem `"+d+"/` of module `"+goal+"`")
		spec = append(spec, []byte("\n\n---\n\n# Subsystem `"+d+"/` (spec of "+ps[d]+", packages under `"+d+"/`, imports `"+goal+"/"+d+"/<pkg>`)\n\n"+text)...)
	}
	return spec, nil
}

// plantPart drops one spec's hidden tests into a checkout under dir and
// returns the package-qualified test names the grader expects.
func plantPart(goal, part, dir, into string) ([]string, error) {
	var want []string
	root := "testdata/" + part + "/hidden"
	if _, err := fs.Stat(goals, root); err != nil {
		return nil, nil
	}
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
				want = append(want, goal+"/"+prefix+dir+pkg+".TestHidden"+name[:strings.Index(name, "(")])
			}
		}
		if part != goal || prefix != "" {
			data = bytes.ReplaceAll(data, []byte(`"`+part+`/`), []byte(`"`+goal+`/`+prefix+dir))
		}
		dst := filepath.Join(into, prefix, dir, pkg)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, "zz_hidden_test.go"), data, 0o644)
	})
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
