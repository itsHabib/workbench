package swarm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// AdmitOptions is the admission policy: what must be true before one more
// builder starts. The fleet ran with no such gate and filled a disk.
type AdmitOptions struct {
	Seats    int           // max branches in working state at once; 0 = unlimited
	DiskMin  uint64        // bytes that must be free under the repository
	Idle     time.Duration // a branch idle longer than this does not occupy a seat
	Base     string
	Resource string        // a resource the new builder will need; refuses while held
	For      string        // the branch this admission is for; it reserves a seat until that branch is on the board
	Hold     time.Duration // how long a reservation lasts without the branch appearing; default 5m
}

// Admission is the answer.
type Admission struct {
	Admitted bool     `json:"admitted"`
	Reasons  []string `json:"reasons,omitempty"`
	Active   int      `json:"active"`
	Reserved int      `json:"reserved"` // admissions granted whose branch is not on the board yet
	Seats    int      `json:"seats"`
	DiskFree uint64   `json:"disk_free"`
	DiskMin  uint64   `json:"disk_min"`
}

// Admit answers whether one more builder may start now.
func (s *State) Admit(opts AdmitOptions) (*Admission, error) {
	if opts.Idle == 0 {
		opts.Idle = 30 * time.Minute
	}
	// Admission is check-and-reserve under one lock: two launchers asking
	// for the last seat cannot both be told yes.
	release, err := s.lock("admissions", 10*time.Second, time.Minute)
	if err != nil {
		return nil, err
	}
	defer release()
	if opts.Hold == 0 {
		opts.Hold = 5 * time.Minute
	}
	a := &Admission{Admitted: true, Seats: opts.Seats, DiskMin: opts.DiskMin}
	free, err := diskFree(s.Repo)
	if err != nil {
		return nil, err
	}
	if v := os.Getenv("SWARM_DISK_FREE_BYTES"); v != "" { // planted-fault hook
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			free = n
		}
	}
	a.DiskFree = free
	if opts.DiskMin > 0 && free < opts.DiskMin {
		a.Admitted = false
		a.Reasons = append(a.Reasons, fmt.Sprintf("disk_low: %s free, floor %s", human(free), human(opts.DiskMin)))
	}
	b, err := s.Board(BoardOptions{Base: opts.Base, Idle: opts.Idle})
	if err != nil {
		return nil, err
	}
	onBoard := map[string]bool{}
	for _, r := range b.Rows {
		onBoard[r.Branch] = true
		if r.State == "working" {
			a.Active++
		}
	}
	held := s.liveReservations(onBoard, opts.For)
	a.Reserved = len(held)
	if opts.Seats > 0 && a.Active+a.Reserved >= opts.Seats {
		a.Admitted = false
		a.Reasons = append(a.Reasons, fmt.Sprintf("seats_full: %d working + %d reserved, %d seats", a.Active, a.Reserved, opts.Seats))
	}
	if opts.Resource != "" {
		if h := s.Holder(opts.Resource); h != "" {
			a.Admitted = false
			a.Reasons = append(a.Reasons, fmt.Sprintf("resource_held: %s by %s", opts.Resource, h))
		}
	}
	if a.Admitted {
		name := opts.For
		if name == "" {
			name = NewID("adm")
		}
		held[name] = Now().Add(opts.Hold)
	}
	_ = writeJSON(s.path("admissions.json"), held)
	kind := "admit"
	if !a.Admitted {
		kind = "refuse_admit"
	}
	s.appendEvent(Event{Kind: kind, Detail: strings.Join(a.Reasons, "; ")})
	return a, nil
}

// liveReservations returns the admissions still holding a seat: not
// expired, and not yet visible on the board as a branch of their own. A
// reservation for the caller's own branch is dropped so a retry is not
// counted against itself.
func (s *State) liveReservations(onBoard map[string]bool, self string) map[string]time.Time {
	held := map[string]time.Time{}
	_ = readJSON(s.path("admissions.json"), &held)
	now := Now()
	for name, until := range held {
		if name == self || onBoard[name] || now.After(until) {
			delete(held, name)
		}
	}
	return held
}

func human(b uint64) string {
	const g = 1 << 30
	if b >= g {
		return fmt.Sprintf("%.1fG", float64(b)/g)
	}
	return fmt.Sprintf("%.0fM", float64(b)/(1<<20))
}

// ParseBytes accepts 10G, 500M, 1024.
func ParseBytes(s string) (uint64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, strings.TrimSuffix(s, "K")
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("bad size %q", s)
	}
	return uint64(n * float64(mult)), nil
}
