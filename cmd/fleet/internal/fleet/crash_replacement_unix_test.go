//go:build !windows

package fleet

import (
	"bufio"
	stdcontext "context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type crashState struct {
	Owner            string `json:"owner"`
	ParentAlive      bool   `json:"parentAlive"`
	ChildAlive       bool   `json:"childAlive"`
	ChildStarted     bool   `json:"childStarted"`
	StaleWrite       bool   `json:"staleWrite"`
	ReplacementWrote bool   `json:"replacementWrote"`
	LastAction       string `json:"lastAction"`
}

// This is a negative-result regression: green means the model's unsafe branch
// trace reproduced against Fleet, NOT that orphan effects are prevented. The
// resource control must refuse replacement under the same process schedule.
func TestCrashReplacementModelTrace(t *testing.T) {
	b, err := os.ReadFile("../../model/artifacts/crash-replacement.trace.json")
	if err != nil {
		t.Fatal(err)
	}
	var trace []crashState
	if err := json.Unmarshal(b, &trace); err != nil {
		t.Fatal(err)
	}
	if len(trace) != 7 || !trace[len(trace)-1].StaleWrite || !trace[len(trace)-1].ReplacementWrote {
		t.Fatal("expected the six-step counterexample with both writers' effects")
	}
	for _, kind := range []string{"branch", "resource"} {
		t.Run(kind, func(t *testing.T) {
			r := newCrashReplay(t, kind)
			for _, want := range trace {
				r.step(want.LastAction)
				if kind == "resource" {
					want = resourceCrashState(want)
				}
				r.check(want)
			}
		})
	}
}

func resourceCrashState(want crashState) crashState {
	if want.Owner == "B" {
		want.Owner = "A"
	}
	want.StaleWrite, want.ReplacementWrote = false, false
	return want
}

type crashReplay struct {
	t        *testing.T
	kind     string
	key      string
	file     string
	parent   *exec.Cmd
	input    io.WriteCloser
	listener *net.TCPListener
	child    net.Conn
	reader   *bufio.Reader
	childPID int
	waited   bool
	observed crashState
}

func newCrashReplay(t *testing.T, kind string) *crashReplay {
	t.Helper()
	oldState, oldReadOnly, oldTakeovers := State, ReadOnly, HookTakeovers
	State, ReadOnly, HookTakeovers = t.TempDir(), false, nil
	t.Cleanup(func() { State, ReadOnly, HookTakeovers = oldState, oldReadOnly, oldTakeovers })
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 15*time.Second)
	t.Cleanup(cancel)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := &crashReplay{t: t, kind: kind, key: "repo:crash-fixture:main", listener: listener,
		file: filepath.Join(t.TempDir(), "effects"), observed: crashState{Owner: "none"}}
	if kind == "resource" {
		r.key = "slot:crash-fixture"
	}
	r.parent = exec.CommandContext(ctx, executable, "-test.run=^TestCrashProcessHelper$")
	r.parent.Env = append(os.Environ(), "FLEET_CRASH_HELPER=parent",
		"FLEET_CRASH_ADDR="+listener.Addr().String(), "FLEET_CRASH_FILE="+r.file)
	r.parent.Stderr = os.Stderr
	r.input, err = r.parent.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if r.child != nil {
			_ = r.child.Close() // EOF makes the orphan exit; it also has a deadline.
		}
		_ = r.input.Close()
		if !r.waited {
			_ = r.parent.Process.Kill()
			_ = r.parent.Wait()
		}
	})
	if err := os.MkdirAll(Path("sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	for sid, pid := range map[string]int{"A": r.parent.Process.Pid, "B": os.Getpid()} {
		rec := Rec{"session": sid, "pid_kind": "harness", "pid": pid}
		if err := os.WriteFile(Path("sessions", sid+".json"), DumpJSON(rec), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *crashReplay) step(action string) {
	r.t.Helper()
	r.observed.LastAction = action
	switch action {
	case "init":
	case "claim":
		if reason := CheckLease(r.key, "main", "A", "", ""); reason != "" {
			r.t.Fatal(reason)
		}
	case "spawnChild":
		r.spawn()
	case "crashParent":
		if err := r.parent.Process.Kill(); err != nil {
			r.t.Fatal(err)
		}
		err := r.parent.Wait() // Reap before Fleet observes the dead harness PID.
		r.waited = true
		if err == nil {
			r.t.Fatal("parent unexpectedly exited successfully")
		}
	case "replace":
		r.replace()
	case "replacementWrite":
		if r.kind == "resource" {
			return // Refused work must not execute.
		}
		if reason := CheckLease(r.key, "main", "B", "", ""); reason != "" {
			r.t.Fatal(reason)
		}
		if err := appendCrashEffect(r.file, "B"); err != nil {
			r.t.Fatal(err)
		}
		r.observed.ReplacementWrote = true
	case "childWrite":
		if _, err := fmt.Fprintln(r.child, "write"); err != nil {
			r.t.Fatal(err)
		}
		if r.line() != "written" {
			r.t.Fatal("child did not confirm its write")
		}
		r.observed.StaleWrite = S(Lease(r.key), "session") != "A"
	default:
		r.t.Fatalf("unsupported model action %q", action)
	}
}

func (r *crashReplay) replace() {
	r.t.Helper()
	reason := CheckLease(r.key, "main", "B", "", "")
	if r.kind == "branch" && reason != "" {
		r.t.Fatal(reason)
	}
	if r.kind == "resource" && !strings.Contains(reason, "not taken over automatically") {
		r.t.Fatalf("resource replacement must refuse, got %q", reason)
	}
}

func (r *crashReplay) spawn() {
	r.t.Helper()
	if _, err := fmt.Fprintln(r.input, "spawn"); err != nil {
		r.t.Fatal(err)
	}
	if err := r.listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		r.t.Fatal(err)
	}
	conn, err := r.listener.Accept()
	if err != nil {
		r.t.Fatal(err)
	}
	r.child, r.reader = conn, bufio.NewReader(conn)
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		r.t.Fatal(err)
	}
	var parentPID int
	if _, err := fmt.Sscanf(r.line(), "ready %d %d", &r.childPID, &parentPID); err != nil {
		r.t.Fatal(err)
	}
	if parentPID != r.parent.Process.Pid {
		r.t.Fatalf("writer is not the harness's child: parent=%d", parentPID)
	}
	r.observed.ChildStarted = true
}

func (r *crashReplay) line() string {
	r.t.Helper()
	line, err := r.reader.ReadString('\n')
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.TrimSpace(line)
}

func (r *crashReplay) check(want crashState) {
	r.t.Helper()
	var known bool
	r.observed.ParentAlive, known = Liveness("A")
	if !known {
		r.t.Fatal("harness liveness must be known")
	}
	r.observed.ChildAlive = r.childPID != 0 && PidAlive(r.childPID)
	r.observed.Owner = S(Lease(r.key), "session")
	if r.observed.Owner == "" {
		r.observed.Owner = "none"
	}
	if r.observed != want {
		r.t.Fatalf("after %s:\nobserved %+v\nmodel    %+v", want.LastAction, r.observed, want)
	}
	if want.LastAction != "childWrite" {
		return
	}
	b, err := os.ReadFile(r.file)
	if err != nil {
		r.t.Fatal(err)
	}
	wantEffects := "A\n"
	if r.kind == "branch" {
		wantEffects = "B\nA\n"
	}
	if string(b) != wantEffects {
		r.t.Fatalf("effects = %q, want %q", b, wantEffects)
	}
	r.t.Logf("%s: owner=%s, parent dead, child alive, effects=%q", r.kind, want.Owner, b)
}

// An ordinary process tree, not an agent session. The child holds an already
// admitted operation across parent death. All effects are confined to t.TempDir.
func TestCrashProcessHelper(_ *testing.T) {
	switch os.Getenv("FLEET_CRASH_HELPER") {
	case "parent":
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || line != "spawn\n" {
			os.Exit(2)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashProcessHelper$")
		cmd.Env = append(os.Environ(), "FLEET_CRASH_HELPER=child")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "child":
		if err := runCrashChild(); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
}

func runCrashChild() error {
	conn, err := net.DialTimeout("tcp", os.Getenv("FLEET_CRASH_ADDR"), 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(conn, "ready "+strconv.Itoa(os.Getpid())+" "+strconv.Itoa(os.Getppid())); err != nil {
		return err
	}
	input := bufio.NewScanner(conn)
	for input.Scan() {
		if input.Text() != "write" {
			return fmt.Errorf("unknown child command %q", input.Text())
		}
		if err := appendCrashEffect(os.Getenv("FLEET_CRASH_FILE"), "A"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(conn, "written"); err != nil {
			return err
		}
	}
	return input.Err()
}

func appendCrashEffect(path, who string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, who)
	return err
}
