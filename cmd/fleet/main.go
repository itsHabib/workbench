// Command fleet is the agent-fleet substrate: the harness hook, the operator's CLI,
// the MCP face and, later, the watcher — one binary, one store, two machines.
//
//	fleet hook claude        read one Claude Code hook event on stdin; exit 0 allow / 2 deny
//	fleet hook codex         the same, translated from Codex's event shape
//	fleet hook <h> --shadow  compute the verdict from the live store, write nothing but
//	                         shadow.jsonl, always exit 0: a day's numbers before a switch
//	fleet mcp                the verbs as MCP tools over stdio
//	fleet <verb> ...         the operator's side: stop, resume, revoke, take, drop, board, ...
//
// Exit codes are a load-bearing seam. For the hook: 0 allow, 2 deny with the reason
// on stderr. For a verb: 0 ok, 1 refused with the reason on stderr (a refusal is the
// substrate doing its one job), 2 usage, and `fleet done` adds 3 for failed evidence.
//
// This is a port of the reference Python (hook.py, fleet.py, fleet-mcp.py,
// codex-adapter.py) kept faithful on purpose: the store shapes, filenames, exit codes
// and refusal texts are a contract that the suite pins and that a store the Python
// wrote must still satisfy.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/codex"
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/mcp"
	"github.com/itsHabib/workbench/cmd/fleet/internal/provider"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
	"github.com/itsHabib/workbench/cmd/fleet/internal/watch"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		verbs.Usage(2)
	}
	switch args[0] {
	case "_provider-exec":
		if err := provider.ExecBarrier(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(127)
		}
	case "_provider-process":
		code, err := provider.ObserveCommand(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	case "hook":
		runHook(args[1:])
	case "mcp":
		mcp.Serve(os.Stdin, os.Stdout)
	case "status":
		if len(args) > 1 && args[1] == "--all" {
			runAllStatus(args[2:])
			return
		}
		verbs.Run(args)
	case "check":
		runCheck(args[1:])
	case "run-report":
		runReport(args[1:])
	case "inspect", "trace":
		runInspect(args)
	case "tail":
		runTail(args[1:])
	case "watch":
		runWatch(args[1:])
	default:
		verbs.Run(args)
	}
}

// runCheck bypasses the mutating verb router and never ticks the watcher.
func runCheck(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: fleet check <address>")
		os.Exit(2)
	}
	result, err := watch.Check(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s\n", fleet.DumpJSON(result))
}

// reviveWatcher starts a detached watcher from SessionStart when none has ticked
// recently. SessionStart is the one event where a spawn is permitted, and this is
// what makes the watcher need no install step: any session revives it. It lives here
// rather than in the hook package because the watcher folds through the verbs, and
// the hook package cannot import what imports it.
func reviveWatcher(ev map[string]any) (started bool) {
	if ev["hook_event_name"] != "SessionStart" {
		return false
	}
	defer func() { _ = recover() }() // never a reason for a hook to fail
	return watch.EnsureRunning()
}

// runWatch: `fleet watch` ticks forever; `fleet watch --once` ticks once and prints the
// board; `--interval 30s` sets the tick.
// watchControl handles the operator verbs on a retained attempt: `cancel` and
// `release`. It reports whether it consumed the arguments.
func watchControl(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "cancel":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: fleet watch cancel <address>")
			os.Exit(2)
		}
		if err := watch.Cancel(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("interrupt requested; inspect watch status for the terminal result")
		return true
	case "release":
		if len(args) != 4 || args[2] != "--why" {
			fmt.Fprintln(os.Stderr, "usage: fleet watch release <address> --why <reason>")
			os.Exit(2)
		}
		if err := watch.Release(args[1], args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("reservation released and recorded; the next eligible wake may launch")
		return true
	}
	return false
}

func runWatch(args []string) {
	if watchControl(args) {
		return
	}
	if len(args) > 0 && args[0] == "status" {
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: fleet watch status [--json]")
			os.Exit(2)
		}
		if len(args) == 2 {
			fmt.Printf("%s\n", fleet.DumpJSON(watch.RuntimeStatus()))
			return
		}
		fmt.Print(watch.RuntimeText())
		return
	}
	interval := watch.DefaultInterval
	once := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--once":
			once = true
		case "--interval":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil && d > 0 {
					interval = d
				}
				i++
			}
		}
	}
	if once {
		md, err := watch.Tick(interval)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fleet watch: "+err.Error())
			os.Exit(4)
		}
		fmt.Print(md)
		return
	}
	if err := watch.Serve(interval); err != nil {
		fmt.Fprintln(os.Stderr, "fleet watch: "+err.Error())
		os.Exit(1)
	}
}

// runShadow is the hook beside the installed one: the same verdict from the same
// store, nothing written but one line of events.jsonl, exit 0 whatever the verdict.
// The harness never sees it. `fleet shadow-report` reads the day.
func runShadow(which string, ev map[string]any) {
	fleet.ReadOnly = true
	defer os.Exit(0)
	defer func() { _ = recover() }()
	t0 := time.Now()
	var v *fleet.Verdict
	switch which {
	case "codex":
		v = codex.Run(ev)
	default:
		v = fleet.Run(ev)
	}
	logVerdict(which, ev, v, t0, true)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// runHook reads one event and applies the verdict. The fail-open law lives here: a
// malformed event, or any panic that escaped Run, exits 0 with no output.
func runHook(args []string) {
	which := "claude"
	shadow := false
	for _, a := range args {
		if a == "--shadow" {
			shadow = true
			continue
		}
		which = a
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(0)
	}
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil || ev == nil {
		os.Exit(0)
	}
	if shadow {
		runShadow(which, ev)
		return
	}
	t0 := time.Now()
	switch which {
	case "claude":
		v := fleet.Run(ev)
		logVerdict(which, ev, v, t0, false)
		started := reviveWatcher(ev)
		fleet.Exit(withWatcherHealth(ev, v, started))
	case "codex":
		v := codex.Run(ev)
		logVerdict(which, ev, v, t0, false)
		started := reviveWatcher(ev)
		fleet.Exit(withWatcherHealth(ev, v, started))
	default:
		fmt.Fprintf(os.Stderr, "fleet hook: unknown harness %q (claude|codex)\n", which)
		os.Exit(2)
	}
}

// logVerdict observes the completed evaluation. Logging cannot change its outcome.
func logVerdict(which string, ev fleet.Rec, v *fleet.Verdict, start time.Time, shadow bool) {
	// The verdict is already decided; this runs before Exit. A panic here would leave
	// the process with Go's own exit status 2 — which on PreToolUse IS the deny code —
	// so observing an evaluation could deny the call it observed. Telemetry is not
	// authority: swallow anything that escapes, exactly as reviveWatcher does.
	defer func() { _ = recover() }()
	rec := fleet.Rec{"at": fleet.Now(), "harness": which, "event": ev["hook_event_name"], "session": ev["session_id"],
		"tool": ev["tool_name"], "tool_use_id": ev["tool_use_id"], "cwd": ev["cwd"], "code": 0, "reason": nil,
		"ms": float64(time.Since(start).Microseconds()) / 1000, "shadow": shadow, "prompt_truncated": fleet.PromptTruncated}
	if inp := ev["tool_input"]; inp != nil {
		sum := sha256.Sum256(fleet.DumpJSON(fleet.Rec{"tool": ev["tool_name"], "input": inp, "cwd": ev["cwd"]}))
		rec["fingerprint"] = fmt.Sprintf("%x", sum)
	}
	if v != nil {
		rec["code"] = v.Code
		rec["reason"] = cut(v.Err, 300)
		rec["out"] = cut(v.Out, 200)
		rec["takeovers"] = fleet.HookTakeovers
	}
	_ = fleet.ShadowAppend(fleet.Path("events.jsonl"), rec)
}

func runTail(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fleet tail <seat|role> [-n 20] [-f]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	count := fs.Int("n", 20, "recent text/tool events")
	follow := fs.Bool("f", false, "follow new events until interrupted")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *count < 1 || *count > 1000 {
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := watch.Tail(ctx, os.Stdout, args[0], *count, *follow); err != nil {
		fmt.Fprintln(os.Stderr, "fleet tail:", err)
		os.Exit(1)
	}
}

func runAllStatus(args []string) {
	fs := flag.NewFlagSet("status --all", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: fleet status --all [--json]")
		os.Exit(2)
	}
	status := watch.AllStatus()
	if *asJSON {
		fmt.Printf("%s\n", fleet.DumpJSON(status))
		return
	}
	fmt.Print(watch.AllStatusText(status))
}

func runReport(args []string) {
	fs := flag.NewFlagSet("run-report", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	since := fs.Duration("since", 24*time.Hour, "output modification window")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *since <= 0 {
		fmt.Fprintln(os.Stderr, "usage: fleet run-report [--since 24h] [--json]")
		os.Exit(2)
	}
	report := watch.RunReport(fleet.Now() - since.Seconds())
	if *asJSON {
		fmt.Printf("%s\n", fleet.DumpJSON(report))
		return
	}
	fmt.Print(watch.RunReportText(report))
}

// withWatcherHealth adds startup context without changing the hook verdict.
func withWatcherHealth(ev fleet.Rec, v *fleet.Verdict, started bool) *fleet.Verdict {
	if fleet.S(ev, "hook_event_name") != "SessionStart" {
		return v
	}
	state, hb := watch.WatcherHealth()
	if started && state != "running" {
		state = "revival requested; last observed " + state
	}
	line := fmt.Sprintf("[fleet] watcher: %s; inspect workers with fleet status --all; trace with fleet tail <address>", state)
	if at := fleet.F(hb, "at"); at > 0 {
		line += fmt.Sprintf("; last tick %s ago", fleet.FmtAge(fleet.Now()-at))
	}
	out := fleet.ReadJSONBytes([]byte(v.Out))
	if out == nil {
		out = fleet.Rec{}
	}
	specific := fleet.M(out, "hookSpecificOutput")
	if specific == nil {
		specific = fleet.Rec{"hookEventName": "SessionStart"}
		out["hookSpecificOutput"] = specific
	}
	context := fleet.S(specific, "additionalContext")
	if context != "" {
		context += "\n"
	}
	specific["additionalContext"] = context + line
	updated := *v
	updated.Out = string(fleet.DumpJSON(out)) + "\n"
	return &updated
}

// runInspect is a read-only projection for operator UIs and trace consumers.
func runInspect(args []string) {
	if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--json") {
		fmt.Fprintln(os.Stderr, "usage: fleet inspect|trace <address> [--json]")
		os.Exit(2)
	}
	var result fleet.Rec
	var err error
	if args[0] == "trace" {
		result, err = watch.Trace(args[1])
	}
	if args[0] == "inspect" {
		result, err = watch.Inspect(args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s\n", fleet.DumpJSON(result))
}
