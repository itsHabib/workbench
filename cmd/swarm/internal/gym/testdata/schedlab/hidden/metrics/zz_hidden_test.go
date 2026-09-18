package metrics

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"schedlab/errs"
)

func TestHiddenCounters(t *testing.T) {
	r := New()
	c, err := r.Counter("runs_total")
	if err != nil {
		t.Fatal(err)
	}
	c.Inc()
	c.Add(4)
	c.Add(0)
	again, err := r.Counter("runs_total")
	if err != nil || again != c || again.Value() != 5 {
		t.Fatalf("%v %v %d", again == c, err, again.Value())
	}
	for _, name := range []string{"", "Runs", "1abc", "a-b", "a b", "a.b", "ünï"} {
		if _, err := r.Counter(name); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"_", "a", "x_1", "task_ms_total"} {
		if _, err := r.Counter(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("negative add did not panic")
		}
	}()
	c.Add(-1)
}

func TestHiddenHistogram(t *testing.T) {
	r := New()
	h, err := r.Histogram("latency", []float64{10, 100, 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{5, 10, 10.5, 100, 2000, 0} {
		h.Observe(v)
	}
	if h.Count() != 6 || h.Sum() != 2125.5 {
		t.Fatalf("count %d sum %v", h.Count(), h.Sum())
	}
	if got := h.Counts(); !reflect.DeepEqual(got, []int64{3, 5, 5, 6}) {
		t.Fatalf("%v", got)
	}
	bounds := []float64{1, 2}
	h2, _ := r.Histogram("other", bounds)
	bounds[0] = 99
	h2.Observe(1)
	if got := h2.Counts(); !reflect.DeepEqual(got, []int64{1, 1, 1}) {
		t.Fatalf("bounds were not copied: %v", got)
	}
	empty, _ := r.Histogram("empty", []float64{1})
	if got := empty.Counts(); !reflect.DeepEqual(got, []int64{0, 0}) || empty.Count() != 0 || empty.Sum() != 0 {
		t.Fatalf("%v", got)
	}
}

func TestHiddenNameConflicts(t *testing.T) {
	r := New()
	_, _ = r.Counter("c")
	h, err := r.Histogram("h", []float64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Histogram("c", []float64{1}); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("histogram over counter: %v", err)
	}
	if _, err := r.Counter("h"); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("counter over histogram: %v", err)
	}
	if _, err := r.Histogram("h", []float64{1, 3}); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("other bounds: %v", err)
	}
	same, err := r.Histogram("h", []float64{1, 2})
	if err != nil || same != h {
		t.Fatalf("same bounds: %v %v", same == h, err)
	}
	for _, b := range [][]float64{nil, {}, {2, 1}, {1, 1}, {1, 2, 2}} {
		if _, err := r.Histogram("new", b); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("bounds %v: %v", b, err)
		}
	}
	if _, err := r.Histogram("Bad", []float64{1}); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
}

func TestHiddenRender(t *testing.T) {
	r := New()
	if r.Render() != "" {
		t.Fatalf("%q", r.Render())
	}
	z, _ := r.Counter("zeta")
	z.Add(12)
	a, _ := r.Counter("alpha")
	_ = a
	h, _ := r.Histogram("mid", []float64{0.5, 2, 1e6})
	h.Observe(0.25)
	h.Observe(1)
	h.Observe(3)
	want := "alpha 0\n" +
		"mid_bucket{le=\"0.5\"} 1\n" +
		"mid_bucket{le=\"2\"} 2\n" +
		"mid_bucket{le=\"1e+06\"} 3\n" +
		"mid_bucket{le=\"+Inf\"} 3\n" +
		"mid_sum 4.25\n" +
		"mid_count 3\n" +
		"zeta 12\n"
	if got := r.Render(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestHiddenConcurrent(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := r.Counter("hits")
			h, _ := r.Histogram("size", []float64{50})
			for j := 0; j < 100; j++ {
				c.Inc()
				h.Observe(float64(j))
				_ = r.Render()
			}
		}()
	}
	wg.Wait()
	c, _ := r.Counter("hits")
	h, _ := r.Histogram("size", []float64{50})
	if c.Value() != 2000 || h.Count() != 2000 || !reflect.DeepEqual(h.Counts(), []int64{1020, 2000}) {
		t.Fatalf("%d %d %v", c.Value(), h.Count(), h.Counts())
	}
}
