package claim_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

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
	if ticks == 0 {
		t.Skip("process-start identity unsupported on this GOOS")
	}

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
