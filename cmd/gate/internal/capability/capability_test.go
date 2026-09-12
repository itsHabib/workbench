package capability

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestBoundGrantRequiresExactAuthorizationSubject(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	head := strings.Repeat("a", 40)
	authorizationID := "gau_" + strings.Repeat("b", 64)
	a, err := MintBound(
		st, key, "o/r", "merge", "T1", 1, "gh:approver via environment:1234",
		20*time.Minute, head, 42, authorizationID, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckBound(st, key, a.ID, "o/r", "merge", head, 42, authorizationID, now); err != nil {
		t.Fatalf("exact subject refused: %v", err)
	}
	if _, err := Check(st, key, a.ID, "o/r", "merge", now); !errors.Is(err, ErrHeadMismatch) {
		t.Fatalf("unscoped check = %v, want head mismatch", err)
	}
	if _, err := CheckBound(st, key, a.ID, "o/r", "merge", strings.Repeat("c", 40), 42, authorizationID, now); !errors.Is(err, ErrHeadMismatch) {
		t.Fatalf("wrong head = %v, want head mismatch", err)
	}
	if _, err := CheckBound(st, key, a.ID, "o/r", "merge", head, 43, authorizationID, now); !errors.Is(err, ErrSubject) {
		t.Fatalf("wrong PR = %v, want subject mismatch", err)
	}
}

func TestCheckSubjectAcceptsLegacyAndRequiresBoundSubject(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	legacy, err := Mint(st, key, "o/r", "merge", "T1", 1, "operator", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	if _, err := CheckSubject(st, key, legacy.ID, "o/r", "merge", head, 7, now); err != nil {
		t.Fatalf("legacy grant stopped working on exact-subject path: %v", err)
	}
	bound, err := MintBound(st, key, "o/r", "merge", "T0", 3, "slack", time.Hour, head, 7, "gau_"+strings.Repeat("b", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSubject(st, key, bound.ID, "o/r", "merge", head, 7, now); err != nil {
		t.Fatalf("exact bound subject refused: %v", err)
	}
	if _, err := CheckSubject(st, key, bound.ID, "o/r", "merge", head, 8, now); !errors.Is(err, ErrSubject) {
		t.Fatalf("wrong PR = %v, want subject mismatch", err)
	}
}

func TestMintBoundRejectsMalformedSubject(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	if _, err := MintBound(st, key, "o/r", "merge", "T1", 1, "test", time.Minute, "short", 1, "gau_"+strings.Repeat("b", 64), now); !errors.Is(err, ErrBadHead) {
		t.Fatalf("bad head = %v, want ErrBadHead", err)
	}
	if _, err := MintBound(st, key, "o/r", "merge", "T1", 1, "test", time.Minute, strings.Repeat("a", 40), 0, "gau_"+strings.Repeat("b", 64), now); !errors.Is(err, ErrBadSubject) {
		t.Fatalf("bad PR = %v, want ErrBadSubject", err)
	}
}

func TestMintBoundOnceRejectsElapsedAuthority(t *testing.T) {
	st, key := setup(t)
	now := fixedClock(time.Unix(1000, 0))
	_, err := MintBoundOnce(
		st, key, "o/r", "merge", "T0", 3, "slack", 0,
		strings.Repeat("a", 40), 7, "gau_"+strings.Repeat("b", 64),
		"run_request", "gqr_request", now,
	)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	if _, statErr := os.Stat(key); !os.IsNotExist(statErr) {
		t.Fatal("elapsed authority created signing key material")
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

func TestConfiguredEmptyKeyRefusesMinting(t *testing.T) {
	st, key := setup(t)
	if err := os.WriteFile(key, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Mint(
		st, key, "o/r", "merge", "T1", 1, "test", time.Hour,
		fixedClock(time.Unix(1000, 0)),
	)
	if !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("want ErrKeyInvalid, got %v", err)
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

// TestTierWithinUnknownCandidateMatchesT3 pins current TierWithin semantics a
// hand-ported Lean model of gate surfaced (workbench-laws-lean
// Verdict/Reachability.md): TierWithin validates the grant's CEILING but not the
// CANDIDATE (capability.go:140-148), and tier.Rank ranks every unknown/empty
// string at 3 — the same rank as T3. So an unknown or empty candidate compares
// "within" a valid T3 ceiling and is rejected by every lower ceiling, exactly as
// a real T3 would be. TestTierCeilingFailsClosed only checks a T1 ceiling (where
// rank-3 is over the ceiling regardless); the T3 row is the case it never saw.
//
// This is a semantics + reachability question, NOT a live vulnerability. Per
// Reachability.md, every current owned producer path (triage-floor, submitted
// judgment, readiness, ci-classify) rejects or pins the tier before an unknown
// candidate could reach a live TierWithin call — reaching this row needs a
// foreign/drifted artifact. Whether TierWithin should also validate the
// candidate is open policy question Q2 in cmd/gate/docs/FOLLOWUPS.md, not a
// change made here.
func TestTierWithinUnknownCandidateMatchesT3(t *testing.T) {
	for _, candidate := range []string{"garbage", ""} {
		for _, c := range []struct {
			ceiling string
			within  bool
		}{
			{"T0", false},
			{"T1", false},
			{"T2", false},
			{"T3", true}, // unknown/empty ranks 3, so only a T3 ceiling admits it
		} {
			g := Grant{MaxTier: c.ceiling}
			if got := g.TierWithin(candidate); got != c.within {
				t.Errorf("Grant{MaxTier:%q}.TierWithin(%q) = %v, want %v",
					c.ceiling, candidate, got, c.within)
			}
		}
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
	key := []byte(strings.Repeat("k", 32))
	g := Grant{Repo: "o/r", Action: "merge", MaxTier: "T1", MaxCycles: 3,
		ExpiresAt: time.Unix(2000, 0), MintedBy: "test"}
	sig := sign(key, g)
	g.MaxCycles = 99
	if sign(key, g) == sig {
		t.Fatal("widening MaxCycles did not change the signature — the ceiling is forgeable")
	}
}

func TestUnboundGrantSignatureRemainsBackwardCompatible(t *testing.T) {
	key := []byte("test-key")
	g := Grant{Repo: "o/r", Action: "merge", MaxTier: "T1", MaxCycles: 3,
		ExpiresAt: time.Unix(2000, 0), MintedBy: "test"}
	if got, want := sign(key, g), legacySign(key, g); got != want {
		t.Fatalf("unbound signature changed: got %s want %s", got, want)
	}
}

func TestCheckBoundRefusesPartialBinding(t *testing.T) {
	st, keyPath := setup(t)
	key := []byte(strings.Repeat("k", 32))
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	g := Grant{
		Repo: "o/r", Action: "merge", MaxTier: "T3", MaxCycles: 1,
		ExpiresAt: time.Unix(2000, 0), BoundHead: strings.Repeat("a", 40),
		MintedBy: "test",
	}
	g.Sig = sign(key, g)
	artifact, err := st.Append(state.KindGrant, "run_mint", nil, g)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CheckBound(
		st, keyPath, artifact.ID, "o/r", "merge", g.BoundHead, 1,
		"gau_"+strings.Repeat("b", 64), fixedClock(time.Unix(1000, 0)),
	)
	if !errors.Is(err, ErrBadSubject) {
		t.Fatalf("want ErrBadSubject, got %v", err)
	}
}

func legacySign(key []byte, g Grant) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprint(mac, g.Repo, "|", g.Action, "|", g.MaxTier, "|", g.MaxCycles,
		"|", g.ExpiresAt.Format(time.RFC3339Nano), "|", g.MintedBy)
	return hex.EncodeToString(mac.Sum(nil))
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
