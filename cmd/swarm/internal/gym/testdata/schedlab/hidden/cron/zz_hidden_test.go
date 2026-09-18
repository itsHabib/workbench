package cron

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"schedlab/errs"
)

func seq(lo, hi, step int) []int {
	var out []int
	for v := lo; v <= hi; v += step {
		out = append(out, v)
	}
	return out
}

func TestHiddenParseFields(t *testing.T) {
	e, err := Parse("  */15\t1-3,20 1,15 */5 0-6/3 ")
	if err != nil {
		t.Fatal(err)
	}
	want := Expr{Minute: []int{0, 15, 30, 45}, Hour: []int{1, 2, 3, 20}, Dom: []int{1, 15}, Month: []int{1, 6, 11}, Dow: []int{0, 3, 6}}
	if !reflect.DeepEqual(e, want) {
		t.Fatalf("%+v", e)
	}
	e, err = Parse("* * * * *")
	if err != nil || !reflect.DeepEqual(e, Expr{Minute: seq(0, 59, 1), Hour: seq(0, 23, 1), Dom: seq(1, 31, 1), Month: seq(1, 12, 1), Dow: seq(0, 6, 1)}) {
		t.Fatalf("%+v %v", e, err)
	}
	e, err = Parse("05,5,3-4 00 01 01 0")
	if err != nil || !reflect.DeepEqual(e, Expr{Minute: []int{3, 4, 5}, Hour: []int{0}, Dom: []int{1}, Month: []int{1}, Dow: []int{0}}) {
		t.Fatalf("dedupe and leading zeros: %+v %v", e, err)
	}
}

func TestHiddenParseRejects(t *testing.T) {
	for _, s := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *", "* * * 13 * ",
		"* * * * 7", "5/2 * * * *", "*/0 * * * *", "10-5 * * * *", "1,,2 * * * *", ",1 * * * *", "+1 * * * *",
		"-1 * * * *", "? * * * *", "* * * jan *", "* * * * mon", "1- * * * *", "*-5 * * * *", "1.5 * * * *", "* * L * *",
	} {
		if _, err := Parse(s); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%q: %v", s, err)
		}
	}
}

func TestHiddenString(t *testing.T) {
	for in, want := range map[string]string{
		"*/15 * * * *":            "0,15,30,45 * * * *",
		"0-59 0-23 1-31 1-12 0-6": "* * * * *",
		"30 4 1,15 * 5":           "30 4 1,15 * 5",
		"5,3 */12 * 6-8/2 *":      "3,5 0,12 * 6,8 *",
	} {
		e, err := Parse(in)
		if err != nil || e.String() != want {
			t.Errorf("%q -> %q (%v), want %q", in, e.String(), err, want)
		}
	}
}

func TestHiddenMatches(t *testing.T) {
	e, _ := Parse("30 4 * * 1-5")
	mon := time.Date(2026, 3, 2, 4, 30, 59, 999, time.UTC) // Monday
	if !e.Matches(mon) {
		t.Fatal("monday 04:30:59 should match")
	}
	if e.Matches(mon.Add(time.Minute)) || e.Matches(mon.Add(time.Hour)) || e.Matches(mon.AddDate(0, 0, 5)) {
		t.Fatal("wrong minute, hour or saturday matched")
	}
	both, _ := Parse("0 0 1 * 1")
	if !both.Matches(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) { // 2026-06-01 is a Monday and the 1st
		t.Fatal("monday the 1st should match")
	}
	if both.Matches(time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)) || both.Matches(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("dom and dow must both match")
	}
	if (Expr{}).Matches(mon) {
		t.Fatal("zero expr matched")
	}
}

func TestHiddenNext(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		expr  string
		after time.Time
		want  time.Time
	}{
		{"* * * * *", time.Date(2026, 1, 1, 10, 5, 0, 0, loc), time.Date(2026, 1, 1, 10, 6, 0, 0, loc)},
		{"* * * * *", time.Date(2026, 1, 1, 10, 5, 30, 0, loc), time.Date(2026, 1, 1, 10, 6, 0, 0, loc)},
		{"5 10 * * *", time.Date(2026, 1, 1, 10, 5, 0, 0, loc), time.Date(2026, 1, 2, 10, 5, 0, 0, loc)},
		{"5 10 * * *", time.Date(2026, 1, 1, 10, 4, 59, 0, loc), time.Date(2026, 1, 1, 10, 5, 0, 0, loc)},
		{"0 0 1 1 *", time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2027, 1, 1, 0, 0, 0, 0, loc)},
		{"*/20 23 * * *", time.Date(2026, 1, 1, 23, 40, 0, 0, loc), time.Date(2026, 1, 2, 23, 0, 0, 0, loc)},
		{"0 9 * * 1", time.Date(2026, 3, 2, 9, 0, 0, 0, loc), time.Date(2026, 3, 9, 9, 0, 0, 0, loc)},
		{"0 0 29 2 *", time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2028, 2, 29, 0, 0, 0, 0, loc)},
		{"0 0 31 * *", time.Date(2026, 1, 31, 0, 0, 0, 0, loc), time.Date(2026, 3, 31, 0, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		e, err := Parse(c.expr)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.Next(c.after)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%q after %v: %v %v, want %v", c.expr, c.after, got, err, c.want)
		}
	}
}

func TestHiddenNextLocationAndNever(t *testing.T) {
	loc := time.FixedZone("plus3", 3*3600)
	e, _ := Parse("0 6 * * *")
	got, err := e.Next(time.Date(2026, 5, 5, 6, 0, 0, 0, loc))
	if err != nil || got.Location() != loc || !got.Equal(time.Date(2026, 5, 6, 6, 0, 0, 0, loc)) {
		t.Fatalf("%v %v", got, err)
	}
	never, _ := Parse("0 0 30 2 *")
	if _, err := never.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("feb 30: %v", err)
	}
	if _, err := (Expr{}).Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("zero expr: %v", err)
	}
}
