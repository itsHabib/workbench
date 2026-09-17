//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/plane"
)

// planeRooms runs the fault workload with every peer in its own Rooms
// microVM clone. It runs on the rooms host: it starts the store there,
// serves this binary to the guests over HTTP (a clone's command line is too
// small to carry it), starts N clones whose command downloads and runs the
// worker, kills one clone and the store mid-run, and checks the store's
// history. Clones of one snapshot share a frozen hostname and address, so
// each peer mints its incarnation from the kernel's random uuid after it
// restores.
func planeRooms(args []string) int {
	fs := flag.NewFlagSet("plane rooms", flag.ContinueOnError)
	roomsBin := fs.String("rooms", "rooms", "rooms binary")
	snapshot := fs.String("snapshot", "", "snapshot to clone")
	image := fs.String("image", "", "rooms image")
	toolstore := fs.String("toolstore", "", "rooms toolstore")
	hostIP := fs.String("host-ip", "", "this host's address as guests reach it")
	clones := fs.Int("n", 6, "clones")
	tasks := fs.Int("tasks", 240, "tasks")
	watchers := fs.Int("watchers", 2, "of the clones, how many deliver events instead of building")
	ttl := fs.Duration("ttl", 2*time.Second, "lease length")
	work := fs.Duration("work", 200*time.Millisecond, "simulated work per task")
	faults := fs.Bool("faults", false, "kill one clone and restart the store mid-run")
	wall := fs.Duration("wall", 3*time.Minute, "max wall per run")
	out := fs.String("out", "", "evidence directory (must not exist)")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	if *snapshot == "" || *hostIP == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "plane rooms needs --snapshot, --host-ip and --out")
		return 3
	}
	if err := os.MkdirAll(filepath.Join(*out, "redis"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	port, binPort := freePort(), freePort()
	rp := &redisProc{dir: filepath.Join(*out, "redis"), port: port, bind: *hostIP}
	if err := rp.start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	defer func() { _ = rp.cmd.Process.Signal(syscall.SIGKILL) }()
	self, _ := os.Executable()
	srv := &http.Server{Addr: fmt.Sprintf("%s:%d", *hostIP, binPort), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, self)
	})}
	go func() { _ = srv.ListenAndServe() }()
	defer srv.Close()

	spec := fmt.Sprintf("resp:%s:%d/rooms:", *hostIP, port)
	s, err := plane.OpenStore(spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	defer s.Close()
	ctx := context.Background()
	for i := 0; i < *tasks; i++ {
		_ = s.Put(ctx, plane.Item{Kind: "task", ID: fmt.Sprintf("t%03d", i)})
	}
	// Every clone runs the same command. Which kind a peer prefers is decided
	// through the store (WorkerOptions.Roles), the one thing clones do not
	// share; a peer with nothing of its own kind to do helps with the other.
	for i := 0; i < *watchers; i++ {
		_ = s.Put(ctx, plane.Item{Kind: "role", ID: fmt.Sprintf("watcher%d", i)})
	}
	base := plane.WorkerOptions{Store: spec, Kind: "task", TTL: *ttl, Poll: 100 * time.Millisecond, WorkMin: *work, WorkMax: *work, SplitEvery: 7, StopWhenDry: true, Roles: true}
	wj, _ := json.Marshal(base)
	guest := fmt.Sprintf(`set -eu
export PATH=/nix/var/rooms/env/bin:$PATH
mkdir -p /tmp/swarm /workspace/out
cd /tmp/swarm
python3 -c "import urllib.request;urllib.request.urlretrieve('http://%s:%d/swarm','swarm')"
chmod +x swarm
ID=$(cat /proc/sys/kernel/random/uuid)
./swarm plane worker '%s' --incarnation "$ID" > /workspace/out/peer.log 2>&1
`, *hostIP, binPort, string(wj))

	argv := []string{"clone", *snapshot, "-n", fmt.Sprint(*clones), "--command", guest, "--max-wall", fmt.Sprintf("%ds", int(wall.Seconds())),
		"--out", filepath.Join(*out, "out"), "--json"}
	if *image != "" {
		argv = append(argv, "--image", *image)
	}
	if *toolstore != "" {
		argv = append(argv, "--toolstore", *toolstore)
	}
	var faultLog []string
	if *faults {
		go func() {
			for !quarterDone(ctx, s, *tasks) {
				time.Sleep(100 * time.Millisecond)
			}
			if room := firstRunningRoom(*roomsBin); room != "" {
				_ = exec.Command(*roomsBin, "kill", room).Run()
				faultLog = append(faultLog, "kill-room "+room)
			}
			if _, err := rp.restart(); err == nil {
				faultLog = append(faultLog, "restart-store")
			}
		}()
	}
	start := time.Now()
	cmd := exec.Command(*roomsBin, argv...)
	stdout, _ := os.Create(filepath.Join(*out, "clone-stdout.json"))
	stderr, _ := os.Create(filepath.Join(*out, "clone-stderr.log"))
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	wallTook := time.Since(start)

	h, err := s.History(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	rep := plane.Check(h, "task", "event")
	summary := map[string]any{"clones": *clones, "tasks": *tasks, "faults": faultLog, "wall_s": wallTook.Seconds(), "clone_exit": fmt.Sprint(runErr),
		"accepted": rep.Accepted, "items": rep.Items, "violations": rep.Violations, "unfinished": rep.Unfinished, "refusals": rep.Refusals, "passed": rep.OK()}
	data, _ := json.MarshalIndent(summary, "", "  ")
	_ = os.WriteFile(filepath.Join(*out, "summary.json"), data, 0o644)
	hf, _ := os.Create(filepath.Join(*out, "history.jsonl"))
	enc := json.NewEncoder(hf)
	for _, e := range h {
		_ = enc.Encode(e)
	}
	_ = hf.Close()
	fmt.Println(string(data))
	if !rep.OK() {
		return 1
	}
	return 0
}

func quarterDone(ctx context.Context, s plane.Store, tasks int) bool {
	items, err := s.List(ctx, "task")
	if err != nil {
		return false
	}
	done := 0
	for _, it := range items {
		if it.State == plane.Done {
			done++
		}
	}
	return done >= tasks/4
}

func firstRunningRoom(roomsBin string) string {
	out, _ := exec.Command(roomsBin, "ls").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, " running") {
			return strings.Fields(line)[0]
		}
	}
	return ""
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 56500
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
