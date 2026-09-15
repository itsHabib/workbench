// Package cli turns a workflow definition into a command-line program.
// The program is compiled from the workflow's own Go source, so running
// it runs that source: `intent`, `plan` and plain `apply` evaluate the
// definition. `apply -plan FILE` does not evaluate it; it applies the
// saved, reviewed plan as data (the binary itself is still user code).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// Exit codes.
const (
	ExitOK     = 0
	ExitFailed = 1 // evaluation, planning or apply error, including a partial apply
	ExitUsage  = 2
	ExitStale  = 3 // a saved plan no longer matches the workspace; nothing was applied
)

const usage = `usage:
  PROGRAM intent [-var key=value]...
  PROGRAM plan   -dir DIR [-var key=value]... [-out PLAN.json]
  PROGRAM apply  -dir DIR [-var key=value]...   plan, show the plan, then apply it
  PROGRAM apply  -dir DIR -plan PLAN.json       apply a saved plan if it is still current

exit codes: 0 ok, 1 failed (including a partial apply), 2 usage, 3 stale plan refused
fault injection for demos and tests: WB_FAULT=fail:<address> or WB_FAULT=crash:<address>
`

// Program is a workflow program: a definition plus the adapters for the
// node kinds it declares. Registration is explicit and compile-time;
// there is no plugin loading.
type Program struct {
	Define   wb.Define
	Adapters []engine.Adapter
}

// Main runs the program with os.Args and exits.
func (p Program) Main() {
	os.Exit(p.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// Run executes one subcommand and returns its exit code.
func (p Program) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	reg, err := engine.NewRegistry(p.Adapters...)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitFailed
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	c := command{define: p.Define, reg: reg, out: stdout, errw: stderr}
	switch args[0] {
	case "intent":
		return c.intent(args[1:])
	case "plan":
		return c.plan(args[1:])
	case "apply":
		return c.apply(ctx, args[1:])
	}
	fmt.Fprint(stderr, usage)
	return ExitUsage
}

type command struct {
	define wb.Define
	reg    engine.Registry
	out    io.Writer
	errw   io.Writer
}

func (c command) fail(err error) int {
	fmt.Fprintf(c.errw, "error: %v\n", err)
	return ExitFailed
}

func (c command) intent(args []string) int {
	fs, vars := flags("intent", c.errw)
	if fs.Parse(args) != nil {
		return ExitUsage
	}
	in, err := wb.Evaluate(c.define, vars)
	if err != nil {
		return c.fail(err)
	}
	b, err := encode(in)
	if err != nil {
		return c.fail(err)
	}
	_, _ = c.out.Write(b)
	return ExitOK
}

func (c command) plan(args []string) int {
	fs, vars := flags("plan", c.errw)
	dir := fs.String("dir", "", "workspace directory (must exist)")
	out := fs.String("out", "", "save the plan as JSON for a later apply -plan")
	if fs.Parse(args) != nil || *dir == "" {
		fmt.Fprint(c.errw, usage)
		return ExitUsage
	}
	in, err := wb.Evaluate(c.define, vars)
	if err != nil {
		return c.fail(err)
	}
	ws, err := open(*dir)
	if err != nil {
		return c.fail(err)
	}
	defer func() { _ = ws.Close() }()
	p, err := engine.Compute(ws, c.reg, in)
	if err != nil {
		return c.fail(err)
	}
	renderPlan(c.out, p)
	if *out == "" {
		return ExitOK
	}
	if err := save(*out, p); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "Saved plan to %s\n", *out)
	return ExitOK
}

func (c command) apply(ctx context.Context, args []string) int {
	fs, vars := flags("apply", c.errw)
	dir := fs.String("dir", "", "workspace directory (must exist)")
	file := fs.String("plan", "", "apply this saved plan instead of planning now")
	if fs.Parse(args) != nil || *dir == "" {
		fmt.Fprint(c.errw, usage)
		return ExitUsage
	}
	if *file != "" && len(vars) > 0 {
		fmt.Fprintln(c.errw, "-var cannot be combined with -plan: the saved plan already fixed its params")
		return ExitUsage
	}
	ws, err := open(*dir)
	if err != nil {
		return c.fail(err)
	}
	defer func() { _ = ws.Close() }()
	p, err := c.planFor(ws, *file, vars)
	if err != nil {
		return c.fail(err)
	}
	results, err := engine.Apply(ctx, ws, c.reg, p)
	var stale *engine.StaleError
	if errors.As(err, &stale) {
		renderStale(c.errw, stale)
		return ExitStale
	}
	if err != nil {
		return c.fail(err)
	}
	renderResults(c.out, results)
	// Observed outcome: plan the same intent again against what is there now.
	after, err := engine.Compute(ws, c.reg, p.Intent)
	if err != nil {
		return c.fail(err)
	}
	renderObserved(c.out, after)
	if count(results, engine.Failed)+count(results, engine.Skipped) > 0 {
		return ExitFailed
	}
	return ExitOK
}

// planFor loads a saved plan, or evaluates the workflow and plans now,
// printing that plan before anything is applied.
func (c command) planFor(ws *foundation.Workspace, file string, vars map[string]string) (engine.Plan, error) {
	if file != "" {
		return load(file)
	}
	in, err := wb.Evaluate(c.define, vars)
	if err != nil {
		return engine.Plan{}, err
	}
	p, err := engine.Compute(ws, c.reg, in)
	if err != nil {
		return engine.Plan{}, err
	}
	renderPlan(c.out, p)
	fmt.Fprintln(c.out)
	return p, nil
}

func open(dir string) (*foundation.Workspace, error) {
	fault, err := foundation.ParseFault(os.Getenv("WB_FAULT"))
	if err != nil {
		return nil, err
	}
	return foundation.Open(dir, fault)
}

func flags(name string, errw io.Writer) (*flag.FlagSet, map[string]string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errw)
	vars := map[string]string{}
	fs.Var(varFlag(vars), "var", "set a workflow param as key=value (repeatable)")
	return fs, vars
}

type varFlag map[string]string

func (v varFlag) String() string { return "" }

func (v varFlag) Set(s string) error {
	k, val, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return errors.New("want key=value")
	}
	v[k] = val
	return nil
}

// encode renders JSON for people: indented, with "<", ">" and "&" left as is.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func save(file string, p engine.Plan) error {
	b, err := encode(p)
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

func load(file string) (engine.Plan, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return engine.Plan{}, err
	}
	var p engine.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return engine.Plan{}, fmt.Errorf("read plan %s: %w", file, err)
	}
	return p, nil
}

func count(results []engine.Result, s engine.Status) int {
	n := 0
	for _, r := range results {
		if r.Status == s {
			n++
		}
	}
	return n
}
