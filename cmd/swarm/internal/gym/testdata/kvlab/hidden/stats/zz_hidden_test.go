package stats

import (
	"reflect"
	"testing"
)

func TestHiddenPercentile(t *testing.T) {
	xs := []float64{15, 20, 35, 40, 50}
	for p, want := range map[float64]float64{0: 15, 5: 15, 30: 20, 40: 20, 50: 35, 95: 50, 100: 50} {
		got, err := Percentile(xs, p)
		if err != nil || got != want {
			t.Errorf("p%v = %v (%v), want %v", p, got, err, want)
		}
	}
}

func TestHiddenPercentileErrorsAndPurity(t *testing.T) {
	if _, err := Percentile(nil, 50); err == nil {
		t.Error("empty accepted")
	}
	if _, err := Percentile([]float64{1}, -1); err == nil {
		t.Error("p<0 accepted")
	}
	if _, err := Percentile([]float64{1}, 100.5); err == nil {
		t.Error("p>100 accepted")
	}
	xs := []float64{3, 1, 2}
	if _, err := Percentile(xs, 50); err != nil || !reflect.DeepEqual(xs, []float64{3, 1, 2}) {
		t.Errorf("caller's slice reordered: %v", xs)
	}
}

func TestHiddenSummary(t *testing.T) {
	if got := Summary(nil); got != (Sum{}) {
		t.Errorf("empty = %+v", got)
	}
	got := Summary([]float64{4, 1, 3, 2})
	want := Sum{N: 4, Min: 1, Max: 4, Mean: 2.5, P50: 2, P95: 4}
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
}
