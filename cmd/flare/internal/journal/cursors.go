package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

// ErrCursorsCorrupt reports that cursors.json exists but does not parse. It is
// recoverable, not fatal: the caller quarantines the bad file and resweeps from
// an empty cursor (dedupe via the journal prevents re-paging), rather than
// wedging the watch loop on every cycle — the failure mode that silenced flare
// when two watchers raced a shared temp file into corrupt JSON.
var ErrCursorsCorrupt = errors.New("journal: cursors file is corrupt")

// Cursors is the small mutable half of flare's state: how far each source
// has been read, and when the watcher last completed a poll (the liveness
// fact `flare status` checks — presence must be a positive signal).
type Cursors struct {
	LastPoll time.Time                `json:"last_poll"`
	Sources  map[string]source.Cursor `json:"sources"`
	// Stalled records the sources whose cursor is stuck behind something that
	// will not settle. It is the fact `flare status` was missing: a fresh
	// last_poll proves the loop is RUNNING, not that anything is getting
	// through, and the two are different kinds of healthy.
	Stalled map[string]Stall `json:"stalled,omitempty"`
}

// Stall is why one source stopped advancing. The cursor is ordered, so an event
// that cannot be delivered blocks every event after it — a stall is a delivery
// outage for that source, not a single lost page.
type Stall struct {
	Since    time.Time `json:"since"`
	EventID  string    `json:"event_id,omitempty"`
	Attempts int       `json:"attempts"`
	Note     string    `json:"note"`
}

// Stall records or extends a source's stall, preserving when it began so the
// operator can see how long the outage has run.
func (c *Cursors) Stall(source, eventID, note string, now time.Time) {
	if c.Stalled == nil {
		c.Stalled = map[string]Stall{}
	}
	s, ok := c.Stalled[source]
	if !ok {
		s = Stall{Since: now}
	}
	s.EventID, s.Note, s.Attempts = eventID, note, s.Attempts+1
	c.Stalled[source] = s
}

// Clear drops a source's stall once it polls cleanly again.
func (c *Cursors) Clear(source string) { delete(c.Stalled, source) }

func (j *Journal) cursorsPath() string { return filepath.Join(j.dir, "cursors.json") }

// LoadCursors reads the cursors file. A missing file is a fresh, empty state,
// not an error. A file that exists but does not parse returns an empty state
// with ErrCursorsCorrupt and leaves the bad file on disk untouched — quarantine
// and resweep are the caller's to own, so a read never mutates state and never
// wedges.
func (j *Journal) LoadCursors() (Cursors, error) {
	fresh := Cursors{Sources: map[string]source.Cursor{}}
	raw, err := os.ReadFile(j.cursorsPath())
	if os.IsNotExist(err) {
		return fresh, nil
	}
	if err != nil {
		return Cursors{}, fmt.Errorf("journal: read cursors: %w", err)
	}
	var c Cursors
	if err := json.Unmarshal(raw, &c); err != nil {
		return fresh, ErrCursorsCorrupt
	}
	if c.Sources == nil {
		c.Sources = map[string]source.Cursor{}
	}
	return c, nil
}

// QuarantineCursors moves a corrupt cursors file aside for forensics, leaving
// no cursors.json so the next save starts clean. It returns the backup path (a
// unique per-nanosecond suffix, so repeated quarantines never collide). A
// missing file is a no-op (empty path, nil error).
func (j *Journal) QuarantineCursors() (string, error) {
	bak := fmt.Sprintf("%s.corrupt-%d", j.cursorsPath(), time.Now().UnixNano())
	if err := os.Rename(j.cursorsPath(), bak); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("journal: quarantine cursors: %w", err)
	}
	return bak, nil
}

// SaveCursors replaces the cursors file atomically (write-then-rename), so a
// crash mid-save never leaves a torn cursor. The temp file is unique per write
// (os.CreateTemp), never a shared fixed name — so two writers can never
// interleave into the same temp and rename a corrupt result. The single-instance
// lock (LockWatch) is the primary guard against concurrent writers; this is
// defense in depth.
func (j *Journal) SaveCursors(c Cursors) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("journal: encode cursors: %w", err)
	}
	tmp, err := os.CreateTemp(j.dir, "cursors-*.json.tmp")
	if err != nil {
		return fmt.Errorf("journal: temp cursors: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has moved it
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("journal: write cursors: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("journal: write cursors: %w", err)
	}
	if err := os.Rename(tmpName, j.cursorsPath()); err != nil {
		return fmt.Errorf("journal: replace cursors: %w", err)
	}
	return nil
}
