// Package evidence is the retained record of effect attempts: an
// append-only JSON-lines journal at <workspace>/.wb/journal.jsonl.
//
// It is the only persistent state wb adds. Resources never need it (every
// plan observes them directly); tasks do, because one-shot work leaves no
// observable trace of which inputs produced its output. A task's receipt is
// simply its latest journal entry.
package evidence

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Event is the phase of one effect attempt.
type Event string

// The phases of an effect attempt. A Started entry with no later terminal
// entry for the same run and address means the process died mid-effect.
const (
	Started   Event = "started"
	Succeeded Event = "succeeded"
	Failed    Event = "failed"
)

// Entry is one retained fact about an effect attempt. Entries are never
// rewritten.
type Entry struct {
	Seq     int               `json:"seq"`
	Run     int               `json:"run"`
	Time    time.Time         `json:"time"`
	Address string            `json:"address"`
	Action  string            `json:"action"`
	Event   Event             `json:"event"`
	Config  map[string]any    `json:"config,omitempty"`
	Inputs  map[string]string `json:"inputs,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
	// Upstream is each referenced block's observed state when a task ran.
	Upstream map[string]string `json:"upstream,omitempty"`
	Fact     string            `json:"fact,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// File is the journal location relative to the workspace root.
var File = filepath.Join(".wb", "journal.jsonl")

// Journal is the evidence for one workspace, loaded in memory.
type Journal struct {
	path    string
	entries []Entry
	// Unreadable counts lines that did not parse, e.g. an append torn by a
	// crash. They are kept on disk and ignored.
	Unreadable int
	needsLF    bool
}

// Open loads the journal under root. A missing journal is empty; nothing is
// created until the first Append.
func Open(root string) (*Journal, error) {
	j := &Journal{path: filepath.Join(root, File)}
	b, err := os.ReadFile(j.path)
	if errors.Is(err, fs.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	j.needsLF = len(b) > 0 && b[len(b)-1] != '\n'
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		j.load(sc.Bytes())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	return j, nil
}

func (j *Journal) load(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var e Entry
	if err := json.Unmarshal(line, &e); err != nil {
		j.Unreadable++
		return
	}
	j.entries = append(j.entries, e)
}

// Entries returns every readable entry in append order.
func (j *Journal) Entries() []Entry { return j.entries }

// Latest returns the most recent entry for address.
func (j *Journal) Latest(address string) (Entry, bool) {
	for i := len(j.entries) - 1; i >= 0; i-- {
		if j.entries[i].Address == address {
			return j.entries[i], true
		}
	}
	return Entry{}, false
}

// LatestSucceeded returns the most recent successful entry for address.
func (j *Journal) LatestSucceeded(address string) (Entry, bool) {
	for i := len(j.entries) - 1; i >= 0; i-- {
		if j.entries[i].Address == address && j.entries[i].Event == Succeeded {
			return j.entries[i], true
		}
	}
	return Entry{}, false
}

// NextRun is the number the next apply records its entries under.
func (j *Journal) NextRun() int {
	run := 0
	for _, e := range j.entries {
		run = max(run, e.Run)
	}
	return run + 1
}

// Append durably records e (fsync before returning), assigning its Seq and
// Time.
func (j *Journal) Append(e Entry) error {
	if err := os.MkdirAll(filepath.Dir(j.path), 0o755); err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	e.Seq = 1
	if n := len(j.entries); n > 0 {
		e.Seq = j.entries[n-1].Seq + 1
	}
	e.Time = time.Now().UTC()
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	if j.needsLF {
		line = append([]byte{'\n'}, line...)
	}
	if err := appendSync(j.path, append(line, '\n')); err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	j.needsLF = false
	j.entries = append(j.entries, e)
	return nil
}

func appendSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close() // the write error is the one to report
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // the sync error is the one to report
		return err
	}
	return f.Close()
}
