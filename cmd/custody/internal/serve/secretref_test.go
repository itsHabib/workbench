package serve

import (
	"testing"

	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
)

// TestSecretRefRoundTrip proves the single-source contract: a manifest secret
// field `wincred:<ref>` and the BARE ref the credstore is keyed on resolve to
// the SAME stored entry. It exercises the real seam — store under the bare ref,
// read back through manifest.SecretRef applied to the manifest field — so any
// drift between the scheme's one home (manifest) and its consumers would fail
// here rather than as a runtime 500 secret_unavailable. The Windows `custody:`
// target prefixing is a WinCred concern covered by TestWinCredRoundTrip; this
// test uses the in-memory fakeSecrets Store so it runs cross-platform.
func TestSecretRefRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		bareRef string
	}{
		{name: "simple", bareRef: "tracker-pat"},
		{name: "with dash", bareRef: "vendor-api-key"},
		{name: "with dot", bareRef: "svc.example.token"},
		{name: "dash and dot", bareRef: "svc-1.eu.west"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secretField := "wincred:" + tc.bareRef

			ref := manifest.SecretRef(secretField)
			if ref != tc.bareRef {
				t.Fatalf("SecretRef(%q) = %q, want %q", secretField, ref, tc.bareRef)
			}

			store := fakeSecrets{m: map[string]string{}}
			if err := store.Set(tc.bareRef, []byte(testSecret)); err != nil {
				t.Fatalf("Set(%q): %v", tc.bareRef, err)
			}

			got, err := store.Get(manifest.SecretRef(secretField))
			if err != nil {
				t.Fatalf("Get(SecretRef(%q)): %v", secretField, err)
			}
			if string(got) != testSecret {
				t.Fatalf("Get returned %q, want %q", got, testSecret)
			}
		})
	}
}

// TestSecretRefUnprefixed documents the helper's behavior on input without the
// scheme: it returns the string unchanged (Load guarantees the prefix for a
// validated Key, so this is only the defensive path).
func TestSecretRefUnprefixed(t *testing.T) {
	if got := manifest.SecretRef("bare-ref"); got != "bare-ref" {
		t.Fatalf("SecretRef(%q) = %q, want unchanged", "bare-ref", got)
	}
}
