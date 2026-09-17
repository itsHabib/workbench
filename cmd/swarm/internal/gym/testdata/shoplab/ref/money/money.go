package money

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"shoplab/errs"
)

type Money int64

var shape = regexp.MustCompile(`^-?[0-9]+(\.[0-9]{1,2})?$`)

func Parse(s string) (Money, error) {
	if !shape.MatchString(s) {
		return 0, fmt.Errorf("money: %q: %w", s, errs.ErrInvalid)
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: %q: %w", s, errs.ErrInvalid)
	}
	if neg {
		n = -n
	}
	return Money(n), nil
}

func (m Money) String() string {
	sign := ""
	n := int64(m)
	if n < 0 {
		sign = "-"
		n = -n
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

func (m Money) Mul(qty int) Money { return m * Money(qty) }

// divRound divides rounding half away from zero. d must be positive.
func divRound(n, d int64) int64 {
	neg := n < 0
	if neg {
		n = -n
	}
	q, r := n/d, n%d
	if 2*r >= d {
		q++
	}
	if neg {
		q = -q
	}
	return q
}

func (m Money) BP(bp int64) Money { return Money(divRound(int64(m)*bp, 10000)) }

func (m Money) Div(n int64) (Money, error) {
	if n <= 0 {
		return 0, fmt.Errorf("money: divide by %d: %w", n, errs.ErrInvalid)
	}
	return Money(divRound(int64(m), n)), nil
}

func Sum(ms ...Money) Money {
	var t Money
	for _, m := range ms {
		t += m
	}
	return t
}

func Allocate(total Money, weights []int64) ([]Money, error) {
	if total < 0 || len(weights) == 0 {
		return nil, fmt.Errorf("money: allocate: %w", errs.ErrInvalid)
	}
	var sum int64
	for _, w := range weights {
		if w < 0 {
			return nil, fmt.Errorf("money: negative weight: %w", errs.ErrInvalid)
		}
		sum += w
	}
	if sum == 0 {
		return nil, fmt.Errorf("money: zero weights: %w", errs.ErrInvalid)
	}
	parts := make([]Money, len(weights))
	rem := make([]int64, len(weights))
	idx := make([]int, len(weights))
	left := int64(total)
	for i, w := range weights {
		parts[i] = Money(int64(total) * w / sum)
		rem[i] = int64(total) * w % sum
		left -= int64(parts[i])
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return rem[idx[a]] > rem[idx[b]] })
	for i := 0; int64(i) < left; i++ {
		parts[idx[i]]++
	}
	return parts, nil
}
