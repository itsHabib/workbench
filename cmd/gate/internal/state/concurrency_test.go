package state

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestConcurrentAppendKeepsChainIntact is the multi-writer probe: N goroutines
// appending to one store must produce a log that (a) lost nothing and (b) still
// audits as an intact hash chain. The naive read-last-hash-then-write Append
// races on the chain head; this test exists to catch that class of failure.
func TestConcurrentAppendKeepsChainIntact(t *testing.T) {
	st, err := Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}

	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_, err := st.Append(KindEvidence, "run_stress", nil, map[string]any{"writer": w, "seq": i})
				if err != nil {
					errs <- fmt.Errorf("writer %d seq %d: %w", w, i, err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	all, err := st.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != writers*perWriter {
		t.Fatalf("lost writes: got %d artifacts, want %d", len(all), writers*perWriter)
	}
	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("chain broken under concurrency at %s: %s", res.Artifact, res.Reason)
	}
}
