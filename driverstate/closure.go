package driverstate

import (
	"encoding/json"
	"fmt"
	"slices"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// foldClosures reconstructs closure receipts only for streams that carry a
// closure_facts or intervention event. Legacy ledgers therefore retain their
// prior reduced shape, while a partially adopted emitter is visibly incomplete.
func foldClosures(events []Event, state *dsc.RunState) {
	streams := closureStreams(events)
	for _, stream := range streams {
		receipt := foldClosure(events, stream)
		rec := state.Streams[stream]
		rec.Closure = &receipt
		state.Streams[stream] = rec
	}
}

func closureStreams(events []Event) []string {
	seen := make(map[string]bool)
	var streams []string
	for _, event := range events {
		if event.Kind != dsc.KindClosureFacts && event.Kind != dsc.KindIntervention {
			continue
		}
		if seen[event.Stream] {
			continue
		}
		seen[event.Stream] = true
		streams = append(streams, event.Stream)
	}
	slices.Sort(streams)
	return streams
}

func foldClosure(events []Event, stream string) dsc.ClosureReceipt {
	acc := closureAccumulator{
		stream:  stream,
		receipt: dsc.ClosureReceipt{WorkflowRef: events[0].Run + "/" + stream},
	}
	for _, event := range events {
		acc.apply(event)
	}
	if acc.repo != "" && acc.pr > 0 && acc.prHead != "" {
		acc.receipt.PRRef = fmt.Sprintf("%s#%d@%s", acc.repo, acc.pr, acc.prHead)
	}
	if acc.receipt.ReviewHeadSHA != "" && acc.prHead != "" && acc.receipt.ReviewHeadSHA != acc.prHead {
		addContradiction(&acc.receipt, "review_head_mismatch")
	}
	if acc.pr > 0 && acc.mergedPR > 0 && acc.pr != acc.mergedPR {
		addContradiction(&acc.receipt, "merged_pr_mismatch")
	}
	if acc.mergeCount > 1 {
		addContradiction(&acc.receipt, "duplicate_terminal_closure")
	}
	finalizeClosure(&acc.receipt)
	return acc.receipt
}

type closureAccumulator struct {
	stream     string
	receipt    dsc.ClosureReceipt
	repo       string
	prHead     string
	pr         int
	mergedPR   int
	mergeCount int
}

func (a *closureAccumulator) apply(event Event) {
	if event.Kind == dsc.KindRunImported {
		var body dsc.RunImportedBody
		if json.Unmarshal(event.Body, &body) == nil {
			a.repo = body.Repo
			setClosureFact(&a.receipt, "ship_run_ref", &a.receipt.ShipRunRef, body.ShipRunRef)
		}
		return
	}
	if event.Stream != a.stream {
		return
	}
	switch event.Kind {
	case dsc.KindClosureFacts:
		applyClosureFacts(&a.receipt, event.Body)
	case dsc.KindIntervention:
		a.applyIntervention(event.Body)
	case dsc.KindStreamPROpened:
		a.applyPROpened(event.Body)
	case dsc.KindReviewCycle:
		a.receipt.ReviewCycles++
	case dsc.KindStreamMerged:
		a.applyMerged(event.Body)
	}
}

func (a *closureAccumulator) applyIntervention(raw json.RawMessage) {
	var body dsc.InterventionBody
	if json.Unmarshal(raw, &body) == nil {
		a.receipt.Interventions = append(a.receipt.Interventions, body)
	}
}

func (a *closureAccumulator) applyPROpened(raw json.RawMessage) {
	var body dsc.StreamPROpenedBody
	if json.Unmarshal(raw, &body) == nil {
		a.pr = body.PR
		a.prHead = body.HeadSHA
	}
}

func (a *closureAccumulator) applyMerged(raw json.RawMessage) {
	var body dsc.StreamMergedBody
	if json.Unmarshal(raw, &body) != nil {
		return
	}
	a.mergeCount++
	if a.mergedPR == 0 {
		a.mergedPR = body.PR
	}
	if a.mergedPR != body.PR {
		addContradiction(&a.receipt, "merged_pr_conflict")
	}
	a.receipt.MergeCommit = body.MergeCommit
	a.receipt.Outcome = "merged"
}

func applyClosureFacts(receipt *dsc.ClosureReceipt, raw json.RawMessage) {
	var body dsc.ClosureFactsBody
	if json.Unmarshal(raw, &body) != nil {
		return
	}
	facts := []struct {
		name   string
		target *string
		value  string
	}{
		{"task_ref", &receipt.TaskRef, body.TaskRef},
		{"seat", &receipt.Seat, body.Seat},
		{"harness", &receipt.Harness, body.Harness},
		{"model", &receipt.Model, body.Model},
		{"provider", &receipt.Provider, body.Provider},
		{"effort", &receipt.Effort, body.Effort},
		{"review_producer", &receipt.ReviewProducer, body.ReviewProducer},
		{"catalog_revision", &receipt.CatalogRevision, body.CatalogRevision},
		{"review_artifact_id", &receipt.ReviewArtifactID, body.ReviewArtifactID},
		{"review_artifact_digest", &receipt.ReviewArtifactDigest, body.ReviewArtifactDigest},
		{"review_head_sha", &receipt.ReviewHeadSHA, body.ReviewHeadSHA},
		{"ship_run_ref", &receipt.ShipRunRef, body.ShipRunRef},
		{"gate_run_ref", &receipt.GateRunRef, body.GateRunRef},
	}
	for _, fact := range facts {
		setClosureFact(receipt, fact.name, fact.target, fact.value)
	}
}

func setClosureFact(receipt *dsc.ClosureReceipt, name string, target *string, value string) {
	if value == "" {
		return
	}
	if *target == "" {
		*target = value
		return
	}
	if *target != value {
		addContradiction(receipt, name+"_conflict")
	}
}

func addContradiction(receipt *dsc.ClosureReceipt, reason string) {
	if slices.Contains(receipt.Contradictions, reason) {
		return
	}
	receipt.Contradictions = append(receipt.Contradictions, reason)
}

func finalizeClosure(receipt *dsc.ClosureReceipt) {
	required := []struct {
		name  string
		value string
	}{
		{"task_ref", receipt.TaskRef},
		{"pr_ref", receipt.PRRef},
		{"ship_run_ref", receipt.ShipRunRef},
		{"gate_run_ref", receipt.GateRunRef},
		{"seat", receipt.Seat},
		{"harness", receipt.Harness},
		{"model", receipt.Model},
		{"effort", receipt.Effort},
		{"provider", receipt.Provider},
		{"review_producer", receipt.ReviewProducer},
		{"catalog_revision", receipt.CatalogRevision},
		{"review_artifact_id", receipt.ReviewArtifactID},
		{"review_artifact_digest", receipt.ReviewArtifactDigest},
		{"review_head_sha", receipt.ReviewHeadSHA},
		{"merge_commit", receipt.MergeCommit},
		{"outcome", receipt.Outcome},
	}
	for _, field := range required {
		if field.value == "" {
			receipt.Missing = append(receipt.Missing, field.name)
		}
	}
	if receipt.ReviewCycles == 0 {
		receipt.Missing = append(receipt.Missing, "review_cycles")
	}
	receipt.Complete = len(receipt.Missing) == 0 && len(receipt.Contradictions) == 0
}
