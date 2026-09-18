package log

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"schedlab/clock"
	"schedlab/errs"
	"schedlab/state"
)

func TestHiddenRoundTrip(t *testing.T) {
	for _, e := range []Entry{
		{Seq: 1, AtMs: 1700000000123, Run: "0000000003e8-000000", Task: "", From: state.None, To: state.Pending, Note: "nightly"},
		{Seq: 2, AtMs: -7, Run: "r", Task: "we ird\n\"task\"\t✓", From: state.Running, To: state.Pending, Note: "retry in 1s\nline2 \\ back \"q\" 世界"},
		{Seq: 3, Run: "r", Task: "t", From: state.Running, To: state.Failed},
	} {
		line := Encode(e)
		if strings.ContainsAny(line, "\n\r") {
			t.Fatalf("newline in %q", line)
		}
		if line != Encode(e) {
			t.Fatal("not deterministic")
		}
		got, err := Decode(line)
		if err != nil || got != e {
			t.Fatalf("got %+v, %v; want %+v", got, err, e)
		}
	}
}

func TestHiddenDecodeRejects(t *testing.T) {
	good := Encode(Entry{Seq: 1, Run: "r", From: state.Pending, To: state.Running})
	if _, err := Decode(good); err != nil {
		t.Fatal(err)
	}
	bad := []Entry{
		{Seq: 0, Run: "r", From: state.Pending, To: state.Running},
		{Seq: -1, Run: "r", From: state.Pending, To: state.Running},
		{Seq: 1, Run: "", From: state.Pending, To: state.Running},
		{Seq: 1, Run: "r", From: "WAITING", To: state.Running},
		{Seq: 1, Run: "r", From: state.Pending, To: state.None},
		{Seq: 1, Run: "r", From: state.Pending, To: "DONE"},
		{Seq: 1, Run: "r", From: state.Pending, To: state.Succeeded},
		{Seq: 1, Run: "r", From: state.Succeeded, To: state.Pending},
		{Seq: 1, Run: "r", From: state.None, To: state.Running},
	}
	for _, e := range bad {
		if _, err := Decode(Encode(e)); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%+v: %v", e, err)
		}
	}
	for _, line := range []string{"", "garbage", good + " trailing", good[:len(good)-1], "{}"} {
		if _, err := Decode(line); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%q: %v", line, err)
		}
	}
}

func TestHiddenReadAll(t *testing.T) {
	a := Encode(Entry{Seq: 1, AtMs: 5, Run: "r", From: state.None, To: state.Pending})
	b := Encode(Entry{Seq: 3, AtMs: 6, Run: "r", Task: "t", From: state.None, To: state.Pending})
	got, err := ReadAll(strings.NewReader("\n" + a + "\n\n" + b + "\n"))
	if err != nil || len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 3 || got[1].Task != "t" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = ReadAll(strings.NewReader(""))
	if err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	_, err = ReadAll(strings.NewReader(a + "\n\nnope\n"))
	if !errors.Is(err, errs.ErrInvalid) || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("bad line: %v", err)
	}
	_, err = ReadAll(strings.NewReader(b + "\n" + a + "\n"))
	if !errors.Is(err, errs.ErrInvalid) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("out of order: %v", err)
	}
	_, err = ReadAll(strings.NewReader(a + "\n" + a + "\n"))
	if !errors.Is(err, errs.ErrInvalid) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("repeated seq: %v", err)
	}
}

func TestHiddenAppend(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	var w bytes.Buffer
	l := New(f, &w)
	e, err := l.Append("r1", "", state.None, state.Pending, "wf")
	if err != nil || e != (Entry{Seq: 1, AtMs: 1000, Run: "r1", From: state.None, To: state.Pending, Note: "wf"}) {
		t.Fatalf("%+v %v", e, err)
	}
	f.Advance(time.Second)
	if _, err := l.Append("", "t", state.None, state.Pending, ""); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("empty run: %v", err)
	}
	if _, err := l.Append("r1", "t", state.Pending, state.Succeeded, ""); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("illegal transition: %v", err)
	}
	if _, err := l.Append("r1", "t", state.None, "NOPE", ""); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("unknown state: %v", err)
	}
	e2, err := l.Append("r1", "t", state.None, state.Pending, "")
	if err != nil || e2.Seq != 2 || e2.AtMs != 2000 {
		t.Fatalf("a refused append used a seq: %+v %v", e2, err)
	}
	_, _ = l.Append("r2", "", state.None, state.Pending, "")
	if got := l.Entries(); len(got) != 3 || got[2].Seq != 3 {
		t.Fatalf("%+v", got)
	}
	if got := l.ForRun("r1"); len(got) != 2 || got[0] != e || got[1] != e2 {
		t.Fatalf("%+v", got)
	}
	if got := l.ForRun("zz"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	want := Encode(e) + "\n" + Encode(e2) + "\n" + Encode(l.Entries()[2]) + "\n"
	if w.String() != want {
		t.Fatalf("writer got %q", w.String())
	}
	nilw := New(f, nil)
	if _, err := nilw.Append("r", "", state.None, state.Pending, ""); err != nil {
		t.Fatal(err)
	}
}

func TestHiddenLastAndLoad(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(0))
	var w bytes.Buffer
	l := New(f, &w)
	_, _ = l.Append("r", "", state.None, state.Pending, "")
	_, _ = l.Append("r", "t", state.None, state.Pending, "")
	_, _ = l.Append("r", "t", state.Pending, state.Running, "attempt 1")
	_, _ = l.Append("r", "t", state.Running, state.Pending, "retry in 1s")
	if st, ok := l.Last("r", "t"); !ok || st != state.Pending {
		t.Fatalf("%q %v", st, ok)
	}
	if st, ok := l.Last("r", ""); !ok || st != state.Pending {
		t.Fatalf("%q %v", st, ok)
	}
	if st, ok := l.Last("r", "other"); ok || st != state.None {
		t.Fatalf("%q %v", st, ok)
	}
	if _, ok := l.Last("zz", ""); ok {
		t.Fatal("unknown run")
	}
	var w2 bytes.Buffer
	loaded, err := Load(f, strings.NewReader(w.String()), &w2)
	if err != nil || w2.Len() != 0 || !reflect.DeepEqual(loaded.Entries(), l.Entries()) {
		t.Fatalf("%v %q", err, w2.String())
	}
	e, err := loaded.Append("r", "t", state.Pending, state.Running, "attempt 2")
	if err != nil || e.Seq != 5 || w2.String() != Encode(e)+"\n" {
		t.Fatalf("%+v %v %q", e, err, w2.String())
	}
	if _, err := Load(f, strings.NewReader("junk\n"), nil); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("bad load: %v", err)
	}
}

func TestHiddenConcurrentAppend(t *testing.T) {
	var w bytes.Buffer
	l := New(clock.NewFake(time.UnixMilli(0)), &w)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := l.Append("r", "", state.None, state.Pending, ""); err != nil {
					t.Error(err)
				}
				_ = l.Entries()
				_, _ = l.Last("r", "")
			}
		}()
	}
	wg.Wait()
	got, err := ReadAll(strings.NewReader(w.String()))
	if err != nil || len(got) != 200 || got[199].Seq != 200 {
		t.Fatalf("%d %v", len(got), err)
	}
	if !reflect.DeepEqual(got, l.Entries()) {
		t.Fatal("writer and entries differ")
	}
}
