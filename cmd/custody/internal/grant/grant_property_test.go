package grant

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// These properties pin the invariants a table-driven example test cannot reach:
// the signing pre-image is INJECTIVE over the ambiguous-encoding class a
// length-prefix defends, Mint->Validate round-trips across arbitrary
// keys/actions/TTLs and clocks with a sharp expiry boundary, and every signed
// field actually enters the pre-image (flipping any one breaks the signature).

// drawDelimHeavy draws a short string biased toward the field-delimiter
// metacharacters ("|", ":", ",", and the unit separator) that a naive
// concatenation would let bleed across field boundaries. Feeding these into
// every string field is what stresses the length-prefix encoding.
func drawDelimHeavy(t *rapid.T, label string) string {
	return rapid.StringOfN(rapid.RuneFrom([]rune("ab|:,\x1f")), 0, 5, -1).Draw(t, label)
}

// drawSignableGrant draws an arbitrary grant for signature-level properties. The
// fields need not form a valid token or record — sign() covers any Grant value,
// and the adversarial cases live precisely in the malformed ones.
func drawSignableGrant(t *rapid.T) Grant {
	actionGen := rapid.StringOfN(rapid.RuneFrom([]rune("ab|:,\x1f")), 0, 4, -1)
	return Grant{
		Version:  rapid.IntRange(0, 3).Draw(t, "version"),
		Domain:   rapid.SampledFrom([]string{Domain, "gate", ""}).Draw(t, "domain"),
		ID:       drawDelimHeavy(t, "id"),
		Key:      drawDelimHeavy(t, "key"),
		Actions:  rapid.SliceOfN(actionGen, 0, 4).Draw(t, "actions"),
		MintedAt: time.Unix(0, rapid.Int64Range(0, 4_000_000_000_000_000_000).Draw(t, "mintedAt")).UTC(),
		TTL:      time.Duration(rapid.Int64Range(-1_000_000_000_000, 1_000_000_000_000).Draw(t, "ttl")),
		MintedBy: drawDelimHeavy(t, "mintedBy"),
	}
}

// signPreimage reconstructs, byte for byte, the pre-image sign() feeds to HMAC —
// reusing the production writeSignField so the two can never drift. Comparing
// pre-images directly tests the ENCODING's injectivity, independent of any HMAC
// collision-resistance assumption.
func signPreimage(g Grant) string {
	var b strings.Builder
	fmt.Fprint(&b, g.Version)
	writeSignField(&b, g.Domain)
	writeSignField(&b, g.ID)
	writeSignField(&b, g.Key)
	writeSignField(&b, g.MintedAt.Format(time.RFC3339Nano))
	writeSignField(&b, g.TTL.String())
	writeSignField(&b, g.MintedBy)
	fmt.Fprint(&b, "|", len(g.Actions))
	for _, a := range g.Actions {
		writeSignField(&b, a)
	}
	return b.String()
}

// sameSignedFields reports whether two grants are identical in every field the
// signature covers, using the exact canonical forms sign() reads (RFC3339Nano
// for the timestamp, Duration.String for the TTL) so it never disagrees with
// the encoding about what counts as "the same".
func sameSignedFields(a, b Grant) bool {
	if a.Version != b.Version || a.Domain != b.Domain || a.ID != b.ID {
		return false
	}
	if a.Key != b.Key || a.MintedBy != b.MintedBy {
		return false
	}
	if a.MintedAt.Format(time.RFC3339Nano) != b.MintedAt.Format(time.RFC3339Nano) {
		return false
	}
	if a.TTL.String() != b.TTL.String() {
		return false
	}
	return equalStrings(a.Actions, b.Actions)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSignPreimageInjectivity: any two grants that differ in a signed field
// produce distinct signing pre-images. This is the ambiguous-encoding guard —
// without the length prefixes, (Key="a", MintedBy="b") and (Key="ab",
// MintedBy="") would collide, letting one grant's signature authorize another.
func TestSignPreimageInjectivity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		g1 := drawSignableGrant(t)
		g2 := drawSignableGrant(t)
		if sameSignedFields(g1, g2) {
			return // equal signed fields must share a pre-image; nothing to disprove
		}
		if signPreimage(g1) == signPreimage(g2) {
			t.Fatalf("distinct grants share a signing pre-image (ambiguous encoding):\n g1=%#v\n g2=%#v", g1, g2)
		}
	})
}

// tamperField returns g with exactly one signed field changed to a value
// guaranteed to differ from the original, so the round-trip needs no filtering.
func tamperField(g Grant, field string) Grant {
	switch field {
	case "key":
		g.Key += "\x00"
	case "actions":
		g.Actions = append(append([]string(nil), g.Actions...), "\x00sentinel")
	case "ttl":
		g.TTL += time.Second
	case "mintedAt":
		g.MintedAt = g.MintedAt.Add(time.Nanosecond)
	case "version":
		g.Version++
	case "domain":
		g.Domain += "\x00"
	}
	return g
}

// TestTamperFlipsSignedField: flipping any one signed field
// (key/actions/ttl/mintedAt/version/domain) changes the HMAC signature. This
// pins that each field genuinely enters the pre-image — a field left out would
// be silently forgeable.
func TestTamperFlipsSignedField(t *testing.T) {
	fields := []string{"key", "actions", "ttl", "mintedAt", "version", "domain"}
	rapid.Check(t, func(t *rapid.T) {
		key := []byte(rapid.StringN(1, 40, -1).Draw(t, "key"))
		g := drawSignableGrant(t)
		orig := sign(key, g)
		field := rapid.SampledFrom(fields).Draw(t, "field")
		if sign(key, tamperField(g, field)) == orig {
			t.Fatalf("flipping signed field %q did not change the signature: %#v", field, g)
		}
	})
}

// assertValidBeforeExpiry validates the token at a random instant strictly
// inside [MintedAt, MintedAt+TTL) and requires acceptance plus a faithful
// round-trip of the grant identity.
func assertValidBeforeExpiry(t *rapid.T, s *Store, g Grant, tok, key string, mintedAt time.Time, ttlNs int64) {
	offset := rapid.Int64Range(0, ttlNs-1).Draw(t, "validOffset")
	at := mintedAt.Add(time.Duration(offset))
	got, err := s.Validate(tok, key, func() time.Time { return at })
	if err != nil {
		t.Fatalf("Validate before expiry (offset %d of %d): %v", offset, ttlNs, err)
	}
	if got.ID != g.ID {
		t.Fatalf("round-trip id mismatch: got %q want %q", got.ID, g.ID)
	}
	if !equalStrings(got.Actions, g.Actions) {
		t.Fatalf("round-trip action-set mismatch: got %v want %v", got.Actions, g.Actions)
	}
}

// assertRefusedAtOrAfterExpiry requires refusal with ErrExpired at any instant
// at or after MintedAt+TTL — the boundary itself is already expired.
func assertRefusedAtOrAfterExpiry(t *rapid.T, s *Store, tok, key string, mintedAt time.Time, ttlNs int64) {
	extra := rapid.Int64Range(0, 1_000_000_000_000).Draw(t, "expiredOffset")
	at := mintedAt.Add(time.Duration(ttlNs)).Add(time.Duration(extra))
	if _, err := s.Validate(tok, key, func() time.Time { return at }); !errors.Is(err, ErrExpired) {
		t.Fatalf("Validate at/after expiry (extra %d past ttl %d): want ErrExpired, got %v", extra, ttlNs, err)
	}
}

// TestMintValidateRoundTripProperty: any grant minted over arbitrary
// key/actions/TTL validates strictly before MintedAt+TTL and is refused
// ErrExpired at or after it, for any clock. One store is reused across
// iterations — each Mint writes a fresh, uniquely-identified record.
func TestMintValidateRoundTripProperty(t *testing.T) {
	s := newStore(t)
	rapid.Check(t, func(t *rapid.T) {
		key := rapid.StringMatching(`[a-z][a-z0-9]{0,11}`).Draw(t, "key")
		actions := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,8}`), 1, 4).Draw(t, "actions")
		ttlNs := rapid.Int64Range(1, 1_000_000_000_000).Draw(t, "ttlNs")
		mintedAt := time.Unix(
			rapid.Int64Range(0, 4_000_000_000).Draw(t, "mintUnix"),
			rapid.Int64Range(0, 999_999_999).Draw(t, "mintNanos"),
		).UTC()
		g, tok, err := s.Mint(key, actions, time.Duration(ttlNs), "operator", func() time.Time { return mintedAt })
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		assertValidBeforeExpiry(t, s, g, tok, key, mintedAt, ttlNs)
		assertRefusedAtOrAfterExpiry(t, s, tok, key, mintedAt, ttlNs)
	})
}
