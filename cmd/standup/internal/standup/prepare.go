package standup

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Preparation reports compiler validity separately from runtime availability.
// Warnings never turn into a launch promise or authorization.
type Preparation struct {
	PlanDigest string      `json:"plan_digest"`
	PlanValid  bool        `json:"plan_valid"`
	Steps      []Step      `json:"steps"`
	Problem    string      `json:"problem,omitempty"`
	Warnings   []string    `json:"warnings"`
	Runtime    Observation `json:"runtime"`
	Delivery   Observation `json:"delivery"`
}

// Prepare checks the same live plan as Apply, with no confirmation or writes.
func Prepare(e Env, cfg Config, r *Record) (Preparation, error) {
	p := Preparation{PlanDigest: r.PlanDigest(), Steps: []Step{}, Warnings: []string{}}
	steps, done, _, err := prepareSteps(e, cfg, r, false)
	if err != nil {
		p.Problem = err.Error()
	}
	if err == nil {
		markStatus(steps, done, true)
		p.Steps, p.PlanValid = steps, true
	}
	p.Runtime = Runtime(e, r)
	p.Delivery = observe(e, e.LeadDir, "watch", "status", "--json")
	scopeWorkers(e, r, &p.Delivery, false)
	p.Warnings = runtimeWarnings(p.Delivery, r)
	if r.Next != "" {
		p.Warnings = append(p.Warnings, "next is recorded only; scheduling the next standup is not implemented")
	}
	return p, err
}

// Observation preserves a tool's output, exit code and collection time. An
// unreadable tool produces an explicit error rather than an empty healthy world.
type Observation struct {
	At       string         `json:"at"`
	Command  []string       `json:"command"`
	ExitCode int            `json:"exit_code"`
	Data     map[string]any `json:"data,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func observe(e Env, dir string, args ...string) Observation {
	o := Observation{At: e.Now().UTC().Format("2006-01-02T15:04:05Z"), Command: append([]string{e.Fleet}, args...)}
	res := e.Run.Run(dir, e.Fleet, args...)
	o.ExitCode = res.Code
	if res.Err != nil {
		o.Error = res.Err.Error()
		o.ExitCode = 4
		return o
	}
	if err := json.Unmarshal([]byte(res.Stdout), &o.Data); err != nil || o.Data == nil {
		o.Error = fmt.Sprintf("unreadable Fleet output (exit %d): %s", res.Code, strings.TrimSpace(res.Stderr+res.Stdout))
		return o
	}
	if res.Code != 0 {
		o.Error = fmt.Sprintf("Fleet exited %d: %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	return o
}

// Runtime returns Fleet's status projection, scoped to this plan's seats and lead.
// Fleet remains the sole interpreter of process activity and watcher health.
func Runtime(e Env, r *Record) Observation {
	o := observe(e, e.LeadDir, "status", "--all", "--json")
	scopeWorkers(e, r, &o, true)
	return o
}

func scopeWorkers(e Env, r *Record, o *Observation, tenantRequired bool) {
	workers, ok := o.Data["workers"].([]any)
	if !ok && o.Error == "" {
		o.Error = "Fleet status has no workers array"
	}
	seats, err := e.Seats(r.Tenant)
	if err != nil {
		o.Error = err.Error()
	}
	wanted := map[string]string{r.Lead: e.LeadDir}
	for _, c := range r.Cards {
		if c.Seat != "" {
			wanted[c.Seat] = seats[c.Seat]
		}
	}
	keep := []any{}
	for _, v := range workers {
		row, ok := v.(map[string]any)
		if !ok || wanted[str(row, "address")] == "" || wanted[str(row, "address")] != str(row, "cwd") {
			continue
		}
		if tenantRequired && str(row, "tenant") != r.Tenant {
			continue
		}
		keep = append(keep, row)
	}
	if o.Data != nil {
		o.Data["workers"] = keep
	}
}

func runtimeWarnings(o Observation, r *Record) []string {
	warnings := []string{}
	if o.Error != "" {
		return append(warnings, "runtime availability unknown: "+o.Error)
	}
	if problem := str(o.Data, "configuration_error"); problem != "" {
		warnings = append(warnings, problem)
	}
	if str(o.Data, "watcher") != "running" {
		warnings = append(warnings, "watcher is "+str(o.Data, "watcher")+"; automatic delivery is not assured")
	}
	for _, c := range r.Cards {
		if c.Seat == "" {
			warnings = append(warnings, c.ID+": assignment only; no seat mail will be sent")
			continue
		}
		warnings = append(warnings, seatWarning(o, c)...)
	}
	return warnings
}

func seatWarning(o Observation, c Card) []string {
	workers, _ := o.Data["workers"].([]any)
	for _, v := range workers {
		row, _ := v.(map[string]any)
		if str(row, "address") != c.Seat {
			continue
		}
		for _, key := range []string{"configuration_error", "binding_error", "error"} {
			if problem := str(row, key); problem != "" {
				return []string{c.ID + ": " + problem}
			}
		}
		if paused, _ := row["starts_paused"].(bool); paused {
			return []string{c.ID + ": starts are paused for " + c.Seat}
		}
		return nil
	}
	return []string{c.ID + ": no runtime target observed for " + c.Seat + "; mail may wait"}
}

// CardStatus keeps receipt evidence distinct from apply and worker activity.
type CardStatus struct {
	ID      string      `json:"id"`
	State   string      `json:"receipt_state"`
	Receipt Observation `json:"receipt"`
}

// Status is a read-only view, not a second work ledger.
type Status struct {
	PlanDigest string       `json:"plan_digest"`
	Confirmed  bool         `json:"confirmed"`
	Applied    []Applied    `json:"applied"`
	Cards      []CardStatus `json:"cards"`
	Runtime    Observation  `json:"runtime"`
}

// ReadStatus asks Fleet for each card's required receipt at its resolved revision.
func ReadStatus(e Env, cfg Config, r *Record) (Status, error) {
	s := Status{PlanDigest: r.PlanDigest(), Confirmed: checkConfirm(cfg, r) == nil, Applied: r.Applied, Cards: []CardStatus{}, Runtime: Runtime(e, r)}
	seats, err := e.Seats(cfg.Tenant)
	if err != nil {
		return s, err
	}
	world := &World{Seats: seats}
	for _, c := range r.Cards {
		s.Cards = append(s.Cards, cardStatus(e, c, world))
	}
	return s, nil
}

func cardStatus(e Env, c Card, w *World) CardStatus {
	s := CardStatus{ID: c.ID, State: "unknown"}
	dir, err := cardDir(e, c, w)
	if err != nil {
		s.Receipt.Error = err.Error()
		return s
	}
	s.Receipt = observe(e, dir, "done", c.Change, "--kind", c.As, "--json")
	if s.Receipt.Data == nil {
		return s
	}
	// Exit 1 is missing evidence; 3 is a failing receipt. Preserve the original
	// result in the observation and never infer completion from worker exits.
	switch s.Receipt.ExitCode {
	case 0:
		if ok, _ := s.Receipt.Data["ok"].(bool); ok && str(s.Receipt.Data, "sha") != "" {
			s.State = "complete"
		}
	case 1:
		s.State = "pending"
	case 3:
		s.State = "failed"
	}
	return s
}
