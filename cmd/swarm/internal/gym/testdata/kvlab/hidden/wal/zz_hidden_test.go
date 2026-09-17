package wal

import (
	"strings"
	"testing"
	"time"

	"kvlab/store"
)

func TestHiddenRoundTrip(t *testing.T) {
	for _, e := range []Entry{
		{Op: "set", Key: "a", Val: "1"},
		{Op: "set", Key: "a b", Val: "x\ty\nz \"q\" \\ é 日本", TTLms: 1500},
		{Op: "set", Key: "", Val: ""},
		{Op: "del", Key: "k\n2"},
	} {
		line := Encode(e)
		if strings.ContainsAny(line, "\n\r") {
			t.Fatalf("newline in %q", line)
		}
		got, err := Decode(line)
		if err != nil || got != e {
			t.Fatalf("round trip %+v -> %+v (%v)", e, got, err)
		}
	}
}

func TestHiddenDecodeGarbage(t *testing.T) {
	for _, bad := range []string{"", "garbage", "set"} {
		if _, err := Decode(bad); err == nil {
			t.Fatalf("decoded %q", bad)
		}
	}
}

func TestHiddenReplay(t *testing.T) {
	now := time.Unix(1000, 0)
	s := store.New(func() time.Time { return now })
	log := Encode(Entry{Op: "set", Key: "a", Val: "1"}) + "\n\n" +
		Encode(Entry{Op: "set", Key: "b", Val: "two words", TTLms: 2000}) + "\n" +
		Encode(Entry{Op: "del", Key: "a"}) + "\n"
	n, err := Replay(strings.NewReader(log), s)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("a survived")
	}
	if v, _ := s.Get("b"); v != "two words" {
		t.Fatalf("b=%q", v)
	}
	now = now.Add(2 * time.Second)
	if _, ok := s.Get("b"); ok {
		t.Fatal("ttl lost in replay")
	}
}

func TestHiddenReplayBadLine(t *testing.T) {
	s := store.New(time.Now)
	log := Encode(Entry{Op: "set", Key: "a", Val: "1"}) + "\n\n!!! not an entry\n" + Encode(Entry{Op: "set", Key: "z", Val: "1"}) + "\n"
	n, err := Replay(strings.NewReader(log), s)
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("err=%v", err)
	}
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	if _, ok := s.Get("z"); ok {
		t.Fatal("kept going after a bad line")
	}
}
