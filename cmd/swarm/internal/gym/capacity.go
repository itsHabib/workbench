package gym

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The capacity test asks how many things one agent can keep straight at
// once. An agent's scarce resource is context, not compute, so the load is
// threads: independent pieces of state it must track while messages from
// all of them arrive interleaved. Each thread is a tiny ledger (SET, ADD,
// SUB, MOVE between its two keys) with ASKs scattered through it. An ASK's
// right answer is the value at that moment, which the grader computes; the
// agent only ever sees the messages. Threads are doubled until answers go
// wrong or go missing. Run twice: with nothing but its own context, and
// with a notes file it may keep. The difference is what externalized state
// is worth.

type mailMsg struct {
	Thread string `json:"thread"`
	Op     string `json:"op"` // SET ADD SUB MOVE ASK
	Key    string `json:"key"`
	To     string `json:"to,omitempty"`
	N      int    `json:"n,omitempty"`
	Want   int    `json:"want,omitempty"` // for ASK: the right answer, never shown
}

type mailReply struct {
	Thread string `json:"thread"`
	Key    string `json:"key"`
	Value  string `json:"value"`
	AtMsg  int    `json:"at_msg"` // how many messages had been handed out when the reply came
}

type mailbox struct {
	Threads int         `json:"threads"`
	Batch   int         `json:"batch"`
	Msgs    []mailMsg   `json:"msgs"`
	Cursor  int         `json:"cursor"`
	Replies []mailReply `json:"replies"`
}

var nouns = []string{"fuel", "crates", "bolts", "seats", "tokens", "pages", "volts", "rounds", "sheets", "liters", "spools", "racks"}

// generate builds a deterministic interleaved stream: every thread gets the
// same number of updates and asks, so load differs only in thread count.
func generate(threads, perThread int, seed int64) []mailMsg {
	rng := rand.New(rand.NewSource(seed))
	type st struct {
		keys [2]string
		val  map[string]int
		left int
	}
	ts := make([]*st, threads)
	for i := range ts {
		a := rng.Intn(len(nouns))
		b := (a + 1 + rng.Intn(len(nouns)-1)) % len(nouns)
		ts[i] = &st{keys: [2]string{nouns[a], nouns[b]}, val: map[string]int{}, left: perThread}
	}
	var out []mailMsg
	for {
		var live []int
		for i, t := range ts {
			if t.left > 0 {
				live = append(live, i)
			}
		}
		if len(live) == 0 {
			return out
		}
		i := live[rng.Intn(len(live))]
		t := ts[i]
		name := "t" + strconv.Itoa(i+1)
		k := t.keys[rng.Intn(2)]
		m := mailMsg{Thread: name, Key: k}
		first := t.left == perThread
		_, known := t.val[k]
		// The last message of every thread is an ASK, so each thread ends on a
		// question whose answer depends on its whole history. It is decided
		// before anything is applied: an update the agent is never shown must
		// never reach the answer key.
		switch r := rng.Intn(10); {
		case t.left == 1 && known:
			m.Op, m.Want = "ASK", t.val[k]
		case first || !known:
			m.Op, m.N = "SET", 10+rng.Intn(90)
			t.val[k] = m.N
		case r < 3 && t.left < perThread-1:
			m.Op, m.Want = "ASK", t.val[k]
		case r < 5:
			m.Op, m.N = "ADD", 1+rng.Intn(30)
			t.val[k] += m.N
		case r < 7:
			m.Op, m.N = "SUB", 1+rng.Intn(20)
			t.val[k] -= m.N
		case r < 8:
			other := t.keys[0]
			if other == k {
				other = t.keys[1]
			}
			m.Op, m.To, m.N = "MOVE", other, 1+rng.Intn(15)
			t.val[k] -= m.N
			t.val[other] += m.N
		default:
			m.Op, m.N = "SET", 10+rng.Intn(90)
			t.val[k] = m.N
		}
		t.left--
		out = append(out, m)
	}
}

func (m mailMsg) line() string {
	switch m.Op {
	case "ASK":
		return fmt.Sprintf("[%s] ASK %s        -> reply %s.%s=<number>", m.Thread, m.Key, m.Thread, m.Key)
	case "MOVE":
		return fmt.Sprintf("[%s] MOVE %d from %s to %s", m.Thread, m.N, m.Key, m.To)
	}
	return fmt.Sprintf("[%s] %s %s %d", m.Thread, m.Op, m.Key, m.N)
}

func loadBox() (*mailbox, string, error) {
	path := os.Getenv("SWARM_GYM_MAIL")
	if path == "" {
		return nil, "", fmt.Errorf("no mailbox: SWARM_GYM_MAIL is not set")
	}
	var b mailbox
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return &b, path, json.Unmarshal(data, &b)
}

func saveBox(path string, b *mailbox) error {
	data, _ := json.Marshal(b)
	return os.WriteFile(path, data, 0o644)
}

// mailMain is what the agent under test calls: `swarm gym mail next` and
// `swarm gym mail reply t3.fuel=40 t1.crates=12`.
func mailMain(args []string) int {
	b, path, err := loadBox()
	if err != nil || len(args) == 0 {
		fmt.Fprintln(os.Stderr, "mail:", err)
		return 3
	}
	switch args[0] {
	case "next":
		if b.Cursor >= len(b.Msgs) {
			fmt.Println("END: no more messages. You are done.")
			return 0
		}
		// A batch ends at its first ASK, so "the current value" has one
		// meaning: nothing the agent has been shown comes after the question.
		end := min(b.Cursor+b.Batch, len(b.Msgs))
		for i := b.Cursor; i < end; i++ {
			if b.Msgs[i].Op == "ASK" {
				end = i + 1
				break
			}
		}
		for _, m := range b.Msgs[b.Cursor:end] {
			fmt.Println(m.line())
		}
		b.Cursor = end
		fmt.Printf("(%d of %d messages delivered)\n", b.Cursor, len(b.Msgs))
	case "reply":
		for _, a := range args[1:] {
			lhs, val, ok := strings.Cut(a, "=")
			thread, key, ok2 := strings.Cut(lhs, ".")
			if !ok || !ok2 {
				fmt.Printf("ignored %q: use thread.key=number\n", a)
				continue
			}
			b.Replies = append(b.Replies, mailReply{Thread: thread, Key: key, Value: strings.TrimSpace(val), AtMsg: b.Cursor})
		}
		fmt.Printf("recorded %d\n", len(args)-1)
	default:
		return 3
	}
	if err := saveBox(path, b); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	return 0
}

// CapRow is one capacity run, graded.
type CapRow struct {
	At        time.Time `json:"at"`
	Model     string    `json:"model"`
	Threads   int       `json:"threads"`
	Notes     bool      `json:"notes"`
	Rep       int       `json:"rep"`
	Messages  int       `json:"messages"`
	Asks      int       `json:"asks"`
	Correct   int       `json:"correct"`
	Wrong     int       `json:"wrong"`
	Dropped   int       `json:"dropped"` // asked and never answered
	Delivered int       `json:"delivered"`
	Turns     int       `json:"turns"`
	CostUSD   float64   `json:"cost_usd"`
	WallS     float64   `json:"wall_s"`
	Killed    bool      `json:"killed,omitempty"`
}

// gradeBox matches each ASK with the first reply for its thread and key
// that arrived after it was delivered and before the same question was
// asked again.
func gradeBox(b *mailbox) (asks, correct, wrong, dropped int) {
	used := map[int]bool{}
	for i, m := range b.Msgs {
		if m.Op != "ASK" {
			continue
		}
		asks++
		if i >= b.Cursor {
			dropped++
			continue
		}
		next := len(b.Msgs) + 1
		for j := i + 1; j < len(b.Msgs); j++ {
			if b.Msgs[j].Op == "ASK" && b.Msgs[j].Thread == m.Thread && b.Msgs[j].Key == m.Key {
				next = j
				break
			}
		}
		found := -1
		for ri, r := range b.Replies {
			if !used[ri] && r.Thread == m.Thread && r.Key == m.Key && r.AtMsg > i && r.AtMsg <= next+b.Batch {
				found = ri
				break
			}
		}
		switch {
		case found < 0:
			dropped++
		case b.Replies[found].Value == strconv.Itoa(m.Want):
			used[found] = true
			correct++
		default:
			used[found] = true
			wrong++
		}
	}
	return
}

const capPrompt = `You are tracking several independent threads at once. Messages from all of them arrive
interleaved. Each thread (t1, t2, ...) has its own named quantities.

  SET k n     the quantity k in that thread is now n
  ADD k n     add n to it          SUB k n     subtract n from it
  MOVE n from a to b               take n from a and add it to b, in that thread
  ASK k       report k's current value in that thread

Run:  swarm gym mail next     to receive the next few messages.
For every ASK you receive, answer before asking for more messages:
      swarm gym mail reply t3.fuel=42 t1.crates=17      (several answers in one call is fine)
Quantities with the same name in different threads are unrelated. Keep going until next prints END.
One shell command per tool call.
`

func capacityCmd(args []string) int {
	fs := flag.NewFlagSet("gym capacity", flag.ContinueOnError)
	model := fs.String("model", "claude-sonnet-5", "the one model under test; this is not a model comparison")
	threadsFlag := fs.String("threads", "2,4,8,16", "thread counts")
	per := fs.Int("per-thread", 10, "messages per thread")
	batch := fs.Int("batch", 5, "messages handed out per next")
	notes := fs.String("notes", "both", "no | yes | both: may the agent keep a notes file")
	reps := fs.Int("reps", 1, "runs per cell, each with its own seed")
	parallel := fs.Int("parallel", 4, "runs at once")
	out := fs.String("out", "", "results directory")
	if fs.Parse(args) != nil || *out == "" {
		return 3
	}
	_ = os.MkdirAll(filepath.Join(*out, "logs"), 0o755)
	self, _ := os.Executable()
	env := childEnv(filepath.Dir(self), filepath.Join(*out, "fleet-state"))
	var modes []bool
	if *notes == "no" || *notes == "both" {
		modes = append(modes, false)
	}
	if *notes == "yes" || *notes == "both" {
		modes = append(modes, true)
	}
	type job struct {
		threads, rep int
		notes        bool
	}
	var jobs []job
	for _, t := range strings.Split(*threadsFlag, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			continue
		}
		for _, nm := range modes {
			for r := 1; r <= *reps; r++ {
				jobs = append(jobs, job{n, r, nm})
			}
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, *parallel)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			row := capacityRun(*model, j.threads, *per, *batch, j.notes, j.rep, *out, env)
			mu.Lock()
			defer mu.Unlock()
			appendAny(filepath.Join(*out, "capacity.jsonl"), row)
			fmt.Printf("threads=%-3d notes=%-5v rep%d  asks=%-3d correct=%-3d wrong=%-3d dropped=%-3d turns=%-3d $%.2f %4.0fs\n",
				row.Threads, row.Notes, row.Rep, row.Asks, row.Correct, row.Wrong, row.Dropped, row.Turns, row.CostUSD, row.WallS)
		}(j)
	}
	wg.Wait()
	fmt.Print("\n" + CapacityTable(filepath.Join(*out, "capacity.jsonl")))
	return 0
}

func capacityRun(model string, threads, per, batch int, notes bool, rep int, out string, env []string) CapRow {
	row := CapRow{At: time.Now().UTC(), Model: model, Threads: threads, Notes: notes, Rep: rep}
	dir, err := os.MkdirTemp("", "gym-cap-")
	if err != nil {
		return row
	}
	defer os.RemoveAll(dir)
	box := &mailbox{Threads: threads, Batch: batch, Msgs: generate(threads, per, int64(threads*1000+rep))}
	row.Messages = len(box.Msgs)
	// The mailbox lives outside the agent's directory: it holds the answers.
	boxDir, _ := os.MkdirTemp("", "gym-box-")
	defer os.RemoveAll(boxDir)
	boxPath := filepath.Join(boxDir, "box.json")
	_ = saveBox(boxPath, box)

	prompt, tools := capPrompt, "Bash(swarm:*)"
	if notes {
		prompt += "You may keep notes in notes.md in this directory, and read them back, however you like.\n"
		tools = "Bash(swarm:*),Read,Write,Edit"
	} else {
		prompt += "You have no files and no scratch space. Keep everything in your head.\n"
	}
	wall := time.Duration(120+threads*per*6) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), wall)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "json", "--permission-mode", "acceptEdits",
		"--max-turns", strconv.Itoa(40+threads*per*2), "--model", model, "--allowedTools", tools)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, env...), "SWARM_GYM_MAIL="+boxPath)
	start := time.Now()
	stdout, _ := cmd.Output()
	row.WallS = time.Since(start).Seconds()
	row.Killed = ctx.Err() != nil
	var res struct {
		NumTurns int     `json:"num_turns"`
		Cost     float64 `json:"total_cost_usd"`
	}
	if json.Unmarshal(stdout, &res) == nil {
		row.Turns, row.CostUSD = res.NumTurns, res.Cost
	}
	_ = os.WriteFile(filepath.Join(out, "logs", fmt.Sprintf("cap-t%d-notes%v-rep%d.out.json", threads, notes, rep)), stdout, 0o644)
	var final mailbox
	if data, err := os.ReadFile(boxPath); err == nil && json.Unmarshal(data, &final) == nil {
		row.Delivered = final.Cursor
		row.Asks, row.Correct, row.Wrong, row.Dropped = gradeBox(&final)
		_ = os.WriteFile(filepath.Join(out, "logs", fmt.Sprintf("cap-t%d-notes%v-rep%d.box.json", threads, notes, rep)), data, 0o644)
	}
	return row
}

func appendAny(path string, v any) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(v)
	_, _ = f.Write(append(data, '\n'))
}

// CapacityTable renders accuracy by thread count, with and without notes.
func CapacityTable(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	type cell struct {
		asks, correct, wrong, dropped, n int
		cost                             float64
	}
	cells := map[string]*cell{}
	threadSet := map[int]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r CapRow
		if json.Unmarshal([]byte(line), &r) != nil || r.Threads == 0 {
			continue
		}
		k := fmt.Sprintf("%d|%v", r.Threads, r.Notes)
		c := cells[k]
		if c == nil {
			c = &cell{}
			cells[k] = c
		}
		c.asks, c.correct, c.wrong, c.dropped, c.n, c.cost = c.asks+r.Asks, c.correct+r.Correct, c.wrong+r.Wrong, c.dropped+r.Dropped, c.n+1, c.cost+r.CostUSD
		threadSet[r.Threads] = true
	}
	var ts []int
	for t := range threadSet {
		ts = append(ts, t)
	}
	sort.Ints(ts)
	var sb strings.Builder
	sb.WriteString("| threads | in its head: correct / wrong / dropped | with notes: correct / wrong / dropped |\n|---|---|---|\n")
	show := func(c *cell) string {
		if c == nil || c.asks == 0 {
			return "-"
		}
		return fmt.Sprintf("%d%% · %d / %d / %d of %d asks · %d runs · $%.2f", 100*c.correct/c.asks, c.correct, c.wrong, c.dropped, c.asks, c.n, c.cost)
	}
	for _, t := range ts {
		fmt.Fprintf(&sb, "| %d | %s | %s |\n", t, show(cells[fmt.Sprintf("%d|false", t)]), show(cells[fmt.Sprintf("%d|true", t)]))
	}
	return sb.String()
}
