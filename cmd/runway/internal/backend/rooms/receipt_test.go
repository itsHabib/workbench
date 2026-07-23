package rooms

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/contracts/authority"
	"github.com/itsHabib/workbench/contracts/execution"
)

// bareHex64 is the digest shape the receipt schema requires for
// evidence.custody_log[].lines_sha256 and evidence.artifacts[].sha256.
var bareHex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)

// durableInputs is a fixed set of at-collection facts standing in for what the
// derive records, collected artifacts, and teardown outcome durably hold.
func durableInputs(teardown authority.Teardown) receiptInputs {
	minted := time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC)
	return receiptInputs{
		runID:        "run_abc",
		allocationID: "room-7",
		records: []DeriveRecord{{
			SecretName:    "CUSTODY_GRANT_TRACKER",
			Key:           "tracker",
			ParentID:      "parentid",
			ParentDigest:  "sha256:aa",
			ParentActions: []string{"read", "comment"},
			ChildID:       "childid",
			ChildDigest:   "sha256:bb",
			ChildToken:    "cst2_childid.sig",
			Actions:       []string{"read"},
			BoundSource:   "172.30.0.7",
			MintedAt:      minted,
			Expiry:        minted.Add(42 * time.Minute),
		}},
		artifacts: []execution.Artifact{
			{Name: "witness.pcap", SHA256: strings.Repeat("c", 64)},
			{Name: "witness.json", SHA256: strings.Repeat("d", 64)},
			{Name: "changeset.diff", SHA256: strings.Repeat("e", 64)},
			{Name: "result.json", SHA256: strings.Repeat("f", 64)},
		},
		custodyLog: []authority.CustodyLogEntry{{ChildID: "childid", RequestCount: 3, LinesSHA256: strings.Repeat("a", 64)}},
		teardown:   teardown,
	}
}

func TestReceiptLineIsIdempotentFromDurableInputs(t *testing.T) {
	teardown := teardownFrom(authority.TeardownDestroyed, time.Date(2026, 7, 22, 19, 43, 0, 0, time.UTC))
	first, err := receiptLine(durableInputs(teardown))
	if err != nil {
		t.Fatal(err)
	}
	second, err := receiptLine(durableInputs(teardown))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("receipt line not byte-identical:\n%s\n%s", first, second)
	}
	if bytes.Contains(first, []byte("cst2_childid.sig")) {
		t.Fatalf("child token leaked into receipt: %s", first)
	}
}

func TestReceiptDecodesWithAttenuationAndEvidence(t *testing.T) {
	teardown := teardownFrom(authority.TeardownDestroyed, time.Date(2026, 7, 22, 19, 43, 0, 0, time.UTC))
	line, err := receiptLine(durableInputs(teardown))
	if err != nil {
		t.Fatal(err)
	}
	got, err := authority.DecodeReceipt(line)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != authority.SchemaVersion || got.RunID != "run_abc" || got.AllocationID != "room-7" {
		t.Fatalf("receipt header=%+v", got)
	}
	if len(got.Grants) != 1 {
		t.Fatalf("grants=%d want 1", len(got.Grants))
	}
	g := got.Grants[0]
	if g.ParentActions[1] != "comment" || g.Actions[0] != "read" || g.BoundSource != "172.30.0.7" {
		t.Fatalf("attenuation not visible in-receipt: %+v", g)
	}
	if g.Delivery.Channel != "vsock" || !g.Delivery.OneShot {
		t.Fatalf("delivery=%+v", g.Delivery)
	}
	// Only recognized witness/changeset artifacts become evidence refs; result.json is dropped.
	if len(got.Evidence.Artifacts) != 3 {
		t.Fatalf("evidence=%v", got.Evidence.Artifacts)
	}
	kinds := map[string]bool{}
	for _, a := range got.Evidence.Artifacts {
		kinds[a.Type] = true
	}
	for _, want := range []string{"witness_pcap", "witness_json", "changeset"} {
		if !kinds[want] {
			t.Fatalf("missing evidence type %q in %v", want, got.Evidence.Artifacts)
		}
	}
	if got.Evidence.CustodyLog[0].RequestCount != 3 {
		t.Fatalf("custody_log=%+v", got.Evidence.CustodyLog)
	}
	assertEvidenceDigestsBareHex(t, got.Evidence)
	if got.Teardown.Status != authority.TeardownDestroyed {
		t.Fatalf("teardown=%+v", got.Teardown)
	}
}

// assertEvidenceDigestsBareHex checks every evidence digest is bare 64-hex, the
// shape the schema requires (never sha256:-prefixed).
func assertEvidenceDigestsBareHex(t *testing.T, ev authority.Evidence) {
	t.Helper()
	for _, e := range ev.CustodyLog {
		if !bareHex64.MatchString(e.LinesSHA256) {
			t.Fatalf("lines_sha256 %q must be bare 64-hex", e.LinesSHA256)
		}
	}
	for _, a := range ev.Artifacts {
		if !bareHex64.MatchString(a.SHA256) {
			t.Fatalf("evidence sha256 %q must be bare 64-hex", a.SHA256)
		}
	}
}

func TestReceiptAssemblesTeardownUnknownOnReconcile(t *testing.T) {
	// §7 F: controller loss after room start still assembles a receipt from the
	// durable derive record; teardown outcome is unknown, a red flag, not silence.
	teardown := teardownFrom(authority.TeardownUnknown, time.Date(2026, 7, 22, 19, 43, 0, 0, time.UTC))
	line, err := receiptLine(durableInputs(teardown))
	if err != nil {
		t.Fatal(err)
	}
	got, err := authority.DecodeReceipt(line)
	if err != nil {
		t.Fatal(err)
	}
	if got.Teardown.Status != authority.TeardownUnknown {
		t.Fatalf("teardown=%+v want unknown", got.Teardown)
	}
	if len(got.Grants) != 1 {
		t.Fatalf("reconcile receipt must still carry the derive record: %+v", got.Grants)
	}
}

func TestAssembleAuthorityReceiptWritesNamedArtifact(t *testing.T) {
	dir := t.TempDir()
	b := New(Config{Launcher: "x", Image: "i", Model: "m"})
	records := []DeriveRecord{{
		SecretName: "CUSTODY_GRANT_TRACKER", Key: "tracker",
		ParentID: "p", ParentDigest: "sha256:aa", ParentActions: []string{"read"},
		ChildID: "c", ChildDigest: "sha256:bb", ChildToken: "cst2_c.sig",
		Actions: []string{"read"}, BoundSource: "10.0.0.2",
		MintedAt: time.Now().UTC(), Expiry: time.Now().Add(time.Hour).UTC(),
	}}
	inputs := backend.AuthorityReceiptInputs{
		RunID: "run_1", AllocationID: "room-1", ArtifactsDir: dir, TeardownOK: true,
		TeardownAt: time.Date(2026, 7, 22, 19, 43, 0, 0, time.UTC),
	}
	art, err := b.AssembleAuthorityReceipt(records, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if art.Name != "authority-receipt" || art.Path != "artifacts/authority-receipt.jsonl" {
		t.Fatalf("artifact=%+v", art)
	}
	if err := execution.ValidateResult(minimalResultWith(art)); err != nil {
		t.Fatalf("receipt artifact fails Result hygiene: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, authorityReceiptFile))
	if err != nil {
		t.Fatal(err)
	}
	// Re-collection from the same durable inputs rewrites a byte-identical line.
	art2, err := b.AssembleAuthorityReceipt(records, inputs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(dir, authorityReceiptFile))
	if err != nil {
		t.Fatal(err)
	}
	if art.SHA256 != art2.SHA256 || !bytes.Equal(first, second) {
		t.Fatalf("re-collection not byte-identical:\n%s\n%s", first, second)
	}
}

func writeCustodyLog(t *testing.T, stateDir string, lines ...string) {
	t.Helper()
	dir := filepath.Join(stateDir, "log")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "requests.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanCustodyLogFiltersByChildAndDigestIsBareHex(t *testing.T) {
	state := t.TempDir()
	writeCustodyLog(t, state,
		`{"grant_id":"child-a","verdict":"pass"}`,
		`{"grant_id":"child-b","verdict":"pass"}`,
		`{"grant_id":"child-a","verdict":"denied"}`,
		`not-json`,
	)
	count, digest := scanCustodyLog(state, "child-a")
	if count != 2 {
		t.Fatalf("count=%d want 2 (only child-a lines)", count)
	}
	if !bareHex64.MatchString(digest) {
		t.Fatalf("digest %q must be bare 64-hex (no sha256: prefix)", digest)
	}
}

func TestScanCustodyLogOversizedLineIsUnreadableNotPartial(t *testing.T) {
	state := t.TempDir()
	// A first valid line, then a line far past the 1 MiB scanner cap. A partial
	// (count=1) result would be a silently wrong pin, so the whole scan is
	// discarded: count 0, and the empty-lines digest (never a schema-breaking "").
	oversized := `{"grant_id":"child-a","blob":"` + strings.Repeat("x", 2<<20) + `"}`
	writeCustodyLog(t, state, `{"grant_id":"child-a","verdict":"pass"}`, oversized)
	count, digest := scanCustodyLog(state, "child-a")
	if count != 0 {
		t.Fatalf("oversized line must yield count 0 (not partial), got %d", count)
	}
	if digest != emptyLinesDigest || !bareHex64.MatchString(digest) {
		t.Fatalf("digest %q must be the valid empty-lines digest", digest)
	}
}

func TestScanCustodyLogMissingLogIsZeroWithValidDigest(t *testing.T) {
	count, digest := scanCustodyLog(t.TempDir(), "child-a")
	if count != 0 || digest != emptyLinesDigest {
		t.Fatalf("missing log must yield (0, emptyLinesDigest), got (%d, %q)", count, digest)
	}
	if !bareHex64.MatchString(digest) {
		t.Fatalf("digest %q must be bare 64-hex", digest)
	}
}

// minimalResultWith embeds art in an otherwise-valid succeeded result so the
// artifact shape is checked against the real contract validator.
func minimalResultWith(art execution.Artifact) execution.Result {
	zero := int64(0)
	sha := "0000000000000000000000000000000000000000000000000000000000000000"
	return execution.Result{
		SchemaVersion:    execution.SchemaVersion,
		RunID:            "run_1",
		RequestID:        "req_1",
		RequestSHA256:    sha,
		WorkSHA256:       sha,
		Status:           execution.StatusSucceeded,
		TerminalPhase:    execution.PhaseTerminal,
		ReasonCode:       execution.ReasonCompleted,
		StartedAt:        "2026-07-22T19:00:00Z",
		EndedAt:          "2026-07-22T19:01:00Z",
		WorkloadExitCode: &zero,
		Placement:        execution.PlacementReceipt{Backend: "rooms", Profile: "agent-cursor", AllocationID: "room-1", StreamDelivery: execution.StreamDeliveryNone},
		Causes:           []execution.Cause{},
		Diagnostics:      []execution.Diagnostic{},
		Artifacts:        []execution.Artifact{art},
	}
}
