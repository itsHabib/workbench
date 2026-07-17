package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	e, minted, err := buildEvent(raw, *run)
	if err != nil {
		return err
	}

	sealed, err := appendOneShot(dir, e, minted)
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
// mirrors the server's prepareEvent so both surfaces produce the same event, and
// reports whether the run was minted so a speculative run Append dedupes away can
// be unwound.
func buildEvent(raw []byte, run string) (driverstate.Event, bool, error) {
	var e driverstate.Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, false, fmt.Errorf("decode event: %w", err)
	}
	if run != "" {
		e.Run = run
	}
	minted, err := fillRun(&e)
	if err != nil {
		return e, false, err
	}
	if e.ID == "" {
		id, err := driverstate.NewEventID()
		if err != nil {
			return e, false, err
		}
		e.ID = id
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	return e, minted, nil
}

// fillRun mints a run id for a run_imported that named none (reporting true),
// and rejects any other kind with no run. Minting is only retry-safe when the
// import carries its (repo, source, generated_at) dedupe key — the same refusal
// the MCP server applies — so a lost-response cron retry cannot mint a duplicate.
func fillRun(e *driverstate.Event) (bool, error) {
	if e.Run != "" {
		return false, nil
	}
	if e.Kind != dsc.KindRunImported {
		return false, fmt.Errorf("event kind %q requires --run", e.Kind)
	}
	if !driverstate.ImportHasDedupeKey(*e) {
		return false, fmt.Errorf("a run_imported without --run must carry (repo, source, generated_at) so a retried import cannot mint a duplicate run")
	}
	id, err := driverstate.NewRunID()
	if err != nil {
		return false, err
	}
	e.Run = id
	return true, nil
}

// appendOneShot claims the run lease, appends, and releases — the CLI's
// no-session lifecycle. The lease is released even on append failure so a cron
// invocation never leaves a run locked behind it. A speculatively minted run
// that Append deduped to an existing run is unwound: the empty run dir is removed
// (the returned event names the original run), mirroring the server's cleanup.
func appendOneShot(dir string, e driverstate.Event, minted bool) (driverstate.Event, error) {
	lease, err := driverstate.Claim(dir, e.Run, e.Actor)
	if err != nil {
		return driverstate.Event{}, err
	}
	out, err := driverstate.Append(dir, lease, e)
	_ = lease.Release()
	if err != nil {
		return driverstate.Event{}, err
	}
	if minted && out.Run != e.Run {
		_ = os.RemoveAll(filepath.Join(dir, e.Run))
	}
	return out, nil
}
