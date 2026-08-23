package org

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// DigestPrefix names the hash these digests are taken with. It is carried in
// the digest string itself so a reader never has to infer the algorithm from
// the length, and so a future algorithm can coexist with this one in a chain
// that spans the change.
const DigestPrefix = "sha256"

// Digest returns the canonical digest of v: the scheme version and the
// canonical bytes, hashed together.
//
// The scheme is *inside* the hash, not merely recorded beside it. If it sat
// beside the digest, a reader that ignored the field would silently accept a
// digest produced under different rules; hashing it in makes a scheme mismatch
// present as a mismatched digest, which every reader already handles. The cost
// is that bumping the scheme changes every digest, which is the honest
// consequence of changing how bytes are produced.
func Digest(v Value) (string, error) {
	b, err := Encode(v)
	if err != nil {
		return "", err
	}
	return DigestBytes(b), nil
}

// DigestBytes hashes already-canonical bytes under the current scheme. Prefer
// Digest; this exists for a caller that has the bytes and wants to avoid
// re-encoding, and for the tests that pin the byte-to-digest mapping.
func DigestBytes(canonical []byte) string {
	h := sha256.New()
	h.Write([]byte(Scheme))
	h.Write([]byte{0})
	h.Write(canonical)
	return fmt.Sprintf("%s:%s", DigestPrefix, hex.EncodeToString(h.Sum(nil)))
}

// DigestOf is the shorthand a record type's own code uses.
func DigestOf(c Canonical) (string, error) {
	return Digest(c.Canonical())
}
