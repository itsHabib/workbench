package local

import (
	"bytes"
	"testing"
)

func TestRedactLongestSecretFirst(t *testing.T) {
	var buf bytes.Buffer
	r := newRedactor(&buf, [][]byte{[]byte("AB"), []byte("ABCD")})
	if _, err := r.Write([]byte("ABCD")); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if got != "[REDACTED]" {
		t.Fatalf("longest-first redaction failed: got %q", got)
	}
}
