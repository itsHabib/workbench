//go:build windows

package credstore

import (
	"bytes"
	"errors"
	"testing"
)

// TestWinCredRoundTrip exercises the real Windows Credential Manager: write a
// secret, read it back, confirm a missing ref is typed, then clean up. It skips
// (rather than fails) when the store is unavailable — a headless or locked-down
// Windows environment where CredWrite cannot persist.
func TestWinCredRoundTrip(t *testing.T) {
	var s WinCred
	const ref = "custody-test-roundtrip"
	secret := []byte("integration-secret-\x00-bytes")

	if err := s.Set(ref, secret); err != nil {
		t.Skipf("credential store unavailable: %v", err)
	}
	t.Cleanup(func() { _ = credDelete(ref) })

	got, err := s.Get(ref)
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(secret))
	}

	if err := credDelete(ref); err != nil {
		t.Fatalf("cleanup delete: %v", err)
	}
	if _, err := s.Get(ref); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("Get after delete = %v, want ErrSecretUnavailable", err)
	}
}
