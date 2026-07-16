// Package source lifts events out of producers' append-only JSONL logs.
// Reads are raw and read-only: no producer lock is taken, a torn final line
// is left for the next poll, and nothing is ever written near a producer.
package source

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/contracts"
)

// Cursor marks how far into a source file flare has read. LastHash pins the
// last processed gate artifact so upstream rewrite/truncation is detected,
// not silently skipped.
type Cursor struct {
	Offset   int64  `json:"offset"`
	LastHash string `json:"last_hash,omitempty"`
}

// Read returns the push-worthy events appended since cur, plus the advanced
// cursor. When the file has been truncated or the hash chain no longer
// matches the cursor, Read emits a cursor-alert event and resweeps from the
// start — the caller's dedupe prevents re-paging, and the alert itself is
// routed like any other event (a broken chain must not read as calm).
func Read(src config.Source, cur Cursor) ([]event.Event, Cursor, error) {
	raw, err := os.ReadFile(src.Path)
	if err != nil {
		return nil, cur, fmt.Errorf("source %s: read %s: %w", src.Name, src.Path, err)
	}
	var alerts []event.Event
	if int64(len(raw)) < cur.Offset {
		alerts = append(alerts, alert(src, fmt.Sprintf("log shrank below cursor (%d < %d): truncated or rewritten upstream; resweeping", len(raw), cur.Offset)))
		cur = Cursor{}
	}
	lines, size := completeLines(raw[cur.Offset:])
	if len(lines) == 0 {
		return alerts, cur, nil
	}
	if bad := chainBreak(src, cur, lines[0]); bad != "" {
		alerts = append(alerts, alert(src, bad))
		cur = Cursor{}
		lines, size = completeLines(raw)
	}
	events, last, err := parse(src, lines)
	if err != nil {
		// A parse failure must not swallow a pending integrity alert: return it
		// so the caller still routes the cursor-alert (a broken chain reaching
		// only stderr is the silent failure this guards against).
		return alerts, cur, err
	}
	next := Cursor{Offset: cur.Offset + size, LastHash: last}
	if next.LastHash == "" {
		next.LastHash = cur.LastHash
	}
	return append(alerts, events...), next, nil
}

// completeLines splits raw into whole lines, dropping a torn final line (a
// writer may be mid-append; it is picked up next poll). Returns the byte
// size consumed, including newlines.
func completeLines(raw []byte) ([]string, int64) {
	s := string(raw)
	end := strings.LastIndexByte(s, '\n')
	if end < 0 {
		return nil, 0
	}
	var lines []string
	for _, l := range strings.Split(s[:end], "\n") {
		l = strings.TrimSuffix(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, l)
	}
	return lines, int64(end + 1)
}

// chainBreak reports why the first new line does not follow the cursor, or
// "" when it does. Gate logs verify the hash chain; ship receipts have no
// chain, so integrity is best-effort via a valid-record check at the offset.
func chainBreak(src config.Source, cur Cursor, first string) string {
	if src.Kind == config.SourceGateLog && cur.LastHash != "" {
		var env contracts.Envelope
		if err := json.Unmarshal([]byte(first), &env); err != nil {
			return fmt.Sprintf("unparseable line at cursor: %v; resweeping", err)
		}
		if env.Prev != cur.LastHash {
			return fmt.Sprintf("hash chain broke at cursor (prev %.12s != last %.12s): rewritten upstream; resweeping", env.Prev, cur.LastHash)
		}
		return ""
	}
	if src.Kind == config.SourceShipReceipts && cur.Offset > 0 {
		var r receipt
		if err := json.Unmarshal([]byte(first), &r); err != nil {
			// No hash chain to verify continuity; a decodable receipt at the
			// offset is the guard against a mid-line / rewritten cursor.
			return fmt.Sprintf("unparseable receipt at cursor: %v; resweeping", err)
		}
	}
	return ""
}

func parse(src config.Source, lines []string) ([]event.Event, string, error) {
	if src.Kind == config.SourceShipReceipts {
		return parseReceipts(src, lines)
	}
	return parseGateLog(src, lines)
}

// alert builds a cursor-integrity event. Its ID is a stable hash of the note,
// not a timestamp: the same failure re-detected on a held cursor produces the
// same ID, so dedupe suppresses it and a persistent chain break pages once, not
// every poll. A genuinely different failure yields a different note, hence a
// different ID, and pages.
func alert(src config.Source, note string) event.Event {
	return event.Event{
		Source:   src.Name,
		ID:       fmt.Sprintf("cursor-alert:%s:%08x", src.Name, noteHash(note)),
		Kind:     "cursor-alert",
		Time:     time.Now(),
		Severity: event.SevEscalate,
		Title:    fmt.Sprintf("flare: %s cursor integrity", src.Name),
		Body:     note,
		Fields:   map[string]string{},
	}
}

func noteHash(note string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(note))
	return h.Sum32()
}
