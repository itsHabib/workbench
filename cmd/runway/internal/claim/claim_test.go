package claim_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
)

func TestClaimExclusiveAcquire(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	var (
		wg       sync.WaitGroup
		wins     atomic.Int64
		busy     atomic.Int64
		otherErr atomic.Int64
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := claim.Acquire(dir)
			if err == nil {
				wins.Add(1)
				return
			}
			if errors.Is(err, claim.ErrBusy) {
				busy.Add(1)
				return
			}
			otherErr.Add(1)
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("want exactly 1 acquire winner, got %d (busy=%d other=%d)", wins.Load(), busy.Load(), otherErr.Load())
	}
	if busy.Load()+otherErr.Load() != n-1 {
		t.Fatalf("losers: busy=%d other=%d", busy.Load(), otherErr.Load())
	}
	owner, err := claim.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Generation != 1 {
		t.Fatalf("generation=%d want 1", owner.Generation)
	}
}

func TestClaimTakeoverDeadOwner(t *testing.T) {
	dir := t.TempDir()
	dead := claim.Owner{PID: 999999, StartTicks: 1, Generation: 3}
	writeOwner(t, dir, dead)
	owner, err := claim.Takeover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Generation != 4 {
		t.Fatalf("generation=%d want 4", owner.Generation)
	}
	if owner.PID != os.Getpid() {
		t.Fatalf("pid=%d want self", owner.PID)
	}
	got, err := claim.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 4 {
		t.Fatalf("durable generation=%d", got.Generation)
	}
}

func TestClaimRefusesLiveOwner(t *testing.T) {
	dir := t.TempDir()
	ticks, err := claim.StartTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if ticks != 0 {
		// Genuinely alive owner must not be taken over.
		live := claim.Owner{PID: os.Getpid(), StartTicks: ticks, Generation: 1}
		writeOwner(t, dir, live)
		if _, err := claim.Takeover(dir); !errors.Is(err, claim.ErrHeld) {
			t.Fatalf("want ErrHeld for live owner, got %v", err)
		}

		// Same PID, different start ticks = PID reuse → takeover must succeed.
		reused := claim.Owner{PID: os.Getpid(), StartTicks: ticks + 1, Generation: 2}
		writeOwner(t, dir, reused)
		owner, err := claim.Takeover(dir)
		if err != nil {
			t.Fatalf("PID-reuse owner must be takeable: %v", err)
		}
		if owner.Generation != 3 {
			t.Fatalf("generation=%d want 3", owner.Generation)
		}
	}

	// StartTicks=0 with a live PID must refuse takeover (pidExists fallback).
	unverifiable := t.TempDir()
	writeOwner(t, unverifiable, claim.Owner{PID: os.Getpid(), StartTicks: 0, Generation: 1})
	if _, err := claim.Takeover(unverifiable); !errors.Is(err, claim.ErrHeld) {
		t.Fatalf("want ErrHeld for StartTicks=0 live pid, got %v", err)
	}
}

func TestClaimClearsCorruptTakeover(t *testing.T) {
	dir := t.TempDir()
	writeOwner(t, dir, claim.Owner{PID: 999999, StartTicks: 1, Generation: 1})
	garbage := filepath.Join(dir, "writer.claim.takeover.2")
	if err := os.WriteFile(garbage, []byte("not-json{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Crash debris is old by definition; backdate past the staleness window
	// so it is distinguishable from a concurrent writer mid-write.
	debris := time.Now().Add(-time.Minute)
	if err := os.Chtimes(garbage, debris, debris); err != nil {
		t.Fatal(err)
	}
	owner, err := claim.Takeover(dir)
	if err != nil {
		t.Fatalf("corrupt takeover must be cleared and retried: %v", err)
	}
	if owner.Generation != 2 {
		t.Fatalf("generation=%d want 2", owner.Generation)
	}
	if _, err := os.Stat(garbage); !os.IsNotExist(err) {
		t.Fatalf("corrupt takeover file must be removed, stat err=%v", err)
	}
}

func TestClaimFreshUnreadableTakeoverIsNotStolen(t *testing.T) {
	dir := t.TempDir()
	writeOwner(t, dir, claim.Owner{PID: 999999, StartTicks: 1, Generation: 1})
	inflight := filepath.Join(dir, "writer.claim.takeover.2")
	// A concurrent writer between O_EXCL create and write: exists, no content.
	if err := os.WriteFile(inflight, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := claim.Takeover(dir)
	if !errors.Is(err, claim.ErrBusy) {
		t.Fatalf("fresh unreadable takeover must yield ErrBusy, got %v", err)
	}
	if _, err := os.Stat(inflight); err != nil {
		t.Fatalf("fresh takeover file must not be removed, stat err=%v", err)
	}
}

func TestClaimTakeoverRace_OneWinner(t *testing.T) {
	dir := t.TempDir()
	writeOwner(t, dir, claim.Owner{PID: 999999, StartTicks: 1, Generation: 1})

	const n = 8
	var (
		wg       sync.WaitGroup
		wins     atomic.Int64
		lost     atomic.Int64
		otherErr atomic.Int64
		mu       sync.Mutex
		gen      uint64
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			owner, err := claim.Takeover(dir)
			if err == nil {
				wins.Add(1)
				mu.Lock()
				gen = owner.Generation
				mu.Unlock()
				return
			}
			if errors.Is(err, claim.ErrBusy) || errors.Is(err, claim.ErrHeld) {
				lost.Add(1)
				return
			}
			otherErr.Add(1)
			t.Errorf("unexpected: %v", err)
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("want 1 takeover winner, got %d (lost=%d other=%d)", wins.Load(), lost.Load(), otherErr.Load())
	}
	if gen != 2 {
		t.Fatalf("winner generation=%d want 2", gen)
	}
}

func writeOwner(t *testing.T, dir string, o claim.Owner) {
	t.Helper()
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "writer.claim")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
