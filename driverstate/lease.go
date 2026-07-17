package driverstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// Event is the contract event this mechanism reads and writes. Aliased so the
// package API speaks the shared vocabulary directly (spec §6 signatures).
type Event = dsc.Event

// DefaultLeaseTTL is the staleness window a Claim inherits when it does not
// specify one: a lease older than this is stale and stealable, and a killed
// session's lease self-clears within one window. It mirrors gate's heartbeat
// threshold and is a var so an MCP server (auto-renewing at TTL/2) or a test can
// tune it.
var DefaultLeaseTTL = 90 * time.Second

// maxRetries bounds the Windows delete-pending → retry-everything loop. Each
// filesystem step that can lose an O_EXCL race or hit a transient
// ERROR_ACCESS_DENIED retries up to this many times before giving up.
const maxRetries = 50

const retryDelay = 2 * time.Millisecond

// leaseRecord is the on-disk lease. Generation increases on every takeover — a
// steal, or a re-Claim after Release — so it is a monotonic fencing token that
// never repeats for a run; a stale holder that wakes up detects it lost the
// lease by the generation drift. Released marks a lease the holder voluntarily
// dropped: the record stays on disk (preserving the generation) so the next
// Claim continues at generation+1 rather than resetting to 1.
type leaseRecord struct {
	Actor      string    `json:"actor"`
	PID        int       `json:"pid"`
	ExpiresAt  time.Time `json:"expires_at"`
	Generation uint64    `json:"generation"`
	Released   bool      `json:"released,omitempty"`
}

// Lease is a held run lease. Its zero value is not usable — obtain one from
// Claim. It is safe to Renew/Release from the goroutine that holds it.
type Lease struct {
	dir   string
	run   string
	actor string
	pid   int
	gen   uint64
	ttl   time.Duration
}

// Actor reports the lease holder's actor string.
func (l Lease) Actor() string { return l.actor }

// Run reports the run this lease owns.
func (l Lease) Run() string { return l.run }

// Claim takes durable ownership of run under dir for actor. It fails fast with
// ErrLocked{Holder} if a live (non-expired) lease is already held, and steals an
// expired one. dir/run need not exist yet. run must be a single directory
// component (no separators or traversal) so it cannot escape the state root.
//
// All lease mutation — Claim, Renew, Release — runs under a per-run lease lock
// (lease.lock), so the read-check-mutate window is atomic against every other
// mutator; the lease file itself is published by atomic rename so a lock-free
// reader (requireLease) never observes a half-write.
func Claim(dir, run, actor string) (Lease, error) {
	if actor == "" {
		return Lease{}, fmt.Errorf("driverstate: claim: actor is empty")
	}
	if err := validateRunID(run); err != nil {
		return Lease{}, fmt.Errorf("driverstate: claim: %w", err)
	}
	rd := runDir(dir, run)
	if err := os.MkdirAll(rd, 0o700); err != nil {
		return Lease{}, fmt.Errorf("driverstate: claim: %w", err)
	}
	ttl := DefaultLeaseTTL
	var lease Lease
	err := withLock(leaseLockPath(rd), func() error {
		l, e := claimLocked(dir, run, actor, ttl)
		if e != nil {
			return e
		}
		lease = l
		return nil
	})
	return lease, err
}

// claimLocked decides the claim under the held lease lock: a live lease locks
// the run out, an expired or corrupt one is taken over at the next generation,
// and an absent one is created at generation 1. Because mutations are serialized
// by the lock and every write is an atomic rename, a decode failure here is real
// corruption — never a concurrent half-write — so it is safely stealable.
func claimLocked(dir, run, actor string, ttl time.Duration) (Lease, error) {
	rd := runDir(dir, run)
	rec, err := readLease(rd)
	if os.IsNotExist(err) {
		return installLease(dir, run, actor, ttl, 1)
	}
	if err != nil {
		return installLease(dir, run, actor, ttl, 1)
	}
	// A live lease (not released, not expired) locks the run out. A released or
	// expired one is claimable and continues at the next generation, so the
	// fencing token stays monotonic across the takeover.
	if !rec.Released && !expired(rec) {
		return Lease{}, ErrLocked{Holder: rec.Actor}
	}
	return installLease(dir, run, actor, ttl, rec.Generation+1)
}

// installLease atomically publishes actor's lease at gen and returns the held
// Lease. Called only under the lease lock.
func installLease(dir, run, actor string, ttl time.Duration, gen uint64) (Lease, error) {
	rec := selfLease(actor, gen, ttl)
	if err := writeLeaseFile(runDir(dir, run), rec); err != nil {
		return Lease{}, err
	}
	return leaseFrom(dir, run, ttl, rec), nil
}

// validateRunID rejects a run identifier that is not a single, safe directory
// component — empty, a traversal ("." / ".."), or carrying a path separator or
// volume — so filepath.Join(dir, run) can never escape the state root. The same
// check guards every path built from a run (Claim here, Append via bindDir).
func validateRunID(run string) error {
	if run == "" {
		return fmt.Errorf("run id is empty")
	}
	if run == "." || run == ".." || strings.ContainsAny(run, `/\`) || run != filepath.Base(run) || filepath.VolumeName(run) != "" {
		return fmt.Errorf("run id %q must be a bare directory name (no separators or traversal)", run)
	}
	return nil
}

// Renew heartbeats the lease, pushing expiry out one TTL. It fails with
// ErrLocked if the lease was stolen out from under this holder (generation or
// actor drift), and with ErrLeaseExpired if this holder's own lease has already
// lapsed — staleness is expiry, so an expired holder re-Claims rather than
// resurrecting a lease another writer may already be about to steal. The whole
// check-then-write runs under the lease lock, so a steal cannot interleave
// between verifying ownership and rewriting the record.
func (l Lease) Renew() error {
	rd := runDir(l.dir, l.run)
	return withLock(leaseLockPath(rd), func() error {
		cur, err := l.ownsCurrent(rd)
		if err != nil {
			return err
		}
		if expired(cur) {
			return ErrLeaseExpired
		}
		return writeLeaseFile(rd, selfLeaseFor(l.actor, l.pid, l.gen, l.ttl))
	})
}

// Release drops the lease if this holder still owns it. A lease already stolen
// (generation drift) or already gone is left untouched — Release never removes
// another writer's lease. It does not delete lease.json: it marks the record
// released, KEEPING the generation, so the next Claim continues at generation+1
// and the fencing token never repeats. The check-then-write runs under the lease
// lock, so a steal cannot interleave.
func (l Lease) Release() error {
	rd := runDir(l.dir, l.run)
	return withLock(leaseLockPath(rd), func() error {
		cur, err := l.ownsCurrent(rd)
		if err != nil {
			if errors.Is(err, ErrNotHolder) || errors.As(err, new(ErrLocked)) {
				return nil // already released/stolen — not ours to touch
			}
			return err
		}
		cur.Released = true
		return writeLeaseFile(rd, cur)
	})
}

// ownsCurrent reads the live lease record and confirms this holder still owns
// it. It returns ErrNotHolder when the record is gone OR this holder has already
// released it, and ErrLocked{Holder} on a generation/actor drift (a steal). The
// returned record lets callers layer an expiry check on top (requireLease).
func (l Lease) ownsCurrent(rd string) (leaseRecord, error) {
	cur, err := readLease(rd)
	if err != nil {
		if os.IsNotExist(err) {
			return leaseRecord{}, ErrNotHolder
		}
		return leaseRecord{}, err
	}
	if cur.Generation != l.gen || cur.Actor != l.actor {
		return leaseRecord{}, ErrLocked{Holder: cur.Actor}
	}
	if cur.Released {
		return leaseRecord{}, ErrNotHolder
	}
	return cur, nil
}

// requireLease verifies this holder still owns a live lease — Append's write
// guard, called INSIDE the append lock so a lease lost while waiting for the
// lock is caught before any write. The read is lock-free (it does not take the
// lease lock), so on Windows it can transiently collide with a concurrent
// Renew/steal rename; withRetry absorbs that (ErrLocked / ErrNotHolder are
// non-transient and return at once).
func requireLease(l Lease) error {
	rd := runDir(l.dir, l.run)
	var cur leaseRecord
	err := withRetry(func() error {
		c, e := l.ownsCurrent(rd)
		if e != nil {
			return e
		}
		cur = c
		return nil
	})
	if err != nil {
		return err
	}
	if expired(cur) {
		return ErrLeaseExpired
	}
	return nil
}

func selfLease(actor string, gen uint64, ttl time.Duration) leaseRecord {
	return selfLeaseFor(actor, os.Getpid(), gen, ttl)
}

func selfLeaseFor(actor string, pid int, gen uint64, ttl time.Duration) leaseRecord {
	return leaseRecord{
		Actor:      actor,
		PID:        pid,
		ExpiresAt:  time.Now().Add(ttl),
		Generation: gen,
	}
}

func leaseFrom(dir, run string, ttl time.Duration, rec leaseRecord) Lease {
	return Lease{dir: dir, run: run, actor: rec.Actor, pid: rec.PID, gen: rec.Generation, ttl: ttl}
}

func expired(rec leaseRecord) bool {
	return !time.Now().Before(rec.ExpiresAt)
}

func runDir(dir, run string) string { return filepath.Join(dir, run) }

func leasePath(rd string) string { return filepath.Join(rd, "lease.json") }

// leaseLockPath is the per-run lease-mutation lock — held across Claim/Renew/
// Release so their read-check-mutate windows are atomic against one another.
func leaseLockPath(rd string) string { return filepath.Join(rd, "lease.lock") }

func readLease(rd string) (leaseRecord, error) {
	return readLeaseFile(leasePath(rd))
}

func readLeaseFile(path string) (leaseRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return leaseRecord{}, err
	}
	var rec leaseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return leaseRecord{}, fmt.Errorf("driverstate: decode lease: %w", err)
	}
	return rec, nil
}

// writeLeaseFile publishes rec as the run's lease by atomic rename: write a
// temp, fsync, rename over lease.json. The rename is what lets a lock-free
// reader (requireLease) always observe a complete lease, never a partial write —
// the root fix for the half-written-lease misread. Callers hold the lease lock,
// so the fixed temp name is race-free.
func writeLeaseFile(rd string, rec leaseRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("driverstate: encode lease: %w", err)
	}
	tmp := filepath.Join(rd, "lease.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("driverstate: open lease temp: %w", err)
	}
	if err := finishWrite(f, tmp, data); err != nil {
		return err
	}
	if err := os.Rename(tmp, leasePath(rd)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("driverstate: install lease: %w", err)
	}
	return nil
}

func finishWrite(f *os.File, path string, data []byte) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("driverstate: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("driverstate: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("driverstate: close: %w", err)
	}
	return nil
}

// withRetry runs fn under the delete-pending → retry-everything discipline: a
// transient race (lost O_EXCL, Windows ERROR_ACCESS_DENIED on a delete-pending
// file) is retried; a real error (ErrLocked, a decode failure) returns at once.
func withRetry(fn func() error) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !isTransient(err) {
			return err
		}
		time.Sleep(retryDelay)
	}
	// The budget is exhausted. errRetry is an internal marker that must never
	// surface (errors.go); replace it with the real contention error. A genuine
	// transient error (e.g. a delete-pending permission failure) is returned
	// as-is.
	if errors.Is(err, errRetry) {
		return fmt.Errorf("driverstate: gave up after %d attempts: %w", maxRetries, errLockContended)
	}
	return err
}

func isTransient(err error) bool {
	if errors.Is(err, errRetry) {
		return true
	}
	// Windows leaves a just-removed file in a delete-pending state; opening it
	// returns ERROR_ACCESS_DENIED, which maps to a permission error. A lock-free
	// read that collides with a concurrent atomic rename returns
	// ERROR_SHARING_VIOLATION, which Go does not map to a permission error.
	// Both clear within microseconds — retry.
	return errors.Is(err, os.ErrPermission) || isSharingViolation(err)
}
