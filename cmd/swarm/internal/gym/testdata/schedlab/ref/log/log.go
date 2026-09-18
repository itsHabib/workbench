package log

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"schedlab/clock"
	"schedlab/errs"
	"schedlab/state"
)

type Entry struct {
	Seq, AtMs int64
	Run, Task string
	From, To  state.State
	Note      string
}

func Encode(e Entry) string {
	return strings.Join([]string{
		strconv.FormatInt(e.Seq, 10), strconv.FormatInt(e.AtMs, 10),
		strconv.Quote(e.Run), strconv.Quote(e.Task), string(e.From), string(e.To), strconv.Quote(e.Note),
	}, "\t")
}

func named(s state.State) bool {
	for _, st := range state.All() {
		if st == s {
			return true
		}
	}
	return false
}

func validate(e Entry) error {
	switch {
	case e.Seq < 1:
		return fmt.Errorf("log: seq %d: %w", e.Seq, errs.ErrInvalid)
	case e.Run == "":
		return fmt.Errorf("log: empty run: %w", errs.ErrInvalid)
	case e.From != state.None && !named(e.From):
		return fmt.Errorf("log: from %q: %w", e.From, errs.ErrInvalid)
	case !named(e.To):
		return fmt.Errorf("log: to %q: %w", e.To, errs.ErrInvalid)
	case !state.CanTransition(e.From, e.To):
		return fmt.Errorf("log: %s -> %s: %w", e.From, e.To, errs.ErrState)
	}
	return nil
}

func Decode(line string) (Entry, error) {
	f := strings.Split(line, "\t")
	if len(f) != 7 {
		return Entry{}, fmt.Errorf("log: want 7 fields, got %d: %w", len(f), errs.ErrInvalid)
	}
	var e Entry
	var err error
	if e.Seq, err = strconv.ParseInt(f[0], 10, 64); err != nil {
		return Entry{}, fmt.Errorf("log: seq: %w", errs.ErrInvalid)
	}
	if e.AtMs, err = strconv.ParseInt(f[1], 10, 64); err != nil {
		return Entry{}, fmt.Errorf("log: at: %w", errs.ErrInvalid)
	}
	strs := [3]*string{&e.Run, &e.Task, &e.Note}
	for i, idx := range []int{2, 3, 6} {
		if *strs[i], err = strconv.Unquote(f[idx]); err != nil {
			return Entry{}, fmt.Errorf("log: field %d: %w", idx, errs.ErrInvalid)
		}
	}
	e.From, e.To = state.State(f[4]), state.State(f[5])
	if err := validate(e); err != nil {
		return Entry{}, fmt.Errorf("%v: %w", err, errs.ErrInvalid)
	}
	return e, nil
}

func ReadAll(r io.Reader) ([]Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	var out []Entry
	ln := 0
	for sc.Scan() {
		ln++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		e, err := Decode(line)
		if err != nil {
			return nil, fmt.Errorf("log: line %d: %w", ln, err)
		}
		if len(out) > 0 && e.Seq <= out[len(out)-1].Seq {
			return nil, fmt.Errorf("log: line %d: seq %d out of order: %w", ln, e.Seq, errs.ErrInvalid)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

type Log struct {
	mu      sync.Mutex
	c       clock.Clock
	w       io.Writer
	entries []Entry
}

func New(c clock.Clock, w io.Writer) *Log { return &Log{c: c, w: w} }

func Load(c clock.Clock, r io.Reader, w io.Writer) (*Log, error) {
	entries, err := ReadAll(r)
	if err != nil {
		return nil, err
	}
	return &Log{c: c, w: w, entries: entries}, nil
}

func (l *Log) Append(run, task string, from, to state.State, note string) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Entry{Seq: 1, AtMs: clock.Millis(l.c), Run: run, Task: task, From: from, To: to, Note: note}
	if n := len(l.entries); n > 0 {
		e.Seq = l.entries[n-1].Seq + 1
	}
	if err := validate(e); err != nil {
		return Entry{}, fmt.Errorf("%v: %w", err, errs.ErrInvalid)
	}
	if l.w != nil {
		if _, err := io.WriteString(l.w, Encode(e)+"\n"); err != nil {
			return Entry{}, err
		}
	}
	l.entries = append(l.entries, e)
	return e, nil
}

func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Entry(nil), l.entries...)
}

func (l *Log) ForRun(run string) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for _, e := range l.entries {
		if e.Run == run {
			out = append(out, e)
		}
	}
	return out
}

func (l *Log) Last(run, task string) (state.State, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		if e := l.entries[i]; e.Run == run && e.Task == task {
			return e.To, true
		}
	}
	return state.None, false
}
