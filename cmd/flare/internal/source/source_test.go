package source

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

const (
	escLine = `{"id":"esc_1","kind":"escalation","run":"run_1","time":"2026-07-08T16:37:12Z","body":{"outcome":"parked_for_judgment","question":"needs judgment"},"prev":"h0","hash":"h1"}`
	vrdPass = `{"id":"vrd_1","kind":"verdict","run":"run_1","time":"2026-07-08T16:37:13Z","body":{"subject":{"repo":"itsHabib/ship","number":181},"source":"reducer","decision":"pass","tier":"T1","why":"ok"},"prev":"h1","hash":"h2"}`
	vrdEsc  = `{"id":"vrd_2","kind":"verdict","run":"run_2","time":"2026-07-08T16:38:00Z","body":{"subject":{"repo":"itsHabib/ship","number":182},"source":"reducer","decision":"escalate","tier":"T3","why":"tier over ceiling"},"prev":"h2","hash":"h3"}`
)

func gateFile(t *testing.T, content string) config.Source {
	t.Helper()
	p := filepath.Join(t.TempDir(), "log.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Source{Name: "gate", Kind: config.SourceGateLog, Path: p}
}

func TestGateLogLiftsEscalationsAndNonPassVerdicts(t *testing.T) {
	src := gateFile(t, escLine+"\n"+vrdPass+"\n"+vrdEsc+"\n")
	events, cur, err := Read(src, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want escalation + escalate-verdict only, got %d: %+v", len(events), events)
	}
	if events[0].ID != "esc_1" || events[0].Severity != event.SevEscalate {
		t.Fatalf("bad escalation event: %+v", events[0])
	}
	if events[1].ID != "vrd_2" || events[1].Fields["decision"] != "escalate" {
		t.Fatalf("bad verdict event: %+v", events[1])
	}
	if cur.LastHash != "h3" {
		t.Fatalf("cursor must pin the last processed hash, got %q", cur.LastHash)
	}
}

func TestTornFinalLineIsLeftForNextPoll(t *testing.T) {
	src := gateFile(t, escLine+"\n"+`{"id":"esc_2","kind":"esc`)
	events, cur, err := Read(src, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("torn line must not be processed, got %d events", len(events))
	}
	if cur.Offset != int64(len(escLine)+1) {
		t.Fatalf("cursor must stop at the last complete newline, got %d", cur.Offset)
	}
}

func TestChainBreakAlertsAndResweeps(t *testing.T) {
	src := gateFile(t, escLine+"\n")
	events, _, err := Read(src, Cursor{Offset: 0, LastHash: "not-h0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want cursor-alert + resweep escalation, got %d: %+v", len(events), events)
	}
	if events[0].Kind != "cursor-alert" {
		t.Fatalf("a broken chain must fire an alert, got %+v", events[0])
	}
	if events[1].ID != "esc_1" {
		t.Fatalf("resweep must re-lift from the start, got %+v", events[1])
	}
}

func TestTruncationAlertsAndResweeps(t *testing.T) {
	src := gateFile(t, escLine+"\n")
	events, _, err := Read(src, Cursor{Offset: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != "cursor-alert" {
		t.Fatalf("a shrunken log must alert + resweep, got %+v", events)
	}
}

func TestReceiptsLiftFailedAndCancelledOnly(t *testing.T) {
	lines := ""
	for _, o := range []string{"succeeded", "failed", "cancelled", "merged", "pending"} {
		lines += fmt.Sprintf(`{"key":"wf_%s","source":"ship-run","outcome":"%s"}`+"\n", o, o)
	}
	p := filepath.Join(t.TempDir(), "receipts.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	src := config.Source{Name: "ship", Kind: config.SourceShipReceipts, Path: p}
	events, _, err := Read(src, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want failed + cancelled, got %d: %+v", len(events), events)
	}
	if events[0].ID != "wf_failed:failed" {
		t.Fatalf("receipt dedupe ID must be key+outcome, got %q", events[0].ID)
	}
}

func TestCorruptGateLineFailsTheRead(t *testing.T) {
	src := gateFile(t, "not json\n")
	if _, _, err := Read(src, Cursor{}); err == nil {
		t.Fatal("a corrupt artifact line must fail the read, not read as quiet")
	}
}
