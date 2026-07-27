package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/cmd/flare/internal/journal"
	"github.com/itsHabib/workbench/cmd/flare/internal/route"
)

func TestSweepFailsWhenASourceCannotBeRead(t *testing.T) {
	// The CLI contract: `flare sweep` exits non-zero on a config/source error.
	// A source whose file is missing must not read as a clean sweep (exit 0) —
	// a broken source that looks swept is the silent failure this guards.
	dir := t.TempDir()
	j, err := journal.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:     config.Version,
		PollSeconds: 60,
		Sources:     []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: filepath.Join(dir, "missing.jsonl")}},
		Channels:    map[string]config.Channel{"toast": {Type: config.ChannelToast}},
		CatchAll:    "toast",
	}
	if code := sweep(cfg, j); code == 0 {
		t.Fatal("sweep must exit non-zero when a configured source cannot be read")
	}
}

func TestCorruptCursorsPagesThenQuarantinesAndResweeps(t *testing.T) {
	// A corrupt cursor file is why flare goes dark, so recovery must PAGE the
	// operator through the notify path (journaled), not silently reset — and it
	// must quarantine only after that alert settles, handing back an empty cursor
	// so the cycle resweeps.
	dir := t.TempDir()
	j, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cursors.json"), []byte(`{"last_poll":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:  config.Version,
		Sources:  []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: "/x"}},
		Channels: map[string]config.Channel{"quiet": {Type: config.ChannelDrop}},
		CatchAll: "quiet",
	}
	cur, err := recoverCorruptCursors(cfg, j, route.New(cfg, time.Now))
	if err != nil {
		t.Fatalf("recovery must succeed once the alert settles, got %v", err)
	}
	if len(cur.Sources) != 0 || !cur.LastPoll.IsZero() {
		t.Fatalf("recovered cursors must be empty so the cycle resweeps, got %+v", cur)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "cursors.json")); !os.IsNotExist(statErr) {
		t.Fatal("the corrupt file must be quarantined after the alert is delivered")
	}
	tail, err := j.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].EventID != "cursor-alert:flare:cursors-corrupt" {
		t.Fatalf("recovery must route the corruption alert through the notifier, got %+v", tail)
	}
}

func TestNamedDropChannelIsJournaledAsDropped(t *testing.T) {
	// A route to a configured channel of type drop must journal the event as
	// dropped, not delivered, even though its name is not the literal "drop".
	j, err := journal.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:  config.Version,
		Sources:  []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: "/x"}},
		Channels: map[string]config.Channel{"toast": {Type: config.ChannelToast}, "quiet": {Type: config.ChannelDrop}},
		Routes:   []config.Route{{Match: config.Match{Source: "gate"}, Channel: "quiet"}},
		CatchAll: "toast",
	}
	r := route.New(cfg, time.Now)
	ev := event.Event{Source: "gate", ID: "e1", Kind: "verdict", Severity: event.SevBlock, Fields: map[string]string{}}
	if ok := dispatch(cfg, j, r, ev); !ok {
		t.Fatal("dispatch to a drop channel must settle (return true)")
	}
	tail, err := j.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].Kind != journal.Dropped {
		t.Fatalf("a named drop channel must journal as dropped, got %+v", tail)
	}
}
