package driverstate

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// withTTL sets DefaultLeaseTTL for the duration of a test so expiry paths do not
// need real-time sleeps of the production 90s window.
func withTTL(t *testing.T, ttl time.Duration) {
	t.Helper()
	prev := DefaultLeaseTTL
	DefaultLeaseTTL = ttl
	t.Cleanup(func() { DefaultLeaseTTL = prev })
}

func TestClaimRenewRelease(t *testing.T) {
	dir := t.TempDir()
	l, err := Claim(dir, "dsr_run1", "session:a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if l.Actor() != "session:a" || l.Run() != "dsr_run1" {
		t.Fatalf("lease identity wrong: %+v", l)
	}
	if _, err := os.Stat(filepath.Join(dir, "dsr_run1", "lease.json")); err != nil {
		t.Fatalf("lease file missing: %v", err)
	}
	if err := l.Renew(); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dsr_run1", "lease.json")); !os.IsNotExist(err) {
		t.Fatalf("lease file should be gone after release, got %v", err)
	}
	// Releasable run can be re-claimed.
	if _, err := Claim(dir, "dsr_run1", "session:b"); err != nil {
		t.Fatalf("re-claim after release: %v", err)
	}
}

func TestSecondClaimerLocked(t *testing.T) {
	dir := t.TempDir()
	if _, err := Claim(dir, "dsr_run1", "session:holder"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, err := Claim(dir, "dsr_run1", "session:intruder")
	var locked ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	if locked.Holder != "session:holder" {
		t.Fatalf("ErrLocked should name the holder, got %q", locked.Holder)
	}
}

func TestStaleLeaseSelfClears(t *testing.T) {
	withTTL(t, 20*time.Millisecond)
	dir := t.TempDir()
	first, err := Claim(dir, "dsr_run1", "session:dead")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_ = first
	time.Sleep(40 * time.Millisecond) // lease now expired
	second, err := Claim(dir, "dsr_run1", "session:fresh")
	if err != nil {
		t.Fatalf("claim over stale lease should succeed, got %v", err)
	}
	if second.Actor() != "session:fresh" {
		t.Fatalf("stolen lease actor = %q", second.Actor())
	}
	// The original holder has lost the lease: Renew must report it.
	if err := first.Renew(); !errors.As(err, new(ErrLocked)) {
		t.Fatalf("stale holder Renew should be ErrLocked, got %v", err)
	}
}

// Cluster 4 — a renewal by a holder that has been stolen out from under must
// fail AND leave the successor's lease untouched (renewal is atomic with
// takeover, never a blind rewrite).
func TestRenewAfterStealRejected(t *testing.T) {
	withTTL(t, 20*time.Millisecond)
	dir := t.TempDir()
	first, _ := Claim(dir, "dsr_run1", "session:one")
	time.Sleep(40 * time.Millisecond)
	second, err := Claim(dir, "dsr_run1", "session:two") // installs generation 2
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	// The stale holder's renew must report the loss and NOT clobber gen 2.
	if err := first.Renew(); !errors.As(err, new(ErrLocked)) {
		t.Fatalf("stale renew should be ErrLocked, got %v", err)
	}
	rec, err := readLease(runDir(dir, "dsr_run1"))
	if err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if rec.Actor != "session:two" || rec.Generation != 2 {
		t.Fatalf("stale renew clobbered the successor: on-disk %+v", rec)
	}
	// The successor still holds a renewable lease.
	if err := second.Renew(); err != nil {
		t.Fatalf("successor renew should succeed, got %v", err)
	}
}

func TestReleaseAfterStealIsNoop(t *testing.T) {
	withTTL(t, 20*time.Millisecond)
	dir := t.TempDir()
	first, _ := Claim(dir, "dsr_run1", "session:one")
	time.Sleep(40 * time.Millisecond)
	second, err := Claim(dir, "dsr_run1", "session:two")
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	// The stale holder releasing must not remove the new holder's lease.
	if err := first.Release(); err != nil {
		t.Fatalf("stale release: %v", err)
	}
	if err := second.Renew(); err != nil {
		t.Fatalf("new holder should still hold lease, got %v", err)
	}
}

func TestClaimCorruptLeaseIsStealable(t *testing.T) {
	dir := t.TempDir()
	rd := filepath.Join(dir, "dsr_run1")
	if err := os.MkdirAll(rd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "lease.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "dsr_run1", "session:fresh"); err != nil {
		t.Fatalf("corrupt lease should be stealable, got %v", err)
	}
}

// Cycle 2, cluster 4 — a run id that is empty, a traversal, or carries a path
// separator/volume must be rejected before any path is built, so a claim cannot
// escape the state root.
func TestTraversalRunIDRejected(t *testing.T) {
	dir := t.TempDir()
	bad := []string{"", ".", "..", "../evil", "a/b", `a\b`, "sub/dsr_run1"}
	for _, run := range bad {
		if _, err := Claim(dir, run, "session:a"); err == nil {
			t.Fatalf("Claim should reject run id %q", run)
		}
	}
	// A bare, safe id still works, and nothing was created outside dir.
	if _, err := Claim(dir, "dsr_ok", "session:a"); err != nil {
		t.Fatalf("valid run id should claim, got %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dir)); len(entries) != 1 {
		t.Fatalf("a rejected traversal must not create siblings of the state root, got %d entries", len(entries))
	}
}

// Cycle 2, cluster 1 — a partial/garbage lease.json (as a half-write or crash
// could leave) must resolve to a single, well-formed, EXCLUSIVE owner: the first
// claimer heals it into a valid lease, and a second claimer is then locked out —
// never a second winner.
func TestPartialLeaseFileClaimIsSafe(t *testing.T) {
	dir := t.TempDir()
	rd := filepath.Join(dir, "dsr_run1")
	if err := os.MkdirAll(rd, 0o700); err != nil {
		t.Fatal(err)
	}
	// A zero-byte lease.json — the exact artifact the old non-atomic create left
	// visible between O_EXCL create and the write.
	if err := os.WriteFile(filepath.Join(rd, "lease.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "dsr_run1", "session:a"); err != nil {
		t.Fatalf("claim over a partial lease should heal and succeed, got %v", err)
	}
	if _, err := Claim(dir, "dsr_run1", "session:b"); !errors.As(err, new(ErrLocked)) {
		t.Fatalf("second claim must be locked out by the healed lease, got %v", err)
	}
}

// Cycle 2, cluster 1 — the lease is published by atomic rename, so a lock-free
// reader never observes a half-write. Churn Claim/Release on one goroutine while
// another reads the raw record: it must always see a complete lease or nothing,
// never a decode error.
func TestLeasePublishedAtomically(t *testing.T) {
	withTTL(t, time.Second)
	dir := t.TempDir()
	rd := filepath.Join(dir, "dsr_run1")
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			l, err := Claim(dir, "dsr_run1", "session:a")
			if err != nil {
				continue
			}
			_ = l.Release()
		}
		close(done)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			// A complete read, ENOENT (between remove and next create), or a
			// transient Windows sharing-violation are all fine. A decode error
			// would mean partial CONTENT was read — the non-atomic symptom.
			_, err := readLease(rd)
			if err == nil || os.IsNotExist(err) || isTransient(err) {
				continue
			}
			t.Errorf("reader saw a non-atomic lease (partial content): %v", err)
			return
		}
	}()
	wg.Wait()
}

func TestConcurrentClaimSingleWinner(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	results := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			_, err := Claim(dir, "dsr_race", "session:racer")
			results <- err
		}()
	}
	close(start)
	wins, locks := 0, 0
	for i := 0; i < n; i++ {
		err := <-results
		switch {
		case err == nil:
			wins++
		case errors.As(err, new(ErrLocked)):
			locks++
		default:
			t.Fatalf("unexpected claim error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("want exactly one winner, got %d wins / %d locked", wins, locks)
	}
}
