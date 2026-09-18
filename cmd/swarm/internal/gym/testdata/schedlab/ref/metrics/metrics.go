package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"schedlab/errs"
)

type Counter struct {
	mu sync.Mutex
	v  int64
}

func (c *Counter) Add(n int64) {
	if n < 0 {
		panic("metrics: negative add")
	}
	c.mu.Lock()
	c.v += n
	c.mu.Unlock()
}

func (c *Counter) Inc() { c.Add(1) }

func (c *Counter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.v
}

type Histogram struct {
	mu     sync.Mutex
	bounds []float64
	counts []int64
	sum    float64
	n      int64
}

func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, b := range h.bounds {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.n++
}

func (h *Histogram) Count() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum
}

func (h *Histogram) Counts() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]int64(nil), h.counts...)
	return append(out, h.n)
}

type Registry struct {
	mu         sync.Mutex
	counters   map[string]*Counter
	histograms map[string]*Histogram
}

func New() *Registry {
	return &Registry{counters: map[string]*Counter{}, histograms: map[string]*Histogram{}}
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		lower := c >= 'a' && c <= 'z'
		digit := c >= '0' && c <= '9'
		if !lower && c != '_' && !(digit && i > 0) {
			return false
		}
	}
	return true
}

func (r *Registry) Counter(name string) (*Counter, error) {
	if !validName(name) {
		return nil, fmt.Errorf("metrics: name %q: %w", name, errs.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.histograms[name]; ok {
		return nil, fmt.Errorf("metrics: %q is a histogram: %w", name, errs.ErrConflict)
	}
	c, ok := r.counters[name]
	if !ok {
		c = &Counter{}
		r.counters[name] = c
	}
	return c, nil
}

func sameBounds(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *Registry) Histogram(name string, bounds []float64) (*Histogram, error) {
	if !validName(name) {
		return nil, fmt.Errorf("metrics: name %q: %w", name, errs.ErrInvalid)
	}
	if len(bounds) == 0 {
		return nil, fmt.Errorf("metrics: no bounds: %w", errs.ErrInvalid)
	}
	for i := 1; i < len(bounds); i++ {
		if bounds[i] <= bounds[i-1] {
			return nil, fmt.Errorf("metrics: bounds not increasing: %w", errs.ErrInvalid)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.counters[name]; ok {
		return nil, fmt.Errorf("metrics: %q is a counter: %w", name, errs.ErrConflict)
	}
	if h, ok := r.histograms[name]; ok {
		if !sameBounds(h.bounds, bounds) {
			return nil, fmt.Errorf("metrics: %q has other bounds: %w", name, errs.ErrConflict)
		}
		return h, nil
	}
	h := &Histogram{bounds: append([]float64(nil), bounds...), counts: make([]int64, len(bounds))}
	r.histograms[name] = h
	return h, nil
}

func ff(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func (r *Registry) Render() string {
	r.mu.Lock()
	names := make([]string, 0, len(r.counters)+len(r.histograms))
	for n := range r.counters {
		names = append(names, n)
	}
	for n := range r.histograms {
		names = append(names, n)
	}
	r.mu.Unlock()
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		r.mu.Lock()
		c, isCounter := r.counters[n]
		h := r.histograms[n]
		r.mu.Unlock()
		if isCounter {
			fmt.Fprintf(&b, "%s %d\n", n, c.Value())
			continue
		}
		h.mu.Lock()
		for i, bound := range h.bounds {
			fmt.Fprintf(&b, "%s_bucket{le=\"%s\"} %d\n", n, ff(bound), h.counts[i])
		}
		fmt.Fprintf(&b, "%s_bucket{le=\"+Inf\"} %d\n", n, h.n)
		fmt.Fprintf(&b, "%s_sum %s\n", n, ff(h.sum))
		fmt.Fprintf(&b, "%s_count %d\n", n, h.n)
		h.mu.Unlock()
	}
	return b.String()
}
