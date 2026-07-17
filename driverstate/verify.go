package driverstate

import (
	"fmt"
	"os"
	"path/filepath"
)

// Verify checks the hash chain of run's ledger under dir: every complete line
// must decode cleanly, each event's hash must seal its own canonical bytes,
// and each Prev must link to the prior event's hash. It returns nil when the
// chain is intact, or ErrChainBroken (with detail naming the offending event)
// when any link fails (spec §8).
//
// A torn final line (no trailing newline) is discarded with a stderr warning —
// a lock-free reader may observe a file that the writer is mid-appending. A
// break anywhere in the completed portion is always loud, never silently
// truncated (a swallowed stream_merged would re-drive a merged PR).
func Verify(dir, run string) error {
	if err := validateRunID(run); err != nil {
		return fmt.Errorf("driverstate: verify: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(runDir(dir, run), "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("driverstate: verify: run %q not found: %w", run, err)
		}
		return fmt.Errorf("driverstate: verify: %w", err)
	}
	_, err = decodeLedger(trimWithWarning(data, "verify", run))
	return err
}
