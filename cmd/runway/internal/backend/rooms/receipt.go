package rooms

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/contracts/authority"
	"github.com/itsHabib/workbench/contracts/execution"
)

// authorityReceiptFile is the run-local JSONL the receipt line is written to and
// named in Result.Artifacts (grant-materialized rooms §5).
const authorityReceiptFile = "authority-receipt.jsonl"

// receiptInputs are the durable facts receipt assembly joins into one line. All
// come from the derive records (written before boot) plus the run's collected
// artifacts and teardown outcome, so the same inputs always assemble the same
// bytes (§8 idempotency).
type receiptInputs struct {
	runID        string
	allocationID string
	records      []DeriveRecord
	artifacts    []execution.Artifact
	custodyLog   []authority.CustodyLogEntry
	teardown     authority.Teardown
}

// assembleReceipt builds the room-authority receipt from durable inputs. It is
// pure and order-preserving: no maps in the wire shape, so json.Marshal of the
// result is deterministic and byte-identical across re-runs (§8).
func assembleReceipt(in receiptInputs) authority.Receipt {
	grants := make([]authority.Grant, 0, len(in.records))
	for _, r := range in.records {
		grants = append(grants, grantFromRecord(r))
	}
	return authority.Receipt{
		SchemaVersion: authority.SchemaVersion,
		RunID:         in.runID,
		AllocationID:  in.allocationID,
		Grants:        grants,
		Evidence: authority.Evidence{
			Artifacts:  evidenceArtifacts(in.artifacts),
			CustodyLog: append([]authority.CustodyLogEntry(nil), in.custodyLog...),
		},
		Teardown: in.teardown,
	}
}

// grantFromRecord maps one derive record to a receipt grant. The child token is
// deliberately dropped: the receipt records what authority existed, never the
// bearer secret itself.
func grantFromRecord(r DeriveRecord) authority.Grant {
	return authority.Grant{
		SecretName:    r.SecretName,
		Key:           r.Key,
		ParentID:      r.ParentID,
		ParentDigest:  r.ParentDigest,
		ParentActions: append([]string(nil), r.ParentActions...),
		ChildID:       r.ChildID,
		ChildDigest:   r.ChildDigest,
		Actions:       append([]string(nil), r.Actions...),
		BoundSource:   r.BoundSource,
		MintedAt:      r.MintedAt.UTC().Format(time.RFC3339),
		Expiry:        r.Expiry.UTC().Format(time.RFC3339),
		Delivery: authority.Delivery{
			Channel:     "vsock",
			DeliveredAt: r.MintedAt.UTC().Format(time.RFC3339),
			OneShot:     true,
		},
	}
}

// receiptLine renders the receipt as one JSONL line (no trailing newline). The
// same durable inputs always yield the same bytes.
func receiptLine(in receiptInputs) ([]byte, error) {
	data, err := json.Marshal(assembleReceipt(in))
	if err != nil {
		return nil, fmt.Errorf("rooms: marshal authority receipt: %w", err)
	}
	return data, nil
}

// evidenceType classifies a collected artifact into the receipt's open evidence
// vocabulary by name. An unrecognized artifact is not evidence and is skipped —
// the receipt references only the digests it can name.
func evidenceType(name string) (string, bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "pcap"):
		return "witness_pcap", true
	case strings.Contains(lower, "witness"):
		return "witness_json", true
	case strings.Contains(lower, "changeset"):
		return "changeset", true
	}
	return "", false
}

// evidenceArtifacts projects recognized Result artifacts into evidence refs,
// preserving artifact order so the line stays deterministic.
func evidenceArtifacts(arts []execution.Artifact) []authority.EvidenceArtifact {
	out := make([]authority.EvidenceArtifact, 0, len(arts))
	for _, a := range arts {
		kind, ok := evidenceType(a.Name)
		if !ok {
			continue
		}
		out = append(out, authority.EvidenceArtifact{Type: kind, SHA256: a.SHA256})
	}
	return out
}

// teardownFrom renders a teardown record from a closed-enum status and instant.
func teardownFrom(status string, at time.Time) authority.Teardown {
	return authority.Teardown{Status: status, At: at.UTC().Format(time.RFC3339)}
}

// AssembleAuthorityReceipt implements backend.AuthorityReceipter: it gathers the
// custody log lines each child produced, assembles the receipt, writes it to the
// run's artifacts dir, and returns the naming artifact for Result.Artifacts.
func (b *Backend) AssembleAuthorityReceipt(records any, in backend.AuthorityReceiptInputs) (execution.Artifact, error) {
	derived, ok := records.([]DeriveRecord)
	if !ok {
		return execution.Artifact{}, fmt.Errorf("rooms: authority receipt records are %T, want []DeriveRecord", records)
	}
	if len(derived) == 0 {
		return execution.Artifact{}, fmt.Errorf("rooms: no derive records for authority receipt")
	}
	status := authority.TeardownDestroyed
	if !in.TeardownOK {
		status = authority.TeardownFailed
	}
	line, err := receiptLine(receiptInputs{
		runID:        in.RunID,
		allocationID: in.AllocationID,
		records:      derived,
		artifacts:    in.Artifacts,
		custodyLog:   custodyLogEntries(custodyStateDir(), derived),
		teardown:     teardownFrom(status, in.TeardownAt),
	})
	if err != nil {
		return execution.Artifact{}, err
	}
	return writeReceiptArtifact(in.ArtifactsDir, line)
}

// AssembleReconcileReceipt assembles the room-authority receipt after controller
// loss (§7 F) from the derive records persisted at resolve time. teardown status
// is unknown — a red flag a reader surfaces, not silence. Returns ok=false when
// the run carried no custody refs (no records file), so reconcile skips cleanly.
func AssembleReconcileReceipt(privateDir, artifactsDir, runID, allocationID string, artifacts []execution.Artifact, at time.Time) (execution.Artifact, bool, error) {
	records, ok, err := readDeriveRecords(privateDir)
	if err != nil {
		return execution.Artifact{}, false, err
	}
	if !ok || len(records) == 0 {
		return execution.Artifact{}, false, nil
	}
	line, err := receiptLine(receiptInputs{
		runID:        runID,
		allocationID: allocationID,
		records:      records,
		artifacts:    artifacts,
		custodyLog:   custodyLogEntries(custodyStateDir(), records),
		teardown:     teardownFrom(authority.TeardownUnknown, at),
	})
	if err != nil {
		return execution.Artifact{}, false, err
	}
	art, err := writeReceiptArtifact(artifactsDir, line)
	if err != nil {
		return execution.Artifact{}, false, err
	}
	return art, true, nil
}

// writeReceiptArtifact writes the receipt line under the artifacts dir and
// returns its named artifact with the Runway-relative path convention
// ("artifacts/<file>", forward-slashed) the Result contract requires.
func writeReceiptArtifact(artifactsDir string, line []byte) (execution.Artifact, error) {
	if err := os.MkdirAll(artifactsDir, 0o700); err != nil {
		return execution.Artifact{}, fmt.Errorf("rooms: authority receipt dir: %w", err)
	}
	body := append(append([]byte(nil), line...), '\n')
	if err := os.WriteFile(filepath.Join(artifactsDir, authorityReceiptFile), body, 0o600); err != nil {
		return execution.Artifact{}, fmt.Errorf("rooms: write authority receipt: %w", err)
	}
	sum := sha256.Sum256(body)
	return execution.Artifact{
		Name:   "authority-receipt",
		Path:   filepath.ToSlash(filepath.Join("artifacts", authorityReceiptFile)),
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(body)),
	}, nil
}

// custodyStateDir is the custody state root whose log/requests.jsonl the receipt
// pins per child. It resolves the SAME location custody's CLI uses: CUSTODY_STATE
// when set, else $HOME/.custody (see cmd/custody/main.go defaultStateDir), so the
// adapter reads the same state the operator ran custody against.
func custodyStateDir() string { return defaultCustodyStateDir() }

// custodyLogEntries pins, per child grant, what custody's interleaved log holds:
// the request count and a digest of the exact lines whose grant_id is the child
// id (§5). It is best-effort — an unreadable log yields a zero-count entry so
// the receipt still assembles (§7 F), never a hard failure.
func custodyLogEntries(stateDir string, records []DeriveRecord) []authority.CustodyLogEntry {
	out := make([]authority.CustodyLogEntry, 0, len(records))
	for _, r := range records {
		count, digest := scanCustodyLog(stateDir, r.ChildID)
		out = append(out, authority.CustodyLogEntry{
			ChildID:      r.ChildID,
			RequestCount: count,
			LinesSHA256:  digest,
		})
	}
	return out
}

// emptyLinesDigest is sha256 over zero selected lines — the digest a custody_log
// entry carries when no requests matched or the log was unreadable. It is a
// valid bare 64-hex (the schema requires the field), so the entry is never
// schema-invalid, while request_count 0 says nothing was pinned.
var emptyLinesDigest = hex.EncodeToString(sha256.New().Sum(nil))

// scanCustodyLog counts and hashes the log lines whose grant_id equals childID.
// The digest is over the selected raw lines in file order (bare 64-hex, the
// shape evidence.custody_log[].lines_sha256 requires), so later tampering is
// detectable against the receipt. A missing, unreadable, or over-long-line log
// returns (0, emptyLinesDigest): a partial count+digest would be a silently
// wrong pin, so on any scan error the whole result is discarded (§7 F) — never
// partial — but the digest stays valid hex rather than an empty, schema-breaking
// string.
func scanCustodyLog(stateDir, childID string) (int, string) {
	if stateDir == "" {
		return 0, emptyLinesDigest
	}
	file, err := os.Open(filepath.Join(stateDir, "log", "requests.jsonl"))
	if err != nil {
		return 0, emptyLinesDigest
	}
	defer file.Close()
	hash := sha256.New()
	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var record struct {
			GrantID string `json:"grant_id"`
		}
		line := scanner.Bytes()
		if json.Unmarshal(line, &record) != nil || record.GrantID != childID {
			continue
		}
		count++
		hash.Write(line)
		hash.Write([]byte{'\n'})
	}
	if err := scanner.Err(); err != nil {
		return 0, emptyLinesDigest
	}
	return count, hex.EncodeToString(hash.Sum(nil))
}
