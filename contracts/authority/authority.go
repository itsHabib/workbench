// Package authority is the cross-repo room-authority receipt contract: the
// wire shape one placed run's authority evidence takes when it carried at
// least one custody: secret reference — the grant chain (parent → child,
// attenuation visible in-receipt), how the child token was delivered, what
// evidence artifacts and custody log lines cover what the run did with it,
// and how the room that held it was torn down.
//
// It is a leaf. It imports nothing else in the module and carries no
// decision logic: no assembly (the Runway rooms adapter's job, TDD §4 D5),
// no hash-chaining (deferred per TDD §4 D5), no driver-state ingestion (TDD
// §10.2). This package is only the shared vocabulary, mirroring the
// contracts/execution and contracts/driverstate pattern. Share contracts,
// not call stacks.
//
// The behavioral source of truth is
// docs/features/grant-materialized-rooms/spec.md §5. The types here are the
// ergonomic view of the embedded JSON Schema; conformance_test.go keeps the
// two in lockstep.
//
// Readers are tolerant: unknown additive fields decode without error, and an
// unrecognized schema_version rejects loudly via DecodeReceipt, mirroring
// contracts/execution's checkVersion posture and error vocabulary. This
// stdlib-only package runs no runtime JSON-Schema validator — structural
// laws the schema document states (closed enums, digest shape) are future
// admission-validator input, not runtime-enforced by decode.
package authority

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownSchemaVersion is the version-gate failure: the instance declares
// a schema_version this reader does not understand. Callers branch with
// errors.Is.
var ErrUnknownSchemaVersion = errors.New("unrecognized schema_version")

// checkVersion accepts only the one version this schema ships. There is
// exactly one version today; a compatibility rule is decided if and when a
// v2 first exists, from evidence.
func checkVersion(schemaVersion string) error {
	if schemaVersion == SchemaVersion {
		return nil
	}
	return fmt.Errorf("authority: %w: got %q, this reader accepts %q", ErrUnknownSchemaVersion, schemaVersion, SchemaVersion)
}

// Receipt is one room-authority receipt: exactly one line per placed run
// that carried at least one custody: ref (TDD §5). A run's independent refs
// are entries in Grants, so a multi-secret run neither drops authority nor
// splits into multiple lines. Self-contained: a cold reader needs no
// external store to answer what authority existed, when, delivered how,
// evidenced by what, and torn down with what outcome. All timestamps are
// RFC 3339 UTC.
type Receipt struct {
	SchemaVersion string   `json:"schema_version"`
	RunID         string   `json:"run_id"`
	AllocationID  string   `json:"allocation_id"`
	Grants        []Grant  `json:"grants"`
	Evidence      Evidence `json:"evidence"`
	Teardown      Teardown `json:"teardown"`
}

// Grant is one resolved custody: ref: the parent-to-child attenuation chain
// — visible in-receipt so a cold reader needs no external grant-store
// lookup to see that the child's actions are a subset of the parent's — the
// source binding, and the delivery record.
type Grant struct {
	SecretName    string   `json:"secret_name"`
	Key           string   `json:"key"`
	ParentID      string   `json:"parent_id"`
	ParentDigest  string   `json:"parent_digest"`
	ParentActions []string `json:"parent_actions"`
	ChildID       string   `json:"child_id"`
	ChildDigest   string   `json:"child_digest"`
	Actions       []string `json:"actions"`
	BoundSource   string   `json:"bound_source"`
	MintedAt      string   `json:"minted_at"`
	Expiry        string   `json:"expiry"`
	Delivery      Delivery `json:"delivery"`
}

// Delivery records how the child token reached the guest.
type Delivery struct {
	Channel     string `json:"channel"`
	DeliveredAt string `json:"delivered_at"`
	OneShot     bool   `json:"one_shot"`
}

// Evidence joins the collected artifact digests to the custody log lines
// that cover what the run did with each grant.
type Evidence struct {
	Artifacts  []EvidenceArtifact `json:"artifacts"`
	CustodyLog []CustodyLogEntry  `json:"custody_log"`
}

// EvidenceArtifact is one digest ref into Result.Artifacts
// (contracts/execution). Type is an open vocabulary (witness_pcap,
// witness_json, changeset, ...) — additive within the major version, so
// rooms can evolve artifact naming without a schema revision.
type EvidenceArtifact struct {
	Type   string `json:"type"`
	SHA256 string `json:"sha256"`
}

// CustodyLogEntry pins what custody's interleaved JSONL log returned for one
// child grant at assembly time. The child grant id is the unambiguous
// selector over the log — unique per (run, ref) — so RequestCount and the
// digest of the selected lines make later log tampering detectable against
// the receipt.
type CustodyLogEntry struct {
	ChildID      string `json:"child_id"`
	RequestCount int    `json:"request_count"`
	LinesSHA256  string `json:"lines_sha256"`
}

// Teardown records the outcome of destroying the only environment the
// receipt's tokens were ever usable in. Status is a closed enum.
type Teardown struct {
	Status string `json:"status"`
	At     string `json:"at"`
}

// Teardown statuses — a closed enum.
const (
	TeardownDestroyed = "destroyed"
	TeardownFailed    = "failed"
	TeardownUnknown   = "unknown"
)

// DecodeReceipt is the tolerant reader for a room-authority receipt: unknown
// additive fields decode, and an unrecognized schema_version rejects loudly.
func DecodeReceipt(data []byte) (Receipt, error) {
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return Receipt{}, fmt.Errorf("authority: decode receipt: %w", err)
	}
	if err := checkVersion(r.SchemaVersion); err != nil {
		return Receipt{}, err
	}
	return r, nil
}
