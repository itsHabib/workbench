// Package cli is the wb command line. It wires source files, the planner
// and a workspace together and maps outcomes to exit codes.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/lang"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/plan"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitFailed  = 1 // an effect or observation failed; completed effects are recorded
	ExitInvalid = 2 // bad usage, source or plan
	ExitRefused = 3 // a stale or conflicting plan; nothing was changed
)

const usage = `usage: wb <command> [flags] [source.wb]

commands:
  check     validate a source file
  intent    print a source file's normalized intent as JSON
  plan      show proposed changes; -out saves the plan
  apply     apply a source file, or a saved plan with -plan
  evidence  print the workspace journal
  kinds     list registered adapter kinds

flags:
  -dir      workspace directory (plan, apply, evidence; default ".")
  -out      file to save a plan to (plan)
  -plan     saved plan to apply instead of a source file (apply)

exit codes: 0 ok, 1 failed partway, 2 invalid input, 3 refused (stale plan or conflict)
`

type cmd struct {
	reg *kind.Registry
	out io.Writer
	err io.Writer
}

// Run executes one wb command and returns its exit code.
func Run(ctx context.Context, args []string, reg *kind.Registry, stdout, stderr io.Writer) int {
	c := &cmd{reg: reg, out: stdout, err: stderr}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitInvalid
	}
	switch args[0] {
	case "check":
		return c.checkCmd(args[1:])
	case "intent":
		return c.intent(args[1:])
	case "plan":
		return c.plan(args[1:])
	case "apply":
		return c.apply(ctx, args[1:])
	case "evidence":
		return c.evidence(args[1:])
	case "kinds":
		return c.kinds()
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return ExitOK
	}
	fmt.Fprintf(stderr, "wb: unknown command %q\n\n%s", args[0], usage)
	return ExitInvalid
}

// parseArgs lets flags appear before or after the source file.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos, args = append(pos, args[0]), args[1:]
	}
}

func (c *cmd) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("wb "+name, flag.ContinueOnError)
	fs.SetOutput(c.err)
	return fs
}

func (c *cmd) fail(code int, format string, args ...any) int {
	fmt.Fprintf(c.err, "wb: "+format+"\n", args...)
	return code
}

// compile reads and checks exactly one source file.
func (c *cmd) compile(pos []string) (*intent.Intent, []byte, int) {
	if len(pos) != 1 {
		return nil, nil, c.fail(ExitInvalid, "expected one source file, got %d", len(pos))
	}
	src, err := os.ReadFile(pos[0])
	if err != nil {
		return nil, nil, c.fail(ExitInvalid, "%v", err)
	}
	in, code := c.check(pos[0], src)
	return in, src, code
}

// check compiles source text, printing any diagnostics.
func (c *cmd) check(name string, src []byte) (*intent.Intent, int) {
	in, err := lang.Compile(name, src, c.reg)
	var ds lang.Diagnostics
	if errors.As(err, &ds) {
		fmt.Fprintln(c.err, ds.Format(name))
		return nil, c.fail(ExitInvalid, "%s: %d error(s)", name, len(ds))
	}
	if err != nil {
		return nil, c.fail(ExitInvalid, "%v", err)
	}
	return in, ExitOK
}

func (c *cmd) checkCmd(args []string) int {
	pos, err := parseArgs(c.flags("check"), args)
	if err != nil {
		return ExitInvalid
	}
	in, _, code := c.compile(pos)
	if in == nil {
		return code
	}
	n := map[string]int{}
	for _, d := range in.Decls {
		n[d.Lifecycle]++
	}
	fmt.Fprintf(c.out, "%s: ok, %d kept resource(s), %d piece(s) of work\n", pos[0], n[string(kind.Keep)], n[string(kind.Run)])
	return ExitOK
}

func (c *cmd) intent(args []string) int {
	pos, err := parseArgs(c.flags("intent"), args)
	if err != nil {
		return ExitInvalid
	}
	in, _, code := c.compile(pos)
	if in == nil {
		return code
	}
	return c.printJSON(in)
}

func (c *cmd) printJSON(v any) int {
	b, err := readableJSON(v)
	if err != nil {
		return c.fail(ExitFailed, "%v", err)
	}
	fmt.Fprintln(c.out, string(b))
	return ExitOK
}

// stringList matches an indented JSON array holding only strings.
var stringList = regexp.MustCompile(`\[\n(?:\s*"(?:[^"\\]|\\.)*",?\n)+\s*\]`)

// readableJSON indents v but keeps each list of strings on one line, so an
// intent or plan reads one attribute per line.
func readableJSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return stringList.ReplaceAllFunc(b, func(list []byte) []byte {
		var items []string
		if err := json.Unmarshal(list, &items); err != nil {
			return list
		}
		quoted := make([]string, len(items))
		for i, item := range items {
			q, _ := json.Marshal(item) // a string always marshals
			quoted[i] = string(q)
		}
		return []byte("[" + strings.Join(quoted, ", ") + "]")
	}), nil
}

func (c *cmd) openWorkspace(dir string) (*foundation.Workspace, int) {
	ws, err := foundation.Open(dir)
	if err != nil {
		return nil, c.fail(ExitInvalid, "%v", err)
	}
	return ws, ExitOK
}

func (c *cmd) plan(args []string) int {
	fs := c.flags("plan")
	dir := fs.String("dir", ".", "workspace directory")
	out := fs.String("out", "", "save the plan to this file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return ExitInvalid
	}
	in, src, code := c.compile(pos)
	if in == nil {
		return code
	}
	ws, code := c.openWorkspace(*dir)
	if ws == nil {
		return code
	}
	defer ws.Close()
	p, err := plan.Make(in, c.reg, ws)
	if err != nil {
		return c.fail(ExitFailed, "plan: %v", err)
	}
	p.Text = string(src)
	p.Render(c.out)
	if *out == "" {
		return ExitOK
	}
	b, err := readableJSON(p)
	if err != nil {
		return c.fail(ExitFailed, "%v", err)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		return c.fail(ExitFailed, "%v", err)
	}
	fmt.Fprintf(c.out, "saved plan to %s\n", *out)
	return ExitOK
}

func (c *cmd) apply(ctx context.Context, args []string) int {
	fs := c.flags("apply")
	dir := fs.String("dir", ".", "workspace directory")
	saved := fs.String("plan", "", "apply this saved plan instead of a source file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return ExitInvalid
	}
	p, code := c.planFor(pos, *saved, *dir)
	if p == nil {
		return code
	}
	ws, code := c.openWorkspace(*dir)
	if ws == nil {
		return code
	}
	defer ws.Close()
	rep, err := plan.Apply(ctx, p, c.reg, ws)
	if rep != nil {
		fmt.Fprintln(c.out, "apply:")
		rep.Render(c.out)
	}
	return c.applyExit(rep, err)
}

// planFor loads a saved plan, or plans a source file now and shows it.
func (c *cmd) planFor(pos []string, saved, dir string) (*plan.Plan, int) {
	if saved != "" && len(pos) > 0 {
		return nil, c.fail(ExitInvalid, "give a source file or -plan, not both")
	}
	if saved != "" {
		return c.loadPlan(saved)
	}
	in, _, code := c.compile(pos)
	if in == nil {
		return nil, code
	}
	ws, code := c.openWorkspace(dir)
	if ws == nil {
		return nil, code
	}
	defer ws.Close()
	p, err := plan.Make(in, c.reg, ws)
	if err != nil {
		return nil, c.fail(ExitFailed, "plan: %v", err)
	}
	p.Render(c.out)
	return p, ExitOK
}

func (c *cmd) loadPlan(path string) (*plan.Plan, int) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, c.fail(ExitInvalid, "%v", err)
	}
	var p plan.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, c.fail(ExitInvalid, "%s: %v", path, err)
	}
	if p.Format != plan.Format || len(p.Changes) != len(p.Decls) {
		return nil, c.fail(ExitInvalid, "%s is not a format-%d plan", path, plan.Format)
	}
	// A saved plan gets the same checks as source: its embedded source must
	// still compile, to exactly the declarations the plan would apply.
	in, code := c.check(p.Source, []byte(p.Text))
	if in == nil {
		return nil, c.fail(code, "%s: its embedded source does not check", path)
	}
	if !sameDecls(in.Decls, p.Decls) {
		return nil, c.fail(ExitInvalid, "%s was edited: its declarations do not match its source", path)
	}
	return &p, ExitOK
}

func sameDecls(a, b []intent.Decl) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

func (c *cmd) applyExit(rep *plan.Report, err error) int {
	var stale *plan.StaleError
	var conflict *plan.ConflictError
	var effect *plan.EffectError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &stale) && rep == nil:
		fmt.Fprintln(c.err, "wb: refused: the plan is stale, so nothing was changed:")
		for _, d := range stale.Diffs {
			fmt.Fprintf(c.err, "  %s\n", d)
		}
		fmt.Fprintln(c.err, "run wb plan again to review the workspace as it is now")
		return ExitRefused
	case errors.As(err, &conflict):
		return c.fail(ExitRefused, "refused, nothing was changed: %v", conflict)
	case errors.As(err, &effect), errors.As(err, &stale):
		fmt.Fprintf(c.err, "wb: apply stopped: %v\n", err)
		fmt.Fprintln(c.err, "completed effects stay and are recorded in "+foundation.JournalPath+"; run wb apply again to retry (completed work is skipped)")
		return ExitFailed
	}
	return c.fail(ExitFailed, "%v", err)
}

func (c *cmd) evidence(args []string) int {
	fs := c.flags("evidence")
	dir := fs.String("dir", ".", "workspace directory")
	if _, err := parseArgs(fs, args); err != nil {
		return ExitInvalid
	}
	ws, code := c.openWorkspace(*dir)
	if ws == nil {
		return code
	}
	defer ws.Close()
	j, err := ws.Journal()
	if err != nil {
		return c.fail(ExitFailed, "%v", err)
	}
	if len(j.Records()) == 0 {
		fmt.Fprintf(c.out, "no evidence yet: %s is empty\n", foundation.JournalPath)
	}
	for _, r := range j.Records() {
		fmt.Fprintf(c.out, "#%-3d %-8s %-8s %s\n", r.Seq, r.Op, r.ID, describe(r))
	}
	if j.Skipped() > 0 {
		fmt.Fprintf(c.out, "(%d unreadable line(s) ignored)\n", j.Skipped())
	}
	return ExitOK
}

func describe(r foundation.Record) string {
	switch r.Op {
	case foundation.OpConverge:
		return fmt.Sprintf("%s %s -> %s", r.Action, r.Subject, plan.Short(r.After))
	case foundation.OpStart:
		var in []string
		for _, k := range slices.Sorted(maps.Keys(r.Inputs)) {
			in = append(in, k+"="+plan.Short(r.Inputs[k]))
		}
		return "inputs " + strings.Join(in, " ")
	case foundation.OpFinish:
		return fmt.Sprintf("attempt #%d -> %s %s", r.Attempt, r.Subject, plan.Short(r.After))
	case foundation.OpFail:
		return fmt.Sprintf("attempt #%d: %s", r.Attempt, r.Detail)
	}
	return r.Op
}

func (c *cmd) kinds() int {
	for _, name := range c.reg.Kinds() {
		s, life, _ := c.reg.Lookup(name)
		var fields []string
		for _, f := range s.Fields {
			req := ""
			if f.Required {
				req = ", required"
			}
			fields = append(fields, fmt.Sprintf("%s (%s%s)", f.Name, f.Type, req))
		}
		fmt.Fprintf(c.out, "%s %s\n  attributes: %s\n  exports: %s\n", life, name, strings.Join(fields, ", "), strings.Join(s.Exports, ", "))
	}
	return ExitOK
}
