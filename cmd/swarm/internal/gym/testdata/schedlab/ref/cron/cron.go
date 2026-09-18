package cron

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"schedlab/errs"
)

type Expr struct {
	Minute, Hour, Dom, Month, Dow []int
}

var ranges = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

func number(s string, lo, hi int) (int, error) {
	if s == "" {
		return 0, errs.ErrInvalid
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errs.ErrInvalid
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, errs.ErrInvalid
	}
	return n, nil
}

func item(s string, lo, hi int, set map[int]bool) error {
	step := 1
	if i := strings.IndexByte(s, '/'); i >= 0 {
		n, err := number(s[i+1:], 1, 1<<30)
		if err != nil {
			return err
		}
		step = n
		s = s[:i]
		if s != "*" && !strings.Contains(s, "-") {
			return errs.ErrInvalid
		}
	}
	a, b := lo, hi
	switch {
	case s == "*":
	case strings.Contains(s, "-"):
		parts := strings.SplitN(s, "-", 2)
		var err error
		if a, err = number(parts[0], lo, hi); err != nil {
			return err
		}
		if b, err = number(parts[1], lo, hi); err != nil {
			return err
		}
		if a > b {
			return errs.ErrInvalid
		}
	default:
		n, err := number(s, lo, hi)
		if err != nil {
			return err
		}
		a, b = n, n
	}
	for v := a; v <= b; v += step {
		set[v] = true
	}
	return nil
}

func field(s string, lo, hi int) ([]int, error) {
	set := map[int]bool{}
	for _, it := range strings.Split(s, ",") {
		if err := item(it, lo, hi, set); err != nil {
			return nil, fmt.Errorf("cron: field %q: %w", s, err)
		}
	}
	out := make([]int, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, nil
}

func Parse(s string) (Expr, error) {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return Expr{}, fmt.Errorf("cron: %q: want 5 fields: %w", s, errs.ErrInvalid)
	}
	var out [5][]int
	for i, f := range fields {
		vals, err := field(f, ranges[i][0], ranges[i][1])
		if err != nil {
			return Expr{}, err
		}
		out[i] = vals
	}
	return Expr{Minute: out[0], Hour: out[1], Dom: out[2], Month: out[3], Dow: out[4]}, nil
}

func render(vals []int, lo, hi int) string {
	if len(vals) == hi-lo+1 {
		return "*"
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func (e Expr) String() string {
	return strings.Join([]string{
		render(e.Minute, 0, 59), render(e.Hour, 0, 23), render(e.Dom, 1, 31), render(e.Month, 1, 12), render(e.Dow, 0, 6),
	}, " ")
}

func has(vals []int, v int) bool {
	i := sort.SearchInts(vals, v)
	return i < len(vals) && vals[i] == v
}

func (e Expr) day(t time.Time) bool {
	return has(e.Dom, t.Day()) && has(e.Month, int(t.Month())) && has(e.Dow, int(t.Weekday()))
}

func (e Expr) Matches(t time.Time) bool {
	return has(e.Minute, t.Minute()) && has(e.Hour, t.Hour()) && e.day(t)
}

func (e Expr) Next(after time.Time) (time.Time, error) {
	loc := after.Location()
	t := time.Date(after.Year(), after.Month(), after.Day(), after.Hour(), after.Minute()+1, 0, 0, loc)
	limit := after.AddDate(5, 0, 0)
	for !t.After(limit) {
		if !e.day(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		for _, h := range e.Hour {
			if h < t.Hour() {
				continue
			}
			for _, m := range e.Minute {
				if h == t.Hour() && m < t.Minute() {
					continue
				}
				return time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, loc), nil
			}
		}
		t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
	}
	return time.Time{}, fmt.Errorf("cron: %s never fires after %s: %w", e, after, errs.ErrNotFound)
}
