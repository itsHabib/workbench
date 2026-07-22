package credstore

import (
	"errors"
	"strings"
	"testing"
)

// fakeStore is an in-memory Store for the platform-neutral logic tests. It
// records the last-written secret so tests can assert what KeysSet stored
// without touching the OS credential store.
type fakeStore struct {
	secrets map[string][]byte
}

func newFake() *fakeStore { return &fakeStore{secrets: map[string][]byte{}} }

func (f *fakeStore) Get(ref string) ([]byte, error) {
	s, ok := f.secrets[ref]
	if !ok {
		return nil, ErrSecretUnavailable
	}
	return s, nil
}

func (f *fakeStore) Set(ref string, secret []byte) error {
	// Copy like a real backend hands bytes to the OS store — KeysSet best-effort
	// wipes the caller's buffer after Set returns, so recording the slice by
	// reference would observe the scrub, not the secret.
	cp := make([]byte, len(secret))
	copy(cp, secret)
	f.secrets[ref] = cp
	return nil
}

func TestKeysSetTrimsTrailingNewline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lf", "s3cr3t\n", "s3cr3t"},
		{"crlf", "s3cr3t\r\n", "s3cr3t"},
		{"none", "s3cr3t", "s3cr3t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			if err := KeysSet(f, "ref", strings.NewReader(tc.in)); err != nil {
				t.Fatalf("KeysSet: %v", err)
			}
			got := string(f.secrets["ref"])
			if got != tc.want {
				t.Fatalf("stored %q, want %q", got, tc.want)
			}
		})
	}
}

// TestKeysSetRejectsControlChars covers the header/request-splitting screen: a
// secret carrying an interior control byte (CR, LF, NUL, or any other) is
// refused with ErrSecretControlChar and never stored. custody serve substitutes
// the stored secret into an injected `Authorization: Bearer <secret>` header, so
// these bytes are an injection vector. Failure messages must not print the
// secret bytes.
func TestKeysSetRejectsControlChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"embedded-cr", "abc\rdef"},
		{"embedded-lf", "abc\ndef"},
		{"embedded-nul", "abc\x00def"},
		{"bare-trailing-cr", "s3cr3t\r"},
		{"interior-lf-after-trim", "line1\nline2\n"},
		{"embedded-del", "abc\x7fdef"},
		{"embedded-tab", "abc\tdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			err := KeysSet(f, "ref", strings.NewReader(tc.in))
			if !errors.Is(err, ErrSecretControlChar) {
				t.Fatalf("error = %v, want ErrSecretControlChar", err)
			}
			if _, ok := f.secrets["ref"]; ok {
				t.Fatal("secret with control byte must not be stored")
			}
			// The error must name only the ref, never echo the secret bytes.
			if strings.Contains(err.Error(), tc.in) {
				t.Fatal("error leaked the secret bytes")
			}
		})
	}
}

// TestKeysSetAcceptsCleanSecret confirms the screen does not reject a legitimate
// secret, including one arriving with a single trailing newline (trimmed before
// the control-char check runs).
func TestKeysSetAcceptsCleanSecret(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "sk-live-abc123XYZ", "sk-live-abc123XYZ"},
		{"trailing-lf", "sk-live-abc123XYZ\n", "sk-live-abc123XYZ"},
		{"printable-punct", "p@ss:w0rd/=+-_.~", "p@ss:w0rd/=+-_.~"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			if err := KeysSet(f, "ref", strings.NewReader(tc.in)); err != nil {
				t.Fatalf("KeysSet rejected a clean secret: %v", err)
			}
			if got := string(f.secrets["ref"]); got != tc.want {
				t.Fatalf("stored wrong bytes (len %d, want len %d)", len(got), len(tc.want))
			}
		})
	}
}

func TestKeysSetRejectsEmpty(t *testing.T) {
	f := newFake()
	err := KeysSet(f, "ref", strings.NewReader("\n"))
	if err == nil {
		t.Fatal("expected error for empty secret")
	}
	if _, ok := f.secrets["ref"]; ok {
		t.Fatal("empty secret must not be stored")
	}
}

func TestKeysSetRejectsEmptyRef(t *testing.T) {
	f := newFake()
	if err := KeysSet(f, "", strings.NewReader("x")); err == nil {
		t.Fatal("expected error for empty ref")
	}
}

func TestKeysSetRejectsOversizedSecret(t *testing.T) {
	f := newFake()
	secret := strings.Repeat("x", maxSecretBytes+1)
	err := KeysSet(f, "ref", strings.NewReader(secret))
	if err == nil {
		t.Fatal("expected oversized secret rejection")
	}
	if !errors.Is(err, ErrSecretTooLarge) {
		t.Fatalf("error = %v, want ErrSecretTooLarge", err)
	}
	if _, ok := f.secrets["ref"]; ok {
		t.Fatal("oversized secret must not be stored")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("error leaked oversized secret")
	}
}

func TestKeysSetAcceptsMaximumSecretWithTrailingNewline(t *testing.T) {
	secret := strings.Repeat("x", maxSecretBytes)
	for _, newline := range []string{"\n", "\r\n"} {
		f := newFake()
		if err := KeysSet(f, "ref", strings.NewReader(secret+newline)); err != nil {
			t.Fatalf("KeysSet with %q terminator: %v", newline, err)
		}
		if got := string(f.secrets["ref"]); got != secret {
			t.Fatalf("stored %d bytes, want %d", len(got), len(secret))
		}
	}
}

// TestKeysSetNeverEchoesSecret guards the never-log-the-secret invariant at the
// one seam that returns an error carrying caller-supplied context: the error
// text must not contain the secret bytes.
func TestKeysSetNeverEchoesSecret(t *testing.T) {
	secret := "TOP-SECRET-VALUE"
	err := KeysSet(errStore{}, "ref", strings.NewReader(secret))
	if err == nil {
		t.Fatal("expected error from failing store")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked the secret: %v", err)
	}
}

// errStore always fails Set, without echoing the secret.
type errStore struct{}

func (errStore) Get(string) ([]byte, error) { return nil, ErrSecretUnavailable }
func (errStore) Set(string, []byte) error   { return errors.New("store offline") }

func TestGetMissingIsTyped(t *testing.T) {
	f := newFake()
	_, err := f.Get("absent")
	if !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("error = %v, want ErrSecretUnavailable", err)
	}
}
