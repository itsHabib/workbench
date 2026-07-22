// Package credstore is custody's secret backend: the OS credential store that
// holds the real vendor credentials the proxy injects. It is a mechanism layer
// — dumb persistence behind a two-method Store interface (spec §4 D7) — so a
// future keychain/secret-service backend is a new file, not a refactor.
//
// Secret bytes are never logged, echoed, or placed in an error message anywhere
// in this package. An absent secret surfaces as a typed ErrSecretUnavailable so
// callers render the operator remedy instead of leaking store internals.
package credstore

import (
	"errors"
	"fmt"
	"io"
)

// Store reads and writes a named secret. Get and Set both take the BARE ref —
// the credstore ref with no manifest `wincred:` scheme (callers translate a
// manifest secret field through manifest.SecretRef first). Any target
// namespacing a backend needs (e.g. the WinCred `custody:` target prefix) is
// that backend's private concern and never leaks into this ref. Get returns
// ErrSecretUnavailable when the reference has no entry. Implementations must
// never log or echo the secret bytes. The interface is intentionally two
// methods (spec §4 D7): the proxy needs Get, the `keys set` verb needs Set, and
// nothing else.
type Store interface {
	Get(ref string) ([]byte, error)
	Set(ref string, secret []byte) error
}

// ErrSecretUnavailable is returned by Get when no secret exists for the ref. It
// is a typed sentinel so the proxy can map it to a `500 secret_unavailable`
// with the `custody keys set` remedy (spec §6, flow F).
var (
	ErrSecretUnavailable = errors.New("secret_unavailable")
	// ErrSecretTooLarge reports a secret that exceeds the backend's hard limit.
	ErrSecretTooLarge = errors.New("secret_too_large")
)

// maxSecretBytes is Windows Credential Manager's CRED_MAX_CREDENTIAL_BLOB_SIZE.
const maxSecretBytes = 5 * 512

// KeysSet reads a secret from r (the CLI passes os.Stdin) and writes it under
// ref via s — the mechanism behind the `custody keys set` verb (spec §6). A
// single trailing newline is trimmed so a piped `echo secret` does not store
// the newline. The secret bytes are never logged or returned; on failure only
// the ref and the underlying store error surface.
func KeysSet(s Store, ref string, r io.Reader) error {
	if ref == "" {
		return errors.New("credstore: empty secret ref")
	}
	secret, err := io.ReadAll(io.LimitReader(r, maxSecretBytes+2))
	if err != nil {
		return fmt.Errorf("credstore: read secret for %q: %w", ref, err)
	}
	secret = trimTrailingNewline(secret)
	if len(secret) > maxSecretBytes {
		return fmt.Errorf("%w: secret for %q exceeds %d-byte limit", ErrSecretTooLarge, ref, maxSecretBytes)
	}
	if len(secret) == 0 {
		return fmt.Errorf("credstore: empty secret for %q", ref)
	}
	if err := s.Set(ref, secret); err != nil {
		return fmt.Errorf("credstore: store secret for %q: %w", ref, err)
	}
	return nil
}

// trimTrailingNewline drops one trailing "\r\n" or "\n" so a secret piped with
// a shell newline stores clean, without touching interior or leading bytes.
func trimTrailingNewline(b []byte) []byte {
	if len(b) >= 2 && b[len(b)-2] == '\r' && b[len(b)-1] == '\n' {
		return b[:len(b)-2]
	}
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}
