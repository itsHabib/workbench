// Command standup is the compiler behind the standup: a conversation with the lead
// lane that ends in one JSON record, which then compiles field by field into Fleet
// verbs that already exist. Design: docs/features/standup/design.md.
//
//	standup agenda [--json]                 derive the agenda from records; write and digest it
//	standup new --agenda <id> [--from <r>]  scaffold a record pinned to that agenda; --from carries a stale draft over
//	standup show <id|path>                  the readback: the record as the lead reads it aloud
//	standup confirm <id|path> --phrase "…"  set confirm iff the phrase is the configured one
//	standup apply <id|path> [--dry-run] [--force-stale]
//	                                        refuse unconfirmed or stale; else run the verbs
//
// Exit codes are a load-bearing seam: 0 ok · 1 refused (unconfirmed, stale, phrase
// mismatch, changed payload, unknown kind) · 2 usage · 4 error. A refusal is not an
// error: it is the record not being ready, and stderr says what would make it ready.
//
// Run it where the lead session runs: fleet mail and send resolve the caller from
// the directory they run in. Binaries come from FLEET_BIN, ORG_BIN, GH_BIN; state
// from FLEET_STATE (default ~/.fleet) and STANDUP_DIR (default $FLEET_STATE/standup).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/standup/internal/standup"
)

const (
	codeOK      = 0
	codeRefused = 1
	codeUsage   = 2
	codeError   = 4
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runInput(args, os.Stdin, stdout, stderr)
}

func runInput(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return codeUsage
	}
	env, err := standup.EnvFromOS()
	if err != nil {
		fmt.Fprintln(stderr, "standup:", err)
		return codeError
	}
	verb, rest := args[0], args[1:]
	if verb == "mcp" {
		if len(rest) != 0 {
			fmt.Fprintln(stderr, "standup mcp: no arguments; configure cwd and environment at launch")
			return codeUsage
		}
		return serveMCP(stdin, stdout, stderr)
	}
	if mutates(verb) {
		release, err := lockStore(env)
		if err != nil {
			return fail(stderr, err)
		}
		defer release()
	}

	switch verb {
	case "draft":
		return cmdDraft(env, rest, stdin, stdout, stderr)
	case "prepare", "status":
		return cmdInspect(env, verb, rest, stdout, stderr)
	case "agenda":
		return cmdAgenda(env, rest, stdout, stderr)
	case "new":
		return cmdNew(env, rest, stdout, stderr)
	case "show":
		return cmdShow(env, rest, stdout, stderr)
	case "confirm":
		return cmdConfirm(env, rest, stdout, stderr)
	case "apply":
		return cmdApply(env, rest, stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return codeOK
	}
	fmt.Fprintf(stderr, "standup: unknown verb %q\n%s", verb, usage)
	return codeUsage
}

const usage = `usage: standup <verb> [flags]
  agenda [--json]                        derive today's agenda from records, write and digest it
  new --agenda <id> [--from <record>]    scaffold a record pinned to that agenda; --from carries a stale draft over
  show <id|path> [--json]                the readback and current plan_digest
  draft <id|path> --expect <digest> --file <path|->
                                         replace editable fields; clear confirmation after changes
  prepare <id|path>                      check live plan before confirmation; JSON, no writes
  status <id|path>                       read plan ledger, Fleet runtime and receipt evidence as JSON
  mcp                                   serve the same operations over local stdio MCP
  confirm <id|path> --phrase "<words>" [--by <who>] [--surface text|voice]
  apply <id|path> [--dry-run] [--force-stale]
exit: 0 ok · 1 refused · 2 usage · 4 error
`

func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "standup:", err)
	if standup.IsRefusal(err) {
		return codeRefused
	}
	return codeError
}

func flags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func cmdAgenda(env standup.Env, args []string, stdout, stderr io.Writer) int {
	fs := flags("agenda", stderr)
	asJSON := fs.Bool("json", false, "print the agenda file instead of the text")
	if fs.Parse(args) != nil {
		return codeUsage
	}
	cfg, err := env.LoadConfig()
	if err != nil {
		return fail(stderr, err)
	}
	id, err := env.NextID()
	if err != nil {
		return fail(stderr, err)
	}
	a, err := env.Build(cfg, id)
	if err != nil {
		return fail(stderr, err)
	}
	path := env.AgendaPath(a.ID)
	if err := a.Save(path); err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		b, _ := os.ReadFile(path)
		_, _ = stdout.Write(b)
	} else {
		fmt.Fprint(stdout, a.Text())
	}
	fmt.Fprintf(stderr, "agenda %s written to %s\n", a.ID, path)
	return codeOK
}

func cmdNew(env standup.Env, args []string, stdout, stderr io.Writer) int {
	fs := flags("new", stderr)
	id := fs.String("agenda", "", "agenda id the record is made against")
	asJSON := fs.Bool("json", false, "return the record and readback")
	out := fs.String("out", "", "record path (default $STANDUP_DIR/records/<agenda id>.json)")
	from := fs.String("from", "", "carry roles, cards, decisions, deferrals and next over from this record (id or path)")
	if fs.Parse(args) != nil {
		return codeUsage
	}
	if *id == "" {
		fmt.Fprintln(stderr, "standup new: --agenda <id> is required")
		return codeUsage
	}
	cfg, err := env.LoadConfig()
	if err != nil {
		return fail(stderr, err)
	}
	a, err := standup.LoadAgenda(env.AgendaPath(*id))
	if err != nil {
		return fail(stderr, err)
	}
	path := *out
	if path == "" {
		path = env.RecordPath(a.ID)
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(stderr, "standup new: %s exists; edit it or pick another --out\n", path)
		return codeRefused
	}
	r := &standup.Record{Schema: standup.SchemaRecord, ID: a.ID, Tenant: cfg.Tenant, Lead: cfg.Lead,
		At: env.Now().UTC().Format("2006-01-02T15:04:05Z"), Agenda: a.ID, AgendaDigest: a.Digest, Deferred: a.Deferred}
	if *from != "" {
		prev, err := standup.LoadRecord(env.ResolveRecord(*from))
		if err != nil {
			return fail(stderr, err)
		}
		r.CarryOver(prev)
	}
	if err := r.Save(path); err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return printView(stdout, stderr, path, r)
	}
	fmt.Fprintln(stdout, path)
	return codeOK
}

func cmdShow(env standup.Env, args []string, stdout, stderr io.Writer) int {
	fs := flags("show", stderr)
	asJSON := fs.Bool("json", false, "include record and plan digest")
	if fs.Parse(positionalFirst(args)) != nil || fs.NArg() != 1 {
		return codeUsage
	}
	path := env.ResolveRecord(fs.Arg(0))
	r, err := standup.LoadRecord(path)
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return printView(stdout, stderr, path, r)
	}
	fmt.Fprint(stdout, r.Readback())
	return codeOK
}

func cmdConfirm(env standup.Env, args []string, stdout, stderr io.Writer) int {
	fs := flags("confirm", stderr)
	phrase := fs.String("phrase", "", "the words the operator said")
	expected := fs.String("expect", "", "plan digest from readback")
	by := fs.String("by", "", "who said them (default human:<tenant>)")
	surface := fs.String("surface", "text", "text or voice")
	if err := fs.Parse(positionalFirst(args)); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "standup confirm: <id|path> --phrase \"<words>\"")
		return codeUsage
	}
	cfg, err := env.LoadConfig()
	if err != nil {
		return fail(stderr, err)
	}
	path := env.ResolveRecord(fs.Arg(0))
	r, err := standup.LoadRecord(path)
	if err != nil {
		return fail(stderr, err)
	}
	if err := expectDigest(r, *expected); err != nil {
		return fail(stderr, err)
	}
	if err := standup.ConfirmRecord(env, cfg, r, *phrase, *by, *surface); err != nil {
		return fail(stderr, err)
	}
	if err := r.Save(path); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "confirmed %s by %s at %s\n", r.ID, r.Confirm.By, r.Confirm.At)
	return codeOK
}

func cmdApply(env standup.Env, args []string, stdout, stderr io.Writer) int {
	fs := flags("apply", stderr)
	dryRun := fs.Bool("dry-run", false, "plan and print the steps; write nothing")
	expected := fs.String("expect", "", "plan digest from readback")
	forceStale := fs.Bool("force-stale", false, "apply even though the world moved since the agenda")
	if err := fs.Parse(positionalFirst(args)); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "standup apply: <id|path> [--dry-run] [--force-stale]")
		return codeUsage
	}
	cfg, err := env.LoadConfig()
	if err != nil {
		return fail(stderr, err)
	}
	path := env.ResolveRecord(fs.Arg(0))
	r, err := standup.LoadRecord(path)
	if err != nil {
		return fail(stderr, err)
	}
	if err := expectDigest(r, *expected); err != nil {
		return fail(stderr, err)
	}
	steps, err := standup.Apply(env, cfg, r, path, *dryRun, *forceStale)
	for _, s := range steps {
		fmt.Fprintf(stdout, "%-11s %s: %s %s", s.Status, s.Name, s.Bin, strings.Join(quoteAll(s.Args), " "))
		if s.Skip != "" {
			fmt.Fprintf(stdout, "  (%s)", s.Skip)
		}
		fmt.Fprintln(stdout)
	}
	if err != nil {
		return fail(stderr, err)
	}
	if *dryRun {
		fmt.Fprintf(stderr, "dry run: %d step(s), nothing written\n", len(steps))
	} else {
		fmt.Fprintf(stderr, "applied %s: %d step(s) recorded in %s\n", r.ID, len(r.Applied), path)
	}
	return codeOK
}

// positionalFirst lets the record come before its flags: `confirm <id> --phrase …`
// reads as naturally as `confirm --phrase … <id>`, and flag stops at the first bare
// argument, so the leading one is moved to the end before parsing.
func positionalFirst(args []string) []string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return append(append([]string{}, args[1:]...), args[0])
	}
	return args
}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \n\t\"") {
			a = fmt.Sprintf("%q", a)
		}
		out[i] = a
	}
	return out
}
