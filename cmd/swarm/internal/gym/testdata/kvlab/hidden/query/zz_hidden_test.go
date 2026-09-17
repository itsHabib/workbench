package query

import (
	"testing"
	"time"
)

func TestHiddenBasics(t *testing.T) {
	cases := map[string]Cmd{
		"GET a":         {Op: "GET", Key: "a"},
		"get a":         {Op: "GET", Key: "a"},
		"  DeL   k  ":   {Op: "DEL", Key: "k"},
		"keys":          {Op: "KEYS"},
		"COUNT":         {Op: "COUNT"},
		"SET a 1":       {Op: "SET", Key: "a", Val: "1"},
		"set a 1 ex 30": {Op: "SET", Key: "a", Val: "1", TTL: 30 * time.Second},
		"SET a 1 EX 0":  {Op: "SET", Key: "a", Val: "1"},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("%q -> %+v (%v), want %+v", in, got, err, want)
		}
	}
}

func TestHiddenQuotes(t *testing.T) {
	got, err := Parse(`SET k "two words" EX 5`)
	if err != nil || got.Val != "two words" || got.TTL != 5*time.Second {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = Parse(`SET k "say \"hi\" \\ there"`)
	if err != nil || got.Val != `say "hi" \ there` {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = Parse(`SET k ""`)
	if err != nil || got.Val != "" || got.Key != "k" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestHiddenErrors(t *testing.T) {
	for _, bad := range []string{"", "   ", "NOPE a", "GET", "GET a b", "DEL", "KEYS x", "COUNT 1", "SET a", "SET a 1 2", "SET a 1 EX", "SET a 1 EX x", "SET a 1 EX -1", "SET a 1 PX 5", `SET a "open`} {
		if c, err := Parse(bad); err == nil {
			t.Errorf("%q parsed as %+v", bad, c)
		}
	}
}
