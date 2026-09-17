//go:build !windows

package plane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// OpenStore opens a store from a spec: "file:DIR", "resp:HOST:PORT/PREFIX"
// or "mem:". A spec is what crosses a process boundary, so worker processes
// and the harness agree on one store.
func OpenStore(spec string) (Store, error) {
	switch {
	case strings.HasPrefix(spec, "file:"):
		return OpenFile(strings.TrimPrefix(spec, "file:"))
	case strings.HasPrefix(spec, "resp:"):
		rest := strings.TrimPrefix(spec, "resp:")
		addr, prefix, _ := strings.Cut(rest, "/")
		return OpenRESP(addr, prefix)
	case spec == "mem:":
		return NewMem(), nil
	}
	return nil, fmt.Errorf("plane: store spec %q is not file:DIR or resp:HOST:PORT/PREFIX", spec)
}

// WorkerOptions configure one worker process.
type WorkerOptions struct {
	Store       string
	Kind        string        // what it claims: "task", or "event" for a watcher
	Incarnation string        // minted fresh at every start; never reused
	TTL         time.Duration // lease length
	Poll        time.Duration
	WorkMin     time.Duration
	WorkMax     time.Duration
	Seed        int64
	CrashProb   float64 // chance to die at each crash point
	SplitEvery  int     // every Nth task commits two children with it; 0 = never
	StopWhenDry bool    // exit when nothing of Kind is left pending
	Seats       bool    // hold a seat from the shared pool around each task, and release it
	Broken      Broken  // file store only: run deliberately wrong
}

// crash dies without cleanup: no deferred release, no flushed reply. It is
// how a killed microVM looks to the store.
func crash(where string) {
	fmt.Fprintf(os.Stderr, "crash:%s\n", where)
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}

// RunWorker is the loop every peer runs: claim under a lease, work, publish
// the result by committing it, discard the work if the commit is refused.
// It dies on purpose at the moments where dying hurts most.
func RunWorker(o WorkerOptions) error {
	s, err := OpenStore(o.Store)
	if err != nil {
		return err
	}
	defer s.Close()
	if f, ok := s.(*File); ok {
		f.Broken = o.Broken
	}
	rng := rand.New(rand.NewSource(o.Seed))
	ctx := context.Background()
	dice := func(where string) {
		if rng.Float64() < o.CrashProb {
			crash(where)
		}
	}
	n := 0
	for {
		n++
		// Admission: a task worker occupies one seat of a fixed pool while it
		// works and gives it back after. A worker that dies holding a seat
		// loses it when the lease lapses; nobody frees it by hand.
		var seat *Grant
		if o.Seats && o.Kind == "task" {
			sg, err := s.ClaimNext(ctx, "seat", o.Incarnation, 3*o.TTL, fmt.Sprintf("%s:s%d", o.Incarnation, n))
			if err != nil {
				time.Sleep(o.Poll)
				continue
			}
			seat = &sg
		}
		release := func() {
			if seat != nil {
				_ = s.Release(ctx, *seat)
			}
		}
		g, err := s.ClaimNext(ctx, o.Kind, o.Incarnation, o.TTL, fmt.Sprintf("%s:c%d", o.Incarnation, n))
		if errors.Is(err, ErrNone) {
			release()
			if o.StopWhenDry && dry(ctx, s, o.Kind) {
				return nil
			}
			time.Sleep(o.Poll)
			continue
		}
		if err != nil {
			release()
			time.Sleep(o.Poll)
			continue
		}
		dice("after-claim")
		work := o.WorkMin + time.Duration(rng.Int63n(int64(o.WorkMax-o.WorkMin)+1))
		time.Sleep(work)
		dice("before-commit")
		var emit []Item
		if o.Kind == "task" {
			emit = append(emit, Item{Kind: "event", ID: g.ID + ".done", Payload: o.Incarnation})
			if o.SplitEvery > 0 && !strings.Contains(g.ID, ".") && hashMod(g.ID, o.SplitEvery) == 0 {
				emit = append(emit, Item{Kind: "task", ID: g.ID + ".a"}, Item{Kind: "task", ID: g.ID + ".b"})
			}
		}
		// The commit key names the attempt, not the process, so a reply
		// lost to a dropped connection is answered from the record.
		_, err = s.Commit(ctx, g, o.Incarnation, fmt.Sprintf("%s/%s:e%d:%s", g.Kind, g.ID, g.Epoch, o.Incarnation), emit)
		dice("after-commit")
		_ = err // fenced or done: the work is discarded, which is the point
		release()
	}
}

func dry(ctx context.Context, s Store, kind string) bool {
	items, err := s.List(ctx, kind)
	if err != nil {
		return false
	}
	for _, it := range items {
		if it.State != Done {
			return false
		}
	}
	return true
}

func hashMod(s string, n int) int {
	h := 0
	for i := 0; i < len(s); i++ {
		h = h*31 + int(s[i])
	}
	if h < 0 {
		h = -h
	}
	return h % n
}

// FaultOptions configure one harness run.
type FaultOptions struct {
	Store      string // file:DIR, or resp:ADDR/PREFIX
	Bin        string // this binary, to start workers
	Tasks      int
	Workers    int
	Watchers   int
	TTL        time.Duration
	Poll       time.Duration
	WorkMin    time.Duration
	WorkMax    time.Duration
	Seed       int64
	CrashProb  float64
	SplitEvery int
	Kills      int                                    // SIGKILLs delivered to random workers
	Pauses     int                                    // SIGSTOP a worker for longer than its lease, then SIGCONT
	Restart    func() (down time.Duration, err error) // restarts the store under load; nil = none
	Restarts   int
	Seats      int // size of the seat pool; 0 = no admission
	Deadline   time.Duration
	Broken     Broken
	Out        string // evidence directory
}

// recoverBound is how long a lapsed lease may wait for a new owner: two
// lease periods and two polls, plus one seat lease when work is gated on a
// seat pool, since the peer that died may have been holding a seat too.
func recoverBound(o FaultOptions) time.Duration {
	b := 2*o.TTL + 2*o.Poll
	if o.Seats > 0 {
		b += 3 * o.TTL
	}
	return b
}

// FaultReport is one run's outcome.
type FaultReport struct {
	Seed         int64         `json:"seed"`
	Store        string        `json:"store"`
	Check        *Report       `json:"check"`
	Tasks        int           `json:"tasks"`
	Events       int           `json:"events"`
	Children     int           `json:"children"`
	Spawned      int           `json:"workers_spawned"`
	Crashed      int           `json:"workers_crashed"` // died at a crash point or to a kill
	Kills        int           `json:"kills_sent"`
	Pauses       int           `json:"pauses_sent"`
	Restarts     int           `json:"store_restarts"`
	Fenced       int           `json:"stale_attempts_refused"` // commits, renews and releases the store turned away
	Takeovers    int           `json:"lease_takeovers"`
	MaxRecoverMS int64         `json:"max_recover_ms"` // longest a lapsed lease waited for a new owner
	BoundMS      int64         `json:"recover_bound_ms"`
	OverBound    int           `json:"over_bound"`
	SeatPool     int           `json:"seat_pool"`
	PeakSeats    int           `json:"peak_seats_held"` // most seats under live lease at once; must not exceed the pool
	Wall         time.Duration `json:"wall"`
	Finished     bool          `json:"finished"`
	Passed       bool          `json:"passed"`
}

type proc struct {
	cmd  *exec.Cmd
	kind string
	done chan struct{}
}

// RunFault seeds a store, runs workers and watchers as real processes,
// injects the fault schedule, and hands the store's history to the checker.
func RunFault(o FaultOptions) (*FaultReport, error) {
	start := time.Now()
	s, err := OpenStore(o.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	ctx := context.Background()
	for i := 0; i < o.Tasks; i++ {
		if err := s.Put(ctx, Item{Kind: "task", ID: fmt.Sprintf("t%03d", i)}); err != nil {
			return nil, err
		}
	}
	for i := 0; i < o.Seats; i++ {
		if err := s.Put(ctx, Item{Kind: "seat", ID: fmt.Sprintf("seat%02d", i)}); err != nil {
			return nil, err
		}
	}
	rng := rand.New(rand.NewSource(o.Seed))
	rep := &FaultReport{Seed: o.Seed, Store: strings.SplitN(o.Store, ":", 2)[0], BoundMS: recoverBound(o).Milliseconds()}
	var mu sync.Mutex
	live := map[int]*proc{}
	next := 0
	spawn := func(kind string) {
		mu.Lock()
		next++
		id := next
		mu.Unlock()
		inc := fmt.Sprintf("%s-%d-%d", kind[:1], o.Seed, id) // a fresh incarnation every start
		w := WorkerOptions{Store: o.Store, Kind: kind, Incarnation: inc, TTL: o.TTL, Poll: o.Poll, WorkMin: o.WorkMin, WorkMax: o.WorkMax,
			Seed: o.Seed*1000 + int64(id), CrashProb: o.CrashProb, SplitEvery: o.SplitEvery, StopWhenDry: false, Broken: o.Broken, Seats: o.Seats > 0}
		spec, _ := json.Marshal(w)
		cmd := exec.Command(o.Bin, "plane", "worker", string(spec))
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return
		}
		p := &proc{cmd: cmd, kind: kind, done: make(chan struct{})}
		mu.Lock()
		live[id] = p
		rep.Spawned++
		mu.Unlock()
		go func() {
			_ = cmd.Wait()
			close(p.done)
			mu.Lock()
			delete(live, id)
			if !cmd.ProcessState.Success() {
				rep.Crashed++
			}
			mu.Unlock()
		}()
	}
	pick := func() *proc {
		mu.Lock()
		defer mu.Unlock()
		var ids []int
		for id := range live {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			return nil
		}
		sort.Ints(ids)
		return live[ids[rng.Intn(len(ids))]]
	}
	count := func(kind string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, p := range live {
			if p.kind == kind {
				n++
			}
		}
		return n
	}
	for i := 0; i < o.Workers; i++ {
		spawn("task")
	}
	for i := 0; i < o.Watchers; i++ {
		spawn("event")
	}

	// The fault schedule is fixed by the seed: spread over the first part of
	// the run, in a shuffled order.
	var faults []string
	for i := 0; i < o.Kills; i++ {
		faults = append(faults, "kill")
	}
	for i := 0; i < o.Pauses; i++ {
		faults = append(faults, "pause")
	}
	for i := 0; i < o.Restarts && o.Restart != nil; i++ {
		faults = append(faults, "restart")
	}
	rng.Shuffle(len(faults), func(i, j int) { faults[i], faults[j] = faults[j], faults[i] })
	var downtime []([2]time.Time)
	go func() {
		for _, f := range faults {
			time.Sleep(time.Duration(150+rng.Intn(350)) * time.Millisecond)
			switch f {
			case "kill":
				if p := pick(); p != nil {
					_ = p.cmd.Process.Signal(syscall.SIGKILL)
					mu.Lock()
					rep.Kills++
					mu.Unlock()
				}
			case "pause":
				if p := pick(); p != nil {
					_ = p.cmd.Process.Signal(syscall.SIGSTOP)
					mu.Lock()
					rep.Pauses++
					mu.Unlock()
					go func(p *proc) {
						time.Sleep(2*o.TTL + 100*time.Millisecond) // well past its lease
						_ = p.cmd.Process.Signal(syscall.SIGCONT)
					}(p)
				}
			case "restart":
				from := time.Now()
				if _, err := o.Restart(); err == nil {
					mu.Lock()
					rep.Restarts++
					downtime = append(downtime, [2]time.Time{from, time.Now()})
					mu.Unlock()
				}
			}
		}
	}()

	// Supervise: keep the head count up (a crashed peer comes back as a new
	// incarnation) until every task and event is committed or time runs out.
	deadline := time.Now().Add(o.Deadline)
	for time.Now().Before(deadline) {
		for count("task") < o.Workers {
			spawn("task")
		}
		for count("event") < o.Watchers {
			spawn("event")
		}
		if dry(ctx, s, "task") && dry(ctx, s, "event") {
			rep.Finished = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	for _, p := range live {
		_ = p.cmd.Process.Signal(syscall.SIGCONT)
		_ = p.cmd.Process.Signal(syscall.SIGKILL)
	}
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)

	h, err := s.History(ctx)
	if err != nil {
		return nil, err
	}
	rep.Check = Check(h, "task", "event")
	rep.measure(h, o, downtime)
	rep.Wall = time.Since(start)
	rep.Passed = rep.Finished && rep.Check.OK() && rep.OverBound == 0 && (o.Seats == 0 || rep.PeakSeats <= o.Seats)
	if o.Out != "" {
		_ = os.MkdirAll(o.Out, 0o755)
		writeJSONL(filepath.Join(o.Out, fmt.Sprintf("history-seed%d.jsonl", o.Seed)), h)
		data, _ := json.MarshalIndent(rep, "", "  ")
		_ = os.WriteFile(filepath.Join(o.Out, fmt.Sprintf("report-seed%d.json", o.Seed)), data, 0o644)
	}
	return rep, nil
}

// measure reads liveness numbers off the history: how many stale attempts
// the store refused, how many leases changed hands, and how long a lapsed
// lease waited for its next owner (ignoring time the store itself was down).
func (rep *FaultReport) measure(h []Event, o FaultOptions, downtime [][2]time.Time) {
	type last struct {
		owner string
		until time.Time
	}
	held := map[string]last{}
	for _, e := range h {
		k := key(e.Kind, e.ID)
		switch {
		case e.Op == "put" && e.Kind == "task" && strings.Contains(e.ID, "."):
			rep.Children++
		case e.Op == "put" && e.Kind == "event":
			rep.Events++
		case !e.OK && (e.Code == "fenced" || e.Code == "done" || e.Code == "expired") && e.Op != "claim":
			rep.Fenced++
		case e.OK && e.Op == "release":
			delete(held, k) // given back, not lapsed: the next claim is not a recovery
		case e.OK && !e.Replay && e.Op == "claim":
			if p, ok := held[k]; ok && p.owner != e.By && !p.until.IsZero() && e.Kind != "seat" {
				rep.Takeovers++
				gap := e.At.Sub(p.until)
				if !duringDowntime(p.until, e.At, downtime) {
					if ms := gap.Milliseconds(); ms > rep.MaxRecoverMS {
						rep.MaxRecoverMS = ms
					}
					if gap > recoverBound(o) {
						rep.OverBound++
					}
				}
			}
			held[k] = last{e.By, e.Until}
		case e.OK && e.Op == "commit":
			delete(held, k)
		}
	}
	rep.Tasks = o.Tasks + rep.Children
	rep.SeatPool = o.Seats
	rep.PeakSeats = peakHeld(h, "seat")
}

// peakHeld replays the history and returns the most items of a kind that
// were under an unexpired lease at the same moment.
func peakHeld(h []Event, kind string) int {
	until := map[string]time.Time{}
	peak := 0
	for _, e := range h {
		if e.Kind != kind || !e.OK || e.Replay {
			continue
		}
		switch e.Op {
		case "claim", "renew":
			until[e.ID] = e.Until
		case "release", "commit":
			delete(until, e.ID)
		}
		n := 0
		for _, u := range until {
			if e.At.Before(u) {
				n++
			}
		}
		if n > peak {
			peak = n
		}
	}
	return peak
}

func duringDowntime(from, to time.Time, downtime [][2]time.Time) bool {
	for _, d := range downtime {
		if from.Before(d[1].Add(time.Second)) && to.After(d[0]) {
			return true
		}
	}
	return false
}

func writeJSONL(path string, h []Event) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range h {
		_ = enc.Encode(e)
	}
}
