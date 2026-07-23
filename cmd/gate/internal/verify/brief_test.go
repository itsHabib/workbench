package verify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// briefEcho captures the prompts SynthesizeBrief sends and returns one canned
// reply, so tests can pin both what the model sees and how its answer parses.
type briefEcho struct {
	system, user string
	reply        string
	err          error
}

func (m *briefEcho) chat(_ context.Context, system, user string, _ json.RawMessage) (string, error) {
	m.system, m.user = system, user
	if m.err != nil {
		return "", m.err
	}
	return m.reply, nil
}

func (m *briefEcho) impl() string { return "brief-echo" }

func briefInputs() (Subject, string, string, []Verdict) {
	subject := Subject{Repo: "itsHabib/rooms", Number: 84}
	verdicts := []Verdict{
		{
			Source: "review-consolidation", Producer: Producer{Class: ClassLocal},
			Decision: DecisionEscalate, Why: "1 bot comments: 1 actionable — needs judgment",
			Findings: []Finding{{
				Title:    "[codex] Treat blocked destinations as expected in the witness (actionable)",
				Severity: "medium", Locus: "spec.md:57",
			}},
		},
		{Source: "reducer", Producer: Producer{Class: ClassCode}, Decision: DecisionEscalate, Why: "composed"},
	}
	return subject, "Exfil witness harness spec", "review-consolidation: needs judgment", verdicts
}

// TestSynthesizeBriefSendsRecordedFactsOnly pins the synthesis input: the PR
// subject, title, park question, and each rung's findings — wrapped in the
// untrusted-data markers — and the reducer's composite excluded (its Why is
// the question already).
func TestSynthesizeBriefSendsRecordedFactsOnly(t *testing.T) {
	m := &briefEcho{reply: `{"what_it_is":"A design spec for a test harness.","concern":"The harness pass check is broken.","risk":"Medium — spec only.","recommendation":"Ask the author to fix the witness."}`}
	subject, title, question, verdicts := briefInputs()
	b, err := SynthesizeBrief(context.Background(), m, subject, title, question, verdicts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"itsHabib/rooms#84", "Exfil witness harness spec",
		"Parked because: review-consolidation: needs judgment",
		"Treat blocked destinations", "spec.md:57", "(severity medium)",
		artifactsBegin, artifactsEnd,
	} {
		if !strings.Contains(m.user, want) {
			t.Fatalf("brief context missing %q:\n%s", want, m.user)
		}
	}
	if strings.Contains(m.user, "Verifier reducer") {
		t.Fatalf("reducer composite must not be re-sent as a rung:\n%s", m.user)
	}
	if b.WhatItIs == "" || b.Concern == "" || b.Risk == "" || b.Recommendation == "" {
		t.Fatalf("parsed brief incomplete: %+v", b)
	}
}

// TestSynthesizeBriefFailsClosedToNoBrief: a model error, bad JSON, or a
// hollow answer all return an error — the caller's fail-open then drops the
// brief and the page falls back to the raw question.
func TestSynthesizeBriefFailsClosedToNoBrief(t *testing.T) {
	subject, title, question, verdicts := briefInputs()
	cases := map[string]*briefEcho{
		"model error": {err: errors.New("anthropic: status 529")},
		"bad json":    {reply: "not json"},
		"empty core":  {reply: `{"what_it_is":"","concern":"","risk":"Low","recommendation":"merge"}`},
	}
	for name, m := range cases {
		if _, err := SynthesizeBrief(context.Background(), m, subject, title, question, verdicts); err == nil {
			t.Fatalf("%s: want error, got none", name)
		}
	}
	if _, err := SynthesizeBrief(context.Background(), nil, subject, title, question, verdicts); err == nil {
		t.Fatal("nil model: want error, got none")
	}
}

// TestPRTitleTolerant: the title enriches the brief, so a missing artifact or
// drifted shape reads as "", never an error surface.
func TestPRTitleTolerant(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	view, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{
		"data": map[string]any{"title": "Exfil witness harness spec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := PRTitle(st, view.ID); got != "Exfil witness harness spec" {
		t.Fatalf("title: %q", got)
	}
	if got := PRTitle(st, "evd_missing"); got != "" {
		t.Fatalf("missing evidence must read as no title, got %q", got)
	}
	bad, err := st.Append(state.KindEvidence, "run_t", nil, []string{"not", "an", "object"})
	if err != nil {
		t.Fatal(err)
	}
	if got := PRTitle(st, bad.ID); got != "" {
		t.Fatalf("drifted shape must read as no title, got %q", got)
	}
}
