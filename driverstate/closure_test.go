package driverstate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

func TestFoldClosureReceiptComplete(t *testing.T) {
	events := closureEvents(t, strings.Repeat("a", 40), strings.Repeat("a", 40), 1)
	state := FoldEvents(events)
	receipt := state.Streams["dss_1"].Closure
	if receipt == nil || !receipt.Complete {
		t.Fatalf("closure = %+v, want complete", receipt)
	}
	if receipt.PRRef != "itsHabib/ship#184@"+strings.Repeat("a", 40) {
		t.Fatalf("pr_ref = %q", receipt.PRRef)
	}
	if receipt.ReviewCycles != 1 || receipt.Outcome != "merged" {
		t.Fatalf("closure facts = %+v", receipt)
	}
}

func TestFoldClosureReceiptLegacyCatalogIsIncomplete(t *testing.T) {
	events := closureEvents(t, strings.Repeat("a", 40), strings.Repeat("a", 40), 1)
	var facts dsc.ClosureFactsBody
	if err := json.Unmarshal(events[4].Body, &facts); err != nil {
		t.Fatal(err)
	}
	facts.CatalogRevision = ""
	events[4].Body = mustBody(t, facts)
	receipt := FoldEvents(events).Streams["dss_1"].Closure
	if receipt == nil || receipt.Complete || !contains(receipt.Missing, "catalog_revision") {
		t.Fatalf("closure = %+v, want visibly incomplete catalog provenance", receipt)
	}
}

func TestFoldClosureReceiptContradictionsStayIncomplete(t *testing.T) {
	tests := []struct {
		name       string
		reviewHead string
		merges     int
		reason     string
	}{
		{"mismatched exact head", strings.Repeat("b", 40), 1, "review_head_mismatch"},
		{"duplicate terminal closure", strings.Repeat("a", 40), 2, "duplicate_terminal_closure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := closureEvents(t, strings.Repeat("a", 40), test.reviewHead, test.merges)
			receipt := FoldEvents(events).Streams["dss_1"].Closure
			if receipt == nil || receipt.Complete || !contains(receipt.Contradictions, test.reason) {
				t.Fatalf("closure = %+v, want contradiction %q", receipt, test.reason)
			}
		})
	}
}

func TestFoldClosureReceiptRefusesMergedPRIdentityMismatch(t *testing.T) {
	events := closureEvents(t, strings.Repeat("a", 40), strings.Repeat("a", 40), 1)
	var merged dsc.StreamMergedBody
	if err := json.Unmarshal(events[len(events)-1].Body, &merged); err != nil {
		t.Fatal(err)
	}
	merged.PR = 185
	events[len(events)-1].Body = mustBody(t, merged)
	receipt := FoldEvents(events).Streams["dss_1"].Closure
	if receipt == nil || receipt.Complete || !contains(receipt.Contradictions, "merged_pr_mismatch") {
		t.Fatalf("closure = %+v, want merged PR identity contradiction", receipt)
	}
}

func TestClosureReceiptExactHeadAndTerminalModel(t *testing.T) {
	for sequence := 1; sequence <= 128; sequence++ {
		prHead := fmt.Sprintf("%040x", sequence)
		reviewHead := prHead
		if sequence%2 == 0 {
			reviewHead = fmt.Sprintf("%040x", sequence+1000)
		}
		merges := 1
		if sequence%3 == 0 {
			merges = 2
		}
		receipt := FoldEvents(closureEvents(t, prHead, reviewHead, merges)).Streams["dss_1"].Closure
		wantComplete := reviewHead == prHead && merges == 1
		if receipt == nil || receipt.Complete != wantComplete {
			t.Fatalf("sequence=%d closure=%+v, want complete=%v", sequence, receipt, wantComplete)
		}
	}
}

func TestLegacyLedgerHasNoSyntheticClosure(t *testing.T) {
	events := closureEvents(t, strings.Repeat("a", 40), strings.Repeat("a", 40), 1)
	filtered := events[:0]
	for _, event := range events {
		if event.Kind != dsc.KindClosureFacts && event.Kind != dsc.KindIntervention {
			filtered = append(filtered, event)
		}
	}
	if closure := FoldEvents(filtered).Streams["dss_1"].Closure; closure != nil {
		t.Fatalf("legacy closure = %+v, want absent additive projection", closure)
	}
}

func closureEvents(t *testing.T, prHead, reviewHead string, merges int) []Event {
	t.Helper()
	at := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	event := func(kind dsc.Kind, body any) Event {
		at = at.Add(time.Second)
		return Event{
			ID: "evt_" + fmt.Sprint(at.Unix()), Run: "dsr_1", V: dsc.Version,
			Kind: kind, Stream: "dss_1", Time: at, Actor: "ship:drv_1",
			Body: mustBody(t, body),
		}
	}
	imported := event(dsc.KindRunImported, dsc.RunImportedBody{
		Repo: "itsHabib/ship", Source: "driver.md", Manifest: json.RawMessage(`{}`),
		Streams:    []dsc.StreamSpec{{Stream: "dss_1", DocPath: "spec.md"}},
		ShipRunRef: "drv_1",
	})
	imported.Stream = ""
	events := []Event{
		imported,
		event(dsc.KindStreamDispatched, dsc.StreamDispatchedBody{}),
		event(dsc.KindStreamAttempt, dsc.StreamAttemptBody{Seq: 1, DocPath: "spec.md", Terminal: true}),
		event(dsc.KindStreamPROpened, dsc.StreamPROpenedBody{PR: 184, URL: "https://github.com/itsHabib/ship/pull/184", HeadSHA: prHead}),
		event(dsc.KindClosureFacts, dsc.ClosureFactsBody{
			TaskRef: "tsk_1", Seat: "codex", Harness: "codex", Model: "gpt-5.6-sol",
			Provider: "openai", Effort: "high", ReviewProducer: "codex:github-plugin",
			CatalogRevision: strings.Repeat("c", 40), ReviewArtifactID: "rf_1",
			ReviewArtifactDigest: strings.Repeat("d", 64),
			ReviewHeadSHA:        reviewHead, ShipRunRef: "drv_1", GateRunRef: "run_1",
		}),
		event(dsc.KindReviewCycle, dsc.ReviewCycleBody{Cycle: 1, PanelSettled: true, Findings: 1}),
	}
	for index := 0; index < merges; index++ {
		events = append(events, event(dsc.KindStreamMerged, dsc.StreamMergedBody{
			PR: 184, MergeCommit: strings.Repeat("e", 40),
			MergedAt: at.Add(time.Second).Format(time.RFC3339),
		}))
	}
	return events
}

func mustBody(t *testing.T, value any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
