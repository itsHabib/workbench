// Package capability bounds effectful verbs. A grant is scoped (repo + action),
// timed (expiry), and capped (a ceiling risk tier it may auto-land). Grants are
// HMAC-signed artifacts in state; checking one is mechanism, minting one is the
// operator's policy surface. Imports point down: state only.
package capability

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
)

// Grant is the capability artifact body.
type Grant struct {
	Repo      string    `json:"repo"`
	Action    string    `json:"action"`
	MaxTier   string    `json:"max_tier"`
	MaxCycles int       `json:"max_cycles"` // review-cycle ceiling; 0 == unbounded (back-compat)
	ExpiresAt time.Time `json:"expires_at"`
	MintedBy  string    `json:"minted_by"`
	Sig       string    `json:"sig"`
}

// Coded errors so callers branch on the code, never on prose.
var (
	ErrExpired       = errors.New("grant_expired")
	ErrScope         = errors.New("grant_scope_mismatch")
	ErrSignature     = errors.New("grant_bad_signature")
	ErrTierCeiling   = errors.New("grant_tier_exceeded")
	ErrBadTier       = errors.New("grant_bad_tier")
	ErrCycleExceeded = errors.New("grant_cycle_exceeded")
	ErrBadCycles     = errors.New("grant_bad_cycles")
)

// Mint signs and records a grant, returning its artifact. A malformed
// ceiling is refused at mint time — an unrecognized tier or a negative cycle
// cap must never become a grant at all, let alone the broadest one.
func Mint(st *state.Store, keyPath, repo, action, maxTier string, maxCycles int, mintedBy string, ttl time.Duration, now func() time.Time) (state.Artifact, error) {
	if !tier.Valid(maxTier) {
		return state.Artifact{}, fmt.Errorf("%w: %q", ErrBadTier, maxTier)
	}
	if maxCycles < 0 {
		return state.Artifact{}, fmt.Errorf("%w: %d", ErrBadCycles, maxCycles)
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return state.Artifact{}, err
	}
	g := Grant{
		Repo:      repo,
		Action:    action,
		MaxTier:   maxTier,
		MaxCycles: maxCycles,
		ExpiresAt: now().UTC().Add(ttl),
		MintedBy:  mintedBy,
	}
	g.Sig = sign(key, g)
	return st.Append(state.KindGrant, "run_mint", nil, g)
}

// Check validates a grant artifact for a repo+action at time now.
// It returns the parsed grant so the caller can enforce the tier ceiling.
// Check never creates the signing key: a missing key is a loud error, not a
// silent fresh key that would invalidate every existing grant.
func Check(st *state.Store, keyPath, grantID, repo, action string, now func() time.Time) (Grant, error) {
	a, err := st.Get(grantID)
	if err != nil {
		return Grant{}, err
	}
	var g Grant
	if err := json.Unmarshal(a.Body, &g); err != nil {
		return Grant{}, fmt.Errorf("capability: parse grant: %w", err)
	}
	key, err := loadKey(keyPath)
	if err != nil {
		return Grant{}, err
	}
	if !hmac.Equal([]byte(sign(key, g)), []byte(g.Sig)) {
		return Grant{}, ErrSignature
	}
	if g.Repo != repo || g.Action != action {
		return Grant{}, fmt.Errorf("%w: grant is %s/%s, asked %s/%s", ErrScope, g.Repo, g.Action, repo, action)
	}
	if now().UTC().After(g.ExpiresAt) {
		return Grant{}, fmt.Errorf("%w: expired %s", ErrExpired, g.ExpiresAt.Format(time.RFC3339))
	}
	return g, nil
}

// TierWithin reports whether t is at or below the grant's ceiling. A grant
// whose ceiling isn't a defined tier authorizes nothing — fail closed, in
// case a malformed grant predates Mint's validation.
func (g Grant) TierWithin(t string) bool {
	if !tier.Valid(g.MaxTier) {
		return false
	}
	return tierRank(t) <= tierRank(g.MaxTier)
}

// CyclesWithin reports whether review cycle n is at or under the grant's
// ceiling. A zero ceiling means unbounded — the honest back-compat reading of
// a grant minted before the field existed.
func (g Grant) CyclesWithin(n int) bool {
	return g.MaxCycles == 0 || n <= g.MaxCycles
}

func tierRank(t string) int { return tier.Rank(t) }

// sign covers every field a ceiling lives in, each at a fixed position — a
// field outside the pre-image would be silently forgeable. Extending the
// pre-image breaks signatures on grants minted before the extension; grants
// are per-run and short-TTL, so the migration is "mint fresh", not versioned
// signatures.
func sign(key []byte, g Grant) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprint(mac, g.Repo, "|", g.Action, "|", g.MaxTier, "|", g.MaxCycles, "|", g.ExpiresAt.Format(time.RFC3339Nano), "|", g.MintedBy)
	return hex.EncodeToString(mac.Sum(nil))
}

// ErrKeyMissing fires when the signing key is absent where one must already
// exist — a coded error so a deleted or misplaced key is diagnosable instead
// of surfacing as bad signatures on every grant.
var ErrKeyMissing = errors.New("grant_key_missing")

// loadKey reads the signing key; it never creates one.
func loadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return key, nil
	}
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrKeyMissing, path)
	}
	return nil, fmt.Errorf("capability: read key: %w", err)
}

// loadOrCreateKey reads the signing key, minting a fresh one only when none
// exists yet. Only Mint may take this path.
func loadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("capability: read key: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("capability: rand: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("capability: key dir: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("capability: write key: %w", err)
	}
	return key, nil
}
