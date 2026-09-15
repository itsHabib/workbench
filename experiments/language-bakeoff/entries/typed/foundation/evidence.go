package foundation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// journalName is the evidence journal, relative to the workspace.
const journalName = reservedDir + "/evidence.jsonl"

// Record events and statuses.
const (
	EventStart   = "start"  // a task run began
	EventFinish  = "finish" // a task run ended; Status says how
	EventApply   = "apply"  // a resource change was made
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// Record is one evidence journal entry: a retained fact about an effect
// that happened. The journal is append-only; nothing rewrites history.
type Record struct {
	Seq         int               `json:"seq"`
	Event       string            `json:"event"` // start | finish | apply
	Subject     string            `json:"subject"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	Status      string            `json:"status,omitempty"` // ok | failed, on finish
	Output      string            `json:"output,omitempty"` // digest of what the effect produced
	Detail      string            `json:"detail,omitempty"`
	At          string            `json:"at,omitempty"`
}

// Evidence is the journal as read.
type Evidence struct {
	Records []Record
	// Unreadable counts lines that did not parse, such as a line torn by a
	// crash mid-append. They are skipped: a lost finish record only makes
	// its work run again, which the replay rules already allow.
	Unreadable int
}

// Head is the sequence number of the newest record, 0 when empty.
func (e Evidence) Head() int {
	if len(e.Records) == 0 {
		return 0
	}
	return e.Records[len(e.Records)-1].Seq
}

// Evidence reads the whole journal. A missing journal is empty.
func (w *Workspace) Evidence() (Evidence, error) {
	raw, err := w.journal()
	if err != nil {
		return Evidence{}, err
	}
	return parse(raw), nil
}

// Record appends r with the next sequence number and the current time and
// returns it as written. There is no fsync and no cross-process lock: one
// writer per workspace at a time is assumed.
func (w *Workspace) Record(r Record) (Record, error) {
	raw, err := w.journal()
	if err != nil {
		return Record{}, err
	}
	r.Seq = parse(raw).Head() + 1
	r.At = time.Now().UTC().Format(time.RFC3339)
	line, err := json.Marshal(r)
	if err != nil {
		return Record{}, err
	}
	// Terminate a torn line left by a crash so the fragment stays one
	// unreadable line instead of swallowing this record.
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		line = append([]byte("\n"), line...)
	}
	if err := w.root.MkdirAll(reservedDir, 0o755); err != nil {
		return Record{}, fmt.Errorf("create evidence dir: %w", err)
	}
	f, err := w.root.OpenFile(journalName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return Record{}, fmt.Errorf("open evidence: %w", err)
	}
	_, err = f.Write(append(line, '\n'))
	if err = errors.Join(err, f.Close()); err != nil {
		return Record{}, fmt.Errorf("append evidence: %w", err)
	}
	return r, nil
}

func (w *Workspace) journal() ([]byte, error) {
	raw, err := w.root.ReadFile(journalName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read evidence: %w", err)
	}
	return raw, nil
}

// parse keeps every line that decodes to a record with a sequence number
// above the previous one; everything else is counted as unreadable.
func parse(raw []byte) Evidence {
	var ev Evidence
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r Record
		if json.Unmarshal(line, &r) != nil || r.Seq <= ev.Head() {
			ev.Unreadable++
			continue
		}
		ev.Records = append(ev.Records, r)
	}
	return ev
}
