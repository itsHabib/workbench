// Package swarm is the substrate for a fleet of coding agents that has no
// management sessions: a board derived from git, a decision ledger, claims
// with fencing epochs, resource leases, admission, a watcher that renders and
// nudges, and a hook that delivers nudges into a session.
//
// All state is files under one directory shared by every worktree of a
// repository. Nothing is remembered by an agent; everything is re-derivable.
package swarm

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

// State is the shared directory. Default: <git common dir>/swarm, so every
// worktree of one repository shares it without configuration. SWARM_STATE
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
	dir := os.Getenv("SWARM_STATE")
	if dir == "" {
		common, err := Git(abs, "rev-parse", "--git-common-dir")
		if err != nil {
			return nil, fmt.Errorf("not inside a git repository (%s); set SWARM_STATE to use swarm elsewhere", abs)
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(abs, common)
		}
		dir = filepath.Join(filepath.Clean(common), "swarm")
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

// writeAtomic writes data to path through a temporary file, an fsync and
// a rename, then fsyncs the directory, so a reader never sees a partial
// file and a crash never leaves a renamed file whose bytes were not on disk.
func writeAtomic(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp.%d.%s", path, os.Getpid(), strconv.FormatInt(Now().UnixNano(), 36))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir makes a rename or create in dir durable. Best effort: some
// platforms cannot sync a directory, and the data file itself is synced.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// lock takes the named mutation lock, waiting up to wait. It is an OS
// advisory lock on a file that is never deleted: a holder that dies
// releases it at once, and a holder that is merely slow keeps it for as
// long as it lives. Age is never evidence of death, so nothing is broken by
// age, and a release can never remove somebody else's lock. The stale
// argument is kept for callers and ignored.
func (s *State) lock(name string, wait, _ time.Duration) (release func(), err error) {
	f, err := os.OpenFile(s.path(name+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		ok, err := tryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if ok {
			return func() { unlock(f); _ = f.Close() }, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s held by another process for over %s", name, wait)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
		e.Session = os.Getenv("SWARM_SESSION")
	}
	_ = appendLine(s.path("events.jsonl"), e)
}

// fileKey turns a seat or resource name into a file name no other name
// maps to: every byte outside [A-Za-z0-9.-] is written as _xx hex, and the
// underscore itself is escaped, so "a/b" and "a_b" stay distinct.
func fileKey(name string) string {
	var sb strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, "_%02x", c)
		}
	}
	return sb.String()
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
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
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
