package outbox

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"shoplab/errs"
)

func TestHiddenRoundTrip(t *testing.T) {
	for _, e := range []Event{
		{Seq: 1, AtMs: 1700000000123, Kind: "order.created", Fields: map[string]string{"order": "O-1", "total": "21.18"}},
		{Seq: 9, AtMs: -5, Kind: "we ird\n\"kind\"\t✓", Fields: map[string]string{"a b": "line1\nline2", "q": `"quoted" \ back`, "u": "héllo <&> 世界", "": ""}},
		{Seq: 2, Kind: "k", Fields: map[string]string{}},
	} {
		line := Encode(e)
		if strings.ContainsAny(line, "\n\r") {
			t.Fatalf("newline in %q", line)
		}
		got, err := Decode(line)
		if err != nil || !reflect.DeepEqual(got, e) {
			t.Fatalf("got %+v, %v; want %+v", got, err, e)
		}
	}
}

func TestHiddenNilFieldsAndDeterminism(t *testing.T) {
	got, err := Decode(Encode(Event{Seq: 3, Kind: "k"}))
	if err != nil || got.Fields == nil || len(got.Fields) != 0 || got.Seq != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	a := Event{Seq: 1, Kind: "k", Fields: map[string]string{}}
	b := Event{Seq: 1, Kind: "k", Fields: map[string]string{}}
	for _, k := range []string{"z", "a", "m", "b", "y", "c", "x", "d"} {
		a.Fields[k] = k
	}
	for _, k := range []string{"d", "x", "c", "y", "b", "m", "a", "z"} {
		b.Fields[k] = k
	}
	if Encode(a) != Encode(b) {
		t.Fatalf("%q != %q", Encode(a), Encode(b))
	}
}

func TestHiddenDecodeRejects(t *testing.T) {
	good := Encode(Event{Seq: 1, Kind: "k"})
	for _, line := range []string{"", "junk", "{", "[]x", good + " trailing", good + good, Encode(Event{Seq: 1})} {
		if _, err := Decode(line); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Decode(%q) err = %v", line, err)
		}
	}
}

func TestHiddenReadAll(t *testing.T) {
	e1, e2 := Event{Seq: 1, Kind: "a", Fields: map[string]string{}}, Event{Seq: 4, Kind: "b", Fields: map[string]string{"k": "v"}}
	got, err := ReadAll(strings.NewReader(Encode(e1) + "\n\n  \n" + Encode(e2) + "\n"))
	if err != nil || !reflect.DeepEqual(got, []Event{e1, e2}) {
		t.Fatalf("%+v %v", got, err)
	}
	_, err = ReadAll(strings.NewReader(Encode(e1) + "\n\nnope\n"))
	if !errors.Is(err, errs.ErrInvalid) || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("bad line: %v", err)
	}
	_, err = ReadAll(strings.NewReader(Encode(e2) + "\n" + Encode(e2) + "\n"))
	if !errors.Is(err, errs.ErrInvalid) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("repeated seq: %v", err)
	}
	if got, err := ReadAll(strings.NewReader("")); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
}

func TestHiddenLogAppend(t *testing.T) {
	now := time.UnixMilli(5000)
	var w bytes.Buffer
	l := NewLog(func() time.Time { return now }, &w)
	if _, err := l.Append("", nil); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("empty kind: %v", err)
	}
	fields := map[string]string{"order": "O-1"}
	e, err := l.Append("order.paid", fields)
	if err != nil || e.Seq != 1 || e.AtMs != 5000 || e.Kind != "order.paid" || e.Fields["order"] != "O-1" {
		t.Fatalf("%+v %v", e, err)
	}
	fields["order"] = "changed"
	now = now.Add(time.Second)
	if e, _ := l.Append("order.shipped", nil); e.Seq != 2 || e.AtMs != 6000 {
		t.Fatalf("%+v", e)
	}
	evs := l.Events()
	if len(evs) != 2 || evs[0].Fields["order"] != "O-1" {
		t.Fatalf("%+v", evs)
	}
	evs[0].Fields["order"] = "scribble"
	if l.Events()[0].Fields["order"] != "O-1" {
		t.Fatal("Events handed out the log's own map")
	}
	if s := l.Since(1); len(s) != 1 || s[0].Seq != 2 {
		t.Fatalf("since 1: %+v", s)
	}
	if s := l.Since(2); len(s) != 0 {
		t.Fatalf("since 2: %+v", s)
	}
	back, err := ReadAll(&w)
	if err != nil || len(back) != 2 || back[0].Fields["order"] != "O-1" || back[1].Kind != "order.shipped" {
		t.Fatalf("written log: %+v %v", back, err)
	}
}

func TestHiddenLogConcurrent(t *testing.T) {
	var w bytes.Buffer
	l := NewLog(time.Now, &w)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Append("tick", map[string]string{"k": "v"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	evs := l.Events()
	if len(evs) != 50 {
		t.Fatalf("%d events", len(evs))
	}
	for i, e := range evs {
		if e.Seq != int64(i)+1 {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
	}
	if back, err := ReadAll(&w); err != nil || len(back) != 50 {
		t.Fatalf("written: %d %v", len(back), err)
	}
}
