// eventq queries projected metadata from local JSONL archives.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/eventq/internal/query"
)

const usage = `eventq: local columnar queries over JSONL metadata

  eventq build --codex --out commands.eq ~/.codex/sessions
  eventq build --column duration=duration_ms:int --column kind=kind:string --out events.eq LOG.jsonl
  eventq query --where 'exit_code != 0' --group cwd commands.eq
  eventq query --where 'duration_ms >= 30000' --top duration_ms --limit 10 commands.eq
  eventq query --where 'day == "2026-09-26"' --count commands.eq
  eventq schema commands.eq

Build reads sources without modifying them. Existing outputs are never overwritten.
--codex indexes completed command metadata only; prompts/commands/output are excluded.
Indexes are frozen snapshots: rebuild into a new file to include later events.
Queries accept AND/&&, == != < <= > >=, IS NULL, IS NOT NULL; no SQL or shell execution.
Use --json for structured output, --scalar for reference scans. Flags precede paths.
Exit codes: 0 success (including no matches), 2 invalid input or I/O failure.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "eventq:", err)
		os.Exit(2)
	}
}

func run(args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := io.WriteString(out, usage)
		return err
	}
	switch args[0] {
	case "build":
		return build(args[1:], out, errOut)
	case "query":
		return queryIndex(args[1:], out, errOut)
	case "schema":
		return schema(args[1:], out)
	default:
		return fmt.Errorf("unknown command %q; run eventq help", args[0])
	}
}

type columns []query.Field

func (c *columns) String() string { return fmt.Sprint([]query.Field(*c)) }
func (c *columns) Set(s string) error {
	f, err := query.ParseField(s)
	if err != nil {
		return err
	}
	*c = append(*c, f)
	return nil
}

func build(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(errOut)
	path := fs.String("out", "", "new index file (required)")
	codex := fs.Bool("codex", false, "index Codex completed command metadata")
	tail := fs.Bool("allow-partial-tail", false, "ignore only invalid unterminated final records; report their count")
	var fields columns
	fs.Var(&fields, "column", "name=dotted.path:int|string (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() == 0 {
		return fmt.Errorf("build needs --out and input paths; run eventq help")
	}
	if *codex && len(fields) > 0 {
		return fmt.Errorf("--codex and --column are mutually exclusive")
	}
	_, statErr := os.Lstat(*path)
	if statErr == nil {
		return fmt.Errorf("output already exists; choose a new index path")
	}
	if !os.IsNotExist(statErr) {
		return statErr
	}
	start := time.Now()
	t, err := query.Build(fs.Args(), fields, *codex, *tail)
	if err != nil {
		return err
	}
	if err := query.Save(*path, t); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"index": *path, "rows": t.Rows, "sources": len(t.Sources), "ignored_tails": t.IgnoredTails, "elapsed_ms": time.Since(start).Milliseconds(), "built_at": t.BuiltAt})
}

func queryIndex(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(errOut)
	where := fs.String("where", "", "typed comparisons joined by AND")
	group := fs.String("group", "", "group counts by a column")
	sum := fs.String("sum", "", "sum a signed integer column")
	top := fs.String("top", "", "show largest values of an integer column (bounded heap)")
	limit := fs.Int("limit", 20, "maximum displayed rows/groups; totals remain exact")
	count := fs.Bool("count", false, "only report aggregate totals")
	scalar := fs.Bool("scalar", false, "force scalar reference kernel")
	asJSON := fs.Bool("json", false, "machine-readable result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("query needs one index; flags must precede the path")
	}
	start := time.Now()
	t, err := query.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	loaded := time.Now()
	p, err := query.Compile(t, *where)
	if err != nil {
		return err
	}
	r, err := p.Execute(query.Options{Group: *group, Sum: *sum, Top: *top, Limit: *limit, CountOnly: *count, Scalar: *scalar})
	if err != nil {
		return err
	}
	backend := query.Backend
	if *scalar {
		backend = "scalar"
	}
	stats := map[string]any{"rows": t.Rows, "built_at": t.BuiltAt, "backend": backend, "load_ms": float64(loaded.Sub(start).Microseconds()) / 1000, "query_ms": float64(time.Since(loaded).Microseconds()) / 1000, "ignored_tails": t.IgnoredTails}
	if *asJSON {
		return json.NewEncoder(out).Encode(struct {
			query.Result
			Stats map[string]any `json:"stats"`
		}{r, stats})
	}
	return render(out, r, stats)
}

func render(out io.Writer, r query.Result, stats map[string]any) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%d matches / %v rows · snapshot %v · %v\n", r.Matches, stats["rows"], stats["built_at"], stats["backend"])
	if stats["ignored_tails"] != 0 {
		fmt.Fprintf(&b, "incomplete final records omitted: %v\n", stats["ignored_tails"])
	}
	if r.Sum != nil {
		fmt.Fprintf(&b, "sum: %d (%d present values)\n", *r.Sum, r.SumPresent)
	}
	for _, g := range r.Groups {
		s, _ := json.Marshal(g)
		fmt.Fprintln(&b, string(s))
	}
	for _, row := range r.Rows {
		s, _ := json.Marshal(row)
		fmt.Fprintln(&b, string(s))
	}
	if r.Truncated {
		fmt.Fprintln(&b, "display limited; use --limit for more; totals include all matches")
	}
	fmt.Fprintf(&b, "load %.3f ms · query %.3f ms\n", stats["load_ms"], stats["query_ms"])
	_, err := io.WriteString(out, b.String())
	return err
}

func schema(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("schema needs one index")
	}
	t, err := query.Load(args[0])
	if err != nil {
		return err
	}
	fields := make([]query.Field, len(t.Columns))
	for i, c := range t.Columns {
		fields[i] = c.Field
	}
	return json.NewEncoder(out).Encode(map[string]any{"rows": t.Rows, "built_at": t.BuiltAt, "columns": fields, "sources": len(t.Sources), "ignored_tails": t.IgnoredTails})
}
