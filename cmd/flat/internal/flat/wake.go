package flat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SessionRecord is what the hook knows about the last session on a seat.
// A wake resumes it, so the seat answers with the context it built, even
// after the session that built it ended.
type SessionRecord struct {
	Seat      string    `json:"seat"`
	SessionID string    `json:"session_id"`
	Cwd       string    `json:"cwd"`
	Started   time.Time `json:"started"`
	LastEvent time.Time `json:"last_event"`
	Stopped   bool      `json:"stopped"` // the turn ended; the session can be resumed
	Ended     bool      `json:"ended"`   // the process ended; still resumable
}

func (s *State) sessionPath(seat string) string {
	return s.path("sessions", strings.ReplaceAll(seat, "/", "_")+".json")
}

// Session returns the last record for seat.
func (s *State) Session(seat string) (*SessionRecord, error) {
	var r SessionRecord
	if err := readJSON(s.sessionPath(seat), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *State) recordSession(seat string, ev hookInput) {
	if seat == "" || ev.SessionID == "" {
		return
	}
	r, err := s.Session(seat)
	now := Now()
	if err != nil || r.SessionID != ev.SessionID {
		r = &SessionRecord{Seat: seat, SessionID: ev.SessionID, Cwd: ev.Cwd, Started: now}
	}
	r.LastEvent = now
	switch ev.Event {
	case "SessionStart":
		r.Stopped, r.Ended = false, false
	case "Stop":
		r.Stopped = true
	case "SessionEnd":
		r.Ended = true
	default:
		r.Stopped = false
	}
	_ = writeJSON(s.sessionPath(seat), r)
}

// Wakeable says whether a seat can take a fresh turn now: it has a recorded
// session and that session is between turns or ended. A session with no
// recorded stop is presumed mid-turn and only ever hears through the hook.
func (s *State) Wakeable(seat string) (*SessionRecord, bool) {
	r, err := s.Session(seat)
	if err != nil || r.Cwd == "" {
		return nil, false
	}
	return r, r.Stopped || r.Ended
}

// WakeOptions is how a wake runs the provider.
type WakeOptions struct {
	Model string
	Tools string
	Max   int           // concurrent wakes
	Wall  time.Duration // per wake
}

func (o *WakeOptions) defaults() {
	if o.Tools == "" {
		o.Tools = "Read,Edit,Write,MultiEdit,Glob,Grep,Bash(git:*),Bash(go:*),Bash(flat:*),Bash(cat:*),Bash(ls:*)"
	}
	if o.Max <= 0 {
		o.Max = 2
	}
	if o.Wall <= 0 {
		o.Wall = 6 * time.Minute
	}
}

// WakeResult is one wake's accounting, appended to wakes.jsonl.
type WakeResult struct {
	Seat      string    `json:"seat"`
	SessionID string    `json:"session_id"`
	Notes     int       `json:"notes"`
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended"`
	Exit      int       `json:"exit"`
	NumTurns  int       `json:"num_turns"`
	OutputTok int       `json:"output_tokens"`
	CostUSD   float64   `json:"cost_usd"`
	Err       string    `json:"err,omitempty"`
}

var (
	wakeMu     sync.Mutex
	wakeActive = map[string]bool{}
)

// WaitWakes blocks until every wake this process started has finished, or
// until limit passes; it returns the seats still running at the limit.
// Every wake is bounded by its own wall clock, so a nonempty return means
// a wake outlived the caller's patience, not that it will never end.
func WaitWakes(limit time.Duration) []string {
	deadline := Now().Add(limit)
	for {
		wakeMu.Lock()
		var active []string
		for seat := range wakeActive {
			active = append(active, seat)
		}
		wakeMu.Unlock()
		if len(active) == 0 || Now().After(deadline) {
			return active
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// WakePending starts a wake for every seat that has undelivered notes and
// can take a turn, up to opts.Max at once. Non-blocking: wakes run in the
// background and record themselves when they finish.
func (s *State) WakePending(opts WakeOptions) []string {
	opts.defaults()
	entries, err := os.ReadDir(s.path("inbox"))
	if err != nil {
		return nil
	}
	var started []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seat := e.Name()
		notes, _ := s.Inbox(seat, false)
		if len(notes) == 0 {
			continue
		}
		rec, ok := s.Wakeable(seat)
		if !ok {
			continue
		}
		wakeMu.Lock()
		busy := wakeActive[seat] || len(wakeActive) >= opts.Max
		if !busy {
			wakeActive[seat] = true
		}
		wakeMu.Unlock()
		if busy {
			continue
		}
		notes, _ = s.Inbox(seat, true)
		started = append(started, seat)
		go s.wake(seat, rec, notes, opts)
	}
	return started
}

func (s *State) wake(seat string, rec *SessionRecord, notes []Note, opts WakeOptions) {
	defer func() {
		wakeMu.Lock()
		delete(wakeActive, seat)
		wakeMu.Unlock()
	}()
	var sb strings.Builder
	fmt.Fprintf(&sb, "[flat wake] You are seat %s, woken for notes addressed to you. Act on them: rule or escalate a request, push WIP, fix RESULT.json, renew or drop a lease. One shell command per tool call. Then stop.\n\n", seat)
	for _, n := range notes {
		fmt.Fprintf(&sb, "- [%s %s] %s\n", n.At.Format("15:04"), n.Kind, n.Text)
	}
	res := WakeResult{Seat: seat, SessionID: rec.SessionID, Notes: len(notes), Started: Now()}
	s.appendEvent(Event{Kind: "wake", Seat: seat, Session: rec.SessionID, Detail: fmt.Sprintf("%d notes", len(notes))})
	ctx, cancel := context.WithTimeout(context.Background(), opts.Wall)
	defer cancel()
	args := []string{"-p", sb.String(), "--resume", rec.SessionID, "--output-format", "json", "--permission-mode", "acceptEdits", "--max-turns", "30", "--allowedTools", opts.Tools}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = rec.Cwd
	cmd.Env = append(os.Environ(), "FLAT_SEAT="+seat)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		res.Err = strings.TrimSpace(err.Error() + " " + tailOf(errb.String(), 300))
		if ee, ok := err.(*exec.ExitError); ok {
			res.Exit = ee.ExitCode()
		}
	}
	var parsed struct {
		NumTurns int     `json:"num_turns"`
		Cost     float64 `json:"total_cost_usd"`
		Usage    struct {
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(out.Bytes(), &parsed) == nil {
		res.NumTurns, res.CostUSD, res.OutputTok = parsed.NumTurns, parsed.Cost, parsed.Usage.Output
	}
	res.Ended = Now()
	_ = appendLine(s.path("wakes.jsonl"), res)
	_ = os.WriteFile(filepath.Join(s.path("watch"), "wake-"+seat+"-"+res.Started.Format("150405")+".out.json"), out.Bytes(), 0o644)
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
