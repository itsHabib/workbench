package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/driverstate"
)

// cmdRecord reads an event as JSON on stdin, appends it to its run, and prints
// the sealed event. Unlike the server it holds no session: it claims the run
// lease, appends, and releases in one shot — the human/cron model. run_imported
// with no run mints one; a missing event id and zero time are filled the same
// way the server fills them, so the two surfaces mint identical shapes.
func cmdRecord(dir string, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	run := fs.String("run", "", "run id (omit on run_imported to mint one)")
	asJSON := fs.Bool("json", false, "emit the sealed event as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read event: %w", err)
	}
	e, err := buildEvent(raw, *run)
	if err != nil {
		return err
	}

	sealed, err := appendOneShot(dir, e)
	if err != nil {
		return err
	}
	if *asJSON {
		return encodeJSON(stdout, sealed)
	}
	fmt.Fprintf(stdout, "recorded %s %s on run %s (hash %s)\n", sealed.Kind, sealed.ID, sealed.Run, sealed.Hash)
	return nil
}

// buildEvent decodes the record event and fills the client-minted defaults —
// run (explicit flag, else minted for run_imported), event id, and time. It
// mirrors the server's prepareEvent so both surfaces produce the same event.
func buildEvent(raw []byte, run string) (driverstate.Event, error) {
	var e driverstate.Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, fmt.Errorf("decode event: %w", err)
	}
	if run != "" {
		e.Run = run
	}
	if err := fillRun(&e); err != nil {
		return e, err
	}
	if e.ID == "" {
		id, err := driverstate.NewEventID()
		if err != nil {
			return e, err
		}
		e.ID = id
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	return e, nil
}

// fillRun mints a run id for a run_imported that named none, and rejects any
// other kind with no run.
func fillRun(e *driverstate.Event) error {
	if e.Run != "" {
		return nil
	}
	if e.Kind != dsc.KindRunImported {
		return fmt.Errorf("event kind %q requires --run", e.Kind)
	}
	id, err := driverstate.NewRunID()
	if err != nil {
		return err
	}
	e.Run = id
	return nil
}

// appendOneShot claims the run lease, appends, and releases — the CLI's
// no-session lifecycle. The lease is released even on append failure so a cron
// invocation never leaves a run locked behind it.
func appendOneShot(dir string, e driverstate.Event) (driverstate.Event, error) {
	lease, err := driverstate.Claim(dir, e.Run, e.Actor)
	if err != nil {
		return driverstate.Event{}, err
	}
	defer lease.Release()
	return driverstate.Append(dir, lease, e)
}
