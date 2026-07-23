package controller_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/backend/local"
	"github.com/itsHabib/workbench/cmd/runway/internal/controller"
	"github.com/itsHabib/workbench/contracts/execution"
)

// custodyBackend is a local backend that also advertises the custody
// capabilities, so the controller's custody routing (resolve → inject → receipt)
// is exercised without a rooms host or a custody binary.
type custodyBackend struct {
	backend.Backend
	resolveErr error
	resolution backend.CustodyResolution
	receiptArt execution.Artifact
	sawRecords any
}

func (b *custodyBackend) ResolveCustody(_ context.Context, _ backend.CustodyRequest) (backend.CustodyResolution, error) {
	if b.resolveErr != nil {
		return backend.CustodyResolution{}, b.resolveErr
	}
	return b.resolution, nil
}

func (b *custodyBackend) AssembleAuthorityReceipt(records any, _ backend.AuthorityReceiptInputs) (execution.Artifact, error) {
	b.sawRecords = records
	return b.receiptArt, nil
}

func custodyWork(h *harness) execution.WorkSpec {
	h.writeProg("main.go", "package main\nfunc main() {}\n")
	work := goRunWork(h, "main.go", nil)
	work.Secrets = []execution.Secret{{Name: "CUSTODY_GRANT_TRACKER", Ref: "custody:tracker/read"}}
	return work
}

func TestCustodyRefWithoutResolverRefusesAtAdmission(t *testing.T) {
	h := newHarness(t)
	work := custodyWork(h)
	// local backend has no CustodyResolver → admission refuses (authority_unsupported).
	_, err := controller.Run(writeSpec(t, h, work), h.bundleDir, h.stateRoot, controller.Options{Backend: local.New()})
	if err == nil || !controller.IsUsage(err) {
		t.Fatalf("want usage refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "authority_unsupported") {
		t.Fatalf("error must name authority_unsupported: %v", err)
	}
}

func TestNoLiveParentRefusesWithAuthorityUnresolvedAndRemedy(t *testing.T) {
	h := newHarness(t)
	work := custodyWork(h)
	be := &custodyBackend{
		Backend: local.New(),
		resolveErr: &backend.AuthorityUnresolved{
			Ref:    "CUSTODY_GRANT_TRACKER",
			Reason: "no parent grant staged for key \"tracker\"",
			Remedy: "custody grant -key tracker -actions read -ttl 8h",
		},
	}
	out := h.runWith(work, execution.Policy{DeadlineMS: 60000, CancelGraceMS: 1000}, controller.Options{Backend: be})
	if out.Result.Status != execution.StatusFailed || out.Result.ReasonCode != execution.ReasonAuthorityUnresolved {
		t.Fatalf("want failed/authority_unresolved, got %s/%s", out.Result.Status, out.Result.ReasonCode)
	}
	if out.Result.TerminalPhase != execution.PhaseStartup {
		t.Fatalf("terminal_phase=%s", out.Result.TerminalPhase)
	}
	if len(out.Result.Diagnostics) == 0 || out.Result.Diagnostics[0].Details["remedy"] != "custody grant -key tracker -actions read -ttl 8h" {
		t.Fatalf("remedy missing from diagnostics: %+v", out.Result.Diagnostics)
	}
}

func TestCustodyHappyPathNamesReceiptInArtifacts(t *testing.T) {
	h := newHarness(t)
	work := custodyWork(h)
	sha := strings.Repeat("a", 64)
	be := &custodyBackend{
		Backend: local.New(),
		resolution: backend.CustodyResolution{
			Env:     map[string]string{"CUSTODY_GRANT_TRACKER": "cst2_child.sig", "CUSTODY_BASE_TRACKER": "http://gw:8127/tracker"},
			Redact:  [][]byte{[]byte("cst2_child.sig")},
			Records: "records-handle",
		},
		receiptArt: execution.Artifact{
			Name: "authority-receipt", Path: "artifacts/authority-receipt.jsonl", SHA256: sha, Size: 1,
		},
	}
	out := h.runWith(work, execution.Policy{DeadlineMS: 60000, CancelGraceMS: 1000}, controller.Options{Backend: be})
	if out.Result.Status != execution.StatusSucceeded {
		t.Fatalf("status=%s reason=%s", out.Result.Status, out.Result.ReasonCode)
	}
	if be.sawRecords != "records-handle" {
		t.Fatalf("receipt assembly did not receive the derive records: %v", be.sawRecords)
	}
	if !hasArtifact(out.Result.Artifacts, "authority-receipt") {
		t.Fatalf("authority receipt not named in artifacts: %+v", out.Result.Artifacts)
	}
}

func writeSpec(t *testing.T, h *harness, work execution.WorkSpec) string {
	t.Helper()
	workBytes, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.bundleDir, "work.json"), workBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_test",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex(workBytes)},
		Placement:     execution.Placement{Backend: "local", Profile: "default"},
		Policy:        execution.Policy{DeadlineMS: 60000, CancelGraceMS: 1000},
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(h.dir, "request.json")
	if err := os.WriteFile(spec, reqBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return spec
}

func hasArtifact(arts []execution.Artifact, name string) bool {
	for _, a := range arts {
		if a.Name == name {
			return true
		}
	}
	return false
}
