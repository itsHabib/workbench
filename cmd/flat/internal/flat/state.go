// Package flat is the substrate for a fleet of coding agents that has no
// management sessions: a board derived from git, a decision ledger, claims
// with fencing epochs, resource leases, admission, a watcher that renders and
// nudges, and a hook that delivers nudges into a session.
//
// All state is files under one directory shared by every worktree of a
// repository. Nothing is remembered by an agent; everything is re-derivable.
package flat

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// State is the shared directory. Default: <git common dir>/flat, so every
// worktree of one repository shares it without configuration. FLAT_STATE
// overrides.
type State struct {
	Dir  string
	Repo string // a checkout of the repository (cwd at open)
}

// Open resolves the state directory for cwd.
func Open(cwd string) (*State, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	dir := os.Getenv("FLAT_STATE")
	if dir == "" {
		common, err := Git(abs, "rev-parse", "--git-common-dir")
		if err != nil {
			return nil, fmt.Errorf("not inside a git repository (%s); set FLAT_STATE to use flat elsewhere", abs)
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(abs, common)
		}
		dir = filepath.Join(filepath.Clean(common), "flat")
	}
	for _, sub := range []string{"requests", "resources", "inbox", "watch", "sessions"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &State{Dir: dir, Repo: abs}, nil
}

func (s *State) path(parts ...string) string {
	return filepath.Join(append([]string{s.Dir}, parts...)...)
}

// Now is the clock; tests may replace it.
var Now = func() time.Time { return time.Now().UTC() }

// NewID returns a sortable, collision-resistant id with a kind prefix.
func NewID(kind string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%s_%s", kind, strconv.FormatInt(Now().UnixNano(), 36), hex.EncodeToString(b[:]))
}

// writeAtomic writes data to path via a temporary file and rename, so a
// reader never sees a partial file. Rename replaces on every platform Go
// supports.
func writeAtomic(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp.%d.%s", path, os.Getpid(), strconv.FormatInt(Now().UnixNano(), 36))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// lock takes an exclusive create-only lock file for a mutation window. It
// waits up to wait for a rival to release; a lock older than stale is
// treated as abandoned and broken, with the break recorded in the event log.
func (s *State) lock(name string, wait, stale time.Duration) (release func(), err error) {
	path := s.path(name + ".lock")
	deadline := Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), Now().Format(time.RFC3339Nano))
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if Now().Sub(lockTakenAt(path)) > stale {
			_ = os.Remove(path)
			s.appendEvent(Event{Kind: "lock_broken", Detail: name})
			continue
		}
		if Now().After(deadline) {
			return nil, fmt.Errorf("lock %s held by another process for over %s", name, wait)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// lockTakenAt reads the time the holder wrote into the lock file, so stale
// detection does not depend on a filesystem's modification time. A lock
// whose content cannot be read falls back to its modification time; one
// that has vanished reads as fresh.
func lockTakenAt(path string) time.Time {
	data, err := os.ReadFile(path)
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 {
			if at, err := time.Parse(time.RFC3339Nano, fields[1]); err == nil {
				return at
			}
		}
	}
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return Now()
}

// WriteFileAtomic writes data to path through a temporary file and a
// rename, for callers outside the package that must never leave a partial
// file behind.
func WriteFileAtomic(path string, data []byte) error {
	return writeAtomic(path, data)
}

// Event is one row of events.jsonl: every transition the substrate makes.
// Stats are computed from these rows, never from memory.
type Event struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Request string    `json:"request,omitempty"`
	By      string    `json:"by,omitempty"`
	Seat    string    `json:"seat,omitempty"`
	Detail  string    `json:"detail,omitempty"`
	Tier    string    `json:"tier,omitempty"`
	Epoch   int       `json:"epoch,omitempty"`
	Session string    `json:"session,omitempty"`
}

func (s *State) appendEvent(e Event) {
	if e.At.IsZero() {
		e.At = Now()
	}
	if e.Session == "" {
		e.Session = os.Getenv("FLAT_SESSION")
	}
	_ = appendLine(s.path("events.jsonl"), e)
}

// RecordRefusal notes that the substrate said no to a seat, so stats can
// count how often builders hit the boundary and on what.
func (s *State) RecordRefusal(seat, verb, code string) {
	s.appendEvent(Event{Kind: "refusal", Seat: seat, By: verb, Detail: code})
}

// Events reads every event in order.
func (s *State) Events() ([]Event, error) {
	var out []Event
	err := readLines(s.path("events.jsonl"), func(line []byte) error {
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		out = append(out, e)
		return nil
	})
	return out, err
}

func appendLine(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func readLines(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if err := fn([]byte(line)); err != nil {
			return err
		}
	}
	return sc.Err()
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

func hashOf(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}
