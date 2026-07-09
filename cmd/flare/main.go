// flare — the escalation-routing plane. A pure sink: it tails the artifact
// logs other planes emit and pushes a notification when something blocks or
// escalates. It never gates, never blocks, never writes into a producer.
//
//	flare watch  [-config path]   poll loop (catch-up sweep first)
//	flare sweep  [-config path]   one catch-up pass, then exit
//	flare status [-config path]   health as JSON; exit 1 when stale
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/cmd/flare/internal/journal"
	"github.com/itsHabib/workbench/cmd/flare/internal/notify"
	"github.com/itsHabib/workbench/cmd/flare/internal/route"
	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: flare watch|sweep|status [-config path] [-state dir]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	stateDir := fs.String("state", defaultStateDir(), "flare's own state dir (journal, cursors)")
	cfgPath := fs.String("config", "", "routes config (default <state>/routes.json)")
	fs.Parse(os.Args[2:])
	if *cfgPath == "" {
		*cfgPath = filepath.Join(*stateDir, "routes.json")
	}
	os.Exit(run(os.Args[1], *cfgPath, *stateDir))
}

func run(verb, cfgPath, stateDir string) int {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	j, err := journal.Open(stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	switch verb {
	case "sweep":
		return sweep(cfg, j)
	case "watch":
		return watch(cfg, j)
	case "status":
		return status(cfg, j)
	}
	fmt.Fprintf(os.Stderr, "flare: unknown verb %q\n", verb)
	return 2
}

func watch(cfg config.Config, j *journal.Journal) int {
	r := route.New(cfg, time.Now)
	for {
		if err := cycle(cfg, j, r); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		time.Sleep(time.Duration(cfg.PollSeconds) * time.Second)
	}
}

func sweep(cfg config.Config, j *journal.Journal) int {
	if err := cycle(cfg, j, route.New(cfg, time.Now)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// cycle is one poll: read every source from its cursor, route what's new,
// advance each cursor only when everything read from it settled (delivered,
// dropped, or throttled) — a failed delivery holds the cursor so the next
// cycle retries, and the journal's seen-set keeps retries from re-paging
// what already got through.
func cycle(cfg config.Config, j *journal.Journal, r *route.Router) error {
	seen, err := j.Seen()
	if err != nil {
		return err
	}
	cur, err := j.LoadCursors()
	if err != nil {
		return err
	}
	for _, src := range cfg.Sources {
		next, err := pollSource(cfg, j, r, src, cur.Sources[src.Name], seen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "flare: %v\n", err)
			continue
		}
		cur.Sources[src.Name] = next
	}
	cur.LastPoll = time.Now()
	return j.SaveCursors(cur)
}

func pollSource(cfg config.Config, j *journal.Journal, r *route.Router, src config.Source, cur source.Cursor, seen map[string]bool) (source.Cursor, error) {
	events, next, err := source.Read(src, cur)
	if err != nil {
		return cur, err
	}
	for _, ev := range events {
		if seen[ev.ID] {
			continue
		}
		if !dispatch(cfg, j, r, ev) {
			return cur, nil // hold the cursor; retry next cycle
		}
	}
	return next, nil
}

// dispatch routes and delivers one event, journaling the outcome. Returns
// false when delivery failed and the event must be retried.
func dispatch(cfg config.Config, j *journal.Journal, r *route.Router, ev event.Event) bool {
	entry := journal.Entry{
		Time:     time.Now(),
		Source:   ev.Source,
		EventID:  ev.ID,
		Severity: ev.Severity.String(),
		Note:     ev.Title,
	}
	d := r.Route(ev)
	entry.Channel = d.Channel
	if d.Throttled {
		entry.Kind = journal.Throttled
		return journalOK(j, entry)
	}
	if d.Channel == config.ChannelDrop {
		entry.Kind = journal.Dropped
		return journalOK(j, entry)
	}
	if err := notify.Send(cfg.Channels[d.Channel], ev); err != nil {
		entry.Kind = journal.Errored
		entry.Note = err.Error()
		journalOK(j, entry)
		return false
	}
	entry.Kind = journal.Delivered
	return journalOK(j, entry)
}

func journalOK(j *journal.Journal, e journal.Entry) bool {
	if err := j.Append(e); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	return true
}

// status prints health as JSON. Exit 0 = a poll completed recently; 1 =
// stale or never ran (flare is best-effort push — a silent watcher must be
// visible where the operator already looks, so wire this into /health).
func status(cfg config.Config, j *journal.Journal) int {
	cur, err := j.LoadCursors()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	stale := time.Duration(3*cfg.PollSeconds) * time.Second
	healthy := !cur.LastPoll.IsZero() && time.Since(cur.LastPoll) < stale
	tail, err := j.Tail(10)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	out, _ := json.MarshalIndent(map[string]any{
		"healthy":   healthy,
		"last_poll": cur.LastPoll,
		"cursors":   cur.Sources,
		"recent":    tail,
	}, "", "  ")
	fmt.Println(string(out))
	if !healthy {
		return 1
	}
	return 0
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".flare"
	}
	return filepath.Join(home, ".flare")
}
