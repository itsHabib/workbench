package swarm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Receipt is the outcome of running the verification command at one exact
// head. Written by the watcher, never by the seat that landed; a landing
// that goes red is caught by a process, not by whoever reads the board.
type Receipt struct {
	Tip    string    `json:"tip"`
	Branch string    `json:"branch"`
	Cmd    string    `json:"cmd"`
	Pass   bool      `json:"pass"`
	At     time.Time `json:"at"`
	Tail   string    `json:"tail,omitempty"`
}

func (s *State) receiptPath(tip string) string { return s.path("receipts", tip+".json") }

// Receipt returns the receipt for tip, if any.
func (s *State) Receipt(tip string) *Receipt {
	var r Receipt
	if err := readJSON(s.receiptPath(tip), &r); err != nil {
		return nil
	}
	return &r
}

// verifyLanded runs cmd at every landed head that has no receipt yet and
// rewrites those rows' state from the outcome.
func (s *State) verifyLanded(b *Board, cmd string) {
	_ = os.MkdirAll(s.path("receipts"), 0o755)
	for i := range b.Rows {
		r := &b.Rows[i]
		if r.State != "landed" {
			continue
		}
		rec := s.Receipt(r.Tip)
		if rec == nil {
			rec = s.runVerify(r, cmd)
		}
		r.Receipt = rec
		if !rec.Pass {
			r.State = "red"
		}
	}
}

func (s *State) runVerify(r *Row, cmd string) *Receipt {
	rec := &Receipt{Tip: r.Tip, Branch: r.Branch, Cmd: cmd, At: Now()}
	dir := filepath.Join(os.TempDir(), "swarm-verify-"+short(r.Tip))
	_ = os.RemoveAll(dir)
	if _, err := Git(s.Repo, "worktree", "add", "-q", "--detach", dir, r.Tip); err != nil {
		rec.Tail = "worktree: " + err.Error()
		_ = writeJSON(s.receiptPath(r.Tip), rec)
		return rec
	}
	defer func() {
		_, _ = Git(s.Repo, "worktree", "remove", "--force", dir)
	}()
	fields := strings.Fields(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	c.Dir = dir
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	rec.Pass = err == nil
	rec.Tail = strings.TrimSpace(tailOf(out.String(), 400))
	_ = writeJSON(s.receiptPath(r.Tip), rec)
	s.appendEvent(Event{Kind: "verify", Seat: r.Branch, Detail: map[bool]string{true: "pass", false: "fail"}[rec.Pass] + " " + short(r.Tip)})
	return rec
}
