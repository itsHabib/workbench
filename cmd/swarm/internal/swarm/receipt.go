package swarm

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/plane"
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

// Receipt returns the receipt for tip, if any. With a shared store the
// receipt lives there, keyed by the exact commit, so every machine and
// every later stage reads the same verdict; a verdict in one machine's
// files gates nothing elsewhere.
func (s *State) Receipt(tip string) *Receipt {
	if remote() {
		return s.receiptP(tip)
	}
	var r Receipt
	if err := readJSON(s.receiptPath(tip), &r); err != nil {
		return nil
	}
	return &r
}

// PutReceipt records a verdict for an exact commit. A receipt is written
// once; a second verdict for the same commit and command is refused so a
// stage cannot quietly overwrite what another stage saw.
func (s *State) PutReceipt(rec *Receipt) error {
	if rec.At.IsZero() {
		rec.At = Now()
	}
	if remote() {
		return s.putReceiptP(rec)
	}
	_ = os.MkdirAll(s.path("receipts"), 0o755)
	return writeJSON(s.receiptPath(rec.Tip), rec)
}

const kindReceipt = "receipt"

func (s *State) receiptP(tip string) *Receipt {
	st, err := s.Plane()
	if err != nil {
		return nil
	}
	defer st.Close()
	it, err := st.Get(context.Background(), kindReceipt, tip)
	if err != nil {
		return nil
	}
	var r Receipt
	if json.Unmarshal([]byte(it.Payload), &r) != nil {
		return nil
	}
	return &r
}

func (s *State) putReceiptP(rec *Receipt) error {
	st, err := s.Plane()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	if it, err := st.Get(ctx, kindReceipt, rec.Tip); err == nil {
		var have Receipt
		_ = json.Unmarshal([]byte(it.Payload), &have)
		if have.Cmd == rec.Cmd {
			return refuse("receipt_exists", "%s already has a receipt for %q (%s)", short(rec.Tip), rec.Cmd, map[bool]string{true: "pass", false: "fail"}[have.Pass])
		}
	}
	payload, _ := json.Marshal(rec)
	return st.Put(ctx, plane.Item{Kind: kindReceipt, ID: rec.Tip, Payload: string(payload)})
}

// Receipts lists every receipt on the store, newest last.
func (s *State) Receipts() ([]Receipt, error) {
	if !remote() {
		return nil, refuse("no_store", "receipts are listed from a shared store; set SWARM_STORE")
	}
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	items, err := st.List(context.Background(), kindReceipt)
	if err != nil {
		return nil, err
	}
	out := make([]Receipt, 0, len(items))
	for _, it := range items {
		var r Receipt
		if json.Unmarshal([]byte(it.Payload), &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
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
		_ = s.PutReceipt(rec)
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
	_ = s.PutReceipt(rec)
	s.appendEvent(Event{Kind: "verify", Seat: r.Branch, Detail: map[bool]string{true: "pass", false: "fail"}[rec.Pass] + " " + short(r.Tip)})
	return rec
}
