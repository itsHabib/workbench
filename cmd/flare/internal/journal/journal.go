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

// Entry kinds: what happened to one event (or, for CursorAlert and
// CursorInit, to a source's cursor). CursorInit records where a source with no
// cursor was first placed — the one written fact that says "the history before
// this offset was deliberately not delivered".
const (
	Delivered   = "delivered"
	Dropped     = "dropped"
	Throttled   = "skipped-throttle"
	CursorAlert = "cursor-alert"
	CursorInit  = "cursor-init"
	Errored     = "error"
	// CardUpdate settles a card-update event: the terminal state of a park was
	// applied to the card(s) showing it (or there was no live card to apply it
	// to, which is equally settled — the fact has been dealt with).
	CardUpdate = "card-updated"
	// CardFinal closes one delivered card. It is keyed on the ESCALATION's event
	// id, not the update's, so replaying the journal knows which cards are still
	// live and which have already been corrected.
	CardFinal = "card-final"
)

// seen reports which entry kinds settle an event: settled events are never
// re-notified, errored ones are retried because the cursor holds. Cursor
// placements and alerts about flare's own state (CursorInit) carry no event
// and are deliberately not in this set.
var seen = map[string]bool{Delivered: true, Dropped: true, Throttled: true, CardUpdate: true}

// SeenKey composes an event's dedupe key. Event IDs are only unique within a
// single source's log (a gate artifact ID, a receipt key+outcome), so dedupe is
// scoped by source: the same producer-local ID from two configured sources is
// two distinct facts, not a duplicate to suppress.
func SeenKey(source, id string) string {
	return source + "\x00" + id
}

// Card locates one delivered Slack message so a later fact can correct it.
// Without it a card can never be updated, and a card that is never updated goes
// on showing live Approve buttons for a park that resolved hours ago.
//
// Subject is the "repo#n" the card was rendered for. gate's inbox reduces
// parked runs by subject — only the latest terminal per PR is still parked — so
// the subject, not the escalation id alone, is what says which cards a later
// park or merge makes stale.
type Card struct {
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	TS        string `json:"ts"`
	Subject   string `json:"subject,omitempty"`
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
	// Card is set on a Delivered entry whose channel returned an updatable
	// message ref. Additive and omitempty: an entry written before cards existed
	// simply has none, and replays as a delivery with no correctable card.
	Card *Card `json:"card,omitempty"`
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

// LiveCards replays the journal into the delivered cards that have NOT yet been
// corrected to a terminal state — the ones whose buttons are still live in
// Slack. It is the same substrate as Seen and for the same reason: the journal
// is the only durable record of what flare put in front of the operator, so
// what is still standing there must be reconstructable across a restart.
//
// Keys are SeenKey(source, escalation id).
func (j *Journal) LiveCards() (map[string]Card, error) {
	cards := map[string]Card{}
	err := j.replay(func(e Entry) {
		key := SeenKey(e.Source, e.EventID)
		if e.Kind == CardFinal {
			delete(cards, key)
			return
		}
		if e.Kind == Delivered && e.Card != nil {
			cards[key] = *e.Card
		}
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// replay walks every well-formed entry oldest-first. A torn or unreadable line
// is skipped, never fatal: a partial tail must not brick dedupe or the card
// index — the cost of skipping one is at worst a duplicate page or a card left
// live, both far cheaper than a wedged loop.
func (j *Journal) replay(visit func(Entry)) error {
	f, err := os.Open(j.path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("journal: open: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		visit(e)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("journal: scan: %w", err)
	}
	return nil
}
