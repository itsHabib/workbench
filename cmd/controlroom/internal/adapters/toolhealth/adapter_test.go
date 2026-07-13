package toolhealth

import (
	"context"
	"reflect"
	"testing"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

type fakeRunner struct {
	stdout []byte
	err    error
	args   []string
}

func (f *fakeRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	f.args = append([]string{executable}, args...)
	return f.stdout, f.err
}

func TestCollectParsesAccumulatedBoard(t *testing.T) {
	f := &fakeRunner{stdout: []byte(`Tool Health Board — accumulated friction
Generated: 2026-07-13T12:00:00Z
| Tool | Severity | Sessions | Last seen | Pain |
|---|---|---|---|---|
| ship | P2 | 4 | 2026-07-13T11:30:00Z | contention |
Kind: accumulated_friction
Additive: ignored`)}
	a := New("toolhealth.exe")
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceOK || len(got.Tools) != 1 || got.Tools[0].SessionCount != 4 {
		t.Fatalf("unexpected result: %#v", got)
	}
	if want := []string{"toolhealth.exe", "board"}; !reflect.DeepEqual(f.args, want) {
		t.Fatalf("argv = %#v, want %#v", f.args, want)
	}
}

func TestCollectParsesLiveIncidentWithoutInventingZero(t *testing.T) {
	f := &fakeRunner{stdout: []byte(`Tool Health Board — LIVE INCIDENT
Generated: 2026-07-13T12:00:00Z
!!! ACTIVE INCIDENT !!!
Status: auth failure
Started: 2026-07-13T11:55:00Z
Severity: P1
Tool: github
Kind: live_incident`)}
	a := New("toolhealth")
	a.runner = f
	got := a.Collect(context.Background())
	if len(got.Tools) != 1 || got.Tools[0].SessionCount != 1 || got.Tools[0].Pain[0] != "auth failure" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestCollectFailsClosedOnAnchorDrift(t *testing.T) {
	a := New("toolhealth")
	a.runner = &fakeRunner{stdout: []byte("Tool Health Board\nGenerated: now\nnew format")}
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceUnavailable || got.Receipt.ErrorCode != "contract_drift" || len(got.Tools) != 0 {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestCollectDegradedOnPartialRows(t *testing.T) {
	a := New("toolhealth")
	a.runner = &fakeRunner{stdout: []byte(`Tool Health Board — accumulated friction
Generated: 2026-07-13T12:00:00Z
| Tool | Severity | Sessions | Last seen | Pain |
|---|---|---|---|---|
| Build Tool | P2 | 4 | 2026-07-13T11:30:00Z | contention |
| zero | P3 | 0 | 2026-07-13T10:00:00Z | unknown count |
| broken row |
Kind: accumulated_friction`)}
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceDegraded || got.Receipt.ErrorCode != "partial_parse" {
		t.Fatalf("expected degraded receipt: %#v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].Tool != "Build Tool" {
		t.Fatalf("valid row was not preserved: %#v", got.Tools)
	}
}

func TestCollectRejectsRepeatedIncidentKeys(t *testing.T) {
	a := New("toolhealth")
	a.runner = &fakeRunner{stdout: []byte(`Tool Health Board — LIVE INCIDENT
Generated: 2026-07-13T12:00:00Z
!!! ACTIVE INCIDENT !!!
Tool: github
Tool: ship
Severity: P1
Started: 2026-07-13T11:55:00Z
Status: auth failure
Kind: live_incident`)}
	got := a.Collect(context.Background())
	if got.Receipt.ErrorCode != "contract_drift" {
		t.Fatalf("unexpected result: %#v", got)
	}
}
