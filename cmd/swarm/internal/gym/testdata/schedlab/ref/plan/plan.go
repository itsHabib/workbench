package plan

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"schedlab/clock"
	"schedlab/cron"
	"schedlab/dag"
	"schedlab/errs"
)

type Spec struct {
	Name, Cron string
	Priority   int
	Tasks      map[string][]string
}

type Workflow struct {
	Name      string
	Priority  int
	Scheduled bool
	Expr      cron.Expr
	Graph     *dag.Graph
}

func badName(s string) bool {
	return s == "" || strings.ContainsRune(s, '/') || strings.IndexFunc(s, unicode.IsSpace) >= 0
}

func Compile(s Spec) (*Workflow, error) {
	if s.Name == "" {
		return nil, fmt.Errorf("plan: empty name: %w", errs.ErrInvalid)
	}
	if len(s.Tasks) == 0 {
		return nil, fmt.Errorf("plan: %q has no tasks: %w", s.Name, errs.ErrInvalid)
	}
	w := &Workflow{Name: s.Name, Priority: s.Priority, Graph: dag.New()}
	names := make([]string, 0, len(s.Tasks))
	for name := range s.Tasks {
		if badName(name) {
			return nil, fmt.Errorf("plan: task name %q: %w", name, errs.ErrInvalid)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_ = w.Graph.AddNode(name)
	}
	for _, name := range names {
		for _, dep := range s.Tasks[name] {
			if _, ok := s.Tasks[dep]; !ok {
				return nil, fmt.Errorf("plan: task %q depends on unknown %q: %w", name, dep, errs.ErrInvalid)
			}
			if dep == name {
				return nil, fmt.Errorf("plan: task %q depends on itself: %w", name, errs.ErrCycle)
			}
			if err := w.Graph.AddEdge(dep, name); err != nil {
				return nil, err
			}
		}
	}
	if s.Cron != "" {
		expr, err := cron.Parse(s.Cron)
		if err != nil {
			return nil, err
		}
		w.Expr, w.Scheduled = expr, true
	}
	return w, nil
}

func (w *Workflow) Order() []string { return w.Graph.Order() }

type Fire struct {
	Workflow string
	At       time.Time
}

func sortFires(fs []Fire) {
	sort.Slice(fs, func(i, j int) bool {
		if !fs[i].At.Equal(fs[j].At) {
			return fs[i].At.Before(fs[j].At)
		}
		return fs[i].Workflow < fs[j].Workflow
	})
}

func Window(ws []*Workflow, from, to time.Time) ([]Fire, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("plan: window ends before it starts: %w", errs.ErrInvalid)
	}
	var out []Fire
	for _, w := range ws {
		if !w.Scheduled {
			continue
		}
		t := from
		for {
			next, err := w.Expr.Next(t)
			if errors.Is(err, errs.ErrNotFound) {
				break
			}
			if err != nil {
				return nil, err
			}
			if next.After(to) {
				break
			}
			out = append(out, Fire{Workflow: w.Name, At: next})
			t = next
		}
	}
	sortFires(out)
	return out, nil
}

func Next(ws []*Workflow, after time.Time) []Fire {
	var out []Fire
	for _, w := range ws {
		if !w.Scheduled {
			continue
		}
		next, err := w.Expr.Next(after)
		if err != nil {
			continue
		}
		out = append(out, Fire{Workflow: w.Name, At: next})
	}
	sortFires(out)
	n := 0
	for n < len(out) && out[n].At.Equal(out[0].At) {
		n++
	}
	return out[:n]
}

type Planner struct {
	mu   sync.Mutex
	c    clock.Clock
	mark time.Time
	ws   map[string]*Workflow
}

func New(c clock.Clock, ws ...*Workflow) (*Planner, error) {
	p := &Planner{c: c, mark: c.Now(), ws: map[string]*Workflow{}}
	for _, w := range ws {
		if err := p.Add(w); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *Planner) Add(w *Workflow) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.ws[w.Name]; ok {
		return fmt.Errorf("plan: workflow %q: %w", w.Name, errs.ErrConflict)
	}
	p.ws[w.Name] = w
	return nil
}

func (p *Planner) Get(name string) (*Workflow, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, ok := p.ws[name]
	return w, ok
}

func (p *Planner) Names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.ws))
	for n := range p.ws {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (p *Planner) Due() []Fire {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.c.Now()
	if now.Before(p.mark) {
		return nil
	}
	all := make([]*Workflow, 0, len(p.ws))
	for _, w := range p.ws {
		all = append(all, w)
	}
	fires, _ := Window(all, p.mark, now)
	p.mark = now
	return fires
}
