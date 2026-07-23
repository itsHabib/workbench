package rooms

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/contracts/authority"
	"github.com/itsHabib/workbench/contracts/execution"
)

// TestResolveCustodyPersistsTokenFreeRecordsAndReconcileAssembles proves the §7 F
// path: ResolveCustody writes a token-free durable copy of the derive records,
// and after the in-memory copy is gone (controller death) AssembleReconcileReceipt
// reads them back and assembles the receipt with teardown: unknown.
func TestResolveCustodyPersistsTokenFreeRecordsAndReconcileAssembles(t *testing.T) {
	t.Setenv("CUSTODY_STATE", t.TempDir()) // isolate custody-log reads from ~/.custody
	now := time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC)
	private := t.TempDir()
	artifacts := t.TempDir()
	port := &fakePort{
		parent: parentGrant{
			id: "parentid", digest: "sha256:" + strings.Repeat("a", 64),
			token: "cst2_parentid.sig", actions: []string{"read", "comment"},
			expiry: now.Add(7 * 24 * time.Hour),
		},
		child: childGrant{
			id: "childid", digest: "sha256:" + strings.Repeat("b", 64),
			token: "cst2_childid.SECRET", actions: []string{"read"},
			boundSource: "172.30.0.7", mintedAt: now, expiry: now.Add(42 * time.Minute),
		},
	}
	b := New(Config{Launcher: "x", Image: "i", Model: "m", TapGateway: "http://gw:8127"})
	b.port = port

	res, err := b.ResolveCustody(context.Background(), backend.CustodyRequest{
		Secrets:    []execution.Secret{{Name: "CUSTODY_GRANT_TRACKER", Ref: "custody:tracker/read"}},
		Deadline:   now.Add(40 * time.Minute),
		Grace:      time.Minute,
		Now:        now,
		PrivateDir: private,
	})
	if err != nil {
		t.Fatal(err)
	}
	// In-memory the token is present (injected into the room); on disk it must not be.
	if res.Env["CUSTODY_GRANT_TRACKER"] != "cst2_childid.SECRET" {
		t.Fatalf("in-memory env missing child token: %v", res.Env)
	}
	data, err := os.ReadFile(filepath.Join(private, authorityRecordsFile))
	if err != nil {
		t.Fatalf("derive records not persisted: %v", err)
	}
	if bytes.Contains(data, []byte("cst2_childid.SECRET")) {
		t.Fatalf("child token was persisted to disk: %s", data)
	}

	// Controller death: no in-memory records. Reconcile reads them from disk.
	art, ok, err := AssembleReconcileReceipt(private, artifacts, "run_dead", "room-9", nil, now.Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("reconcile assemble: ok=%v err=%v", ok, err)
	}
	if art.Name != "authority-receipt" || art.Path != "artifacts/authority-receipt.jsonl" {
		t.Fatalf("artifact=%+v", art)
	}
	body, err := os.ReadFile(filepath.Join(artifacts, authorityReceiptFile))
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
	if got.Grants[0].ParentActions[1] != "comment" || got.Grants[0].Actions[0] != "read" {
		t.Fatalf("attenuation not preserved through persistence: %+v", got.Grants[0])
	}
	if !bareHex64.MatchString(got.Evidence.CustodyLog[0].LinesSHA256) {
		t.Fatalf("custody_log digest must be valid hex even with zero requests: %q", got.Evidence.CustodyLog[0].LinesSHA256)
	}
}

func TestAssembleReconcileReceiptNoRecordsSkips(t *testing.T) {
	art, ok, err := AssembleReconcileReceipt(t.TempDir(), t.TempDir(), "run", "room", nil, time.Now())
	if err != nil || ok {
		t.Fatalf("no records must skip cleanly: ok=%v err=%v", ok, err)
	}
	if art.Name != "" {
		t.Fatalf("art=%+v", art)
	}
}

func TestRequireCustodyGatewayRefusesWhenUnset(t *testing.T) {
	work := execution.WorkSpec{Secrets: []execution.Secret{{Name: "CUSTODY_GRANT_TRACKER", Ref: "custody:tracker/read"}}}

	unset := New(Config{Launcher: "x", Image: "i", Model: "m"}) // no TapGateway
	err := unset.requireCustodyGateway(work)
	if err == nil || !strings.Contains(err.Error(), "authority_gateway_unset") {
		t.Fatalf("want authority_gateway_unset refusal, got %v", err)
	}

	set := New(Config{Launcher: "x", Image: "i", Model: "m", TapGateway: "http://gw:8127"})
	if err := set.requireCustodyGateway(work); err != nil {
		t.Fatalf("gateway set must admit: %v", err)
	}
	// env: refs are unaffected by the gateway guard.
	envOnly := execution.WorkSpec{Secrets: []execution.Secret{{Name: "CURSOR_API_KEY", Ref: "env:CURSOR_API_KEY"}}}
	if err := unset.requireCustodyGateway(envOnly); err != nil {
		t.Fatalf("env-only work must not trip the gateway guard: %v", err)
	}
}

// TestDeriveErrorFoldsInStderr proves cliCustody surfaces custody's stderr on a
// derive failure instead of a bare exit status. A re-exec of this test binary
// stands in for the custody CLI, writing a coded refusal to stderr and exiting 1.
func TestDeriveErrorFoldsInStderr(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestAuthorityDeriveFailHelper")
	cmd.Env = append(os.Environ(), "AUTHORITY_DERIVE_FAIL=1")
	_, err := cmd.Output()
	if err == nil {
		t.Fatal("helper must exit non-zero")
	}
	got := deriveError(err).Error()
	if !strings.Contains(got, "refused_attenuation_ttl") {
		t.Fatalf("derive error must fold in the CLI stderr, got %q", got)
	}
	if !strings.Contains(got, "custody derive:") {
		t.Fatalf("derive error must stay wrapped, got %q", got)
	}
}

// TestAuthorityDeriveFailHelper is a re-exec stand-in for a failing custody
// derive: it writes a coded refusal to stderr and exits 1.
func TestAuthorityDeriveFailHelper(_ *testing.T) {
	if os.Getenv("AUTHORITY_DERIVE_FAIL") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "refused_attenuation_ttl: child expiry after parent expiry")
	os.Exit(1)
}
