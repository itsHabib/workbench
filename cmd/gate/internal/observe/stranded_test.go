package observe

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts"
)

// parented is art plus provenance: the stranded join is keyed on a judgment's
// parents, which the existing helper does not carry.
func parented(id, kind, run string, parents []string, body any) state.Artifact {
	a := art(kind, run, id, at, body)
	a.Parents = parents
	return a
}

func judgment(decision string, decider *contracts.Decider) contracts.Verdict {
	return contracts.Verdict{
		Subject:  contracts.Subject{Repo: "itsHabib/ivy", Number: 22, HeadSHA: "d1ad174"},
		Source:   "operator-judgment",
		Producer: contracts.Producer{Class: contracts.ClassJudgment, Impl: "operator"},
		Decision: decision,
		Tier:     "T0",
		Why:      "approved in Slack by @mhdevstuff",
		Decider:  decider,
	}
}

var at = time.Date(2026, 8, 23, 2, 1, 14, 0, time.UTC)

// TestStrandedRunsFindTheDecisionThatNeverLanded reproduces the live shape of
// itsHabib/ivy#22 run_fe7ac73ddb7c59a7: park, judgment, and then nothing. The
// run still reads as parked everywhere else, which is why this surface exists.
func TestStrandedRunsFindTheDecisionThatNeverLanded(t *testing.T) {
	arts := []state.Artifact{
		parented("vrd_1", state.KindVerdict, "run_a", nil, judgment("escalate", nil)),
		parented("esc_1", state.KindEscalation, "run_a", []string{"vrd_1", "grt_1"}, map[string]any{"outcome": "parked_for_judgment", "repo": "itsHabib/ivy", "number": 22}),
		parented("jdg_1", state.KindJudgment, "run_a", []string{"esc_1", "grt_1"}, judgment("pass", nil)),
	}
	rows := strandedRuns(arts, " -state /s")
	if len(rows) != 1 {
		t.Fatalf("want exactly one stranded run, got %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Run != "run_a" || r.Judgment != "jdg_1" || r.Escalation != "esc_1" {
		t.Fatalf("row must name the run, judgment, and park it stalled on: %+v", r)
	}
	if r.Decision != "pass" {
		t.Fatalf("decision = %q; the resume must re-assert the recorded one", r.Decision)
	}
	if r.Grant != "grt_1" {
		t.Fatalf("grant = %q, want the judgment's grant parent", r.Grant)
	}
	if r.Decider != "unattributed" {
		t.Fatalf("decider = %q, want the absence named", r.Decider)
	}
	for _, want := range []string{"gate judge", "-state /s", "run_a", "grt_1", "-decision pass"} {
		if !strings.Contains(r.ResumeCommand, want) {
			t.Fatalf("resume command %q missing %q", r.ResumeCommand, want)
		}
	}
}

// TestStrandedRunsIgnoreEveryCompletedOutcome is the other half: any action
// after a judgment means the decision landed, whatever it decided. Only the
// absence of an outcome is the anomaly.
func TestStrandedRunsIgnoreEveryCompletedOutcome(t *testing.T) {
	for _, outcome := range []string{"would_merge", "blocked", "capability_refused"} {
		t.Run(outcome, func(t *testing.T) {
			arts := []state.Artifact{
				parented("esc_1", state.KindEscalation, "run_a", []string{"vrd_1", "grt_1"}, map[string]any{"outcome": "parked_for_judgment"}),
				parented("jdg_1", state.KindJudgment, "run_a", []string{"esc_1", "grt_1"}, judgment("pass", nil)),
				parented("vrd_2", state.KindVerdict, "run_a", []string{"jdg_1"}, judgment("pass", nil)),
				parented("act_1", state.KindAction, "run_a", []string{"vrd_2", "grt_1"}, map[string]any{"outcome": outcome}),
			}
			if rows := strandedRuns(arts, ""); len(rows) != 0 {
				t.Fatalf("a judgment followed by %s is not stranded: %+v", outcome, rows)
			}
		})
	}
}

// TestStrandedRunsIgnoreARepark pins the narrowness of the join: a judgment that
// resolved an EARLIER park, followed by a fresh park, is an ordinary parked run
// awaiting a new judgment — not a stranded decision.
func TestStrandedRunsIgnoreARepark(t *testing.T) {
	arts := []state.Artifact{
		parented("esc_1", state.KindEscalation, "run_a", []string{"vrd_1", "grt_1"}, map[string]any{"outcome": "parked_for_judgment"}),
		parented("jdg_1", state.KindJudgment, "run_a", []string{"esc_1", "grt_1"}, judgment("pass", nil)),
		parented("esc_2", state.KindEscalation, "run_a", []string{"vrd_2", "grt_1"}, map[string]any{"outcome": "parked_for_judgment"}),
	}
	if rows := strandedRuns(arts, ""); len(rows) != 0 {
		t.Fatalf("a judgment whose park was superseded is not stranded: %+v", rows)
	}
}

// TestStrandedRunNamesAnAttributedDecider closes the loop with the binding: once
// judgments carry a decider, the row that surfaces a stranded one says whose
// decision is sitting unapplied.
func TestStrandedRunNamesAnAttributedDecider(t *testing.T) {
	d := &contracts.Decider{Who: "@mhdevstuff (U0B17RNEFCK)", Method: contracts.MethodSlackInteractive, At: "2026-08-23T02:01:14Z"}
	arts := []state.Artifact{
		parented("esc_1", state.KindEscalation, "run_a", []string{"vrd_1", "grt_1"}, map[string]any{"outcome": "parked_for_judgment"}),
		parented("jdg_1", state.KindJudgment, "run_a", []string{"esc_1", "grt_1"}, judgment("pass", d)),
	}
	rows := strandedRuns(arts, "")
	if len(rows) != 1 {
		t.Fatalf("want one stranded run, got %d", len(rows))
	}
	for _, want := range []string{"@mhdevstuff", contracts.MethodSlackInteractive} {
		if !strings.Contains(rows[0].Decider, want) {
			t.Fatalf("decider %q missing %q", rows[0].Decider, want)
		}
	}
}
