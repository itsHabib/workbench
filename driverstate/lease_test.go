package driverstate

import (
	"errors"
	"os"
	"path/filepath"
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
