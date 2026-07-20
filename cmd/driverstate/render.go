package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/driverstate"
)

// cmdRender pretty-prints a run's reduced state and event timeline.
func cmdRender(dir string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	run := fs.String("run", "", "run id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *run == "" {
		return fmt.Errorf("render: --run is required")
	}
	// One ledger snapshot: read the events once and fold the state from that
	// same slice, so on a live run the header/stream blocks can never
	// contradict the timeline they sit beside.
	events, err := driverstate.Events(dir, *run)
	if err != nil {
		return err
	}
	writeRender(stdout, *run, driverstate.FoldEvents(events), events)
	return nil
}

func writeRender(w io.Writer, run string, state dsc.RunState, events []driverstate.Event) {
	fmt.Fprintf(w, "run %s: %s (repo %s, %d streams)\n\n", run, state.Run.Status, state.Run.Repo, len(state.Streams))

	fmt.Fprintln(w, "timeline:")
	for _, e := range events {
		fmt.Fprintln(w, formatTimelineLine(e))
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "streams:")
	for _, key := range sortedStreamKeys(state.Streams) {
		writeStreamBlock(w, key, state.Streams[key])
	}
}

func sortedStreamKeys(streams map[string]dsc.StreamRecord) []string {
	keys := make([]string, 0, len(streams))
	for k := range streams {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func writeStreamBlock(w io.Writer, stream string, rec dsc.StreamRecord) {
	fmt.Fprintf(w, "  stream %s: %s\n", stream, rec.Status)
	for _, att := range rec.Attempts {
		fmt.Fprintln(w, formatAttemptLine(att))
	}
	if rec.PR != 0 {
		line := fmt.Sprintf("    pr %d", rec.PR)
		if rec.URL != "" {
			line += ": " + rec.URL
		}
		fmt.Fprintln(w, line)
	}
	if rec.MergeCommit != "" {
		fmt.Fprintf(w, "    merge %s\n", shortCommit(rec.MergeCommit))
	}
}

// formatAttemptLine renders one attempt's facts after a single "attempt N:"
// prefix, so an unusual combination (a failure category on a non-terminal
// attempt) never yields a dangling comma.
func formatAttemptLine(att dsc.AttemptRecord) string {
	var facts []string
	if att.Terminal {
		facts = append(facts, "terminal")
	}
	if att.FailureCategory != "" {
		facts = append(facts, "failure="+att.FailureCategory)
	}
	line := fmt.Sprintf("    attempt %d", att.Seq)
	if len(facts) == 0 {
		return line
	}
	return line + ": " + strings.Join(facts, ", ")
}

func formatTimelineLine(e driverstate.Event) string {
	parts := []string{
		e.Time.UTC().Format(time.RFC3339),
		string(e.Kind),
	}
	if e.Stream != "" {
		parts = append(parts, "stream="+e.Stream)
	}
	if fact := eventFact(e); fact != "" {
		parts = append(parts, fact)
	}
	return "  " + strings.Join(parts, "  ")
}

func eventFact(e driverstate.Event) string {
	if !e.Kind.Known() {
		return ""
	}
	switch e.Kind {
	case dsc.KindRunImported:
		var b dsc.RunImportedBody
		if err := json.Unmarshal(e.Body, &b); err != nil {
			return ""
		}
		if b.Repo == "" {
			return ""
		}
		return "repo=" + b.Repo
	case dsc.KindStreamAttempt:
		return formatAttemptFact(e.Body)
	case dsc.KindStreamPROpened:
		return formatPRFact(e.Body)
	case dsc.KindStreamMerged:
		return formatMergeFact(e.Body)
	case dsc.KindReviewCycle:
		return formatReviewCycleFact(e.Body)
	default:
		return ""
	}
}

func formatAttemptFact(body json.RawMessage) string {
	var b dsc.StreamAttemptBody
	if err := json.Unmarshal(body, &b); err != nil {
		return ""
	}
	parts := []string{fmt.Sprintf("seq=%d", b.Seq)}
	if b.Terminal {
		parts = append(parts, "terminal")
	}
	if b.FailureCategory != "" {
		parts = append(parts, "failure="+b.FailureCategory)
	}
	return strings.Join(parts, " ")
}

func formatPRFact(body json.RawMessage) string {
	var b dsc.StreamPROpenedBody
	if err := json.Unmarshal(body, &b); err != nil {
		return ""
	}
	if b.PR == 0 {
		return ""
	}
	if b.URL == "" {
		return fmt.Sprintf("pr=%d", b.PR)
	}
	return fmt.Sprintf("pr=%d url=%s", b.PR, b.URL)
}

func formatMergeFact(body json.RawMessage) string {
	var b dsc.StreamMergedBody
	if err := json.Unmarshal(body, &b); err != nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if b.PR != 0 {
		parts = append(parts, fmt.Sprintf("pr=%d", b.PR))
	}
	if b.MergeCommit != "" {
		parts = append(parts, "commit="+shortCommit(b.MergeCommit))
	}
	return strings.Join(parts, " ")
}

func formatReviewCycleFact(body json.RawMessage) string {
	var b dsc.ReviewCycleBody
	if err := json.Unmarshal(body, &b); err != nil {
		return ""
	}
	return fmt.Sprintf("cycle=%d findings=%d", b.Cycle, b.Findings)
}

func shortCommit(commit string) string {
	if len(commit) <= 7 {
		return commit
	}
	return commit[:7]
}
