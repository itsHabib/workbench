package passage

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is an inspectable next-handoff result; readiness is not authority.
type Status struct {
	Name     string   `json:"name"`
	Revision int      `json:"revision"`
	Phase    string   `json:"phase"`
	Brief    string   `json:"brief,omitempty"`
	Ready    bool     `json:"ready"`
	Problems []string `json:"problems"`
}

// Change describes one compare-and-swap update to the work record.
type Change struct {
	Action                         string
	Expect                         int
	By, Note                       string
	Requirement, Verdict, Evidence string
	Phase                          string
}

// Init freezes a contract and binds it to a canonical local root.
func Init(path, root, contractPath string) error {
	var contract Contract
	if err := decode(contractPath, &contract); err != nil {
		return err
	}
	if err := validateContract(contract); err != nil {
		return err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("root must be a directory")
	}
	return locked(path, func() error {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fmt.Errorf("work record already exists or cannot be inspected")
		}
		return publish(path, Record{Version: 1, Root: root, Contract: contract, Events: []Event{}})
	})
}

// Read validates the entire history before returning any state.
func Read(path string) (Record, error) {
	var r Record
	if err := decode(path, &r); err != nil {
		return r, err
	}
	if !filepath.IsAbs(r.Root) {
		return r, fmt.Errorf("record root must be absolute")
	}
	_, err := replay(r)
	return r, err
}

// Inspect checks current inputs and the evidence retained at earlier handoffs.
func Inspect(r Record) (Status, error) {
	c, err := replay(r)
	if err != nil {
		return Status{}, err
	}
	s := Status{Name: r.Contract.Name, Revision: len(r.Events), Phase: "complete", Problems: []string{}}
	s.Problems = append(s.Problems, upstream(r, c)...)
	if c.phase == len(r.Contract.Phases) {
		s.Ready = len(s.Problems) == 0
		return s, nil
	}
	p := r.Contract.Phases[c.phase]
	s.Phase, s.Brief = p.Name, p.Brief
	subject, err := snapshot(r.Root, p)
	if err != nil {
		s.Problems = append(s.Problems, err.Error())
		return s, nil
	}
	s.Problems = append(s.Problems, requirements(p, c.receipts, subject)...)
	s.Ready = len(s.Problems) == 0
	return s, nil
}

func upstream(r Record, c cursor) []string {
	for _, e := range c.completed {
		p := r.Contract.Phases[e.Phase]
		subject, err := snapshot(r.Root, p)
		if err != nil {
			return []string{fmt.Sprintf("reopen %s: %v", p.Name, err)}
		}
		for _, receipt := range e.Accepted {
			if !reflect.DeepEqual(subject, receipt.Subject) {
				return []string{"reopen " + p.Name + ": accepted inputs changed"}
			}
		}
	}
	return nil
}

func requirements(p Phase, receipts map[string]Receipt, subject Snapshot) []string {
	var problems []string
	for _, name := range p.Requires {
		r, ok := receipts[name]
		if !ok {
			problems = append(problems, name+": missing receipt")
			continue
		}
		if r.Verdict != "pass" {
			problems = append(problems, name+": latest verdict is fail")
			continue
		}
		if !reflect.DeepEqual(r.Subject, subject) {
			problems = append(problems, name+": stale inputs")
		}
	}
	return problems
}

// Update serializes mutation and rejects a caller's stale observed revision.
func Update(path string, change Change) error {
	return locked(path, func() error {
		r, err := Read(path)
		if err != nil {
			return err
		}
		if len(r.Events) != change.Expect {
			return fmt.Errorf("revision changed: expected %d, current %d; inspect history before retry", change.Expect, len(r.Events))
		}
		e, err := prepare(r, change)
		if err != nil {
			return err
		}
		r.Events = append(r.Events, e)
		if _, err := replay(r); err != nil {
			return err
		}
		return publish(path, r)
	})
}

func prepare(r Record, change Change) (Event, error) {
	c, err := replay(r)
	if err != nil {
		return Event{}, err
	}
	e := Event{Action: change.Action, Phase: c.phase, By: change.By, Note: change.Note, At: time.Now().UTC().Format(time.RFC3339Nano)}
	if e.Action == "reopen" {
		e.Phase = phaseIndex(r.Contract, change.Phase)
		return e, nil
	}
	if c.phase == len(r.Contract.Phases) {
		return e, fmt.Errorf("work is complete; reopen a phase before changing it")
	}
	if problems := upstream(r, c); len(problems) > 0 {
		return e, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	p := r.Contract.Phases[c.phase]
	subject, err := snapshot(r.Root, p)
	if err != nil {
		return e, err
	}
	if e.Action == "record" {
		e.Receipt, err = evidence(change, subject)
		return e, err
	}
	if e.Action != "advance" {
		return e, fmt.Errorf("unknown action %s", e.Action)
	}
	if problems := requirements(p, c.receipts, subject); len(problems) > 0 {
		return e, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	for _, name := range p.Requires {
		e.Accepted = append(e.Accepted, c.receipts[name])
	}
	return e, nil
}

func evidence(change Change, subject Snapshot) (*Receipt, error) {
	b, err := readLimited(change.Evidence, maxEvidence)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("evidence must be UTF-8 text")
	}
	return &Receipt{Requirement: change.Requirement, Verdict: change.Verdict, Subject: subject,
		Evidence: change.Evidence, SHA256: digest(b), Content: string(b)}, nil
}

func phaseIndex(c Contract, name string) int {
	for i, p := range c.Phases {
		if p.Name == name {
			return i
		}
	}
	return -1
}
