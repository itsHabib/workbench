package state

import (
	"errors"
	"reflect"
	"testing"

	"schedlab/errs"
)

func TestHiddenValuesAndAll(t *testing.T) {
	if None != "" || Pending != "PENDING" || Running != "RUNNING" || Succeeded != "SUCCEEDED" || Failed != "FAILED" || Cancelled != "CANCELLED" {
		t.Fatal("state values")
	}
	if got := All(); !reflect.DeepEqual(got, []State{Pending, Running, Succeeded, Failed, Cancelled}) {
		t.Fatalf("%v", got)
	}
}

func TestHiddenParse(t *testing.T) {
	for in, want := range map[string]State{"PENDING": Pending, "pending": Pending, "Running": Running, "succeeded": Succeeded, "FAILED": Failed, "cAnCeLlEd": Cancelled} {
		if got, err := Parse(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", " PENDING", "PENDING ", "NONE", "DONE", "PEND"} {
		if _, err := Parse(in); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestHiddenTransitionTable(t *testing.T) {
	all := append([]State{None}, All()...)
	allowed := map[[2]State]bool{
		{None, Pending}: true, {Pending, Running}: true, {Pending, Cancelled}: true,
		{Running, Succeeded}: true, {Running, Failed}: true, {Running, Cancelled}: true, {Running, Pending}: true,
	}
	n := 0
	for _, from := range all {
		for _, to := range all {
			got := CanTransition(from, to)
			if got != allowed[[2]State{from, to}] {
				t.Errorf("%q -> %q = %v", from, to, got)
			}
			if got {
				n++
			}
		}
	}
	if n != 7 {
		t.Fatalf("%d legal transitions", n)
	}
}

func TestHiddenTerminal(t *testing.T) {
	for s, want := range map[State]bool{None: false, Pending: false, Running: false, Succeeded: true, Failed: true, Cancelled: true} {
		if Terminal(s) != want {
			t.Errorf("Terminal(%q) = %v", s, !want)
		}
	}
	for _, s := range All() {
		if !Terminal(s) {
			continue
		}
		for _, to := range All() {
			if CanTransition(s, to) {
				t.Errorf("terminal %q -> %q allowed", s, to)
			}
		}
	}
}
