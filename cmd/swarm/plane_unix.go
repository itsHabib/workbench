//go:build !windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/plane"
)

const planeUsage = `swarm plane: the coordination store and its fault harness

  swarm plane fault --store file|resp [--seeds 10] [--tasks 120] [--workers 8] [--watchers 2]
                    [--ttl 400ms] [--kills 12] [--pauses 4] [--restarts 2] [--crash-prob 0.04]
                    [--split-every 7] [--out DIR] [--mutant no-epoch|no-done|no-pending|epoch-reuse]
  swarm plane rooms --snapshot S --host-ip IP --out DIR [--image I] [--toolstore T] [-n 6] [--tasks 240] [--faults]
                    every peer in its own Rooms microVM clone; run on the rooms host
  swarm plane worker JSON [--incarnation ID] [--kind task|event]     (started by the harness)
`

func planeMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, planeUsage)
		return 3
	}
	switch args[0] {
	case "worker":
		var o plane.WorkerOptions
		if len(args) < 2 || json.Unmarshal([]byte(args[1]), &o) != nil {
			return 3
		}
		// A guest mints its own identity and role after it restores.
		wf := flag.NewFlagSet("plane worker", flag.ContinueOnError)
		wf.StringVar(&o.Incarnation, "incarnation", o.Incarnation, "this peer's incarnation")
		wf.StringVar(&o.Kind, "kind", o.Kind, "task or event")
		if err := wf.Parse(args[2:]); err != nil || o.Incarnation == "" || o.Kind == "" {
			return 3
		}
		if o.Seed == 0 {
			o.Seed = time.Now().UnixNano()
		}
		if err := plane.RunWorker(o); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		return 0
	case "fault":
		return planeFault(args[1:])
	case "rooms":
		return planeRooms(args[1:])
	}
	fmt.Fprint(os.Stderr, planeUsage)
	return 3
}

// redisProc is a store the harness owns, so it can kill it under load.
type redisProc struct {
	dir  string
	port int
	bind string // default loopback; a rooms host binds the address guests reach
	cmd  *exec.Cmd
}

func (r *redisProc) start() error {
	bind := r.bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	r.cmd = exec.Command("redis-server", "--port", fmt.Sprint(r.port), "--bind", bind, "--protected-mode", "no", "--appendonly", "yes",
		"--appendfsync", "always", "--dir", r.dir, "--save", "")
	if err := r.cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 100; i++ {
		if s, err := plane.OpenRESP(fmt.Sprintf("%s:%d", bind, r.port), "ping:"); err == nil {
			_ = s.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("redis-server did not come up on %d", r.port)
}

// restart kills the server without warning and brings it back on the same
// data directory: what survives is what appendfsync always put on disk.
func (r *redisProc) restart() (time.Duration, error) {
	from := time.Now()
	_ = r.cmd.Process.Signal(syscall.SIGKILL)
	_, _ = r.cmd.Process.Wait()
	err := r.start()
	return time.Since(from), err
}

func planeFault(args []string) int {
	fs := flag.NewFlagSet("plane fault", flag.ContinueOnError)
	store := fs.String("store", "file", "file or resp")
	seeds := fs.Int("seeds", 10, "how many seeded runs")
	o := plane.FaultOptions{}
	fs.IntVar(&o.Tasks, "tasks", 120, "tasks per run")
	fs.IntVar(&o.Workers, "workers", 8, "worker processes")
	fs.IntVar(&o.Watchers, "watchers", 2, "watcher processes racing for deliveries")
	fs.DurationVar(&o.TTL, "ttl", 400*time.Millisecond, "lease length")
	fs.DurationVar(&o.Poll, "poll", 40*time.Millisecond, "worker poll")
	fs.DurationVar(&o.WorkMin, "work-min", 10*time.Millisecond, "work time")
	fs.DurationVar(&o.WorkMax, "work-max", 150*time.Millisecond, "work time")
	fs.Float64Var(&o.CrashProb, "crash-prob", 0.04, "chance to die at each crash point")
	fs.IntVar(&o.SplitEvery, "split-every", 7, "every Nth task commits two children")
	fs.IntVar(&o.Kills, "kills", 12, "SIGKILLs")
	fs.IntVar(&o.Pauses, "pauses", 4, "SIGSTOP past the lease, then SIGCONT")
	fs.IntVar(&o.Restarts, "restarts", 2, "store restarts (resp only)")
	fs.IntVar(&o.Seats, "seats", 5, "seat pool every task worker claims and releases; fewer than workers, so admission is contended")
	fs.DurationVar(&o.Deadline, "deadline", 90*time.Second, "per run")
	out := fs.String("out", "", "evidence directory")
	mutant := fs.String("mutant", "", "run a deliberately broken file store: no-epoch, no-done, no-pending, epoch-reuse")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	o.Bin, _ = os.Executable()
	switch *mutant {
	case "no-epoch":
		o.Broken.NoEpochCheck = true
	case "no-done":
		o.Broken.NoDoneCheck, o.Broken.NoPendingCheck = true, true
	case "no-pending":
		o.Broken.NoPendingCheck = true
	case "epoch-reuse":
		o.Broken.EpochReuse = true
	}
	base, _ := os.MkdirTemp("", "swarm-plane-")
	defer os.RemoveAll(base)
	failed := 0
	fmt.Printf("%-5s %-5s %-7s %-6s %-6s %-6s %-8s %-7s %-9s %-7s %-10s %-9s %s\n", "seed", "store", "accept", "tasks", "events", "crash", "refused", "taken", "recoverMS", "seats", "violations", "unfinish", "verdict")
	for seed := 1; seed <= *seeds; seed++ {
		o.Seed = int64(seed)
		o.Restart = nil
		if *out != "" {
			o.Out = filepath.Join(*out, *store)
		}
		var rp *redisProc
		switch *store {
		case "file":
			o.Store = "file:" + filepath.Join(base, fmt.Sprintf("s%d", seed))
		case "resp":
			rp = &redisProc{dir: filepath.Join(base, fmt.Sprintf("r%d", seed)), port: 56400 + seed}
			_ = os.MkdirAll(rp.dir, 0o755)
			if err := rp.start(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 3
			}
			o.Store = fmt.Sprintf("resp:127.0.0.1:%d/run%d:", rp.port, seed)
			o.Restart = rp.restart
		}
		rep, err := plane.RunFault(o)
		if rp != nil {
			_ = rp.cmd.Process.Signal(syscall.SIGKILL)
			_, _ = rp.cmd.Process.Wait()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "run failed:", err)
			failed++
			continue
		}
		verdict := "PASS"
		if !rep.Passed {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%-5d %-5s %-7d %-6d %-6d %-6d %-8d %-7d %-9d %-7s %-10d %-9d %s\n", rep.Seed, rep.Store, rep.Check.Accepted, rep.Tasks, rep.Events,
			rep.Crashed, rep.Fenced, rep.Takeovers, rep.MaxRecoverMS, fmt.Sprintf("%d/%d", rep.PeakSeats, rep.SeatPool), len(rep.Check.Violations), len(rep.Check.Unfinished), verdict)
		for i, v := range rep.Check.Violations {
			if i == 3 {
				fmt.Printf("      ... %d more\n", len(rep.Check.Violations)-3)
				break
			}
			fmt.Printf("      %s %s: %s\n", v.Rule, v.Item, v.Detail)
		}
	}
	if failed > 0 {
		fmt.Printf("%d of %d runs FAILED\n", failed, *seeds)
		return 1
	}
	fmt.Printf("all %d runs passed\n", *seeds)
	return 0
}
