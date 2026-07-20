package driverstate

import (
	"fmt"
	"os"
	"path/filepath"
)

// Events reads a run's hash-chained ledger and returns every complete event in
// file order, including unknown kinds. It is a pure read: no lease required, no
// state modification. Torn-tail tolerance and chain verification match Reduce.
func Events(dir, run string) ([]Event, error) {
	if err := validateRunID(run); err != nil {
		return nil, fmt.Errorf("driverstate: events: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(runDir(dir, run), "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("driverstate: events: run %q not found: %w", run, err)
		}
		return nil, fmt.Errorf("driverstate: events: %w", err)
	}
	events, err := decodeLedger(trimWithWarning(data, "events", run))
	if err != nil {
		return nil, fmt.Errorf("driverstate: events: %w", err)
	}
	return events, nil
}
