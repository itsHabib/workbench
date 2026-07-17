package capability

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func setup(t *testing.T) (*state.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(dir, fixedClock(time.Unix(1000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	return st, filepath.Join(dir, "grant.key")
}

func TestGrantExpiry(t *testing.T) {
	st, key := setup(t)
	mintedAt := time.Unix(1000, 0)
	a, err := Mint(st, key, "o/r", "merge", "T1", 0, "test", time.Hour, fixedClock(mintedAt))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(st, key, a.ID, "o/r", "merge", fixedClock(mintedAt.Add(30*time.Minute))); err != nil {
		t.Fatalf("live grant refused: %v", err)
	}
	_, err = Check(st, key, a.ID, "o/r", "merge", fixedClock(mintedAt.Add(2*time.Hour)))
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestGrantScope(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	a, err := Mint(st, key, "o/r", "merge", "T1", 0, "test", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(st, key, a.ID, "other/repo", "merge", now)
	if !errors.Is(err, ErrScope) {
		t.Fatalf("want ErrScope, got %v", err)
	}
}

func TestCheckRefusesToMintKey(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	a, err := Mint(st, key, "o/r", "merge", "T1", 0, "test", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	_, err = Check(st, key, a.ID, "o/r", "merge", now)
	if !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("want ErrKeyMissing, got %v", err)
	}
	if _, statErr := os.Stat(key); !os.IsNotExist(statErr) {
		t.Fatal("Check silently recreated the signing key")
	}
}

func TestTierCeilingFailsClosed(t *testing.T) {
	g := Grant{MaxTier: "T1"}
	if !g.TierWithin("T0") || !g.TierWithin("T1") {
		t.Fatal("tiers at or under the ceiling must pass")
	}
	if g.TierWithin("T2") || g.TierWithin("T3") || g.TierWithin("garbage") {
		t.Fatal("tiers over the ceiling (and unknown tiers) must fail closed")
	}
	bad := Grant{MaxTier: "garbage"}
	if bad.TierWithin("T0") {
		t.Fatal("a grant with an unknown ceiling must authorize nothing")
	}
}

func TestMintRejectsUnknownCeiling(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	_, err := Mint(st, key, "o/r", "merge", "T9", 0, "test", time.Hour, now)
	if !errors.Is(err, ErrBadTier) {
		t.Fatalf("want ErrBadTier, got %v", err)
	}
}

// TestSignatureCoversMaxCycles pins that the cycle ceiling sits inside the
// HMAC pre-image: flipping it after signing must change the signature, or the
// cap would be silently widenable by anyone who can write state.
func TestSignatureCoversMaxCycles(t *testing.T) {
	key := []byte("test-key")
	g := Grant{Repo: "o/r", Action: "merge", MaxTier: "T1", MaxCycles: 3,
		ExpiresAt: time.Unix(2000, 0), MintedBy: "test"}
	sig := sign(key, g)
	g.MaxCycles = 99
	if sign(key, g) == sig {
		t.Fatal("widening MaxCycles did not change the signature — the ceiling is forgeable")
	}
}

// TestCheckRefusesForgedCycleCeiling is the end-to-end form: a grant body with
// a widened ceiling but the original signature must fail Check.
func TestCheckRefusesForgedCycleCeiling(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	a, err := Mint(st, key, "o/r", "merge", "T1", 3, "test", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	art, err := st.Get(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var g Grant
	if err := json.Unmarshal(art.Body, &g); err != nil {
		t.Fatal(err)
	}
	g.MaxCycles = 99
	forged, err := st.Append(state.KindGrant, "run_mint", nil, g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(st, key, forged.ID, "o/r", "merge", now); !errors.Is(err, ErrSignature) {
		t.Fatalf("want ErrSignature for forged ceiling, got %v", err)
	}
}

// TestCyclesWithinBoundary pins the ceiling arithmetic: cycles at or under the
// cap pass, the first cycle past it fails, and a zero cap means unbounded —
// the back-compat reading of a grant minted before the field existed.
func TestCyclesWithinBoundary(t *testing.T) {
	g := Grant{MaxCycles: 3}
	for n := 1; n <= 3; n++ {
		if !g.CyclesWithin(n) {
			t.Fatalf("cycle %d at or under ceiling 3 must pass", n)
		}
	}
	if g.CyclesWithin(4) {
		t.Fatal("cycle 4 over ceiling 3 must fail")
	}
	unbounded := Grant{}
	if !unbounded.CyclesWithin(1) || !unbounded.CyclesWithin(1000) {
		t.Fatal("a zero ceiling means unbounded")
	}
}

func TestMintRejectsNegativeCycles(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	if _, err := Mint(st, key, "o/r", "merge", "T1", -1, "test", time.Hour, now); !errors.Is(err, ErrBadCycles) {
		t.Fatalf("want ErrBadCycles, got %v", err)
	}
}

// TestGrantSurvivesKeyRelocation pins the key-custody move: the signing key
// content relocates (out of the state dir, in production), and previously
// minted grants still validate. The key is moved, never re-minted — a re-mint
// would silently invalidate every existing grant.
func TestGrantSurvivesKeyRelocation(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	a, err := Mint(st, key, "o/r", "merge", "T1", 0, "test", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "sub", "grant.key")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}

	if _, err := Check(st, moved, a.ID, "o/r", "merge", now); err != nil {
		t.Fatalf("grant failed to validate after key relocation: %v", err)
	}
}
