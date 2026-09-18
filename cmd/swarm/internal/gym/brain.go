package gym

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The brain is the judgment layer above a team. Every interval it reads
// what the store and git say, hands that to a model with a fixed menu of
// actions, and applies what comes back: add a seat, retire one, form a
// team, disband one, nudge, or escalate to a person. It never writes code
// and it never talks to a seat except by note. Every judgment is recorded
// on the store with the telemetry it saw, so a decision can be audited
// against what was true when it was made.

// liveSeat is one seat as the harness sees it right now.
type liveSeat struct {
	mu       sync.Mutex
	seat     string
	role     string
	joined   time.Time
	turns    int
	cost     float64
	wakes    int
	running  bool
	stopped  time.Time
	retired  bool
	proc     *exec.Cmd
	lastLand time.Time
}

// SeatTelemetry is what the brain is told about one seat.
type SeatTelemetry struct {
	Seat      string   `json:"seat"`
	Role      string   `json:"role"`
	Running   bool     `json:"running"`
	Retired   bool     `json:"retired,omitempty"`
	AgeS      float64  `json:"age_s"`
	Turns     int      `json:"turns"`
	CostUSD   float64  `json:"cost_usd"`
	Wakes     int      `json:"wakes"`
	Holds     []string `json:"holds,omitempty"`
	IdleS     float64  `json:"idle_s,omitempty"`
	LandedAgo float64  `json:"last_landing_ago_s,omitempty"`
	Landings  int      `json:"landings"`
}

// Telemetry is one snapshot the brain judges from.
type Telemetry struct {
	At          float64          `json:"elapsed_s"`
	WallCapS    float64          `json:"wall_cap_s"`
	CostUSD     float64          `json:"cost_usd"`
	CostCapUSD  float64          `json:"cost_cap_usd,omitempty"`
	Seats       []SeatTelemetry  `json:"seats"`
	Units       map[string]int   `json:"units"`
	StaleUnits  []string         `json:"stale_units,omitempty"`
	Teams       []ChildTelemetry `json:"teams,omitempty"`
	RedLandings int              `json:"red_landings"`
	Landings    int              `json:"landings"`
	OpenAsks    int              `json:"open_questions"`
	Recent      []BrainDecision  `json:"recent_decisions,omitempty"`
}

// ChildTelemetry is one child team as the parent brain sees it.
type ChildTelemetry struct {
	Unit    string  `json:"unit"`
	Seats   int     `json:"seats"`
	CostUSD float64 `json:"cost_usd"`
	AgeS    float64 `json:"age_s"`
	Done    bool    `json:"done"`
}

// BrainAction is one thing the brain may do.
type BrainAction struct {
	Action string   `json:"action"` // hold | add_seat | retire_seat | form_team | disband_team | nudge | escalate
	Target string   `json:"target,omitempty"`
	Brief  string   `json:"brief,omitempty"`
	Files  []string `json:"files,omitempty"`
	Why    string   `json:"why"`
}

// BrainDecision is one judgment, as recorded.
type BrainDecision struct {
	At         float64       `json:"elapsed_s"`
	Assessment string        `json:"assessment"`
	Actions    []BrainAction `json:"actions"`
	Applied    []string      `json:"applied,omitempty"`
	CostUSD    float64       `json:"brain_cost_usd"`
}

const brainPolicy = `You are the brain of a team of coding agents. You are not a manager and you do not write code. You read telemetry and decide, sparingly, whether the team's shape should change. Seats cost money every minute they run; an idle seat waiting on others costs nothing. The work list is the load signal: open units are unclaimed work, claimed units are in progress, done units are finished.

Actions you may return, each with a one-sentence why:
- hold: nothing to change (the usual answer).
- add_seat: one more peer joins and claims from the list. Use when open units outnumber idle seats for more than one interval and the wall and cost caps allow it.
- retire_seat (target: seat): stop resuming that seat. Use for a seat that has been stopped a long time with nothing to do, or that keeps waking without landing anything, or when the list is nearly drained and seats outnumber remaining units.
- form_team (target: a short id, brief, files: the directories it owns): a part of the goal large enough to need a team of its own. Only when the goal is clearly too large for the seats present and the part is separable by directory. Never for a directory a team already owns.
- disband_team (target: unit id): stop a child team that is stuck, red, or duplicating work.
- nudge (target: seat, why is the message): a note to one seat. Use for a seat holding a unit far longer than its peers take, or holding two units while others idle.
- escalate (why): something a person must decide: cost about to exceed the cap with the goal far from done, a red landing nobody fixes, a question open for a long time. Escalation stops nothing; it is a flag.

Rules: prefer hold. At most two actions per judgment. Never add a seat and retire one in the same judgment. Do not add seats when fewer than two units are open. Do not retire a running seat that holds a unit. Do not form a team on a goal that has fewer than eight open units. Say what you see in one or two sentences, then the actions.

Reply with one JSON object and nothing else: {"assessment": "...", "actions": [{"action": "...", "target": "...", "brief": "...", "files": ["..."], "why": "..."}]}`

// brain is the controller loop.
func (r *teamRun) brain() {
	defer r.wg.Done()
	for r.ctx.Err() == nil {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(r.brainEvery):
		}
		if r.finished() {
			return
		}
		tel := r.telemetry()
		dec, err := r.judge(tel)
		if err != nil {
			fmt.Printf("[%4.0fs] brain: %v\n", tel.At, err)
			continue
		}
		dec.Applied = r.apply(dec.Actions)
		r.mu.Lock()
		r.decisions = append(r.decisions, dec)
		r.brainCost += dec.CostUSD
		r.mu.Unlock()
		r.record(tel, dec)
		acts := "hold"
		if len(dec.Applied) > 0 {
			acts = strings.Join(dec.Applied, "; ")
		}
		fmt.Printf("[%4.0fs] brain: %s -> %s\n", tel.At, tailLine(dec.Assessment), acts)
	}
}

// telemetry gathers the snapshot from the harness, the store and git.
func (r *teamRun) telemetry() Telemetry {
	now := time.Now()
	tel := Telemetry{At: time.Since(r.start).Seconds(), WallCapS: r.wall.Seconds(), CostCapUSD: r.costCap, Units: map[string]int{}}
	holds := map[string][]string{}
	for _, w := range r.workList() {
		tel.Units[w.State]++
		if w.State == "claimed" {
			holds[w.Holder] = append(holds[w.Holder], w.ID)
		}
	}
	r.mu.Lock()
	seats := make([]*liveSeat, 0, len(r.lives))
	for _, s := range r.lives {
		seats = append(seats, s)
	}
	for _, rc := range r.receipts {
		tel.Landings++
		if !rc.Green {
			tel.RedLandings++
		}
	}
	landings := map[string]int{}
	for _, rc := range r.receipts {
		landings[rc.Author]++
	}
	for _, c := range r.children {
		tel.Teams = append(tel.Teams, ChildTelemetry{Unit: c.Unit, Seats: len(c.Result.Seats), CostUSD: c.Result.CostUSD, AgeS: c.Result.WallS, Done: true})
	}
	for id, ch := range r.childRuns {
		ch.mu.Lock()
		cost := 0.0
		for _, s := range ch.lives {
			cost += s.cost
		}
		tel.Teams = append(tel.Teams, ChildTelemetry{Unit: id, Seats: len(ch.lives), CostUSD: cost, AgeS: time.Since(ch.start).Seconds()})
		ch.mu.Unlock()
	}
	recent := r.decisions
	if len(recent) > 3 {
		recent = recent[len(recent)-3:]
	}
	tel.Recent = append([]BrainDecision{}, recent...)
	tel.CostUSD = r.brainCost
	r.mu.Unlock()
	sort.Slice(seats, func(i, j int) bool { return seats[i].seat < seats[j].seat })
	for _, s := range seats {
		s.mu.Lock()
		st := SeatTelemetry{Seat: s.seat, Role: s.role, Running: s.running, Retired: s.retired, AgeS: now.Sub(s.joined).Seconds(),
			Turns: s.turns, CostUSD: s.cost, Wakes: s.wakes, Holds: holds[s.seat], Landings: landings[s.seat]}
		if !s.running && !s.stopped.IsZero() {
			st.IdleS = now.Sub(s.stopped).Seconds()
		}
		if !s.lastLand.IsZero() {
			st.LandedAgo = now.Sub(s.lastLand).Seconds()
		}
		tel.CostUSD += s.cost
		s.mu.Unlock()
		tel.Seats = append(tel.Seats, st)
	}
	for _, t := range tel.Teams {
		tel.CostUSD += t.CostUSD
	}
	tel.OpenAsks = r.countOpenRequests()
	return tel
}

func (r *teamRun) countOpenRequests() int {
	cmd := exec.Command(r.self, "requests", "--json", "--as", "harness")
	cmd.Dir, cmd.Env = filepath.Join(r.out, "seed"), r.env
	data, err := cmd.Output()
	if err != nil {
		return 0
	}
	var rows []json.RawMessage
	_ = json.Unmarshal(data, &rows)
	return len(rows)
}

// judge asks the model. One turn, no tools, JSON out.
func (r *teamRun) judge(tel Telemetry) (BrainDecision, error) {
	snapshot, _ := json.MarshalIndent(tel, "", " ")
	prompt := brainPolicy + "\n\nTelemetry now:\n" + string(snapshot)
	ctx, cancel := context.WithTimeout(r.ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "json", "--max-turns", "1", "--model", r.brainModel, "--allowedTools", "")
	cmd.Dir = filepath.Join(r.out, "seed")
	cmd.Env = append(append([]string{}, r.env...), "SWARM_SEAT=brain")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		return BrainDecision{}, fmt.Errorf("model: %w", err)
	}
	var res struct {
		Result string  `json:"result"`
		Cost   float64 `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(lastJSONLine(stdout.Bytes()), &res); err != nil {
		return BrainDecision{}, fmt.Errorf("model output: %w", err)
	}
	dec := BrainDecision{At: tel.At, CostUSD: res.Cost}
	body := res.Result
	if i := strings.Index(body, "{"); i >= 0 {
		body = body[i:]
	}
	if j := strings.LastIndex(body, "}"); j >= 0 {
		body = body[:j+1]
	}
	if err := json.Unmarshal([]byte(body), &dec); err != nil {
		return BrainDecision{}, fmt.Errorf("model reply is not the schema: %w: %s", err, tailLine(res.Result))
	}
	return dec, nil
}

// apply carries out the actions the policy allows and reports what it did.
func (r *teamRun) apply(actions []BrainAction) []string {
	var applied []string
	for i, a := range actions {
		if i >= 2 {
			break
		}
		switch a.Action {
		case "add_seat":
			r.mu.Lock()
			n := len(r.live) + 1
			roster := append([]string{}, r.live...)
			r.mu.Unlock()
			seat := fmt.Sprintf("p%d", n)
			r.spawn(seat, "joiner", roster)
			applied = append(applied, "add_seat "+seat)
		case "retire_seat":
			if r.retire(a.Target) {
				applied = append(applied, "retire_seat "+a.Target)
			}
		case "form_team":
			if r.shape != "teams" && r.shape != "brain" {
				continue
			}
			if err := r.swarm("brain", "work", "add", a.Target, "--title", tailLine(a.Brief), "--files", strings.Join(a.Files, ","), "--team", "--brief", a.Brief); err == nil {
				applied = append(applied, "form_team "+a.Target)
			}
		case "disband_team":
			r.mu.Lock()
			ch := r.childRuns[a.Target]
			r.mu.Unlock()
			if ch != nil && ch.cancel != nil {
				ch.cancel()
				applied = append(applied, "disband_team "+a.Target)
			}
		case "nudge":
			if a.Target != "" && r.swarm("brain", "nudge", a.Target, a.Why) == nil {
				applied = append(applied, "nudge "+a.Target)
			}
		case "escalate":
			_ = os.WriteFile(filepath.Join(r.out, "ESCALATION.md"), []byte(fmt.Sprintf("%s\n\n%s\n", time.Now().UTC().Format(time.RFC3339), a.Why)), 0o644)
			fmt.Printf("ESCALATION: %s\n", a.Why)
			applied = append(applied, "escalate")
		}
	}
	return applied
}

// retire stops resuming a seat, and stops it now if it is between turns.
// A seat in the middle of a turn finishes that turn; nothing preempts it.
func (r *teamRun) retire(seat string) bool {
	r.mu.Lock()
	s := r.lives[seat]
	r.mu.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired {
		return false
	}
	s.retired = true
	return true
}

// record leaves the judgment and the telemetry it saw on the store and in
// the run directory.
func (r *teamRun) record(tel Telemetry, dec BrainDecision) {
	entry, _ := json.Marshal(map[string]any{"telemetry": tel, "decision": dec})
	f, err := os.OpenFile(filepath.Join(r.out, "brain.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.Write(append(entry, '\n'))
		_ = f.Close()
	}
	_ = r.swarm("brain", "journal", "brain", fmt.Sprintf("%06.0f", dec.At), "--payload", string(entry))
}

// metrics serves the same telemetry in Prometheus text format so a run can
// be scraped and graphed.
func (r *teamRun) metrics(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		tel := r.telemetry()
		var sb strings.Builder
		fmt.Fprintf(&sb, "swarm_elapsed_seconds %.0f\nswarm_cost_usd %.4f\nswarm_landings_total %d\nswarm_red_landings_total %d\nswarm_open_questions %d\n", tel.At, tel.CostUSD, tel.Landings, tel.RedLandings, tel.OpenAsks)
		for state, n := range tel.Units {
			fmt.Fprintf(&sb, "swarm_units{state=%q} %d\n", state, n)
		}
		for _, s := range tel.Seats {
			running := 0
			if s.Running {
				running = 1
			}
			fmt.Fprintf(&sb, "swarm_seat_running{seat=%q,role=%q} %d\nswarm_seat_turns{seat=%q} %d\nswarm_seat_cost_usd{seat=%q} %.4f\nswarm_seat_wakes{seat=%q} %d\nswarm_seat_holds{seat=%q} %d\n",
				s.Seat, s.Role, running, s.Seat, s.Turns, s.Seat, s.CostUSD, s.Seat, s.Wakes, s.Seat, len(s.Holds))
		}
		r.mu.Lock()
		counts := map[string]int{}
		for _, d := range r.decisions {
			for _, a := range d.Applied {
				counts[strings.Fields(a)[0]]++
			}
		}
		r.mu.Unlock()
		for action, n := range counts {
			fmt.Fprintf(&sb, "swarm_brain_actions_total{action=%q} %d\n", action, n)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(sb.String()))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-r.ctx.Done()
		_ = srv.Close()
	}()
	_ = srv.ListenAndServe()
}
