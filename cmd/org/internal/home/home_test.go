package home

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts/org"
)

const (
	tenant = "acme"
	role   = "lead:platform"
	work   = "github:acme/api#88"
)

func open(t *testing.T) *Home {
	t.Helper()
	h, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open home: %v", err)
	}
	return h
}

func charter(t *testing.T, h *Home) {
	t.Helper()
	_, _, err := h.Append(tenant, role, Draft{
		Kind:  org.KindCharter,
		Terms: &org.Terms{Scope: []string{work}, Supervisors: []string{"human:op"}, MinReader: 1},
	})
	if err != nil {
		t.Fatalf("charter: %v", err)
	}
}

func mustAppend(t *testing.T, h *Home, d Draft) org.RoleState {
	t.Helper()
	_, state, err := h.Append(tenant, role, d)
	if err != nil {
		t.Fatalf("append %s: %v", d.Kind, err)
	}
	return state
}

// TestLifecycleFoldsBack drives the full loop through the home and re-loads it
// from disk, proving that what Append admitted is what Load folds.
func TestLifecycleFoldsBack(t *testing.T) {
	h := open(t)
	charter(t, h)
	mustAppend(t, h, Draft{Kind: org.KindAttach, NextDue: time.Now().Add(time.Hour)})
	mustAppend(t, h, Draft{Kind: org.KindAssign, Subject: org.Subject{Work: work, Digest: org.DigestBytes([]byte("v1"))}})
	mustAppend(t, h, Draft{Kind: org.KindClaim, Subject: org.Subject{Work: work}})
	mustAppend(t, h, Draft{Kind: org.KindNote, Body: []byte("progress")})
	state := mustAppend(t, h, Draft{Kind: org.KindYield, Subject: org.Subject{Work: work}})
	if state.Phase != org.PhaseHeld {
		t.Fatalf("after yield, phase = %s, want %s", state.Phase, org.PhaseHeld)
	}

	records, loaded, err := h.Load(tenant, role)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 6 {
		t.Fatalf("loaded %d records, want 6", len(records))
	}
	if loaded.Tip != state.Tip || loaded.Seq != state.Seq {
		t.Fatalf("loaded state (seq %d, tip %s) differs from appended (seq %d, tip %s)",
			loaded.Seq, loaded.Tip, state.Seq, state.Tip)
	}
	if !loaded.Holds(work) {
		t.Fatalf("loaded state lost the held work %s", work)
	}
}

// TestRefusalCarriesKernelReason proves the home adds no judgment of its own:
// a record the kernel refuses surfaces the kernel's reason, and the chain does
// not grow.
func TestRefusalCarriesKernelReason(t *testing.T) {
	h := open(t)
	charter(t, h)
	mustAppend(t, h, Draft{Kind: org.KindAttach})

	_, _, err := h.Append(tenant, role, Draft{Kind: org.KindClaim, Subject: org.Subject{Work: "jira:NOPE-1"}})
	if got := org.RefusalReason(err); got != org.ReasonWorkNotHeld {
		t.Fatalf("refusal reason = %q, want %q (err: %v)", got, org.ReasonWorkNotHeld, err)
	}
	records, _, err := h.Load(tenant, role)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("a refused record reached the chain: %d records, want 2", len(records))
	}
}

// TestConcurrentAppendsSerialize hammers one chain from many goroutines. Every
// append lands or is refused under the lock; the survivors must be contiguous
// and fold clean. This is the serialized-ownership property the switchboard
// experiment measured, here as a regression test.
func TestConcurrentAppendsSerialize(t *testing.T) {
	h := open(t)
	charter(t, h)
	mustAppend(t, h, Draft{Kind: org.KindAttach})

	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Append(tenant, role, Draft{Kind: org.KindNote, Body: []byte("concurrent")})
		}()
	}
	wg.Wait()

	records, state, err := h.Load(tenant, role)
	if err != nil {
		t.Fatalf("chain corrupted by concurrent appends: %v", err)
	}
	if len(records) != 2+writers {
		t.Fatalf("%d records, want %d", len(records), 2+writers)
	}
	if state.Seq != int64(2+writers) {
		t.Fatalf("state.Seq = %d, want %d", state.Seq, 2+writers)
	}
}

// TestBlobRoundTripAndErasure proves bodies are content-addressed, readable
// back, and that erasing one leaves the chain folding — erasability is the
// design, not a failure.
func TestBlobRoundTripAndErasure(t *testing.T) {
	h := open(t)
	charter(t, h)
	mustAppend(t, h, Draft{Kind: org.KindAttach})
	r, _, err := h.Append(tenant, role, Draft{Kind: org.KindCheckpoint, Body: []byte("the conclusion")})
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	body, found, err := h.Blob(r.BodyDigest)
	if err != nil || !found {
		t.Fatalf("blob read: found=%v err=%v", found, err)
	}
	if string(body) != "the conclusion" {
		t.Fatalf("blob = %q", body)
	}

	path := filepath.Join(h.Root(), "blobs", "sha256-"+r.BodyDigest[len("sha256:"):])
	if err := os.Remove(path); err != nil {
		t.Fatalf("erase blob: %v", err)
	}
	if _, found, err := h.Blob(r.BodyDigest); err != nil || found {
		t.Fatalf("erased blob: found=%v err=%v, want found=false, nil", found, err)
	}
	if _, _, err := h.Load(tenant, role); err != nil {
		t.Fatalf("chain must fold after body erasure: %v", err)
	}
}

// TestTakeoverAdvancesFence proves the home stamps a takeover at its own chain
// position, which is what kills credentials minted before it.
func TestTakeoverAdvancesFence(t *testing.T) {
	h := open(t)
	charter(t, h)
	mustAppend(t, h, Draft{Kind: org.KindAttach})
	state := mustAppend(t, h, Draft{Kind: org.KindTakeover, Subject: org.Subject{Party: "human:op"}})
	if state.Fence != state.Seq {
		t.Fatalf("fence = %d after takeover at seq %d; a takeover must advance the fence", state.Fence, state.Seq)
	}
}

// TestRolesEnumeratesLayout proves the role listing derives from the directory
// layout, colon mapping included.
func TestRolesEnumeratesLayout(t *testing.T) {
	h := open(t)
	charter(t, h)
	pairs, err := h.Roles()
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != [2]string{tenant, role} {
		t.Fatalf("roles = %v, want [[%s %s]]", pairs, tenant, role)
	}
}
