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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/codex"
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/mcp"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
	"github.com/itsHabib/workbench/cmd/fleet/internal/watch"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		verbs.Usage(2)
	}
	switch args[0] {
	case "hook":
		runHook(args[1:])
	case "mcp":
		mcp.Serve(os.Stdin, os.Stdout)
	case "watch":
		runWatch(args[1:])
	default:
		verbs.Run(args)
	}
}

// reviveWatcher starts a detached watcher from SessionStart when none has ticked
// recently. SessionStart is the one event where a spawn is permitted, and this is
// what makes the watcher need no install step: any session revives it. It lives here
// rather than in the hook package because the watcher folds through the verbs, and
// the hook package cannot import what imports it.
func reviveWatcher(ev map[string]any) {
	if ev["hook_event_name"] != "SessionStart" {
		return
	}
	defer func() { _ = recover() }() // never a reason for a hook to fail
	watch.EnsureRunning()
}

// runWatch: `fleet watch` ticks forever; `fleet watch --once` ticks once and prints the
// board; `--interval 30s` sets the tick.
func runWatch(args []string) {
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
// store, nothing written but one line of shadow.jsonl, exit 0 whatever the verdict.
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
	rec := map[string]any{"at": fleet.Now(), "harness": which, "event": ev["hook_event_name"], "session": ev["session_id"],
		"tool": ev["tool_name"], "tool_use_id": ev["tool_use_id"], "code": 0.0, "reason": nil, "ms": float64(time.Since(t0).Microseconds()) / 1000}
	if v != nil {
		rec["code"] = float64(v.Code)
		if v.Err != "" {
			rec["reason"] = cut(v.Err, 300)
		}
		if v.Out != "" {
			rec["out"] = cut(v.Out, 200)
		}
	}
	_ = fleet.ShadowAppend(fleet.Path("shadow.jsonl"), rec)
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
	switch which {
	case "claude":
		v := fleet.Run(ev)
		reviveWatcher(ev)
		fleet.Exit(v)
	case "codex":
		v := codex.Run(ev)
		reviveWatcher(ev)
		fleet.Exit(v)
	default:
		fmt.Fprintf(os.Stderr, "fleet hook: unknown harness %q (claude|codex)\n", which)
		os.Exit(2)
	}
}
