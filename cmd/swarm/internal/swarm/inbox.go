package swarm

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Note is one nudge in a seat's inbox. The watcher writes them; the hook
// reads them into the seat's next tool call; nobody has to poll and no
// management session has to send them.
type Note struct {
	ID   string    `json:"id"`
	At   time.Time `json:"at"`
	Seat string    `json:"seat"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

func (s *State) inboxDir(seat string) string {
	return s.path("inbox", fileKey(seat))
}

// Nudge drops a note in seat's inbox.
func (s *State) Nudge(seat, kind, text string) (*Note, error) {
	if seat == "" {
		return nil, refuse("no_seat", "nudge needs a seat")
	}
	if remote() {
		return s.nudgeP(seat, kind, text)
	}
	dir := s.inboxDir(seat)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	n := &Note{ID: NewID("note"), At: Now(), Seat: seat, Kind: kind, Text: text}
	if err := writeJSON(filepath.Join(dir, n.ID+".json"), n); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "nudge", Seat: seat, Detail: kind + ": " + text})
	return n, nil
}

func (s *State) notify(seat, text string) {
	if seat != "" {
		_, _ = s.Nudge(seat, "ruling", text)
	}
}

// Inbox lists undelivered notes for seat, oldest first. consume moves them
// to delivered/ so each is injected once.
func (s *State) Inbox(seat string, consume bool) ([]Note, error) {
	if remote() {
		return s.inboxP(seat, consume)
	}
	dir := s.inboxDir(seat)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Note
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var n Note
		if err := readJSON(filepath.Join(dir, e.Name()), &n); err != nil {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if consume && len(out) > 0 {
		done := filepath.Join(dir, "delivered")
		_ = os.MkdirAll(done, 0o755)
		for _, n := range out {
			_ = os.Rename(filepath.Join(dir, n.ID+".json"), filepath.Join(done, n.ID+".json"))
		}
	}
	return out, nil
}

// SeatOf resolves the seat a session is: SWARM_SEAT, else the branch checked
// out at cwd. Location is identity; a renamed session changes nothing.
func SeatOf(cwd string) string {
	if v := os.Getenv("SWARM_SEAT"); v != "" {
		return v
	}
	return CurrentBranch(cwd)
}

// hookInput is the subset of the harness hook event we read.
type hookInput struct {
	Event     string `json:"hook_event_name"`
	Cwd       string `json:"cwd"`
	SessionID string `json:"session_id"`
}

// Hook reads one harness hook event on stdin, records the seat's session
// and turn boundary, and on SessionStart, UserPromptSubmit and PostToolUse
// prints additional context carrying the seat's undelivered notes. Exit 0
// always; a hook must never block a session.
func Hook(in io.Reader, out io.Writer) error {
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	var ev hookInput
	_ = json.Unmarshal(data, &ev)
	cwd := ev.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	s, err := Open(cwd)
	if err != nil {
		return nil // not a flat repository; say nothing
	}
	seat := SeatOf(cwd)
	if seat == "" {
		return nil
	}
	s.recordSession(seat, ev)
	switch ev.Event {
	case "PreToolUse", "Stop", "SessionEnd":
		return nil // recorded; these events carry no context back
	}
	var lines []string
	if ev.Event == "SessionStart" {
		summary, _ := s.summarizeRequests()
		lines = append(lines, fmt.Sprintf("[swarm] you are seat %s. %d decision requests open (%s). `swarm inbox` reads your notes; `swarm board --md` reads the fleet.", seat, summary.Open, summary.byNeeds()))
		if ev.SessionID != "" {
			s.appendEvent(Event{Kind: "session_start", Seat: seat, Session: ev.SessionID})
		}
	}
	notes, _ := s.Inbox(seat, true)
	for _, n := range notes {
		lines = append(lines, fmt.Sprintf("[swarm %s] %s", n.Kind, n.Text))
	}
	if len(lines) == 0 {
		return nil
	}
	resp := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     ev.Event,
			"additionalContext": strings.Join(lines, "\n"),
		},
	}
	enc, _ := json.Marshal(resp)
	_, err = fmt.Fprintln(out, string(enc))
	return err
}

// HookSettings is the settings.json fragment that installs the hook in a
// project. cmd is the swarm binary path (swarm.exe on Windows).
func HookSettings(cmd string) map[string]any {
	entry := func() []map[string]any {
		return []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": cmd + " hook"}}}}
	}
	return map[string]any{"hooks": map[string]any{
		"SessionStart":     entry(),
		"UserPromptSubmit": entry(),
		"PreToolUse":       entry(),
		"PostToolUse":      entry(),
		"Stop":             entry(),
		"SessionEnd":       entry(),
	}}
}
