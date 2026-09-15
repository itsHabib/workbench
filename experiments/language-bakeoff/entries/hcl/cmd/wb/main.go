// Command wb compiles *.wb.hcl source into normalized intent, plans it
// against live observations and retained evidence, and applies it through
// adapters.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hashicorp/hcl/v2"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapters"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/apply"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/plan"
)

// Exit codes.
const (
	exitOK     = 0
	exitFailed = 1  // apply incomplete, or an observation failed
	exitUsage  = 2  // bad flags, or source that does not compile
	exitStale  = 3  // a saved plan no longer matches the workspace
	exitCrash  = 70 // injected crash (WB_FAULT=<address>:crash-after)
)

const usage = `wb: a declarative workbench over HCL

usage:
  wb compile [-C dir]               print normalized intent as JSON
  wb plan    [-C dir] [-out file]   show proposed changes; optionally save them
  wb apply   [-C dir] [-plan file]  apply a fresh plan, or a saved plan if still current
  wb log     [-C dir]               show retained evidence

exit codes: 0 ok, 1 failed or incomplete, 2 usage or source error, 3 stale plan refused
fault injection (demo and tests only):
  WB_FAULT=<address>              that step fails before its effect
  WB_FAULT=<address>:crash-after  wb exits after that effect, before journaling it
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmds := map[string]func([]string, io.Writer, io.Writer) int{
		"compile": cmdCompile,
		"plan":    cmdPlan,
		"apply":   cmdApply,
		"log":     cmdLog,
	}
	if len(args) == 0 || cmds[args[0]] == nil {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	return cmds[args[0]](args[1:], stdout, stderr)
}

func flags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("C", ".", "workspace directory holding *.wb.hcl")
	return fs, dir
}

// session is one command's view: compiled intent plus what planning reads.
type session struct {
	in  *intent.Intent
	env plan.Env
}

func compileDir(dir string, stderr io.Writer) (*intent.Intent, *adapter.Registry, bool) {
	reg, err := adapter.NewRegistry(adapters.Builtin()...)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, nil, false
	}
	srcs, err := intent.LoadDir(dir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, nil, false
	}
	in, files, diags := intent.Compile(srcs, reg)
	if diags.HasErrors() {
		wr := hcl.NewDiagnosticTextWriter(stderr, files, 0, false)
		_ = wr.WriteDiagnostics(diags)
		return nil, nil, false
	}
	return in, reg, true
}

func open(dir string, stderr io.Writer) (*session, bool) {
	in, reg, ok := compileDir(dir, stderr)
	if !ok {
		return nil, false
	}
	ws, err := adapter.NewWorkspace(dir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, false
	}
	jr, err := evidence.Open(ws.Root())
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, false
	}
	if jr.Unreadable > 0 {
		fmt.Fprintf(stderr, "warning: %d unreadable journal line(s) ignored (a torn append?)\n", jr.Unreadable)
	}
	return &session{in: in, env: plan.Env{Registry: reg, Workspace: ws, Journal: jr}}, true
}

func cmdCompile(args []string, stdout, stderr io.Writer) int {
	fs, dir := flags("compile", stderr)
	if fs.Parse(args) != nil {
		return exitUsage
	}
	in, _, ok := compileDir(*dir, stderr)
	if !ok {
		return exitUsage
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(in); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFailed
	}
	return exitOK
}

func cmdPlan(args []string, stdout, stderr io.Writer) int {
	fs, dir := flags("plan", stderr)
	out := fs.String("out", "", "save the plan here for `wb apply -plan`")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	s, ok := open(*dir, stderr)
	if !ok {
		return exitUsage
	}
	p, err := plan.Make(s.in, s.env)
	if err != nil {
		fmt.Fprintf(stderr, "error: plan: %v\n", err)
		return exitFailed
	}
	p.Render(stdout)
	if *out == "" {
		return exitOK
	}
	if err := p.Save(*out); err != nil {
		fmt.Fprintf(stderr, "error: save plan: %v\n", err)
		return exitFailed
	}
	fmt.Fprintf(stdout, "Saved plan %s to %s. Apply exactly this plan with: wb apply -plan %s\n", plan.Short(p.Digest()), *out, *out)
	return exitOK
}

func cmdApply(args []string, stdout, stderr io.Writer) int {
	fs, dir := flags("apply", stderr)
	saved := fs.String("plan", "", "apply this saved plan; refuse if the workspace moved since")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	opts, ok := faultOptions(stderr)
	if !ok {
		return exitUsage
	}
	s, ok := open(*dir, stderr)
	if !ok {
		return unverifiable(*saved, exitUsage, stderr)
	}
	p, err := plan.Make(s.in, s.env)
	if err != nil {
		fmt.Fprintf(stderr, "error: plan: %v\n", err)
		return unverifiable(*saved, exitFailed, stderr)
	}
	if code := review(p, *saved, stdout, stderr); code != exitOK {
		return code
	}
	if p.Changes() == 0 {
		fmt.Fprintln(stdout, "Nothing to apply.")
		return exitOK
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "\nApplying (run %d):\n", s.env.Journal.NextRun())
	results, err := apply.Apply(ctx, p, s.in, s.env, opts)
	renderResults(stdout, results)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFailed
	}
	return report(results, s, stdout, stderr)
}

// unverifiable maps a failure to re-plan onto the saved-plan contract: a
// reviewed plan that cannot be checked against the workspace is stale.
func unverifiable(saved string, code int, stderr io.Writer) int {
	if saved == "" {
		return code
	}
	fmt.Fprintln(stderr, "error: the saved plan cannot be verified against the workspace; nothing was applied.")
	return exitStale
}

// review prints a fresh plan, or checks that a saved plan still describes
// the workspace: same source, same steps, same observations.
func review(fresh *plan.Plan, savedPath string, stdout, stderr io.Writer) int {
	if savedPath == "" {
		fresh.Render(stdout)
		return exitOK
	}
	saved, err := plan.Load(savedPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitUsage
	}
	why := plan.Stale(saved, fresh)
	if len(why) == 0 {
		fmt.Fprintf(stdout, "Saved plan %s still matches the workspace. %s\n", plan.Short(saved.Digest()), saved.Summary())
		return exitOK
	}
	fmt.Fprintf(stderr, "error: saved plan %s is stale; nothing was applied.\n", plan.Short(saved.Digest()))
	for _, w := range why {
		fmt.Fprintf(stderr, "  - %s\n", w)
	}
	fmt.Fprintln(stderr, "Run `wb plan` again, review the new plan, then apply it.")
	return exitStale
}

func renderResults(w io.Writer, results []apply.Result) {
	width := 0
	for _, r := range results {
		width = max(width, len(r.Address))
	}
	for _, r := range results {
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("  %-*s  %-9s  %s", width, r.Address, r.Outcome, r.Note), " "))
	}
}

// report summarizes the apply, then observes the result afresh: the outcome
// as the backend reports it, not as the executor believes it went.
func report(results []apply.Result, s *session, stdout, stderr io.Writer) int {
	n := map[apply.Outcome]int{}
	for _, r := range results {
		n[r.Outcome]++
	}
	incomplete := n[apply.Failed]+n[apply.Skipped] > 0
	verdict := "complete"
	if incomplete {
		verdict = "incomplete"
	}
	fmt.Fprintf(stdout, "\nApply %s: %d created, %d updated, %d ran, %d failed, %d skipped.\n",
		verdict, n[apply.Created], n[apply.Updated], n[apply.Ran], n[apply.Failed], n[apply.Skipped])
	if incomplete {
		fmt.Fprintln(stdout, "Completed effects stay in place (no rollback). Fix the cause, then run `wb apply` again to retry what is left.")
	}
	observe(s, stdout, stderr)
	if incomplete {
		return exitFailed
	}
	return exitOK
}

func observe(s *session, stdout, stderr io.Writer) {
	p, err := plan.Make(s.in, s.env)
	if err != nil {
		fmt.Fprintf(stderr, "warning: could not observe the result: %v\n", err)
		return
	}
	if p.Changes() == 0 {
		fmt.Fprintln(stdout, "Observed after apply: converged (a fresh plan finds no changes).")
		return
	}
	fmt.Fprintf(stdout, "Observed after apply: %d pending:\n", p.Changes())
	for _, st := range p.Steps {
		if st.Action != plan.Keep {
			fmt.Fprintf(stdout, "  %s %s: %s\n", st.Action, st.Address, strings.Join(st.Reasons, "; "))
		}
	}
}

func faultOptions(stderr io.Writer) (apply.Options, bool) {
	spec := os.Getenv("WB_FAULT")
	if spec == "" {
		return apply.Options{}, true
	}
	addr, mode, _ := strings.Cut(spec, ":")
	fmt.Fprintf(stderr, "fault injection active: WB_FAULT=%s\n", spec)
	switch mode {
	case "", "fail":
		return apply.Options{FailBefore: addr}, true
	case "crash-after":
		return apply.Options{AfterEffect: crashAfter(addr, stderr)}, true
	}
	fmt.Fprintf(stderr, "error: WB_FAULT mode %q: want <address>, <address>:fail or <address>:crash-after\n", mode)
	return apply.Options{}, false
}

// crashAfter simulates the process dying between an effect and its journal
// entry: os.Exit skips every deferred cleanup, like a real crash would.
func crashAfter(addr string, stderr io.Writer) func(string) {
	return func(a string) {
		if a != addr {
			return
		}
		fmt.Fprintf(stderr, "fault injection: exiting after %s's effect, before its result is journaled\n", a)
		os.Exit(exitCrash)
	}
}
