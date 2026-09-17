package stats

import (
	"errors"
	"math"
	"sort"
)

func Percentile(xs []float64, p float64) (float64, error) {
	if len(xs) == 0 || p < 0 || p > 100 || math.IsNaN(p) {
		return 0, errors.New("stats: bad input")
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	rank := int(math.Ceil(p / 100 * float64(len(c))))
	if rank < 1 {
		rank = 1
	}
	return c[rank-1], nil
}

type Sum struct {
	N                        int
	Min, Max, Mean, P50, P95 float64
}

func Summary(xs []float64) Sum {
	if len(xs) == 0 {
		return Sum{}
	}
	s := Sum{N: len(xs), Min: xs[0], Max: xs[0]}
	total := 0.0
	for _, x := range xs {
		s.Min, s.Max = math.Min(s.Min, x), math.Max(s.Max, x)
		total += x
	}
	s.Mean = total / float64(len(xs))
	s.P50, _ = Percentile(xs, 50)
	s.P95, _ = Percentile(xs, 95)
	return s
}
