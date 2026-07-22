package credstore

import (
	"bytes"
	"strings"
	"testing"
)

// recordingStore is an in-memory Store that captures the last secret written,
// so the fuzz target can assert exactly what KeysSet handed the backend.
type recordingStore struct {
	last   []byte
	stored bool
}

func (r *recordingStore) Get(string) ([]byte, error) { return nil, ErrSecretUnavailable }

func (r *recordingStore) Set(_ string, secret []byte) error {
	r.last = append([]byte(nil), secret...)
	r.stored = true
	return nil
}

// FuzzKeysSet drives the `keys set` mechanism with an arbitrary ref and secret.
// Invariants: it never panics; on success it stores exactly the size-limited
// input minus at most one trailing newline (tying KeysSet to
// trimTrailingNewline); and a non-trivial secret is NEVER echoed into an error
// message — the package's no-secret-in-errors rule.
func FuzzKeysSet(f *testing.F) {
	f.Add("ref", []byte("secret"))
	f.Add("ref", []byte("secret\n"))
	f.Add("ref", []byte("secret\r\n"))
	f.Add("", []byte("x"))
	f.Add("ref", []byte{})
	f.Add("ref", bytes.Repeat([]byte("a"), maxSecretBytes+5))
	f.Fuzz(func(t *testing.T, ref string, secret []byte) {
		store := &recordingStore{}
		err := KeysSet(store, ref, bytes.NewReader(secret))
		if err != nil {
			assertNoSecretEcho(t, err.Error(), secret)
			return
		}
		assertStoredTrimmed(t, store, secret)
	})
}

// assertNoSecretEcho fails if a distinctive (>=16 byte) secret appears verbatim
// in an error string. Short secrets are skipped: a byte or two can incidentally
// occur in a fixed error template, but a 16+ byte arbitrary blob cannot without
// having been echoed.
func assertNoSecretEcho(t *testing.T, msg string, secret []byte) {
	t.Helper()
	if len(secret) < 16 {
		return
	}
	if strings.Contains(msg, string(secret)) {
		t.Fatalf("error message echoes the secret bytes: %q", msg)
	}
}

// assertStoredTrimmed requires that a successful KeysSet stored a non-empty,
// within-limit secret equal to the size-limited read of the input with at most
// one trailing newline trimmed.
func assertStoredTrimmed(t *testing.T, store *recordingStore, secret []byte) {
	t.Helper()
	if !store.stored {
		t.Fatalf("KeysSet succeeded but stored nothing")
	}
	if len(store.last) == 0 || len(store.last) > maxSecretBytes {
		t.Fatalf("stored secret length %d out of bounds (1..%d)", len(store.last), maxSecretBytes)
	}
	limited := secret
	if len(limited) > maxSecretBytes+2 {
		limited = limited[:maxSecretBytes+2]
	}
	want := trimTrailingNewline(limited)
	if !bytes.Equal(store.last, want) {
		t.Fatalf("stored %q, want size-limited/trimmed %q", store.last, want)
	}
}
