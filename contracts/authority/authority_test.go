package authority

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func decodeReceiptFixture(t *testing.T, name string) Receipt {
	t.Helper()
	r, err := DecodeReceipt(readFixture(t, name))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return r
}

func TestDecodeReceipt_Valid(t *testing.T) {
	for _, f := range []string{"receipt-single-grant.json", "receipt-multi-grant.json"} {
		if _, err := DecodeReceipt(readFixture(t, f)); err != nil {
			t.Errorf("%s: golden fixture must decode: %v", f, err)
		}
	}
}

// TestMultiSecretOneLine pins the multi-grant shape (acceptance): two
// resolved refs are two grants[] entries on one receipt, never two lines.
func TestMultiSecretOneLine(t *testing.T) {
	r := decodeReceiptFixture(t, "receipt-multi-grant.json")
	if len(r.Grants) != 2 {
		t.Fatalf("want 2 grants entries on one receipt, got %d", len(r.Grants))
	}
	if r.Grants[0].ChildID == r.Grants[1].ChildID {
		t.Fatal("distinct grants must carry distinct child grant ids")
	}
}

// TestAttenuationVisibleInReceipt pins the point of carrying parent_actions
// alongside actions: a cold reader can see the child is a subset of the
// parent with no external grant-store lookup.
func TestAttenuationVisibleInReceipt(t *testing.T) {
	g := decodeReceiptFixture(t, "receipt-single-grant.json").Grants[0]
	if len(g.Actions) >= len(g.ParentActions) && !reflect.DeepEqual(g.Actions, g.ParentActions) {
		t.Fatalf("fixture must demonstrate attenuation: child actions %v, parent actions %v", g.Actions, g.ParentActions)
	}
}

func TestDecodeReceipt_VersionGate(t *testing.T) {
	if _, err := DecodeReceipt(readFixture(t, "invalid-receipt-unknown-version.json")); !errors.Is(err, ErrUnknownSchemaVersion) {
		t.Fatal("unknown schema_version must reject via the version gate")
	}
}

// TestTolerantDecodeDefersToAdmission pins the honest layering (mirrors
// contracts/execution's TestTolerantDecodeDefersToAdmission): this
// stdlib-only leaf runs no runtime JSON-Schema validator, so an instance
// that only the schema DOCUMENT constrains — a closed-enum violation, a
// missing digest — still decodes here. Its rejection belongs to a future
// admission validator (the Runway receipt-assembly adapter, TDD §4 D5),
// never a claim this package enforces at runtime.
func TestTolerantDecodeDefersToAdmission(t *testing.T) {
	r, err := DecodeReceipt(readFixture(t, "invalid-receipt-bad-teardown-enum.json"))
	if err != nil {
		t.Fatalf("a schema-only enum violation must still decode: %v", err)
	}
	if r.Teardown.Status != "vaporized" {
		t.Errorf("the violating value must survive decode untouched, got %q", r.Teardown.Status)
	}

	missing, err := DecodeReceipt(readFixture(t, "invalid-receipt-missing-digest.json"))
	if err != nil {
		t.Fatalf("a missing digest must still decode: %v", err)
	}
	if missing.Grants[0].ParentDigest != "" {
		t.Errorf("a missing parent_digest must decode to the zero value, got %q", missing.Grants[0].ParentDigest)
	}
}

// TestUnknownFieldsIgnored is the tolerant-reader guarantee: a body carrying
// a field this binary predates still decodes, and known fields survive. The
// body is otherwise a fully valid receipt (≥1 grant, matching custody_log
// entry) — a receipt exists only for runs carrying at least one custody:
// ref, and grants[] carries minItems: 1 — so this test isolates
// unknown-field tolerance instead of relying on an otherwise-invalid body.
func TestUnknownFieldsIgnored(t *testing.T) {
	body := []byte(`{"schema_version":"authority-receipt.v1","run_id":"run_1","allocation_id":"alloc_1","grants":[{"secret_name":"CUSTODY_GRANT_TRACKER","key":"tracker","parent_id":"cst2_parent0000000000000000001","parent_digest":"sha256:a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1","parent_actions":["read"],"child_id":"cst2_child00000000000000000001","child_digest":"sha256:b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2","actions":["read"],"bound_source":"172.30.0.7","minted_at":"2026-07-22T19:00:00Z","expiry":"2026-07-22T19:42:00Z","delivery":{"channel":"vsock","delivered_at":"2026-07-22T19:00:05Z","one_shot":true}}],"evidence":{"artifacts":[{"type":"changeset","sha256":"e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5"}],"custody_log":[{"child_id":"cst2_child00000000000000000001","request_count":17,"lines_sha256":"f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6"}]},"teardown":{"status":"destroyed","at":"2026-07-22T00:00:00Z"},"future_field":{"x":1}}`)
	r, err := DecodeReceipt(body)
	if err != nil {
		t.Fatalf("tolerant reader must ignore unknown fields: %v", err)
	}
	if r.RunID != "run_1" || r.Teardown.Status != TeardownDestroyed {
		t.Fatalf("known fields did not survive an unknown-field body: %+v", r)
	}
	if len(r.Grants) != 1 || r.Grants[0].ChildID != "cst2_child00000000000000000001" {
		t.Fatalf("the otherwise-valid grant must survive an unknown-field body: %+v", r.Grants)
	}
}

// TestGoldenRoundTrip proves each valid fixture survives decode and
// re-encode losslessly: the re-encoded JSON is value-equal to the fixture
// (no field dropped or mutated) and a second decode is Go-value-equal to the
// first.
func TestGoldenRoundTrip(t *testing.T) {
	for _, f := range []string{"receipt-single-grant.json", "receipt-multi-grant.json"} {
		t.Run(f, func(t *testing.T) {
			raw := readFixture(t, f)
			first, err := DecodeReceipt(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			assertJSONEqual(t, raw, out)
			second, err := DecodeReceipt(out)
			if err != nil {
				t.Fatalf("second decode: %v", err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("round-trip mismatch:\n first=%+v\nsecond=%+v", first, second)
			}
		})
	}
}

// assertJSONEqual compares two JSON documents as decoded values, so
// formatting differs but any dropped or mutated field fails.
func assertJSONEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("parse re-encoded: %v", err)
	}
	if !reflect.DeepEqual(w, g) {
		t.Fatalf("re-encoded JSON is not value-equal to the fixture:\n want=%s\n  got=%s", want, got)
	}
}
