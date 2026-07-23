package controller_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
	"github.com/itsHabib/workbench/cmd/runway/internal/controller"
	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/authority"
	"github.com/itsHabib/workbench/contracts/execution"
)

// TestReconcileAssemblesAuthorityReceiptFromPersistedRecords proves §7 F end to
// end at the controller seam: a rooms run persisted its derive records before the
// controller died; reconcile (in-memory records gone) reads them back from
// PrivateDir and names a teardown: unknown authority receipt in the result.
func TestReconcileAssemblesAuthorityReceiptFromPersistedRecords(t *testing.T) {
	t.Setenv("CUSTODY_STATE", t.TempDir()) // isolate custody-log reads
	h := newHarness(t)
	runID := "run_" + sha256Hex([]byte(t.Name()))[:16]
	run, err := state.Create(h.stateRoot, runID)
	if err != nil {
		t.Fatal(err)
	}
	seedRoomsDeadController(t, run, runID)

	// The resolver would have written this before the crash (token-free copy).
	records := fmt.Sprintf(`[{"secret_name":"CUSTODY_GRANT_TRACKER","key":"tracker",`+
		`"parent_id":"parentid","parent_digest":"sha256:%s","parent_actions":["read","comment"],`+
		`"child_id":"childid","child_digest":"sha256:%s","actions":["read"],`+
		`"bound_source":"172.30.0.7","minted_at":"2026-07-22T19:00:00Z","expiry":"2026-07-22T19:42:00Z"}]`,
		strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err := os.WriteFile(filepath.Join(run.PrivateDir(), "authority-records.json"), []byte(records), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := controller.Reconcile(h.stateRoot, runID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result == nil || out.Result.ReasonCode != execution.ReasonControllerLost {
		t.Fatalf("want controller_lost, got %+v", out.Result)
	}
	if err := execution.ValidateResult(*out.Result); err != nil {
		t.Fatalf("reconcile result invalid: %v", err)
	}
	if !hasArtifact(out.Result.Artifacts, "authority-receipt") {
		t.Fatalf("authority receipt not named after reconcile: %+v", out.Result.Artifacts)
	}
	body, err := os.ReadFile(filepath.Join(run.ArtifactsDir(), "authority-receipt.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := authority.DecodeReceipt(bytes.TrimSpace(body))
	if err != nil {
		t.Fatal(err)
	}
	if got.Teardown.Status != authority.TeardownUnknown {
		t.Fatalf("teardown=%+v want unknown", got.Teardown)
	}
	if len(got.Grants) != 1 || got.Grants[0].ChildID != "childid" {
		t.Fatalf("grants=%+v", got.Grants)
	}
}

func seedRoomsDeadController(t *testing.T, run state.RunDir, runID string) {
	t.Helper()
	req := execution.Request{
		SchemaVersion: execution.SchemaVersion,
		RequestID:     "req_dead",
		Work:          execution.Work{Manifest: "work.json", SHA256: sha256Hex([]byte(`{}`))},
		Placement:     execution.Placement{Backend: "rooms", Profile: "agent-cursor"},
		Policy:        execution.Policy{DeadlineMS: 1000, CancelGraceMS: 0},
	}
	reqBytes, _ := json.Marshal(req)
	mustWrite(t, run.RequestPath(), reqBytes)
	mustWrite(t, run.WorkPath(), []byte(`{}`))
	mustWrite(t, run.ControllerPath(), []byte(`{"pid":999999,"start_ticks":1}`))
	writeClaim(t, run, claim.Owner{PID: 999999, StartTicks: 1, Generation: 1})
	j, err := journal.Create(run.EventsPath(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(execution.PhaseAdmission, execution.KindRunAccepted, map[string]any{"request_id": "req_dead"}); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
