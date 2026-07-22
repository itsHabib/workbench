// Package grant is custody's signed-capability mechanism: mint an HMAC-signed,
// key-scoped, action-scoped, TTL-bounded grant; validate one before any
// forwarding. It is the mechanism only — sign, verify, persist, read — never
// custody's proxy or rule-matching policy.
//
// The shape is copied deliberately from gate's capability.Grant (HMAC over a
// canonical scope, coded refusals, loud-on-missing-key) rather than imported:
// per the repo's one rule a tenant shares types through contracts, never
// another tenant's decision code. Grants are versioned from the first commit
// (a `version`+`domain` field, a visibly versioned `cst2_` token prefix) so a
// later lift of the shared mechanism into contracts stays mechanical and
// non-breaking — spec §4 D2, copy-then-converge.
//
// Grants also chain: Derive mints a child attenuated from a live parent —
// narrower on every axis (actions a subset, expiry no later, delegation depth
// capped at one) — with the attenuation carried in the signed envelope, not by
// caller discipline. A child names its parent and the transport source it is
// bound to; both join the signature.
package grant

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
)

// Version and Domain are stamped into every grant and covered by the signature.
// They pin which scheme signed a token so convergence onto a shared mechanism
// is a field bump, not a silent reinterpretation. Bump Version only alongside a
// signing-scheme change; grants are re-minted across it (short TTLs, mint-fresh).
const (
	Version = 2
	Domain  = "custody"

	// tokenPrefix versions the wire token so a scheme change is visible in the
	// token itself, not just the record. Version 2 added parent-chaining and the
	// bound source to the signed pre-image; a version-1 cst1_ token fails at the
	// prefix and refuses as ErrNoGrant — the two prefixes are never both accepted.
	tokenPrefix = "cst2_"
)

// Grant is the signed capability body. MintedBy is free-form and
// UNAUTHENTICATED — the same caveat as gate: an honest label of who ran the
// mint, not a verified identity. Mint authority is key custody (whoever can
// read the mint key can sign), never a property of this field.
type Grant struct {
	Version int      `json:"version"`
	Domain  string   `json:"domain"`
	ID      string   `json:"id"`
	Key     string   `json:"key"`     // vendor key name the grant is scoped to
	Actions []string `json:"actions"` // action names this grant covers
	// Parent is the id of the grant this was derived from; empty means an
	// operator-minted root. BoundSource is the transport source the grant is
	// usable from; empty means unbound (usable on the localhost listener only —
	// enforcement of the bind itself is P3, out of scope here). Both join the
	// signature, so neither can be widened in the record without breaking it.
	Parent      string        `json:"parent,omitempty"`
	BoundSource string        `json:"bound_source,omitempty"`
	MintedAt    time.Time     `json:"minted_at"`
	TTL         time.Duration `json:"ttl"`
	MintedBy    string        `json:"minted_by"`
	Sig         string        `json:"-"` // bearer proof: returned in the token, never persisted
}

// Expiry is MintedAt + TTL. A grant is refused at or after this instant.
func (g Grant) Expiry() time.Time { return g.MintedAt.Add(g.TTL) }

// Covers reports whether the grant's action set includes action.
func (g Grant) Covers(action string) bool {
	for _, a := range g.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// Coded refusals: callers branch on the code, never on prose. These are the
// four grant-layer refusal classes of spec §6.
var (
	ErrNoGrant      = errors.New("refused_no_grant")
	ErrExpired      = errors.New("refused_expired")
	ErrBadSignature = errors.New("refused_bad_signature")
	ErrWrongKey     = errors.New("refused_wrong_key")
)

// Attenuation refusals: Derive mints a child only when it narrows every axis.
// Each is a coded error callers branch on. ErrChainDepth also fires at Validate
// time for a presented chain deeper than one, so a depth-2 record assembled
// outside Derive refuses per-request, not only at mint.
var (
	ErrAttenuationActions = errors.New("refused_attenuation_actions")
	ErrAttenuationTTL     = errors.New("refused_attenuation_ttl")
	ErrChainDepth         = errors.New("refused_chain_depth")
)

// ErrKeyMissing fires when the mint key is absent where one must already exist
// (validation) — a coded error so a deleted or misplaced key is diagnosable
// instead of surfacing as bad signatures on every grant. It is an operational
// error, not one of the four refusal classes.
var ErrKeyMissing = errors.New("mint_key_missing")

// ErrKeyInvalid fires when the persisted mint key is not the exact 32-byte
// key this package creates. Signing with a truncated or empty key would turn
// corrupted state into a weakened authorization boundary.
var ErrKeyInvalid = errors.New("mint_key_invalid")

// Store mints and reads grant records under a state dir, signing with a mint
// key held in a SEPARATE trust domain. NewStore refuses to build a Store whose
// key dir sits inside the state dir — co-locating the signing key with the
// grants it signs means anything that can read state can forge broader grants.
type Store struct {
	stateDir    string
	mintKeyPath string
}

// NewStore validates that keyDir is a distinct trust domain from stateDir and
// returns a Store signing with keyDir/mint.key. This mirrors gate's newEnv
// refusal exactly: a key dir equal to or nested under the state dir is refused
// at startup rather than silently restoring the co-location the design removes.
func NewStore(stateDir, keyDir string) (*Store, error) {
	within, err := dirWithin(keyDir, stateDir)
	if err != nil {
		return nil, err
	}
	if within {
		return nil, fmt.Errorf("custody: mint key dir %q must be outside state dir %q", keyDir, stateDir)
	}
	resolvedState, err := resolvePath(stateDir)
	if err != nil {
		return nil, fmt.Errorf("custody: resolve state dir %q: %w", stateDir, err)
	}
	resolvedKey, err := resolvePath(keyDir)
	if err != nil {
		return nil, fmt.Errorf("custody: resolve key dir %q: %w", keyDir, err)
	}
	within, err = dirWithin(resolvedKey, resolvedState)
	if err != nil {
		return nil, err
	}
	if within {
		return nil, fmt.Errorf("custody: mint key dir %q must be outside state dir %q", keyDir, stateDir)
	}
	return &Store{stateDir: resolvedState, mintKeyPath: filepath.Join(resolvedKey, "mint.key")}, nil
}

// Mint signs and persists a grant, returning it and its wire token. The mint
// key is created on first use and only here; validation never mints a key.
func (s *Store) Mint(key string, actions []string, ttl time.Duration, mintedBy string, now func() time.Time) (Grant, string, error) {
	if key == "" {
		return Grant{}, "", errors.New("custody: key name required")
	}
	if len(actions) == 0 {
		return Grant{}, "", errors.New("custody: at least one action required")
	}
	if ttl <= 0 {
		return Grant{}, "", errors.New("custody: ttl must be positive")
	}
	id, err := newID()
	if err != nil {
		return Grant{}, "", err
	}
	mintKey, err := loadOrCreateKey(s.mintKeyPath)
	if err != nil {
		return Grant{}, "", err
	}
	g := Grant{
		Version:  Version,
		Domain:   Domain,
		ID:       id,
		Key:      key,
		Actions:  append([]string(nil), actions...),
		MintedAt: now().UTC(),
		TTL:      ttl,
		MintedBy: mintedBy,
	}
	g.Sig = sign(mintKey, g)
	if err := s.save(g); err != nil {
		return Grant{}, "", err
	}
	return g, token(g), nil
}

// RequireMintKey enforces the fresh-mint-key opt-in, mirroring gate's
// checkGrantStateDir guard. It refuses when no mint key exists yet unless
// allowInit is set: an absent key without -init reads as a misdirected
// -mint-key-dir, and minting a fresh key there would sign grants with an orphan
// key that serve (reading the canonical key dir) later rejects as
// refused_bad_signature — the silent split-brain that costs a four-hour hunt.
// The error names the resolved key path so the wrong dir is obvious. With
// allowInit an absent key is fine (Mint creates it on first use); a present key
// always passes. Callers run this before Mint; Mint itself never gates creation.
func (s *Store) RequireMintKey(allowInit bool) error {
	if allowInit {
		return nil
	}
	_, err := os.Stat(s.mintKeyPath)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("custody: check mint key: %w", err)
	}
	return fmt.Errorf("%w: %s — refusing to mint with a fresh mint key; point -mint-key-dir at your canonical key dir, or pass -init to create one here", ErrKeyMissing, s.mintKeyPath)
}

// Validate parses a token, loads its record, and checks signature, chain depth,
// key scope, and TTL for time now — returning the parsed grant so the caller can
// extract the action set. Refusal order is: no usable grant, bad signature, over
// depth, wrong key, expired. Signature is checked before any scope field is
// trusted; depth is checked before key/expiry so a forged deep chain refuses
// even for the right key at the right time.
func (s *Store) Validate(tok, key string, now func() time.Time) (Grant, error) {
	g, err := s.authenticate(tok)
	if err != nil {
		return Grant{}, err
	}
	if g.Key != key {
		return Grant{}, fmt.Errorf("%w: grant is for %q, asked %q", ErrWrongKey, g.Key, key)
	}
	if !now().UTC().Before(g.Expiry()) {
		return Grant{}, fmt.Errorf("%w: expired %s", ErrExpired, g.Expiry().Format(time.RFC3339))
	}
	return g, nil
}

// authenticate does the key- and time-independent checks — parse, load, id
// match, signature, scheme, chain depth — and returns the parsed grant. Validate
// layers key scope and expiry on top; Derive reuses it to authenticate a parent
// whose key the caller does not restate, since a child inherits the parent's key.
func (s *Store) authenticate(tok string) (Grant, error) {
	id, sig, err := parseToken(tok)
	if err != nil {
		return Grant{}, err
	}
	g, err := s.load(id)
	if err != nil {
		return Grant{}, err
	}
	if g.ID != id {
		return Grant{}, fmt.Errorf("%w: grant record id does not match token", ErrBadSignature)
	}
	mintKey, err := loadKey(s.mintKeyPath)
	if err != nil {
		return Grant{}, err
	}
	if !hmac.Equal([]byte(sign(mintKey, g)), []byte(sig)) {
		return Grant{}, ErrBadSignature
	}
	if g.Version != Version || g.Domain != Domain {
		return Grant{}, fmt.Errorf("%w: unsupported grant scheme", ErrBadSignature)
	}
	if err := s.checkChainDepth(g); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// checkChainDepth refuses a presented grant whose parent record itself carries a
// parent: delegation depth is capped at one, enforced here so a depth-2 chain
// assembled outside Derive (an old binary, a hand-built record) refuses per
// request, not only when Derive would decline to mint it. It loads the parent
// record for its own parent field alone — not a live re-check of the parent's
// signature chain — keeping validation pure crypto plus local records.
func (s *Store) checkChainDepth(g Grant) error {
	if g.Parent == "" {
		return nil
	}
	parent, err := s.load(g.Parent)
	if err != nil {
		return err
	}
	if parent.Parent != "" {
		return fmt.Errorf("%w: parent %s is itself derived", ErrChainDepth, g.Parent)
	}
	return nil
}

// Derive mints a child grant attenuated from a live parent token. It
// authenticates the parent, refuses to chain past depth one, then refuses unless
// the child narrows every axis — actions a subset of the parent's, expiry at or
// before the parent's. The child inherits the parent's key, names the parent,
// carries the bound source, is signed and persisted beside grants. Attenuation
// is enforced in this signed envelope, never by caller discipline.
func (s *Store) Derive(parentTok string, actions []string, ttl time.Duration, boundSource, mintedBy string, now func() time.Time) (Grant, string, error) {
	if len(actions) == 0 {
		return Grant{}, "", errors.New("custody: at least one action required")
	}
	if ttl <= 0 {
		return Grant{}, "", errors.New("custody: ttl must be positive")
	}
	parent, err := s.authenticate(parentTok)
	if err != nil {
		return Grant{}, "", err
	}
	if !now().UTC().Before(parent.Expiry()) {
		return Grant{}, "", fmt.Errorf("%w: parent expired %s", ErrExpired, parent.Expiry().Format(time.RFC3339))
	}
	if parent.Parent != "" {
		return Grant{}, "", fmt.Errorf("%w: parent %s is itself derived", ErrChainDepth, parent.ID)
	}
	if !subset(actions, parent.Actions) {
		return Grant{}, "", fmt.Errorf("%w: child actions %v exceed parent %v", ErrAttenuationActions, actions, parent.Actions)
	}
	id, err := newID()
	if err != nil {
		return Grant{}, "", err
	}
	child := Grant{
		Version:     Version,
		Domain:      Domain,
		ID:          id,
		Key:         parent.Key,
		Actions:     append([]string(nil), actions...),
		Parent:      parent.ID,
		BoundSource: boundSource,
		MintedAt:    now().UTC(),
		TTL:         ttl,
		MintedBy:    mintedBy,
	}
	if child.Expiry().After(parent.Expiry()) {
		return Grant{}, "", fmt.Errorf("%w: child expiry %s after parent expiry %s", ErrAttenuationTTL, child.Expiry().Format(time.RFC3339), parent.Expiry().Format(time.RFC3339))
	}
	mintKey, err := loadKey(s.mintKeyPath)
	if err != nil {
		return Grant{}, "", err
	}
	child.Sig = sign(mintKey, child)
	if err := s.save(child); err != nil {
		return Grant{}, "", err
	}
	return child, token(child), nil
}

// subset reports whether every action in child appears in parent — the action
// axis of attenuation. The parent set is small (a grant's action list), so a map
// keeps the check linear without an ordering assumption.
func subset(child, parent []string) bool {
	set := make(map[string]struct{}, len(parent))
	for _, a := range parent {
		set[a] = struct{}{}
	}
	for _, a := range child {
		if _, ok := set[a]; !ok {
			return false
		}
	}
	return true
}

// save writes the record to <state>/grants/<id>.json. The grants dir and record
// are operator-owned; permissions match gate's state (0o700 dir, 0o600 file).
func (s *Store) save(g Grant) error {
	dir := filepath.Join(s.stateDir, "grants")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("custody: grants dir: %w", err)
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return fmt.Errorf("custody: encode grant: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(dir, g.ID+".json"), data, 0o600); err != nil {
		return fmt.Errorf("custody: write grant: %w", err)
	}
	return nil
}

// load reads a grant record by id. A missing record is ErrNoGrant — the token
// names a grant that does not exist here.
func (s *Store) load(id string) (Grant, error) {
	data, err := os.ReadFile(filepath.Join(s.stateDir, "grants", id+".json"))
	if os.IsNotExist(err) {
		return Grant{}, fmt.Errorf("%w: %s", ErrNoGrant, id)
	}
	if err != nil {
		return Grant{}, fmt.Errorf("custody: read grant: %w", err)
	}
	var g Grant
	if err := json.Unmarshal(data, &g); err != nil {
		return Grant{}, fmt.Errorf("%w: parse grant %s: %v", ErrNoGrant, id, err)
	}
	return g, nil
}

// token renders the wire form cst2_<id>.<sig>.
func token(g Grant) string { return tokenPrefix + g.ID + "." + g.Sig }

// parseToken splits cst2_<id>.<sig> and rejects anything malformed. id and sig
// are hex (Mint emits only hex), so validating the alphabet here also stops a
// crafted token from steering the record path outside the grants dir — a
// structurally invalid token is ErrNoGrant (no usable grant to check).
func parseToken(tok string) (id, sig string, err error) {
	body, ok := strings.CutPrefix(tok, tokenPrefix)
	if !ok {
		return "", "", fmt.Errorf("%w: token prefix", ErrNoGrant)
	}
	id, sig, ok = strings.Cut(body, ".")
	if !ok || len(id) != 32 || len(sig) != sha256.Size*2 || !isHex(id) || !isHex(sig) {
		return "", "", fmt.Errorf("%w: malformed token", ErrNoGrant)
	}
	return id, sig, nil
}

// sign is the HMAC over the canonical scope. Every field a scope lives in is
// covered at a fixed position — version, domain, id, key, minted-at, ttl,
// minted-by, parent, bound-source, then a length-prefixed action list that keeps
// ["a,b"] distinct from ["a","b"]. A field outside this pre-image would be
// silently forgeable, so extend it only alongside a Version bump; parent and
// bound-source joined it at Version 2.
func sign(key []byte, g Grant) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprint(mac, g.Version)
	writeSignField(mac, g.Domain)
	writeSignField(mac, g.ID)
	writeSignField(mac, g.Key)
	writeSignField(mac, g.MintedAt.Format(time.RFC3339Nano))
	writeSignField(mac, g.TTL.String())
	writeSignField(mac, g.MintedBy)
	writeSignField(mac, g.Parent)
	writeSignField(mac, g.BoundSource)
	fmt.Fprint(mac, "|", len(g.Actions))
	for _, a := range g.Actions {
		writeSignField(mac, a)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func writeSignField(mac interface{ Write([]byte) (int, error) }, value string) {
	fmt.Fprintf(mac, "|%d:", len(value))
	_, _ = mac.Write([]byte(value))
}

// newID returns a 16-byte random hex id.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("custody: rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// loadKey reads the mint key; it never creates one. A missing key where one
// must exist is a loud coded error, not a silent fresh key that would
// invalidate every existing grant.
func loadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return validateMintKey(path, key)
	}
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrKeyMissing, path)
	}
	return nil, fmt.Errorf("custody: read mint key: %w", err)
}

// loadOrCreateKey reads the mint key, minting a fresh 32-byte key only when none
// exists yet. Only Mint takes this path.
func loadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return validateMintKey(path, key)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("custody: read mint key: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("custody: rand: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("custody: mint key dir: %w", err)
	}
	created, err := createFileExclusive(path, key, 0o600)
	if err != nil {
		return nil, fmt.Errorf("custody: write mint key: %w", err)
	}
	if !created {
		return loadKey(path)
	}
	return key, nil
}

func validateMintKey(path string, key []byte) ([]byte, error) {
	if len(key) != sha256.Size {
		return nil, fmt.Errorf("%w: %s has %d bytes, want %d", ErrKeyInvalid, path, len(key), sha256.Size)
	}
	return key, nil
}

func createFileExclusive(path string, data []byte, perm os.FileMode) (bool, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".custody-key-*.tmp")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".custody-grant-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// dirWithin reports whether sub is the same directory as base or nested under
// it. Callers pass paths canonicalized by resolvePath so symlink spelling
// cannot put the mint key inside state while appearing outside it.
func dirWithin(sub, base string) (bool, error) {
	absSub, err := filepath.Abs(sub)
	if err != nil {
		return false, fmt.Errorf("custody: resolve key dir %q: %w", sub, err)
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false, fmt.Errorf("custody: resolve state dir %q: %w", base, err)
	}
	rel, err := filepath.Rel(absBase, absSub)
	if err != nil {
		// Different volumes have no relative path — genuinely outside.
		return false, nil
	}
	if rel == "." {
		return true, nil
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// resolvePath returns an absolute path with every existing symlink resolved.
// For a path that does not exist yet, it resolves the nearest existing parent
// and appends the missing suffix. NewStore retains this canonical spelling so
// later key creation does not follow the configured symlink spelling.
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	info, lstatErr := os.Lstat(abs)
	if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(abs)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(abs), target)
		}
		return resolvePath(target)
	}
	if lstatErr != nil && !os.IsNotExist(lstatErr) {
		return "", lstatErr
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs, nil
	}
	resolvedParent, err := resolvePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(abs)), nil
}

// isHex reports whether s is non-empty and all lowercase-hex — the alphabet
// Mint emits for ids and signatures.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
