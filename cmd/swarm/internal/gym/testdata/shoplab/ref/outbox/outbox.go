package outbox

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"shoplab/errs"
)

type Event struct {
	Seq    int64
	AtMs   int64
	Kind   string
	Fields map[string]string
}

func Encode(e Event) string {
	b, _ := json.Marshal(e)
	return string(b)
}

func Decode(line string) (Event, error) {
	var e Event
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Event{}, fmt.Errorf("outbox: %v: %w", err, errs.ErrInvalid)
	}
	if dec.More() {
		return Event{}, fmt.Errorf("outbox: trailing data: %w", errs.ErrInvalid)
	}
	if e.Kind == "" {
		return Event{}, fmt.Errorf("outbox: empty kind: %w", errs.ErrInvalid)
	}
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	return e, nil
}

func ReadAll(r io.Reader) ([]Event, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	var out []Event
	ln := 0
	for sc.Scan() {
		ln++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		e, err := Decode(line)
		if err != nil {
			return nil, fmt.Errorf("outbox: line %d: %w", ln, err)
		}
		if len(out) > 0 && e.Seq <= out[len(out)-1].Seq {
			return nil, fmt.Errorf("outbox: line %d: seq %d out of order: %w", ln, e.Seq, errs.ErrInvalid)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

type Log struct {
	mu     sync.Mutex
	now    func() time.Time
	w      io.Writer
	events []Event
}

func NewLog(now func() time.Time, w io.Writer) *Log { return &Log{now: now, w: w} }

func clone(e Event) Event {
	f := make(map[string]string, len(e.Fields))
	for k, v := range e.Fields {
		f[k] = v
	}
	e.Fields = f
	return e
}

func (l *Log) Append(kind string, fields map[string]string) (Event, error) {
	if kind == "" {
		return Event{}, fmt.Errorf("outbox: empty kind: %w", errs.ErrInvalid)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := clone(Event{Seq: int64(len(l.events)) + 1, AtMs: l.now().UnixMilli(), Kind: kind, Fields: fields})
	l.events = append(l.events, e)
	if l.w != nil {
		_, _ = io.WriteString(l.w, Encode(e)+"\n")
	}
	return clone(e), nil
}

func (l *Log) Events() []Event { return l.Since(0) }

func (l *Log) Since(seq int64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Event{}
	for _, e := range l.events {
		if e.Seq > seq {
			out = append(out, clone(e))
		}
	}
	return out
}
