package driverstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// leaseRecord is the on-disk lease. Generation increases on every steal so a
// stale holder that wakes up can detect it lost the lease.
type leaseRecord struct {
	Actor      string    `json:"actor"`
	PID        int       `json:"pid"`
	ExpiresAt  time.Time `json:"expires_at"`
	Generation uint64    `json:"generation"`
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
// expired one atomically. dir/run need not exist yet.
func Claim(dir, run, actor string) (Lease, error) {
	if actor == "" {
		return Lease{}, fmt.Errorf("driverstate: claim: actor is empty")
	}
	if run == "" {
		return Lease{}, fmt.Errorf("driverstate: claim: run is empty")
	}
	rd := runDir(dir, run)
	if err := os.MkdirAll(rd, 0o700); err != nil {
		return Lease{}, fmt.Errorf("driverstate: claim: %w", err)
	}
	ttl := DefaultLeaseTTL
	var lease Lease
	err := withRetry(func() error {
		l, e := claimOnce(dir, run, actor, ttl)
		if e != nil {
			return e
		}
		lease = l
		return nil
	})
	return lease, err
}

func claimOnce(dir, run, actor string, ttl time.Duration) (Lease, error) {
	rd := runDir(dir, run)
	rec, err := readLease(rd)
	if err != nil && !os.IsNotExist(err) {
		// A corrupt lease record blocks nobody: treat it as stealable at gen 1.
		return steal(dir, run, actor, ttl, 1, 0, false)
	}
	if err == nil {
		if !expired(rec) {
			return Lease{}, ErrLocked{Holder: rec.Actor}
		}
		return steal(dir, run, actor, ttl, rec.Generation+1, rec.Generation, true)
	}
	// No lease file: create it exclusively at generation 1.
	newRec := selfLease(actor, 1, ttl)
	if err := createExclusiveJSON(leasePath(rd), newRec); err != nil {
		if os.IsExist(err) {
			return Lease{}, errRetry
		}
		return Lease{}, fmt.Errorf("driverstate: claim: %w", err)
	}
	return leaseFrom(dir, run, ttl, newRec), nil
}

// steal race-safely replaces an expired (or corrupt) lease. Exactly one caller
// wins the O_EXCL on a generation-suffixed temp, re-checks the current lease has
// not advanced or gone live, then renames over lease.json. Mirrors gate's
// takeover discipline — never "verify stale, then write in place" (TOCTOU).
func steal(dir, run, actor string, ttl time.Duration, gen, prevGen uint64, havePrev bool) (Lease, error) {
	rd := runDir(dir, run)
	tmp := filepath.Join(rd, fmt.Sprintf("lease.steal.%d", gen))
	rec := selfLease(actor, gen, ttl)
	if err := createExclusiveJSON(tmp, rec); err != nil {
		if os.IsExist(err) {
			return Lease{}, clearStaleSteal(tmp)
		}
		return Lease{}, fmt.Errorf("driverstate: claim: steal: %w", err)
	}
	cur, err := readLease(rd)
	if err != nil && !os.IsNotExist(err) {
		// Current lease unreadable/corrupt: keep the temp and take over.
		cur, havePrev, err = leaseRecord{}, false, nil
	}
	if havePrev && err == nil {
		if cur.Generation != prevGen {
			_ = os.Remove(tmp)
			return Lease{}, errRetry
		}
		if !expired(cur) {
			_ = os.Remove(tmp)
			return Lease{}, ErrLocked{Holder: cur.Actor}
		}
	}
	if err := os.Rename(tmp, leasePath(rd)); err != nil {
		_ = os.Remove(tmp)
		return Lease{}, fmt.Errorf("driverstate: claim: rename lease: %w", err)
	}
	return leaseFrom(dir, run, ttl, rec), nil
}

// clearStaleSteal removes a steal temp whose owner is dead (expired) so a
// crashed stealer cannot leave a stuck lock, then signals a retry. A live steal
// temp yields a retry too (the winner is about to rename it away).
func clearStaleSteal(tmp string) error {
	rec, err := readLeaseFile(tmp)
	if err != nil {
		if os.IsNotExist(err) {
			return errRetry
		}
		_ = os.Remove(tmp)
		return errRetry
	}
	if !expired(rec) {
		return errRetry
	}
	_ = os.Remove(tmp)
	return errRetry
}

// Renew heartbeats the lease, pushing expiry out one TTL. It fails with
// ErrLocked if the lease was stolen out from under this holder (generation or
// actor drift).
func (l Lease) Renew() error {
	rd := runDir(l.dir, l.run)
	return withRetry(func() error {
		cur, err := readLease(rd)
		if err != nil {
			if os.IsNotExist(err) {
				return errNoLease
			}
			return err
		}
		if cur.Generation != l.gen || cur.Actor != l.actor {
			return ErrLocked{Holder: cur.Actor}
		}
		rec := selfLeaseFor(l.actor, l.pid, l.gen, l.ttl)
		return writeLeaseAtomic(rd, rec)
	})
}

// Release drops the lease if this holder still owns it. A lease already stolen
// (generation drift) or already gone is left untouched — Release never removes
// another writer's lease.
func (l Lease) Release() error {
	rd := runDir(l.dir, l.run)
	cur, err := readLease(rd)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if cur.Generation != l.gen || cur.Actor != l.actor {
		return nil
	}
	return withRetry(func() error {
		e := os.Remove(leasePath(rd))
		if os.IsNotExist(e) {
			return nil
		}
		return e
	})
}

// requireLease verifies this holder still owns a live lease — Append's write
// guard.
func requireLease(l Lease) error {
	cur, err := readLease(runDir(l.dir, l.run))
	if err != nil {
		if os.IsNotExist(err) {
			return errNoLease
		}
		return err
	}
	if cur.Generation != l.gen || cur.Actor != l.actor {
		return ErrLocked{Holder: cur.Actor}
	}
	if expired(cur) {
		return errLeaseExpired
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

func writeLeaseAtomic(rd string, rec leaseRecord) error {
	tmp := filepath.Join(rd, fmt.Sprintf("lease.tmp.%d", rec.Generation))
	if err := writeJSONSynced(tmp, rec); err != nil {
		return err
	}
	if err := os.Rename(tmp, leasePath(rd)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("driverstate: renew: rename lease: %w", err)
	}
	return nil
}

func createExclusiveJSON(path string, rec leaseRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("driverstate: encode lease: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return finishWrite(f, path, data)
}

func writeJSONSynced(path string, rec leaseRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("driverstate: encode lease: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return finishWrite(f, path, data)
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
	return err
}

func isTransient(err error) bool {
	if errors.Is(err, errRetry) {
		return true
	}
	// Windows leaves a just-removed file in a delete-pending state; opening it
	// returns ERROR_ACCESS_DENIED, which maps to a permission error. Retry.
	return errors.Is(err, os.ErrPermission)
}
