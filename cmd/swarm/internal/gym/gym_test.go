package gym

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/swarm/internal/swarm"
)

// The gym holds itself to the rule it grades authors by: on an untouched
// fixture the grader fails, and on a reference solution it passes. A grader
// that cannot fail, or cannot pass, measures nothing.

var references = map[string]string{
	"author-intervals": `package intervals

import "sort"

type Interval struct{ Lo, Hi int }

func Merge(in []Interval) []Interval {
	s := append([]Interval(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i].Lo < s[j].Lo })
	out := []Interval{}
	for _, iv := range s {
		if n := len(out); n > 0 && iv.Lo <= out[n-1].Hi {
			if iv.Hi > out[n-1].Hi {
				out[n-1].Hi = iv.Hi
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
`,
	"author-lru": `package lru

type Cache struct {
	cap  int
	keys []string // most recent first
	vals map[string]int
}

func New(capacity int) *Cache { return &Cache{cap: capacity, vals: map[string]int{}} }

func (c *Cache) touch(key string) {
	for i, k := range c.keys {
		if k == key {
			c.keys = append(c.keys[:i], c.keys[i+1:]...)
			break
		}
	}
	c.keys = append([]string{key}, c.keys...)
}

func (c *Cache) Get(key string) (int, bool) {
	v, ok := c.vals[key]
	if ok {
		c.touch(key)
	}
	return v, ok
}

func (c *Cache) Put(key string, value int) {
	if c.cap <= 0 {
		return
	}
	if _, ok := c.vals[key]; !ok && len(c.keys) >= c.cap {
		last := c.keys[len(c.keys)-1]
		c.keys = c.keys[:len(c.keys)-1]
		delete(c.vals, last)
	}
	c.vals[key] = value
	c.touch(key)
}

func (c *Cache) Len() int       { return len(c.keys) }
func (c *Cache) Keys() []string { return append([]string(nil), c.keys...) }
`,
	"author-toposort": `package toposort

import (
	"fmt"
	"sort"
)

func Order(deps map[string][]string) ([]string, error) {
	need := map[string]map[string]bool{}
	for n, ds := range deps {
		if need[n] == nil {
			need[n] = map[string]bool{}
		}
		for _, d := range ds {
			need[n][d] = true
			if need[d] == nil {
				need[d] = map[string]bool{}
			}
		}
	}
	out := []string{}
	done := map[string]bool{}
	for len(out) < len(need) {
		var ready []string
		for n, ds := range need {
			if done[n] {
				continue
			}
			ok := true
			for d := range ds {
				if !done[d] {
					ok = false
				}
			}
			if ok {
				ready = append(ready, n)
			}
		}
		if len(ready) == 0 {
			var left []string
			for n := range need {
				if !done[n] {
					left = append(left, n)
				}
			}
			sort.Strings(left)
			return nil, fmt.Errorf("cycle through %s", left[0])
		}
		sort.Strings(ready)
		done[ready[0]] = true
		out = append(out, ready[0])
	}
	return out, nil
}
`,
	"author-bucket": `package bucket

import "time"

type Bucket struct {
	cap, rate, tokens float64
	last              time.Time
}

func New(capacity, refillPerSecond float64, now time.Time) *Bucket {
	return &Bucket{cap: capacity, rate: refillPerSecond, tokens: capacity, last: now}
}

func (b *Bucket) refill(now time.Time) {
	if now.Before(b.last) {
		return
	}
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.cap {
		b.tokens = b.cap
	}
	b.last = now
}

func (b *Bucket) AllowN(now time.Time, n float64) bool {
	b.refill(now)
	if b.tokens+1e-12 < n {
		return false
	}
	b.tokens -= n
	return true
}

func (b *Bucket) Allow(now time.Time) bool { return b.AllowN(now, 1) }

func (b *Bucket) Tokens(now time.Time) float64 { b.refill(now); return b.tokens }
`,
}

func task(t *testing.T, id string) Task {
	t.Helper()
	for _, tk := range Tasks() {
		if tk.ID == id {
			return tk
		}
	}
	t.Fatalf("no task %s", id)
	return Task{}
}

func TestAuthorGradersFailOnStubAndPassOnReference(t *testing.T) {
	for id, ref := range references {
		t.Run(id, func(t *testing.T) {
			fx, err := build(task(t, id), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if ok, _ := fx.grade(); ok {
				t.Fatal("the untouched stub passed the hidden tests")
			}
			spec := authors[id]
			if err := os.WriteFile(filepath.Join(fx.cwd, "pkg", spec.pkg, spec.pkg+".go"), []byte(ref), 0o644); err != nil {
				t.Fatal(err)
			}
			if ok, detail := fx.grade(); !ok {
				t.Fatalf("the reference solution failed the hidden tests:\n%s", detail)
			}
		})
	}
}

func TestRulerGraders(t *testing.T) {
	t.Setenv("SWARM_STATE", "")
	for _, c := range []struct {
		id     string
		act    func(s *swarm.State, req string) error
		wantOK bool
	}{
		{"ruler-order", func(*swarm.State, string) error { return nil }, false},
		{"ruler-order", func(s *swarm.State, req string) error {
			_, err := s.Rule(req, "helper", 0, "parse first", "report calls Parse", "", []string{"feat-parse", "feat-report"})
			return err
		}, true},
		{"ruler-order", func(s *swarm.State, req string) error {
			_, err := s.Rule(req, "helper", 0, "report first", "", "", []string{"feat-report", "feat-parse"})
			return err
		}, false},
		{"ruler-escalate", func(s *swarm.State, req string) error {
			_, err := s.Escalate(req, "helper", swarm.TierOperator, "product")
			return err
		}, true},
		{"ruler-escalate", func(s *swarm.State, req string) error {
			_, err := s.Rule(req, "helper", 0, "csv", "", "", []string{"feat-export-csv", "feat-export-jsonl"})
			return err
		}, false},
	} {
		fx, err := build(task(t, c.id), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		s, err := swarm.Open(fx.cwd)
		if err != nil {
			t.Fatal(err)
		}
		reqs, _ := s.Requests(true)
		if len(reqs) != 1 {
			t.Fatalf("%s: %d open requests", c.id, len(reqs))
		}
		if err := c.act(s, reqs[0].ID); err != nil {
			t.Fatal(err)
		}
		if ok, detail := fx.grade(); ok != c.wantOK {
			t.Fatalf("%s: graded %v, want %v: %s", c.id, ok, c.wantOK, detail)
		}
	}
}

func TestConsolidatorGraderFailsUntouched(t *testing.T) {
	fx, err := build(task(t, "consolidate-three"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := fx.grade(); ok {
		t.Fatal("an empty theme branch passed")
	}
}
