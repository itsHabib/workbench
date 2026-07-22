package grant

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// guardClock is a deterministic instant the guard tests mint/validate against
// (via the package's fixedClock factory).
var guardClock = time.Unix(1_700_000_000, 0).UTC()

// TestRequireMintKeyRefusesFreshKeyWithoutInit is the core guard: minting
// against an empty key dir without -init fails loud, names the resolved key
// path, and creates no key file — so a misdirected -mint-key-dir can never
// silently produce an orphan-signed grant.
func TestRequireMintKeyRefusesFreshKeyWithoutInit(t *testing.T) {
	root := t.TempDir()
	keyDir := filepath.Join(root, "key")
	s, err := NewStore(filepath.Join(root, "state"), keyDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	err = s.RequireMintKey(false)
	if !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("RequireMintKey(false) on empty dir = %v, want ErrKeyMissing", err)
	}
	// The guard names the RESOLVED mint-key path (NewStore resolves keyDir, e.g.
	// an 8.3 short name to its long form), so assert against the resolved path.
	resolvedKey, rerr := resolvePath(keyDir)
	if rerr != nil {
		t.Fatalf("resolvePath: %v", rerr)
	}
	wantPath := filepath.Join(resolvedKey, "mint.key")
	if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("error must name the resolved key path %q, got: %v", wantPath, err)
	}
	if _, statErr := os.Stat(filepath.Join(keyDir, "mint.key")); !os.IsNotExist(statErr) {
		t.Fatalf("RequireMintKey must not create a key file; stat err = %v", statErr)
	}
}

// TestRequireMintKeyInitBootstraps confirms the explicit -init opt-in lets a
// first-run mint proceed, and the minted token validates.
func TestRequireMintKeyInitBootstraps(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(filepath.Join(root, "state"), filepath.Join(root, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.RequireMintKey(true); err != nil {
		t.Fatalf("RequireMintKey(true) = %v, want nil", err)
	}
	_, tok, err := s.Mint("tracker", []string{"read"}, time.Hour, "test", fixedClock(guardClock))
	if err != nil {
		t.Fatalf("Mint after init: %v", err)
	}
	if _, err := s.Validate(tok, "tracker", fixedClock(guardClock)); err != nil {
		t.Fatalf("Validate after init: %v", err)
	}
}

// TestRequireMintKeyPassesWhenKeyExists confirms an already-bootstrapped key dir
// passes the guard without needing -init on every subsequent mint.
func TestRequireMintKeyPassesWhenKeyExists(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(filepath.Join(root, "state"), filepath.Join(root, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Seed a key (Mint creates it on first use).
	if _, _, err := s.Mint("tracker", []string{"read"}, time.Hour, "test", fixedClock(guardClock)); err != nil {
		t.Fatalf("seed Mint: %v", err)
	}
	if err := s.RequireMintKey(false); err != nil {
		t.Fatalf("RequireMintKey(false) with existing key = %v, want nil", err)
	}
}
