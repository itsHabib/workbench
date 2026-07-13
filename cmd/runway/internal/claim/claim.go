// Package claim is the atomic per-run writer-claim MECHANISM: exclusivity
// comes only from O_CREATE|O_EXCL (atomic on Linux and Windows). PID +
// process-start identity is DETECTION of a live owner, never the lock.
// Policy (when to take over, what receipt to write) stays in controller.
package claim

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	// fileName is the durable claim under a run's private/ directory.
	fileName = "writer.claim"
	// takeoverPrefix names the generation-suffixed O_EXCL lock used by Takeover.
	takeoverPrefix = "writer.claim.takeover."
)

// Owner is the recorded claim holder. Generation increases on every successful
// Acquire or Takeover so concurrent reconcilers can race on distinct paths.
type Owner struct {
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
	Generation uint64 `json:"generation"`
}

var (
	// ErrHeld means a live owner (matching PID + start identity) holds the claim.
	ErrHeld = errors.New("claim: held by live owner")
	// ErrBusy means another process won the O_EXCL race (acquire or takeover).
	ErrBusy = errors.New("claim: concurrent acquire lost")
)

// Path returns the durable claim path under privateDir.
func Path(privateDir string) string {
	return filepath.Join(privateDir, fileName)
}

// Acquire atomically creates the claim for this process at generation 1.
// Fails with ErrBusy if the claim file already exists — never overwrites.
func Acquire(privateDir string) (Owner, error) {
	owner, err := selfOwner(1)
	if err != nil {
		return Owner{}, err
	}
	path := Path(privateDir)
	if err := createExclusive(path, owner); err != nil {
		if os.IsExist(err) {
			return Owner{}, ErrBusy
		}
		return Owner{}, err
	}
	return owner, nil
}

// Takeover race-safely steals a claim whose recorded owner is dead or reused.
// Two callers may both observe death; exactly one wins via O_EXCL on a
// generation-suffixed takeover file, then renames over the claim. Never
// "verify dead, then write in place" (TOCTOU).
func Takeover(privateDir string) (Owner, error) {
	for attempts := 0; attempts < 3; attempts++ {
		owner, err := takeoverOnce(privateDir)
		if err == nil {
			return owner, nil
		}
		if !errors.Is(err, errStaleTakeover) {
			return Owner{}, err
		}
	}
	return Owner{}, ErrBusy
}

var errStaleTakeover = errors.New("claim: stale takeover cleared")

func takeoverOnce(privateDir string) (Owner, error) {
	cur, err := Read(privateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Acquire(privateDir)
		}
		return Owner{}, err
	}
	if LiveMatches(cur) {
		return Owner{}, ErrHeld
	}
	nextGen := cur.Generation + 1
	owner, err := selfOwner(nextGen)
	if err != nil {
		return Owner{}, err
	}
	takeoverPath := filepath.Join(privateDir, takeoverPrefix+strconv.FormatUint(nextGen, 10))
	if err := createExclusive(takeoverPath, owner); err != nil {
		if !os.IsExist(err) {
			return Owner{}, err
		}
		if err := clearStaleTakeover(takeoverPath); err != nil {
			return Owner{}, err
		}
		return Owner{}, errStaleTakeover
	}
	// Re-read before rename: if another reconciler already advanced the
	// claim, the O_EXCL file we hold is stale (winner renamed theirs away
	// and freed the generation-suffixed name). Abort without mutating.
	latest, err := Read(privateDir)
	if err != nil {
		_ = os.Remove(takeoverPath)
		return Owner{}, err
	}
	if latest.Generation != cur.Generation {
		_ = os.Remove(takeoverPath)
		return Owner{}, ErrBusy
	}
	if LiveMatches(latest) {
		_ = os.Remove(takeoverPath)
		return Owner{}, ErrHeld
	}
	claimPath := Path(privateDir)
	if err := os.Rename(takeoverPath, claimPath); err != nil {
		_ = os.Remove(takeoverPath)
		return Owner{}, fmt.Errorf("claim: rename takeover: %w", err)
	}
	return owner, nil
}

// clearStaleTakeover removes a takeover file whose recorded owner is dead so
// a crashed reconciler cannot leave a stuck lock. Live owners yield ErrBusy.
// Corrupt or unreadable content is removed and retried — a crash mid-write
// must not permanently block reconcile (exclusivity is O_EXCL, not content).
func clearStaleTakeover(takeoverPath string) error {
	data, err := os.ReadFile(takeoverPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		_ = os.Remove(takeoverPath)
		return errStaleTakeover
	}
	var o Owner
	if err := json.Unmarshal(data, &o); err != nil {
		_ = os.Remove(takeoverPath)
		return errStaleTakeover
	}
	if LiveMatches(o) {
		return ErrBusy
	}
	_ = os.Remove(takeoverPath)
	return nil
}

// Read loads the current claim owner. Missing claim yields os.ErrNotExist.
func Read(privateDir string) (Owner, error) {
	data, err := os.ReadFile(Path(privateDir))
	if err != nil {
		return Owner{}, err
	}
	var o Owner
	if err := json.Unmarshal(data, &o); err != nil {
		return Owner{}, fmt.Errorf("claim: decode: %w", err)
	}
	if o.PID <= 0 || o.Generation == 0 {
		return Owner{}, fmt.Errorf("claim: invalid owner record")
	}
	return o, nil
}

// LiveMatches reports whether o still refers to a live process with the same
// start identity. StartTicks 0 (unverifiable platform or degraded record)
// falls back to pid existence so a live owner cannot be stolen.
func LiveMatches(o Owner) bool {
	if o.PID <= 0 {
		return false
	}
	if o.StartTicks == 0 {
		return pidExists(o.PID)
	}
	got, err := identityOf(o.PID)
	if err != nil {
		return false
	}
	return got.PID == o.PID && got.StartTicks == o.StartTicks
}

// StartTicks returns the process-start identity for pid (Linux /proc starttime
// or Windows creation FILETIME). Used by controller identity and claim owner
// records alike — one stdlib-only primitive.
func StartTicks(pid int) (uint64, error) {
	return startTicks(pid)
}

func selfOwner(gen uint64) (Owner, error) {
	id, err := identityOf(os.Getpid())
	if err != nil {
		return Owner{}, err
	}
	id.Generation = gen
	return id, nil
}

func identityOf(pid int) (Owner, error) {
	ticks, err := startTicks(pid)
	if err != nil {
		return Owner{}, err
	}
	return Owner{PID: pid, StartTicks: ticks}, nil
}

func createExclusive(path string, owner Owner) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("claim: encode: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.Remove(path) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return fmt.Errorf("claim: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		cleanup()
		return fmt.Errorf("claim: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("claim: close: %w", err)
	}
	return nil
}
