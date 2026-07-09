package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

// Cursors is the small mutable half of flare's state: how far each source
// has been read, and when the watcher last completed a poll (the liveness
// fact `flare status` checks — presence must be a positive signal).
type Cursors struct {
	LastPoll time.Time                `json:"last_poll"`
	Sources  map[string]source.Cursor `json:"sources"`
}

func (j *Journal) cursorsPath() string { return filepath.Join(j.dir, "cursors.json") }

// LoadCursors reads the cursors file; a missing file is a fresh, empty
// state, not an error.
func (j *Journal) LoadCursors() (Cursors, error) {
	raw, err := os.ReadFile(j.cursorsPath())
	if os.IsNotExist(err) {
		return Cursors{Sources: map[string]source.Cursor{}}, nil
	}
	if err != nil {
		return Cursors{}, fmt.Errorf("journal: read cursors: %w", err)
	}
	var c Cursors
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursors{}, fmt.Errorf("journal: parse cursors: %w", err)
	}
	if c.Sources == nil {
		c.Sources = map[string]source.Cursor{}
	}
	return c, nil
}

// SaveCursors replaces the cursors file atomically (write-then-rename), so a
// crash mid-save never leaves a torn cursor.
func (j *Journal) SaveCursors(c Cursors) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("journal: encode cursors: %w", err)
	}
	tmp := j.cursorsPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("journal: write cursors: %w", err)
	}
	if err := os.Rename(tmp, j.cursorsPath()); err != nil {
		return fmt.Errorf("journal: replace cursors: %w", err)
	}
	return nil
}
