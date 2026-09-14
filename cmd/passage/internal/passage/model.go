// Package passage owns local phase contracts and their evidence-bound handoffs.
package passage

import (
	"fmt"
	"reflect"
	"strings"
)

// Contract is frozen when work starts. Phases are selected by the work's owner.
type Contract struct {
	Name   string  `json:"name"`
	Phases []Phase `json:"phases"`
}

// Phase specifies inputs and required judgments, not how work must be performed.
type Phase struct {
	Name     string   `json:"name"`
	Brief    string   `json:"brief"`
	Inputs   []string `json:"inputs"`
	Git      bool     `json:"git"`
	Requires []string `json:"requires"`
}

// Snapshot binds evidence to file bytes and, where requested, a clean Git head.
type Snapshot struct {
	Files map[string]string `json:"files"`
	Head  string            `json:"head,omitempty"`
}

// Receipt is an attributed judgment with retained evidence bytes.
type Receipt struct {
	Requirement string   `json:"requirement"`
	Verdict     string   `json:"verdict"`
	Subject     Snapshot `json:"subject"`
	Evidence    string   `json:"evidence"`
	SHA256      string   `json:"sha256"`
	Content     string   `json:"content"`
}

// Event retains who changed the work and the exact evidence consumed.
type Event struct {
	Action   string    `json:"action"`
	Phase    int       `json:"phase"`
	By       string    `json:"by"`
	Note     string    `json:"note"`
	At       string    `json:"at"`
	Receipt  *Receipt  `json:"receipt,omitempty"`
	Accepted []Receipt `json:"accepted,omitempty"`
}

// Record is a single atomically published work record. History is never trimmed.
type Record struct {
	Version  int      `json:"version"`
	Root     string   `json:"root"`
	Contract Contract `json:"contract"`
	Events   []Event  `json:"events"`
}

type cursor struct {
	phase     int
	receipts  map[string]Receipt
	completed []Event
}

func validateContract(c Contract) error {
	if strings.TrimSpace(c.Name) == "" || len(c.Phases) == 0 {
		return fmt.Errorf("contract needs a name and at least one phase")
	}
	names := map[string]bool{}
	for _, p := range c.Phases {
		if names[p.Name] {
			return fmt.Errorf("duplicate phase %q", p.Name)
		}
		names[p.Name] = true
		if err := validatePhase(p); err != nil {
			return err
		}
	}
	return nil
}

func validatePhase(p Phase) error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Brief) == "" || len(p.Requires) == 0 {
		return fmt.Errorf("phase needs name, brief and requirements")
	}
	if !p.Git && len(p.Inputs) == 0 {
		return fmt.Errorf("phase %s needs file inputs or git", p.Name)
	}
	seen := map[string]bool{}
	for _, name := range p.Requires {
		if strings.TrimSpace(name) == "" || seen[name] {
			return fmt.Errorf("empty or duplicate requirement in %s", p.Name)
		}
		seen[name] = true
	}
	return nil
}

func replay(r Record) (cursor, error) {
	c := cursor{receipts: map[string]Receipt{}}
	if r.Version != 1 {
		return c, fmt.Errorf("unsupported record version %d", r.Version)
	}
	if err := validateContract(r.Contract); err != nil {
		return c, err
	}
	for i, e := range r.Events {
		if err := applyEvent(r.Contract, &c, e); err != nil {
			return c, fmt.Errorf("event %d: %w", i+1, err)
		}
	}
	return c, nil
}

func applyEvent(contract Contract, c *cursor, e Event) error {
	if strings.TrimSpace(e.By) == "" || strings.TrimSpace(e.Note) == "" || e.At == "" {
		return fmt.Errorf("event needs attribution, note and time")
	}
	if e.Action == "reopen" {
		return reopen(contract, c, e)
	}
	if e.Phase != c.phase || c.phase >= len(contract.Phases) {
		return fmt.Errorf("unexpected phase")
	}
	p := contract.Phases[c.phase]
	switch e.Action {
	case "record":
		if e.Receipt == nil || len(e.Accepted) != 0 {
			return fmt.Errorf("invalid receipt event")
		}
		if err := validateReceipt(p, *e.Receipt); err != nil {
			return err
		}
		c.receipts[e.Receipt.Requirement] = *e.Receipt
	case "advance":
		if e.Receipt != nil || len(e.Accepted) != len(p.Requires) {
			return fmt.Errorf("incomplete admission")
		}
		for i, name := range p.Requires {
			got, ok := c.receipts[name]
			if !ok || got.Verdict != "pass" || !reflect.DeepEqual(got, e.Accepted[i]) {
				return fmt.Errorf("admission differs from latest passing receipts")
			}
		}
		c.completed = append(c.completed, e)
		c.phase++
		c.receipts = map[string]Receipt{}
	default:
		return fmt.Errorf("unknown action %q", e.Action)
	}
	return nil
}

func reopen(contract Contract, c *cursor, e Event) error {
	if e.Phase < 0 || e.Phase > c.phase || e.Phase >= len(contract.Phases) || e.Receipt != nil || len(e.Accepted) != 0 {
		return fmt.Errorf("invalid reopen")
	}
	c.phase = e.Phase
	c.completed = c.completed[:e.Phase]
	c.receipts = map[string]Receipt{}
	return nil
}

func validateReceipt(p Phase, r Receipt) error {
	if r.Verdict != "pass" && r.Verdict != "fail" {
		return fmt.Errorf("verdict must be pass or fail")
	}
	if strings.TrimSpace(r.Content) == "" || digest([]byte(r.Content)) != r.SHA256 || r.Evidence == "" {
		return fmt.Errorf("missing or damaged evidence")
	}
	for _, name := range p.Requires {
		if name == r.Requirement {
			return nil
		}
	}
	return fmt.Errorf("unknown requirement %q", r.Requirement)
}
