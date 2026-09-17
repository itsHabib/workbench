package command

import (
	"errors"
	"reflect"
	"testing"

	"shoplab/errs"
)

func TestHiddenParseBasics(t *testing.T) {
	for line, want := range map[string]Cmd{
		"REPORT":                        {Op: "REPORT", Args: []string{}},
		"  report\t":                    {Op: "REPORT", Args: []string{}},
		"add Cart-1 tea-001 2":          {Op: "ADD", Args: []string{"Cart-1", "tea-001", "2"}},
		"Pay O-1\t21.18":                {Op: "PAY", Args: []string{"O-1", "21.18"}},
		"CHECKOUT c1 dom":               {Op: "CHECKOUT", Args: []string{"c1", "dom"}},
		"CHECKOUT c1 dom SAVE10":        {Op: "CHECKOUT", Args: []string{"c1", "dom", "SAVE10"}},
		"COUPON x PCT 1000":             {Op: "COUPON", Args: []string{"x", "PCT", "1000"}},
		"COUPON x fixed 5.00 20.00":     {Op: "COUPON", Args: []string{"x", "fixed", "5.00", "20.00"}},
		"STOCK not-checked-here banana": {Op: "STOCK", Args: []string{"not-checked-here", "banana"}},
	} {
		got, err := Parse(line)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", line, got, err, want)
		}
	}
}

func TestHiddenQuoting(t *testing.T) {
	got, err := Parse(`PRODUCT tea-001 "Green \"Dragon\" Tea \\ 50g" 5.05 120 food`)
	want := []string{"tea-001", `Green "Dragon" Tea \ 50g`, "5.05", "120", "food"}
	if err != nil || !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = Parse(`PRODUCT tea-001 "" 5.05 120 "two words"`)
	if err != nil || got.Args[1] != "" || got.Args[4] != "two words" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = Parse("SHOW \"tab\there\"")
	if err != nil || got.Args[0] != "tab\there" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, line := range []string{`SHOW "open`, `SHOW "bad \n escape"`, `SHOW "a"b`, `SHOW a"b"`, `SHOW ab"`, `SHOW "trailing \`} {
		if _, err := Parse(line); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Parse(%q) err = %v", line, err)
		}
	}
}

func TestHiddenIdemKey(t *testing.T) {
	for _, line := range []string{"PAY O-1 5.00 @k-1", "PAY @k-1 O-1 5.00", "@k-1 pay O-1 5.00"} {
		got, err := Parse(line)
		want := Cmd{Op: "PAY", Args: []string{"O-1", "5.00"}, IdemKey: "k-1"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %+v, %v", line, got, err)
		}
	}
	got, err := Parse(`SHOW "@cart"`)
	if err != nil || got.IdemKey != "" || got.Args[0] != "@cart" {
		t.Fatalf("quoted @ token: %+v %v", got, err)
	}
	if got, _ := Parse("SHOW c1"); got.IdemKey != "" {
		t.Fatalf("%+v", got)
	}
	for _, line := range []string{"PAY O-1 5.00 @a @b", "PAY O-1 5.00 @", "@only"} {
		if _, err := Parse(line); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Parse(%q) err = %v", line, err)
		}
	}
}

func TestHiddenArity(t *testing.T) {
	counts := map[string][]int{
		"PRODUCT": {5}, "STOCK": {2}, "AVAIL": {1}, "COUPON": {3, 4}, "ADD": {3}, "REMOVE": {2}, "SHOW": {1},
		"CHECKOUT": {2, 3}, "PAY": {2}, "SHIP": {1}, "DELIVER": {1}, "CANCEL": {1}, "ORDER": {1}, "BALANCE": {1}, "REPORT": {0},
	}
	for op, ok := range counts {
		allowed := map[int]bool{}
		for _, n := range ok {
			allowed[n] = true
		}
		line := op
		for n := 0; n <= 6; n++ {
			cmd, err := Parse(line)
			if allowed[n] && (err != nil || cmd.Op != op || len(cmd.Args) != n) {
				t.Errorf("%q refused: %+v %v", line, cmd, err)
			}
			if !allowed[n] && !errors.Is(err, errs.ErrInvalid) {
				t.Errorf("%q accepted: %v", line, err)
			}
			line += " x"
		}
	}
}

func TestHiddenRejectsAndOps(t *testing.T) {
	for _, line := range []string{"", "   ", "\t", "BOGUS", "BOGUS x", "GET a"} {
		if _, err := Parse(line); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Parse(%q) err = %v", line, err)
		}
	}
	want := []string{"ADD", "AVAIL", "BALANCE", "CANCEL", "CHECKOUT", "COUPON", "DELIVER", "ORDER", "PAY", "PRODUCT", "REMOVE", "REPORT", "SHIP", "SHOW", "STOCK"}
	if !reflect.DeepEqual(Ops(), want) {
		t.Fatalf("%v", Ops())
	}
}
