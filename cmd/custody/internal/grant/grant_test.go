package grant

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixedClock returns a now func pinned to t — no wall-clock reads in any test.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// newStore builds a Store with the state dir and key dir as siblings under a
// fresh temp root, so the key dir is a distinct trust domain from state.
func newStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	s, err := NewStore(filepath.Join(root, "state"), filepath.Join(root, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestNewStoreRefusesKeyDirUnderState(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	cases := map[string]string{
		"nested":       filepath.Join(state, "keys"),
		"deeperNested": filepath.Join(state, "a", "b"),
		"equal":        state,
	}
	for name, keyDir := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewStore(state, keyDir); err == nil {
				t.Fatalf("expected refusal for key dir %q under state %q", keyDir, state)
			}
		})
	}
}

func TestNewStoreAllowsSiblingKeyDir(t *testing.T) {
	root := t.TempDir()
	if _, err := NewStore(filepath.Join(root, "state"), filepath.Join(root, "key")); err != nil {
		t.Fatalf("sibling key dir should be allowed: %v", err)
	}
}

func TestNewStoreRefusesSymlinkedKeyDirUnderState(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	keys := filepath.Join(state, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatalf("mkdir keys: %v", err)
	}
	link := filepath.Join(root, "key-link")
	if err := os.Symlink(keys, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewStore(state, link); err == nil {
		t.Fatal("expected refusal for symlinked key dir under state")
	}
}

func TestNewStoreRefusesDanglingSymlinkedKeyDirUnderState(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	target := filepath.Join(state, "not-created-yet")
	link := filepath.Join(root, "dangling-key-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewStore(state, link); err == nil {
		t.Fatal("expected refusal for dangling symlinked key dir under state")
	}
}

func TestNewStoreRefusesKeySymlinkLocatedUnderState(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	link := filepath.Join(state, "key-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewStore(state, link); err == nil {
		t.Fatal("expected refusal for key symlink located under state")
	}
}

func TestMintValidateRoundTrip(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	g, tok, err := s.Mint("tracker", []string{"read", "comment"}, 8*time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !strings.HasPrefix(tok, "cst2_") {
		t.Fatalf("token missing versioned prefix: %q", tok)
	}
	if g.Version != Version || g.Domain != Domain {
		t.Fatalf("grant missing version/domain stamp: %+v", g)
	}
	got, err := s.Validate(tok, "tracker", now)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.ID != g.ID {
		t.Fatalf("round-trip id mismatch: %q vs %q", got.ID, g.ID)
	}
	if !got.Covers("read") || !got.Covers("comment") || got.Covers("all") {
		t.Fatalf("action-set extraction wrong: %v", got.Actions)
	}
}

func TestMintDoesNotPersistBearerSignature(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	g, _, err := s.Mint("tracker", []string{"read"}, time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(s.stateDir, "grants", g.ID+".json"))
	if err != nil {
		t.Fatalf("read grant record: %v", err)
	}
	if strings.Contains(string(data), `"sig"`) || strings.Contains(string(data), g.Sig) {
		t.Fatal("persisted grant record contains bearer signature")
	}
}

func TestValidateRejectsRecordWhoseIDDoesNotMatchToken(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	g, _, err := s.Mint("tracker", []string{"read"}, time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(s.stateDir, "grants", g.ID+".json"))
	if err != nil {
		t.Fatalf("read grant record: %v", err)
	}
	otherID := strings.Repeat("a", 32)
	if otherID == g.ID {
		otherID = strings.Repeat("b", 32)
	}
	if err := os.WriteFile(filepath.Join(s.stateDir, "grants", otherID+".json"), data, 0o600); err != nil {
		t.Fatalf("copy grant record: %v", err)
	}
	tok := tokenPrefix + otherID + "." + g.Sig
	if _, err := s.Validate(tok, "tracker", now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Validate copied record error = %v, want %v", err, ErrBadSignature)
	}
}

func TestValidateRefusals(t *testing.T) {
	mintClock := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))

	tests := []struct {
		name string
		// mutate returns (token, key, validateClock) for the case, given a
		// freshly minted store, grant, and its valid token.
		mutate func(t *testing.T, s *Store, g Grant, tok string) (token, key string, at func() time.Time)
		want   error
	}{
		{
			name: "no_grant_unknown_id",
			mutate: func(_ *testing.T, _ *Store, _ Grant, _ string) (string, string, func() time.Time) {
				return "cst2_" + strings.Repeat("d", 32) + "." + strings.Repeat("c", 64), "tracker", mintClock
			},
			want: ErrNoGrant,
		},
		{
			name: "no_grant_bad_prefix",
			mutate: func(_ *testing.T, _ *Store, _ Grant, tok string) (string, string, func() time.Time) {
				return strings.TrimPrefix(tok, "cst2_"), "tracker", mintClock
			},
			want: ErrNoGrant,
		},
		{
			name: "expired",
			mutate: func(_ *testing.T, _ *Store, _ Grant, tok string) (string, string, func() time.Time) {
				after := fixedClock(time.Date(2026, 7, 20, 21, 0, 0, 0, time.UTC)) // +9h > 8h ttl
				return tok, "tracker", after
			},
			want: ErrExpired,
		},
		{
			name: "expired_at_exact_boundary",
			mutate: func(_ *testing.T, _ *Store, _ Grant, tok string) (string, string, func() time.Time) {
				atExpiry := fixedClock(time.Date(2026, 7, 20, 20, 0, 0, 0, time.UTC))
				return tok, "tracker", atExpiry
			},
			want: ErrExpired,
		},
		{
			name: "bad_signature_tampered_token",
			mutate: func(_ *testing.T, _ *Store, _ Grant, tok string) (string, string, func() time.Time) {
				// Flip the last sig nibble; id still resolves the record.
				return flipLastHex(tok), "tracker", mintClock
			},
			want: ErrBadSignature,
		},
		{
			name: "bad_signature_tampered_record",
			mutate: func(t *testing.T, s *Store, g Grant, tok string) (string, string, func() time.Time) {
				// Widen the persisted action set without re-signing.
				path := filepath.Join(s.stateDir, "grants", g.ID+".json")
				g.Actions = append(g.Actions, "all")
				data, err := json.Marshal(g)
				if err != nil {
					t.Fatalf("marshal tampered: %v", err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatalf("write tampered: %v", err)
				}
				return tok, "tracker", mintClock
			},
			want: ErrBadSignature,
		},
		{
			name: "bad_signature_wrong_scheme",
			mutate: func(t *testing.T, s *Store, g Grant, _ string) (string, string, func() time.Time) {
				g.Version++
				key, err := os.ReadFile(s.mintKeyPath)
				if err != nil {
					t.Fatalf("read mint key: %v", err)
				}
				g.Sig = sign(key, g)
				data, err := json.Marshal(g)
				if err != nil {
					t.Fatalf("marshal wrong-scheme grant: %v", err)
				}
				if err := os.WriteFile(filepath.Join(s.stateDir, "grants", g.ID+".json"), data, 0o600); err != nil {
					t.Fatalf("write wrong-scheme grant: %v", err)
				}
				return token(g), "tracker", mintClock
			},
			want: ErrBadSignature,
		},
		{
			name: "wrong_key",
			mutate: func(_ *testing.T, _ *Store, _ Grant, tok string) (string, string, func() time.Time) {
				return tok, "hobbyvendor", mintClock
			},
			want: ErrWrongKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			g, tok, err := s.Mint("tracker", []string{"read"}, 8*time.Hour, "operator", mintClock)
			if err != nil {
				t.Fatalf("Mint: %v", err)
			}
			token, key, at := tc.mutate(t, s, g, tok)
			_, err = s.Validate(token, key, at)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateMissingKeyIsLoud(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	_, tok, err := s.Mint("tracker", []string{"read"}, time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// Remove the mint key: validation must be a loud coded error, not a silent
	// fresh key or a bad-signature masquerade.
	if err := os.Remove(s.mintKeyPath); err != nil {
		t.Fatalf("remove mint key: %v", err)
	}
	if _, err := s.Validate(tok, "tracker", now); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("want ErrKeyMissing, got %v", err)
	}
}

func TestLoadKeyRejectsWrongLength(t *testing.T) {
	for _, size := range []int{0, 1, sha256.Size - 1, sha256.Size + 1} {
		path := filepath.Join(t.TempDir(), "mint.key")
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatalf("write %d-byte key: %v", size, err)
		}
		if _, err := loadKey(path); !errors.Is(err, ErrKeyInvalid) {
			t.Fatalf("loadKey %d bytes: want ErrKeyInvalid, got %v", size, err)
		}
	}
}

func TestValidateCorruptRecordIsNoGrant(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	g, tok, err := s.Mint("tracker", []string{"read"}, time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.stateDir, "grants", g.ID+".json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt record: %v", err)
	}
	if _, err := s.Validate(tok, "tracker", now); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("Validate: want ErrNoGrant, got %v", err)
	}
}

func TestLoadOrCreateKeyConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "mint.key")
	const workers = 16
	start := make(chan struct{})
	results := make(chan []byte, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			key, err := loadOrCreateKey(path)
			results <- key
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("loadOrCreateKey: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read winner: %v", err)
	}
	for got := range results {
		if string(got) != string(want) {
			t.Fatal("concurrent creator returned a losing key")
		}
	}
}

func TestMintValidatesInputs(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	cases := map[string]struct {
		key     string
		actions []string
		ttl     time.Duration
	}{
		"empty_key":       {"", []string{"read"}, time.Hour},
		"no_actions":      {"tracker", nil, time.Hour},
		"nonpositive_ttl": {"tracker", []string{"read"}, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := s.Mint(c.key, c.actions, c.ttl, "operator", now); err == nil {
				t.Fatalf("expected Mint to reject %s", name)
			}
		})
	}
}

func TestParseTokenRejectsWrongComponentLengths(t *testing.T) {
	cases := []string{
		"cst2_deadbeef." + strings.Repeat("a", sha256.Size*2),
		"cst2_" + strings.Repeat("a", 32) + ".cafef00d",
	}
	for _, tok := range cases {
		if _, _, err := parseToken(tok); !errors.Is(err, ErrNoGrant) {
			t.Fatalf("parseToken(%q): want ErrNoGrant, got %v", tok, err)
		}
	}
}

func TestSignLengthPrefixesVariableFields(t *testing.T) {
	g := Grant{Version: Version, Domain: Domain, ID: strings.Repeat("a", 32), Key: "tracker", MintedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), TTL: time.Hour}
	a := g
	a.MintedBy = "op|1\x1fread"
	a.Actions = []string{"write"}
	b := g
	b.MintedBy = "op"
	b.Actions = []string{"read|1\x1fwrite"}
	if sign([]byte("key"), a) == sign([]byte("key"), b) {
		t.Fatal("distinct grants produced the same signature pre-image")
	}
}

func TestMintDeriveValidateRoundTrip(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	_, parentTok, err := s.Mint("tracker", []string{"read", "comment", "label"}, 8*time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	child, childTok, err := s.Derive(parentTok, []string{"read", "label"}, 2*time.Hour, "10.0.0.5", "agent", now)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !strings.HasPrefix(childTok, "cst2_") {
		t.Fatalf("child token missing versioned prefix: %q", childTok)
	}
	if child.Parent == "" || child.BoundSource != "10.0.0.5" {
		t.Fatalf("child missing chain fields: %+v", child)
	}
	got, err := s.Validate(childTok, "tracker", now)
	if err != nil {
		t.Fatalf("Validate child: %v", err)
	}
	if !got.Covers("read") || !got.Covers("label") {
		t.Fatalf("child should cover the derived subset: %v", got.Actions)
	}
	if got.Covers("comment") {
		t.Fatalf("child covers an action outside its derived subset: %v", got.Actions)
	}
}

func TestDeriveRefusesActionSuperset(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	_, parentTok, err := s.Mint("tracker", []string{"read"}, 8*time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, _, err := s.Derive(parentTok, []string{"read", "comment"}, time.Hour, "", "agent", now); !errors.Is(err, ErrAttenuationActions) {
		t.Fatalf("want ErrAttenuationActions, got %v", err)
	}
}

func TestDeriveRefusesTTLPastParent(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	_, parentTok, err := s.Mint("tracker", []string{"read"}, 2*time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, _, err := s.Derive(parentTok, []string{"read"}, 3*time.Hour, "", "agent", now); !errors.Is(err, ErrAttenuationTTL) {
		t.Fatalf("want ErrAttenuationTTL, got %v", err)
	}
	// Equal expiry (child ttl == parent ttl at the same mint instant) is the
	// boundary and must be allowed — attenuation is "no later", not "earlier".
	if _, _, err := s.Derive(parentTok, []string{"read"}, 2*time.Hour, "", "agent", now); err != nil {
		t.Fatalf("equal-expiry derive should be allowed: %v", err)
	}
}

func TestDeriveRefusesGrandchild(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	_, parentTok, err := s.Mint("tracker", []string{"read", "comment"}, 8*time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	_, childTok, err := s.Derive(parentTok, []string{"read"}, 4*time.Hour, "", "agent", now)
	if err != nil {
		t.Fatalf("Derive child: %v", err)
	}
	if _, _, err := s.Derive(childTok, []string{"read"}, time.Hour, "", "agent", now); !errors.Is(err, ErrChainDepth) {
		t.Fatalf("deriving from a child must refuse ErrChainDepth, got %v", err)
	}
}

func TestValidateRejectsDepthTwoChain(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	_, parentTok, err := s.Mint("tracker", []string{"read"}, 8*time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	child, _, err := s.Derive(parentTok, []string{"read"}, 4*time.Hour, "", "agent", now)
	if err != nil {
		t.Fatalf("Derive child: %v", err)
	}
	// Hand-build a grandchild the Derive path would never mint: a valid signature
	// over a record whose parent is itself derived. Validate must refuse it on
	// depth, not merely trust that Derive declined to produce it.
	id, err := newID()
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	grandchild := Grant{
		Version: Version, Domain: Domain, ID: id, Key: "tracker",
		Actions: []string{"read"}, Parent: child.ID,
		MintedAt: now().UTC(), TTL: time.Hour, MintedBy: "agent",
	}
	grandchild.Sig = sign(readMintKey(t, s), grandchild)
	if err := s.save(grandchild); err != nil {
		t.Fatalf("save grandchild: %v", err)
	}
	if _, err := s.Validate(token(grandchild), "tracker", now); !errors.Is(err, ErrChainDepth) {
		t.Fatalf("depth-2 chain: want ErrChainDepth, got %v", err)
	}
}

func TestSignCoversParentAndBoundSource(t *testing.T) {
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	cases := map[string]func(g *Grant){
		"parent":       func(g *Grant) { g.Parent = "" },
		"bound_source": func(g *Grant) { g.BoundSource = "10.0.0.9" },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			_, parentTok, err := s.Mint("tracker", []string{"read"}, 8*time.Hour, "operator", now)
			if err != nil {
				t.Fatalf("Mint: %v", err)
			}
			child, childTok, err := s.Derive(parentTok, []string{"read"}, time.Hour, "10.0.0.5", "agent", now)
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			tamper(&child)
			data, err := json.Marshal(child)
			if err != nil {
				t.Fatalf("marshal tampered: %v", err)
			}
			if err := os.WriteFile(filepath.Join(s.stateDir, "grants", child.ID+".json"), data, 0o600); err != nil {
				t.Fatalf("write tampered: %v", err)
			}
			if _, err := s.Validate(childTok, "tracker", now); !errors.Is(err, ErrBadSignature) {
				t.Fatalf("mutating %s should break the signature: got %v", name, err)
			}
		})
	}
}

func TestVersionOneTokenRefuses(t *testing.T) {
	s := newStore(t)
	now := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	root, _, err := s.Mint("tracker", []string{"read"}, 8*time.Hour, "operator", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// A cst1_ token refuses at the prefix as ErrNoGrant — the two prefixes are
	// never both accepted.
	legacy := "cst1_" + root.ID + "." + strings.Repeat("a", sha256.Size*2)
	if _, err := s.Validate(legacy, "tracker", now); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("cst1_ token: want ErrNoGrant, got %v", err)
	}
	// A hand-built cst2_ token over a genuinely version-1 record: the signature
	// verifies (we sign it), but the version/domain backstop still refuses.
	id, err := newID()
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	v1 := Grant{Version: 1, Domain: Domain, ID: id, Key: "tracker", Actions: []string{"read"}, MintedAt: now().UTC(), TTL: 8 * time.Hour, MintedBy: "operator"}
	v1.Sig = sign(readMintKey(t, s), v1)
	if err := s.save(v1); err != nil {
		t.Fatalf("save v1 record: %v", err)
	}
	if _, err := s.Validate(token(v1), "tracker", now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("v1 record under cst2_ prefix: want ErrBadSignature (scheme), got %v", err)
	}
}

// readMintKey reads the store's mint key bytes — used by tests that hand-build a
// signed record the public API would never mint.
func readMintKey(t *testing.T, s *Store) []byte {
	t.Helper()
	key, err := os.ReadFile(s.mintKeyPath)
	if err != nil {
		t.Fatalf("read mint key: %v", err)
	}
	return key
}

// flipLastHex flips the final hex character of s to a different hex digit.
func flipLastHex(s string) string {
	if s == "" {
		return s
	}
	last := s[len(s)-1]
	repl := byte('a')
	if last == 'a' {
		repl = 'b'
	}
	return s[:len(s)-1] + string(repl)
}
