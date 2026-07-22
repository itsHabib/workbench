package grant

import (
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"
)

// assertIDStaysInGrantsDir requires that a parsed token id resolves to a path
// strictly inside <state>/grants — a crafted token must never steer the record
// read to a sibling or parent directory.
func assertIDStaysInGrantsDir(t *testing.T, id string) {
	t.Helper()
	base := filepath.Join("state", "grants")
	full := filepath.Clean(filepath.Join(base, id+".json"))
	rel, err := filepath.Rel(base, full)
	if err != nil {
		t.Fatalf("token id %q produced an unrelatable path: %v", id, err)
	}
	if rel != id+".json" {
		t.Fatalf("token id %q escapes grants dir: cleaned rel %q", id, rel)
	}
}

// FuzzParseToken drives the grant token parser with arbitrary input. It must
// never panic, and it must accept ONLY a well-formed cst1_<32-hex>.<64-hex>
// token — anything it accepts is provably safe to use as a record path, so no
// input escapes the grants directory.
func FuzzParseToken(f *testing.F) {
	valid := tokenPrefix + strings.Repeat("a", 32) + "." + strings.Repeat("b", sha256.Size*2)
	seeds := []string{
		"", tokenPrefix, valid,
		tokenPrefix + "x.y",
		tokenPrefix + strings.Repeat("a", 32) + ".",
		tokenPrefix + "../../../../etc/passwd." + strings.Repeat("b", sha256.Size*2),
		tokenPrefix + strings.Repeat("A", 32) + "." + strings.Repeat("B", sha256.Size*2),
		"cst2_" + strings.Repeat("a", 32) + "." + strings.Repeat("b", sha256.Size*2),
		strings.Repeat("a", 32) + "." + strings.Repeat("b", sha256.Size*2),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tok string) {
		id, sig, err := parseToken(tok)
		if err != nil {
			return
		}
		if !strings.HasPrefix(tok, tokenPrefix) {
			t.Fatalf("accepted token without %q prefix: %q", tokenPrefix, tok)
		}
		if len(id) != 32 || !isHex(id) {
			t.Fatalf("accepted non-hex/wrong-length id %q from token %q", id, tok)
		}
		if len(sig) != sha256.Size*2 || !isHex(sig) {
			t.Fatalf("accepted non-hex/wrong-length sig %q from token %q", sig, tok)
		}
		assertIDStaysInGrantsDir(t, id)
	})
}

// FuzzKeyDirTrustDomain drives NewStore's path resolution with arbitrary key-dir
// spellings — dot-segments, "..", symlink-like and UNC-flavored fragments. The
// invariant: if NewStore accepts a configuration, the resolved mint-key
// directory is NEVER inside the resolved state directory. Co-locating the
// signing key with the grants it signs would let anything that can read state
// forge broader grants, so the resolution must fail closed on every spelling.
func FuzzKeyDirTrustDomain(f *testing.F) {
	seeds := []string{
		"key", "..", "../state", "state", "state/keys",
		"key/../../state/keys", "./key/./../state", `\\?\C:\keys`,
		"state/../state/keys", "key\x00null",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, rawKey string) {
		root := t.TempDir()
		state := filepath.Join(root, "state")
		keyDir := filepath.Join(root, filepath.FromSlash(rawKey))
		s, err := NewStore(state, keyDir)
		if err != nil {
			return // refused configurations carry no trust-domain claim
		}
		resolvedState, err := resolvePath(state)
		if err != nil {
			t.Fatalf("resolve state dir: %v", err)
		}
		within, err := dirWithin(filepath.Dir(s.mintKeyPath), resolvedState)
		if err != nil {
			t.Fatalf("dirWithin: %v", err)
		}
		if within {
			t.Fatalf("NewStore accepted mint-key dir inside state: keyPath=%q state=%q (input %q)", s.mintKeyPath, resolvedState, rawKey)
		}
	})
}
