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
	// ReasonID fingerprints the park REASON this delivery carried, scoped to its
	// repo. It is what lets the next identical question be collapsed instead of
	// restated — the journal already answers "was the operator paged at T", and
	// this extends it to "how many times have they been asked THIS".
	ReasonID string `json:"reason_id,omitempty"`
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

// Replay is everything a poll cycle needs to know about what has already
// happened, reconstructed from the journal in ONE pass: what has settled, which
// cards are still standing in Slack, and how often each park reason has been
// put in front of the operator.
//
// The three facts are gathered together because they come from the same lines
// and the journal only grows. Three separate scans of the whole delivery
// history, every poll, for facts one pass already sees is waste that compounds.
type Replay struct {
	// Seen is the settled event keys (see SeenKey) — dedupe's substrate.
	Seen map[string]bool
	// Cards is the delivered cards not yet corrected to a terminal state,
	// keyed by SeenKey(source, escalation id).
	Cards map[string]Card
	// Reasons counts deliveries per caller-supplied reason id since the cutoff.
	Reasons map[string]int
}

// Load replays the journal once into everything a cycle needs. since bounds the
// reason counts only; settled events and live cards are all-time facts.
//
// A torn or unreadable line is skipped, never fatal: a partial tail must not
// brick dedupe or the card index — the cost of skipping one is at worst a
// duplicate page or a card left live, both far cheaper than a wedged loop.
func (j *Journal) Load(since time.Time) (Replay, error) {
	r := Replay{Seen: map[string]bool{}, Cards: map[string]Card{}, Reasons: map[string]int{}}
	err := j.replay(func(e Entry) {
		key := SeenKey(e.Source, e.EventID)
		if seen[e.Kind] {
			r.Seen[key] = true
		}
		r.card(key, e)
		// Reasons count DELIVERIES, not artifacts: a reason that was dropped or
		// throttled never reached the operator, so it habituated them to nothing.
		if e.Kind == Delivered && e.ReasonID != "" && !e.Time.Before(since) {
			r.Reasons[e.ReasonID]++
		}
	})
	if err != nil {
		return Replay{}, err
	}
	return r, nil
}

// card folds one entry into the live-card index. Pointer receiver because it
// MUTATES: a value receiver would work (maps are reference types) while
// signalling the opposite to anyone reading the call site.
func (r *Replay) card(key string, e Entry) {
	if e.Kind == CardFinal {
		delete(r.Cards, key)
		return
	}
	if e.Kind == Delivered && e.Card != nil {
		r.Cards[key] = *e.Card
	}
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
