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

// JournalPath is where a workspace retains evidence, inside StateDir.
const JournalPath = StateDir + "/journal.jsonl"

// Journal operations.
const (
	OpConverge = "converge" // a kept resource was created or updated
	OpStart    = "start"    // one-shot work began; written before the effect
	OpFinish   = "finish"   // that work completed and left its output
	OpFail     = "fail"     // that work failed; its output was not replaced
)

// Record is one line of retained evidence. wb appends records and never
// rewrites or deletes them.
type Record struct {
	Seq     int               `json:"seq"`
	At      string            `json:"at"`
	Op      string            `json:"op"`
	ID      string            `json:"id"`
	Kind    string            `json:"kind"`
	Action  string            `json:"action,omitempty"`  // converge: create or update
	Subject string            `json:"subject,omitempty"` // the path the effect touched
	Def     string            `json:"def,omitempty"`     // start: digest of the declaration
	Inputs  map[string]string `json:"inputs,omitempty"`  // start: input versions it ran against
	Attempt int               `json:"attempt,omitempty"` // finish, fail: seq of the start record
	After   string            `json:"after,omitempty"`   // converge, finish: digest the effect left
	Detail  string            `json:"detail,omitempty"`  // fail: what went wrong
}

// Journal is the workspace's append-only evidence log.
type Journal struct {
	ws      *Workspace
	records []Record
	skipped int
	next    int
}

// Journal reads the workspace journal. A missing journal reads as empty and
// is not created: planning never writes.
func (w *Workspace) Journal() (*Journal, error) {
	if err := w.checkState(); err != nil {
		return nil, err
	}
	j := &Journal{ws: w, next: 1}
	data, err := w.root.ReadFile(JournalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		j.load(line)
	}
	return j, nil
}

func (j *Journal) load(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var r Record
	if err := json.Unmarshal(line, &r); err != nil || r.Seq < 1 {
		j.skipped++
		return
	}
	j.records = append(j.records, r)
	j.next = max(j.next, r.Seq+1)
}

// Records returns the readable records in file order.
func (j *Journal) Records() []Record { return j.records }

// Skipped counts unreadable lines, such as a record torn by a crash.
func (j *Journal) Skipped() int { return j.skipped }

// Append durably adds one record, assigning its sequence number and time.
func (j *Journal) Append(r Record) (Record, error) {
	r.Seq = j.next
	r.At = time.Now().UTC().Format(time.RFC3339)
	line, err := json.Marshal(r)
	if err != nil {
		return Record{}, err
	}
	if err := j.ws.checkState(); err != nil {
		return Record{}, err
	}
	if err := j.ws.root.MkdirAll(StateDir, 0o755); err != nil {
		return Record{}, err
	}
	seal, err := j.sealTail()
	if err != nil {
		return Record{}, err
	}
	if err := j.write(append(append(seal, line...), '\n')); err != nil {
		return Record{}, err
	}
	j.records = append(j.records, r)
	j.next++
	return r, nil
}

func (j *Journal) write(b []byte) error {
	f, err := j.ws.root.OpenFile(JournalPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// checkState refuses a state directory or journal that is not what wb
// creates, such as a symlink, so evidence is never read from or appended to
// a file wb does not own.
func (w *Workspace) checkState() error {
	dir, err := w.root.Lstat(StateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !dir.IsDir() {
		return fmt.Errorf("%s is not a real directory; wb will not keep evidence there", StateDir)
	}
	file, err := w.root.Lstat(JournalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !file.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file; wb will not read or append evidence there", JournalPath)
	}
	return nil
}

// sealTail returns a newline when the journal ends mid-line (a write torn by
// a crash), so the torn fragment stays one unreadable line instead of
// corrupting the record appended after it.
func (j *Journal) sealTail() ([]byte, error) {
	f, err := j.ws.root.Open(JournalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return nil, err
	}
	if last[0] == '\n' {
		return nil, nil
	}
	return []byte("\n"), nil
}
