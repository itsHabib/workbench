// Package receipt writes the append-only JSONL decision record. It is pure
// mechanism: it marshals one Receipt to a single line and appends it, and holds
// no decision logic. The exit-5 ordering contract lives in the caller (the
// receipt is appended before the placement reaches stdout, so a failed append
// never hands a caller a placement from a failed invocation); this package's
// job is to return an honest error when the append fails.
package receipt

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/dispatch/internal/placement"
)

// Receipt is one decision's durable evidence: when it was decided, the rule
// that fired, the policy hash it fired under, and the full descriptor and
// placement. decided_at is the only clock read in the whole decide path, and it
// is written here — never into the stdout placement, which must stay
// deterministic.
type Receipt struct {
	DecidedAt    time.Time            `json:"decided_at"`
	Rule         string               `json:"rule"`
	PolicySHA256 string               `json:"policy_sha256"`
	Descriptor   placement.Descriptor `json:"descriptor"`
	Placement    placement.Placement  `json:"placement"`
}

// Append marshals r to a single JSONL line and appends it to path (created if
// absent). O_APPEND single-line write; no cross-process atomicity is claimed
// (spec §8 — callers are serial). Any failure is returned so the caller can
// fail closed (exit 5) rather than proceed on a lost record.
func Append(path string, r Receipt) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("receipt: marshal: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("receipt: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("receipt: write %s: %w", path, err)
	}
	return nil
}
