// Package journal is flare's private state under ~/.flare: an append-only
// delivery journal (the dedupe substrate and the answer to "was the operator
// paged at T"), and the per-source cursors with the last-poll liveness fact.
// No other plane reads this directory; flare never writes anywhere else.
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry kinds: what happened to one event (or, for CursorAlert, to a
// source's cursor).
const (
	Delivered   = "delivered"
	Dropped     = "dropped"
	Throttled   = "skipped-throttle"
	CursorAlert = "cursor-alert"
	Errored     = "error"
)

// seen reports which entry kinds settle an event: settled events are never
// re-notified, errored ones are retried because the cursor holds.
var seen = map[string]bool{Delivered: true, Dropped: true, Throttled: true}

// SeenKey composes an event's dedupe key. Event IDs are only unique within a
// single source's log (a gate artifact ID, a receipt key+outcome), so dedupe is
// scoped by source: the same producer-local ID from two configured sources is
// two distinct facts, not a duplicate to suppress.
func SeenKey(source, id string) string {
	return source + "\x00" + id
}

// Entry is one journaled delivery fact.
type Entry struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Source   string    `json:"source"`
	EventID  string    `json:"event_id"`
	Channel  string    `json:"channel,omitempty"`
	Severity string    `json:"severity,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// Journal is flare's private state directory.
type Journal struct {
	dir string
}

// Open ensures the state directory exists and returns the journal over it.
func Open(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("journal: create %s: %w", dir, err)
	}
	return &Journal{dir: dir}, nil
}

func (j *Journal) path() string { return filepath.Join(j.dir, "journal.jsonl") }

// Append records one entry at the end of the journal.
func (j *Journal) Append(e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("journal: encode: %w", err)
	}
	f, err := os.OpenFile(j.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("journal: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("journal: append: %w", err)
	}
	return nil
}

// Seen replays the journal into the set of settled event keys (see SeenKey).
// Rebuilding from the journal keeps dedupe truthful across restarts and
// resweeps: only what was actually delivered (or explicitly dropped/throttled)
// is skipped.
func (j *Journal) Seen() (map[string]bool, error) {
	f, err := os.Open(j.path())
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: open: %w", err)
	}
	defer f.Close()
	ids := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // a torn tail line must not brick dedupe
		}
		if seen[e.Kind] {
			ids[SeenKey(e.Source, e.EventID)] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("journal: scan: %w", err)
	}
	return ids, nil
}

// Tail returns the last n entries, oldest first.
func (j *Journal) Tail(n int) ([]Entry, error) {
	f, err := os.Open(j.path())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: open: %w", err)
	}
	defer f.Close()
	var entries []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	return entries, nil
}
