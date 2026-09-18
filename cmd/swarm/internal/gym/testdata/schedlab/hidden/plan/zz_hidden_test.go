package plan

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"schedlab/clock"
	"schedlab/errs"
)

var etl = Spec{Name: "etl", Cron: "0 * * * *", Priority: 2, Tasks: map[string][]string{
	"extract": nil, "transform": {"extract"}, "load": {"transform"}, "audit": {"extract", "transform"},
}}

func compile(t *testing.T, s Spec) *Workflow {
	t.Helper()
	w, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestHiddenCompile(t *testing.T) {
	w := compile(t, etl)
	if w.Name != "etl" || w.Priority != 2 || !w.Scheduled || w.Expr.String() != "0 * * * *" || w.Graph.Len() != 4 {
		t.Fatalf("%+v", w)
	}
	if got := w.Order(); !reflect.DeepEqual(got, []string{"extract", "transform", "audit", "load"}) {
		t.Fatalf("%v", got)
	}
	deps, _ := w.Graph.Deps("audit")
	if !reflect.DeepEqual(deps, []string{"extract", "transform"}) {
		t.Fatalf("%v", deps)
	}
	manual := compile(t, Spec{Name: "m", Tasks: map[string][]string{"only": nil}})
	if manual.Scheduled || manual.Priority != 0 || !reflect.DeepEqual(manual.Order(), []string{"only"}) {
		t.Fatalf("%+v", manual)
	}
}

func TestHiddenCompileRejects(t *testing.T) {
	one := map[string][]string{"a": nil}
	for name, s := range map[string]Spec{
		"empty name":    {Name: "", Tasks: one},
		"no tasks":      {Name: "x", Tasks: nil},
		"empty tasks":   {Name: "x", Tasks: map[string][]string{}},
		"empty task":    {Name: "x", Tasks: map[string][]string{"": nil}},
		"slash":         {Name: "x", Tasks: map[string][]string{"a/b": nil}},
		"space":         {Name: "x", Tasks: map[string][]string{"a b": nil}},
		"tab":           {Name: "x", Tasks: map[string][]string{"a\tb": nil}},
		"unknown dep":   {Name: "x", Tasks: map[string][]string{"a": {"zz"}}},
		"bad cron":      {Name: "x", Cron: "* * *", Tasks: one},
		"cron out of r": {Name: "x", Cron: "60 * * * *", Tasks: one},
	} {
		if _, err := Compile(s); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, s := range map[string]Spec{
		"self":  {Name: "x", Tasks: map[string][]string{"a": {"a"}}},
		"pair":  {Name: "x", Tasks: map[string][]string{"a": {"b"}, "b": {"a"}}},
		"three": {Name: "x", Tasks: map[string][]string{"a": {"c"}, "b": {"a"}, "c": {"b"}}},
	} {
		if _, err := Compile(s); !errors.Is(err, errs.ErrCycle) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestHiddenWindow(t *testing.T) {
	hourly := compile(t, etl)
	daily := compile(t, Spec{Name: "daily", Cron: "30 2 * * *", Tasks: map[string][]string{"a": nil}})
	manual := compile(t, Spec{Name: "manual", Tasks: map[string][]string{"a": nil}})
	never := compile(t, Spec{Name: "never", Cron: "0 0 30 2 *", Tasks: map[string][]string{"a": nil}})
	ws := []*Workflow{manual, daily, hourly, never}
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	got, err := Window(ws, from, from.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []Fire{
		{"etl", from.Add(time.Hour)}, {"etl", from.Add(2 * time.Hour)},
		{"daily", from.Add(2*time.Hour + 30*time.Minute)}, {"etl", from.Add(3 * time.Hour)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	got, err = Window(ws, from, from)
	if err != nil || got != nil {
		t.Fatalf("empty window: %v %v", got, err)
	}
	got, err = Window(ws, from.Add(-time.Second), from)
	if err != nil || !reflect.DeepEqual(got, []Fire{{"etl", from}}) {
		t.Fatalf("inclusive end: %v %v", got, err)
	}
	if _, err := Window(ws, from, from.Add(-time.Nanosecond)); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("backwards: %v", err)
	}
	if got, err := Window(nil, from, from.Add(time.Hour)); err != nil || got != nil {
		t.Fatalf("no workflows: %v %v", got, err)
	}
}

func TestHiddenNext(t *testing.T) {
	a := compile(t, Spec{Name: "a", Cron: "0 12 * * *", Tasks: map[string][]string{"t": nil}})
	b := compile(t, Spec{Name: "b", Cron: "0 12 * * *", Tasks: map[string][]string{"t": nil}})
	c := compile(t, Spec{Name: "c", Cron: "0 18 * * *", Tasks: map[string][]string{"t": nil}})
	manual := compile(t, Spec{Name: "m", Tasks: map[string][]string{"t": nil}})
	never := compile(t, Spec{Name: "never", Cron: "0 0 30 2 *", Tasks: map[string][]string{"t": nil}})
	after := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	noon := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	got := Next([]*Workflow{never, manual, c, b, a}, after)
	if !reflect.DeepEqual(got, []Fire{{"a", noon}, {"b", noon}}) {
		t.Fatalf("%v", got)
	}
	got = Next([]*Workflow{c, manual}, noon)
	if !reflect.DeepEqual(got, []Fire{{"c", time.Date(2026, 4, 1, 18, 0, 0, 0, time.UTC)}}) {
		t.Fatalf("%v", got)
	}
	if got := Next([]*Workflow{manual, never}, noon); got != nil {
		t.Fatalf("%v", got)
	}
	if got := Next(nil, noon); got != nil {
		t.Fatalf("%v", got)
	}
}

func TestHiddenPlannerRegistry(t *testing.T) {
	f := clock.NewFake(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	a := compile(t, Spec{Name: "a", Tasks: map[string][]string{"t": nil}})
	b := compile(t, Spec{Name: "b", Tasks: map[string][]string{"t": nil}})
	if _, err := New(f, a, b, a); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("dup in New: %v", err)
	}
	p, err := New(f, b, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(a); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("dup add: %v", err)
	}
	c := compile(t, Spec{Name: "c", Tasks: map[string][]string{"t": nil}})
	if err := p.Add(c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Names(), []string{"a", "b", "c"}) {
		t.Fatalf("%v", p.Names())
	}
	if got, ok := p.Get("b"); !ok || got != b {
		t.Fatalf("%v %v", got, ok)
	}
	if _, ok := p.Get("zz"); ok {
		t.Fatal("unknown found")
	}
	if got := p.Due(); got != nil {
		t.Fatalf("manual workflows are never due: %v", got)
	}
}

func TestHiddenPlannerDue(t *testing.T) {
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	every := compile(t, Spec{Name: "every", Cron: "*/10 * * * *", Tasks: map[string][]string{"t": nil}})
	p, _ := New(f, every)
	if got := p.Due(); got != nil {
		t.Fatalf("nothing elapsed: %v", got)
	}
	f.Advance(10 * time.Minute)
	if got := p.Due(); !reflect.DeepEqual(got, []Fire{{"every", start.Add(10 * time.Minute)}}) {
		t.Fatalf("%v", got)
	}
	if got := p.Due(); got != nil {
		t.Fatalf("mark moved: %v", got)
	}
	f.Advance(25 * time.Minute)
	got := p.Due()
	if !reflect.DeepEqual(got, []Fire{{"every", start.Add(20 * time.Minute)}, {"every", start.Add(30 * time.Minute)}}) {
		t.Fatalf("catch-up: %v", got)
	}
	late := compile(t, Spec{Name: "late", Cron: "*/5 * * * *", Tasks: map[string][]string{"t": nil}})
	_ = p.Add(late)
	f.Advance(5 * time.Minute)
	got = p.Due()
	if !reflect.DeepEqual(got, []Fire{{"every", start.Add(40 * time.Minute)}, {"late", start.Add(40 * time.Minute)}}) {
		t.Fatalf("%v", got)
	}
	f.Set(start)
	if got := p.Due(); got != nil {
		t.Fatalf("clock went back: %v", got)
	}
	f.Set(start.Add(45 * time.Minute))
	if got := p.Due(); !reflect.DeepEqual(got, []Fire{{"late", start.Add(45 * time.Minute)}}) {
		t.Fatalf("mark kept while the clock was back: %v", got)
	}
}
