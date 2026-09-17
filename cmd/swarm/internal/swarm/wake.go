package swarm

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
	return s.path("sessions", fileKey(seat)+".json")
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
	if err == nil && r.SessionID != ev.SessionID && ev.Event != "SessionStart" {
		// A late event from a session this seat no longer runs. Only a
		// SessionStart may replace the seat's session; an old session's
		// delayed Stop must not make the old session the resumable one.
		return
	}
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
	Model      string
	Tools      string
	Max        int           // concurrent wakes
	Wall       time.Duration // per wake
	PerSeatHr  int           // most wakes one seat may get in an hour; default 6
	MaxAttempt int           // failed wakes before a note is parked for the operator; default 3
	Backoff    time.Duration // wait after a failed wake before trying that seat again; default 2m
}

func (o *WakeOptions) defaults() {
	if o.Tools == "" {
		o.Tools = "Read,Edit,Write,MultiEdit,Glob,Grep,Bash(git:*),Bash(go:*),Bash(swarm:*),Bash(cat:*),Bash(ls:*)"
	}
	if o.Max <= 0 {
		o.Max = 2
	}
	if o.Wall <= 0 {
		o.Wall = 6 * time.Minute
	}
	if o.PerSeatHr <= 0 {
		o.PerSeatHr = 6
	}
	if o.MaxAttempt <= 0 {
		o.MaxAttempt = 3
	}
	if o.Backoff <= 0 {
		o.Backoff = 2 * time.Minute
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
	Delivered bool      `json:"delivered"` // the provider took at least one turn, so the notes were acknowledged
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
		if !ok || !s.wakeAllowed(seat, opts) {
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
		// Notes stay in the inbox while the wake runs. They are acknowledged
		// (moved to delivered) only after the provider took a turn, so a wake
		// that never starts loses nothing.
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
	fmt.Fprintf(&sb, "[swarm wake] You are seat %s, woken for notes addressed to you. Act on them: rule or escalate a request, push WIP, fix RESULT.json, renew or drop a lease. One shell command per tool call. Then stop.\n\n", seat)
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
	cmd.Env = append(os.Environ(), "SWARM_SEAT="+seat)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	if runErr != nil {
		res.Err = strings.TrimSpace(runErr.Error() + " " + tailOf(errb.String(), 300))
		res.Exit = -1 // could not run at all, unless the provider reported a code
		if ee, ok := runErr.(*exec.ExitError); ok {
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
	res.Delivered = res.NumTurns > 0
	if res.Delivered {
		s.ackNotes(seat, notes)
	} else {
		s.wakeFailed(seat, notes, opts, res.Err)
	}
	_ = appendLine(s.path("wakes.jsonl"), res)
	_ = os.WriteFile(filepath.Join(s.path("watch"), "wake-"+fileKey(seat)+"-"+res.Started.Format("150405")+".out.json"), out.Bytes(), 0o644)
}

// ackNotes moves exactly the notes a wake carried to delivered.
func (s *State) ackNotes(seat string, notes []Note) {
	var ws wakeState
	if readJSON(s.wakeStatePath(seat), &ws) == nil && ws.Failures > 0 {
		ws.Failures, ws.RetryAt = 0, time.Time{}
		_ = writeJSON(s.wakeStatePath(seat), &ws)
	}
	dir := s.inboxDir(seat)
	done := filepath.Join(dir, "delivered")
	_ = os.MkdirAll(done, 0o755)
	for _, n := range notes {
		_ = os.Rename(filepath.Join(dir, n.ID+".json"), filepath.Join(done, n.ID+".json"))
	}
}

// wakeState is one seat's wake history: for the hourly budget, the backoff
// after a failure, and the attempts a parked note has had.
type wakeState struct {
	Started  []time.Time `json:"started"`
	Failures int         `json:"failures"`
	RetryAt  time.Time   `json:"retry_at"`
}

func (s *State) wakeStatePath(seat string) string {
	return s.path("sessions", fileKey(seat)+".wakes.json")
}

// wakeAllowed applies the backoff and the hourly budget, and records the
// start when it says yes.
func (s *State) wakeAllowed(seat string, opts WakeOptions) bool {
	var ws wakeState
	_ = readJSON(s.wakeStatePath(seat), &ws)
	now := Now()
	if now.Before(ws.RetryAt) {
		return false
	}
	recent := ws.Started[:0]
	for _, t := range ws.Started {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	ws.Started = recent
	if len(ws.Started) >= opts.PerSeatHr {
		s.appendEvent(Event{Kind: "wake_budget", Seat: seat, Detail: fmt.Sprintf("%d wakes in the last hour", len(ws.Started))})
		return false
	}
	ws.Started = append(ws.Started, now)
	_ = writeJSON(s.wakeStatePath(seat), &ws)
	return true
}

// wakeFailed leaves the notes in the inbox, backs the seat off, and after
// MaxAttempt failures parks the notes for the operator instead of trying
// forever.
func (s *State) wakeFailed(seat string, notes []Note, opts WakeOptions, why string) {
	var ws wakeState
	_ = readJSON(s.wakeStatePath(seat), &ws)
	ws.Failures++
	ws.RetryAt = Now().Add(opts.Backoff * time.Duration(ws.Failures))
	s.appendEvent(Event{Kind: "wake_failed", Seat: seat, Detail: why})
	if ws.Failures >= opts.MaxAttempt {
		dir := s.inboxDir(seat)
		dead := filepath.Join(dir, "undeliverable")
		_ = os.MkdirAll(dead, 0o755)
		for _, n := range notes {
			_ = os.Rename(filepath.Join(dir, n.ID+".json"), filepath.Join(dead, n.ID+".json"))
		}
		_, _ = s.Nudge("operator", "undeliverable", fmt.Sprintf("%d notes for %s could not be delivered after %d wakes: %s", len(notes), seat, ws.Failures, why))
		ws.Failures = 0
	}
	_ = writeJSON(s.wakeStatePath(seat), &ws)
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
