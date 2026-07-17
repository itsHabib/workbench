package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

const (
	escLine = `{"id":"esc_1","kind":"escalation","run":"run_1","time":"2026-07-08T16:37:12Z","body":{"outcome":"parked_for_judgment","question":"needs judgment"},"prev":"h0","hash":"h1"}`
	vrdPass = `{"id":"vrd_1","kind":"verdict","run":"run_1","time":"2026-07-08T16:37:13Z","body":{"subject":{"repo":"itsHabib/ship","number":181},"source":"reducer","decision":"pass","tier":"T1","why":"ok"},"prev":"h1","hash":"h2"}`
	vrdEsc  = `{"id":"vrd_2","kind":"verdict","run":"run_2","time":"2026-07-08T16:38:00Z","body":{"subject":{"repo":"itsHabib/ship","number":182},"source":"reducer","decision":"escalate","tier":"T3","why":"tier over ceiling"},"prev":"h2","hash":"h3"}`
)

func receiptLine(key, outcome string) string {
	return fmt.Sprintf(`{"key":"%s","source":"ship-run","outcome":"%s"}`, key, outcome)
}

func shipFile(t *testing.T, content string) config.Source {
	t.Helper()
	p := filepath.Join(t.TempDir(), "receipts.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Source{Name: "ship", Kind: config.SourceShipReceipts, Path: p}
}

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
	if events[0].Fields["run"] != "run_1" {
		t.Fatalf("escalation must surface its run for the card, got %q", events[0].Fields["run"])
	}
	if v := events[1].Fields; v["repo"] != "itsHabib/ship" || v["number"] != "182" ||
		v["tier"] != "T3" || v["dimension"] != "reducer" || v["run"] != "run_2" {
		t.Fatalf("verdict must surface repo/number/tier/dimension/run, got %+v", v)
	}
	if cur.LastHash != "h3" {
		t.Fatalf("cursor must pin the last processed hash, got %q", cur.LastHash)
	}
}

// TestGateLogEscalationCarriesPRSubject pins the escalation click-target: an
// escalation body naming a PR surfaces repo+number for notify's button, and
// one naming none stays subjectless so no button is invented.
func TestGateLogEscalationCarriesPRSubject(t *testing.T) {
	withPR := `{"id":"esc_pr","kind":"escalation","run":"run_7","time":"2026-07-08T16:39:00Z","body":{"outcome":"parked_for_judgment","question":"your call","repo":"itsHabib/workbench","number":64},"prev":"h1","hash":"h2"}`
	src := gateFile(t, escLine+"\n"+withPR+"\n")
	events, _, err := Read(src, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want both escalations, got %d: %+v", len(events), events)
	}
	if f := events[0].Fields; f["repo"] != "" || f["number"] != "" {
		t.Fatalf("subjectless escalation must not invent a PR, got %+v", f)
	}
	if f := events[1].Fields; f["repo"] != "itsHabib/workbench" || f["number"] != "64" {
		t.Fatalf("escalation must surface its PR subject, got %+v", f)
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

func TestReceiptsLiftFailedCancelledAndParked(t *testing.T) {
	lines := ""
	for _, o := range []string{"succeeded", "failed", "cancelled", "merged", "pending", "parked"} {
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
	// failed, cancelled, parked lift; succeeded/merged/pending stay dropped.
	if len(events) != 3 {
		t.Fatalf("want failed + cancelled + parked, got %d: %+v", len(events), events)
	}
	byID := make(map[string]event.Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}
	failed, ok := byID["wf_failed:failed"]
	if !ok {
		t.Fatalf("failed receipt must still lift; got %+v", events)
	}
	if failed.Severity != event.SevFailed {
		t.Fatalf("failed severity must stay SevFailed, got %v", failed.Severity)
	}
	cancelled, ok := byID["wf_cancelled:cancelled"]
	if !ok {
		t.Fatalf("cancelled receipt must still lift; got %+v", events)
	}
	if cancelled.Severity != event.SevCancelled {
		t.Fatalf("cancelled severity must stay SevCancelled, got %v", cancelled.Severity)
	}
	parked, ok := byID["wf_parked:parked"]
	if !ok {
		t.Fatalf("park receipt must lift with dedupe ID key+outcome; got %+v", events)
	}
	if parked.Severity != event.SevEscalate {
		t.Fatalf("a park is page-worthy (SevEscalate), got %v", parked.Severity)
	}
}

func TestCorruptGateLineFailsTheRead(t *testing.T) {
	src := gateFile(t, "not json\n")
	if _, _, err := Read(src, Cursor{}); err == nil {
		t.Fatal("a corrupt artifact line must fail the read, not read as quiet")
	}
}

func TestCorruptVerdictBodyFailsTheRead(t *testing.T) {
	// A valid envelope whose verdict body will not decode (decision is a number,
	// not a string) must fail the read loudly — exactly like a corrupt envelope
	// line — never vanish and let a block/escalate go unpaged.
	bad := `{"id":"vrd_x","kind":"verdict","run":"r","time":"2026-07-08T16:00:00Z","body":{"subject":{"repo":"x","number":1},"decision":123,"tier":"T1","why":"?"},"prev":"h0","hash":"h1"}`
	src := gateFile(t, bad+"\n")
	if _, _, err := Read(src, Cursor{}); err == nil {
		t.Fatal("a verdict body that will not decode must fail the read, not read as quiet")
	}
}

func TestParseErrorStillDeliversPendingAlert(t *testing.T) {
	// A truncation fires a cursor-alert and resweeps; when the resweep then hits
	// a corrupt line, Read must still surface the alert alongside the error, so
	// the integrity notification reaches the routing path, not only stderr.
	src := gateFile(t, "not json\n")
	events, _, err := Read(src, Cursor{Offset: 10_000})
	if err == nil {
		t.Fatal("a corrupt line in the resweep must fail the read")
	}
	if len(events) != 1 || events[0].Kind != "cursor-alert" {
		t.Fatalf("the pending truncation alert must survive the parse error, got %+v", events)
	}
}

func TestShipReceiptsMidlineOffsetResweepsNotErrors(t *testing.T) {
	r1 := receiptLine("wf1", "failed")
	r2 := receiptLine("wf2", "parked")
	src := shipFile(t, r1+"\n"+r2+"\n")
	// Advance through r1 only; the cursor sits at the start of r2.
	cur := Cursor{Offset: int64(len(r1) + 1)}
	// Upstream rewrite keeps valid receipts but leaves the stored offset mid-line.
	if err := os.WriteFile(src.Path, []byte(r1+"\n"+r2+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cur.Offset += 12
	events, _, err := Read(src, cur)
	if err != nil {
		t.Fatalf("mid-line offset must resweep, not fail the poll: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("want cursor-alert + reswept receipts, got %d: %+v", len(events), events)
	}
	if events[0].Kind != "cursor-alert" || events[0].Severity != event.SevEscalate {
		t.Fatalf("mid-line offset must fire cursor-alert, got %+v", events[0])
	}
	if !strings.Contains(events[0].Body, "unparseable receipt at cursor") {
		t.Fatalf("mid-line offset must use receipt guard, got body %q", events[0].Body)
	}
	byID := make(map[string]event.Event, len(events))
	for _, e := range events[1:] {
		byID[e.ID] = e
	}
	if _, ok := byID["wf1:failed"]; !ok {
		t.Fatalf("resweep must re-lift failed receipt, got %+v", events[1:])
	}
	if _, ok := byID["wf2:parked"]; !ok {
		t.Fatalf("resweep must re-lift parked receipt, got %+v", events[1:])
	}
}

func TestShipReceiptsAppendOnlyNoAlert(t *testing.T) {
	r1 := receiptLine("wf1", "failed")
	src := shipFile(t, r1+"\n")
	_, cur, err := Read(src, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	r2 := receiptLine("wf2", "parked")
	if err := os.WriteFile(src.Path, []byte(r1+"\n"+r2+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	events, next, err := Read(src, cur)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == "cursor-alert" {
			t.Fatalf("append-only growth must not fire cursor-alert, got %+v", e)
		}
	}
	if len(events) != 1 || events[0].ID != "wf2:parked" {
		t.Fatalf("want only the appended parked receipt, got %+v", events)
	}
	if next.Offset <= cur.Offset {
		t.Fatalf("cursor must advance on append-only growth: %d -> %d", cur.Offset, next.Offset)
	}
}

func TestCursorAlertIDIsStableForSameFailure(t *testing.T) {
	// The dedupe ID must be stable for the same integrity failure, so a held
	// cursor does not re-page the same break every poll.
	src := gateFile(t, escLine+"\n")
	e1, _, err := Read(src, Cursor{Offset: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	e2, _, err := Read(src, Cursor{Offset: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if e1[0].Kind != "cursor-alert" || e2[0].Kind != "cursor-alert" {
		t.Fatalf("expected leading cursor-alerts, got %+v / %+v", e1[0], e2[0])
	}
	if e1[0].ID != e2[0].ID {
		t.Fatalf("cursor-alert ID must be stable for the same failure: %q != %q", e1[0].ID, e2[0].ID)
	}
}
