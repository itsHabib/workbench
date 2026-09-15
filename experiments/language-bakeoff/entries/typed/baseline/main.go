// Command baseline is the direct-tools comparison: the textpipe workload
// as a plain program over the same foundation operations and evidence
// records, with no graph, intent, plan or adapters. It is written to be
// correct, not to lose: it is idempotent and it retries only what failed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
)

const defaultText = "the quick brown fox\njumps over the lazy dog\n"

type step struct {
	name, output string
	argv         []string
}

var steps = []step{
	{"upper", "upper.txt", []string{"tr", "[:lower:]", "[:upper:]"}},
	{"wordcount", "wordcount.txt", []string{"awk", "{ n += NF } END { print n + 0 }"}},
}

func main() {
	dir := flag.String("dir", "", "workspace directory (must exist)")
	text := flag.String("text", defaultText, "content of input.txt")
	flag.Parse()
	if err := start(*dir, *text); err != nil {
		fmt.Fprintln(os.Stderr, "baseline:", err)
		os.Exit(1)
	}
}

func start(dir, text string) error {
	fault, err := foundation.ParseFault(os.Getenv("WB_FAULT"))
	if err != nil {
		return err
	}
	ws, err := foundation.Open(dir, fault)
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()
	return run(context.Background(), ws, text, os.Stdout)
}

// run writes input.txt if it differs, then runs each step whose newest
// successful run used other input or whose output is gone or altered.
func run(ctx context.Context, ws *foundation.Workspace, text string, log io.Writer) error {
	in := []byte(text)
	cur, err := ws.ReadFile("input.txt")
	if err != nil {
		return err
	}
	if cur.Digest != foundation.Digest(in) {
		if err := ws.WriteFile("input.txt", in); err != nil {
			return err
		}
		fmt.Fprintln(log, "wrote input.txt")
	}
	var failed []error
	for _, s := range steps {
		if err := runStep(ctx, ws, s, foundation.Digest(in), log); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

func runStep(ctx context.Context, ws *foundation.Workspace, s step, input string, log io.Writer) error {
	fp := foundation.Digest([]byte(strings.Join(s.argv, "\x00") + "\x00" + input))
	ev, err := ws.Evidence()
	if err != nil {
		return err
	}
	out, err := ws.ReadFile(s.output)
	if err != nil {
		return err
	}
	if last := lastOK(ev.Records, s.name); last != nil && last.Fingerprint == fp && out.Digest == last.Output {
		fmt.Fprintf(log, "%s: up to date\n", s.name)
		return nil
	}
	if _, err := ws.Record(foundation.Record{Event: foundation.EventStart, Subject: s.name, Fingerprint: fp}); err != nil {
		return err
	}
	res, runErr := ws.Exec(ctx, foundation.ExecSpec{Label: s.name, Argv: s.argv, Stdin: "input.txt", Output: s.output})
	fin := foundation.Record{Event: foundation.EventFinish, Subject: s.name, Fingerprint: fp,
		Status: foundation.StatusOK, Output: res.Digest}
	if runErr != nil {
		fin.Status, fin.Output, fin.Detail = foundation.StatusFailed, "", runErr.Error()
	}
	if _, err := ws.Record(fin); err != nil {
		return errors.Join(runErr, err)
	}
	if runErr != nil {
		return runErr
	}
	fmt.Fprintf(log, "%s: ran\n", s.name)
	return nil
}

func lastOK(recs []foundation.Record, name string) *foundation.Record {
	var last *foundation.Record
	for i := range recs {
		r := &recs[i]
		if r.Subject == name && r.Event == foundation.EventFinish && r.Status == foundation.StatusOK {
			last = r
		}
	}
	return last
}
