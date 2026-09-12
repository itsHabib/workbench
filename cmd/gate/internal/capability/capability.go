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
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
)

// Grant is the capability artifact body.
type Grant struct {
	Repo            string    `json:"repo"`
	Action          string    `json:"action"`
	MaxTier         string    `json:"max_tier"`
	MaxCycles       int       `json:"max_cycles"` // review-cycle ceiling; 0 == unbounded (back-compat)
	ExpiresAt       time.Time `json:"expires_at"`
	BoundHead       string    `json:"bound_head,omitempty"`
	BoundPR         int       `json:"bound_pr,omitempty"`
	AuthorizationID string    `json:"authorization_id,omitempty"`
	MintedBy        string    `json:"minted_by"`
	Sig             string    `json:"sig"`
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
	ErrBadHead       = errors.New("grant_bad_head")
	ErrHeadMismatch  = errors.New("grant_head_mismatch")
	ErrBadSubject    = errors.New("grant_bad_subject")
	ErrSubject       = errors.New("grant_subject_mismatch")
)

// Mint signs and records a grant, returning its artifact. A malformed
// ceiling is refused at mint time — an unrecognized tier or a negative cycle
// cap must never become a grant at all, let alone the broadest one.
func Mint(st *state.Store, keyPath, repo, action, maxTier string, maxCycles int, mintedBy string, ttl time.Duration, now func() time.Time) (state.Artifact, error) {
	return mint(st, keyPath, Grant{
		Repo: repo, Action: action, MaxTier: maxTier, MaxCycles: maxCycles,
		ExpiresAt: now().UTC().Add(ttl), MintedBy: mintedBy,
	})
}

// MintBound records a protected-workflow grant scoped to one authorization,
// PR, and exact head. Callers must authenticate approval before entering this
// mechanism; the bound fields make any wider or replayed use fail closed.
func MintBound(st *state.Store, keyPath, repo, action, maxTier string, maxCycles int, mintedBy string, ttl time.Duration, head string, pr int, authorizationID string, now func() time.Time) (state.Artifact, error) {
	if !validSHA(head) {
		return state.Artifact{}, fmt.Errorf("%w: %q", ErrBadHead, head)
	}
	if pr < 1 || !validAuthorizationID(authorizationID) {
		return state.Artifact{}, ErrBadSubject
	}
	return mint(st, keyPath, Grant{
		Repo: repo, Action: action, MaxTier: maxTier, MaxCycles: maxCycles,
		ExpiresAt: now().UTC().Add(ttl), BoundHead: head, BoundPR: pr,
		AuthorizationID: authorizationID, MintedBy: mintedBy,
	})
}

// MintBoundOnce signs and records an exact-subject grant only when the request
// has no terminal grant or denial. The conflict check and append share Gate
// state's cross-process lock, so a double tap or approve/deny race has one
// winner. requestID is the sole parent and run is the request's run.
func MintBoundOnce(st *state.Store, keyPath, repo, action, maxTier string, maxCycles int, mintedBy string, ttl time.Duration, head string, pr int, authorizationID, run, requestID string, now func() time.Time) (state.Artifact, error) {
	if !validSHA(head) {
		return state.Artifact{}, fmt.Errorf("%w: %q", ErrBadHead, head)
	}
	if pr < 1 || !validAuthorizationID(authorizationID) || run == "" || requestID == "" {
		return state.Artifact{}, ErrBadSubject
	}
	if ttl <= 0 {
		return state.Artifact{}, ErrExpired
	}
	g := Grant{
		Repo: repo, Action: action, MaxTier: maxTier, MaxCycles: maxCycles,
		ExpiresAt: now().UTC().Add(ttl), BoundHead: head, BoundPR: pr,
		AuthorizationID: authorizationID, MintedBy: mintedBy,
	}
	if !tier.Valid(g.MaxTier) {
		return state.Artifact{}, fmt.Errorf("%w: %q", ErrBadTier, g.MaxTier)
	}
	if g.MaxCycles < 0 {
		return state.Artifact{}, fmt.Errorf("%w: %d", ErrBadCycles, g.MaxCycles)
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return state.Artifact{}, err
	}
	g.Sig = sign(key, g)
	return st.AppendIfAbsentParentKinds(
		state.KindGrant,
		[]string{state.KindGrant, state.KindGrantDenied},
		run, requestID, []string{requestID}, g,
	)
}

func mint(st *state.Store, keyPath string, g Grant) (state.Artifact, error) {
	if !tier.Valid(g.MaxTier) {
		return state.Artifact{}, fmt.Errorf("%w: %q", ErrBadTier, g.MaxTier)
	}
	if g.MaxCycles < 0 {
		return state.Artifact{}, fmt.Errorf("%w: %d", ErrBadCycles, g.MaxCycles)
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return state.Artifact{}, err
	}
	g.Sig = sign(key, g)
	return st.Append(state.KindGrant, "run_mint", nil, g)
}

// Check validates a grant artifact for a repo+action at time now.
// It returns the parsed grant so the caller can enforce the tier ceiling.
// Check never creates the signing key: a missing key is a loud error, not a
// silent fresh key that would invalidate every existing grant.
func Check(st *state.Store, keyPath, grantID, repo, action string, now func() time.Time) (Grant, error) {
	return CheckBound(st, keyPath, grantID, repo, action, "", 0, "", now)
}

// CheckSubject validates a grant for the exact subject Gate is evaluating.
// Legacy unbound grants remain valid. A bound grant must match head and PR; its
// authorization id remains HMAC-covered provenance but need not be supplied by
// the evaluator, which starts from the signed grant artifact itself.
func CheckSubject(st *state.Store, keyPath, grantID, repo, action, head string, pr int, now func() time.Time) (Grant, error) {
	g, err := check(st, keyPath, grantID, repo, action)
	if err != nil {
		return Grant{}, err
	}
	if g.BoundHead != "" && g.BoundHead != head {
		return Grant{}, fmt.Errorf("%w: grant is bound to %s", ErrHeadMismatch, g.BoundHead)
	}
	if g.BoundPR != 0 && g.BoundPR != pr {
		return Grant{}, fmt.Errorf("%w: grant is bound to PR %d", ErrSubject, g.BoundPR)
	}
	return checkExpiry(g, now)
}

// CheckBound validates both ordinary scope and any exact authorization subject
// encoded in a protected-workflow grant. An unbound legacy grant still matches
// any subject; a bound grant requires every supplied subject field.
func CheckBound(st *state.Store, keyPath, grantID, repo, action, head string, pr int, authorizationID string, now func() time.Time) (Grant, error) {
	g, err := check(st, keyPath, grantID, repo, action)
	if err != nil {
		return Grant{}, err
	}
	if g.BoundHead != "" && g.BoundHead != head {
		return Grant{}, fmt.Errorf("%w: grant is bound to %s", ErrHeadMismatch, g.BoundHead)
	}
	if g.BoundPR != 0 && (g.BoundPR != pr || g.AuthorizationID != authorizationID) {
		return Grant{}, fmt.Errorf("%w: grant is bound to PR %d authorization %s", ErrSubject, g.BoundPR, g.AuthorizationID)
	}
	return checkExpiry(g, now)
}

func check(st *state.Store, keyPath, grantID, repo, action string) (Grant, error) {
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
	if err := validateBinding(g); err != nil {
		return Grant{}, err
	}
	if g.Repo != repo || g.Action != action {
		return Grant{}, fmt.Errorf("%w: grant is %s/%s, asked %s/%s", ErrScope, g.Repo, g.Action, repo, action)
	}
	return g, nil
}

func checkExpiry(g Grant, now func() time.Time) (Grant, error) {
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

// sign preserves the original pre-image for ordinary grants so a Gate binary
// upgrade does not invalidate a live operator grant. Bound grants use a
// domain-separated suffix that covers every exact-authorization field.
func sign(key []byte, g Grant) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprint(mac, g.Repo, "|", g.Action, "|", g.MaxTier, "|", g.MaxCycles,
		"|", g.ExpiresAt.Format(time.RFC3339Nano), "|", g.MintedBy)
	if g.BoundHead != "" || g.BoundPR != 0 || g.AuthorizationID != "" {
		fmt.Fprint(mac, "|bound-v1|", g.BoundHead, "|", g.BoundPR, "|", g.AuthorizationID)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func validateBinding(g Grant) error {
	bound := g.BoundHead != "" || g.BoundPR != 0 || g.AuthorizationID != ""
	if !bound {
		return nil
	}
	if !validSHA(g.BoundHead) {
		return ErrBadHead
	}
	if g.BoundPR < 1 || !validAuthorizationID(g.AuthorizationID) {
		return ErrBadSubject
	}
	return nil
}

func validSHA(value string) bool {
	if len(value) != 40 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validAuthorizationID(value string) bool {
	const prefix = "gau_"
	return strings.HasPrefix(value, prefix) &&
		len(value) == len(prefix)+64 &&
		validSHA256(strings.TrimPrefix(value, prefix))
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ErrKeyMissing fires when the signing key is absent where one must already
// exist — a coded error so a deleted or misplaced key is diagnosable instead
// of surfacing as bad signatures on every grant.
var ErrKeyMissing = errors.New("grant_key_missing")

// ErrKeyInvalid fires when configured key material is too short to be a
// cryptographic HMAC secret. In particular, an unset workflow secret decoded
// into an empty file must never arm minting.
var ErrKeyInvalid = errors.New("grant_key_invalid")

// loadKey reads the signing key; it never creates one.
func loadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return validateKey(key)
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
		return validateKey(key)
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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return loadKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("capability: create key: %w", err)
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("capability: write key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("capability: sync key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("capability: close key: %w", err)
	}
	return key, nil
}

func validateKey(key []byte) ([]byte, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("%w: need at least 32 bytes", ErrKeyInvalid)
	}
	return key, nil
}
