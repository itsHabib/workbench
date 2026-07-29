package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

func TestPanelCompletenessPermutations(t *testing.T) {
	required := []string{"codex", "cursor", "claude"}
	const states = 4
	permutations := 1
	for range required {
		permutations *= states
	}
	for permutation := 0; permutation < permutations; permutation++ {
		panel := panelEvidenceStates(required, permutation, "head")
		v := panelVerdict(t, panel, Subject{Repo: "o/r", Number: 1, HeadSHA: "head"})
		if len(panel.Completed) == len(required) {
			if v.Decision != DecisionPass {
				t.Fatalf("permutation %d: decision=%s why=%s", permutation, v.Decision, v.Why)
			}
			continue
		}
		if v.Decision != DecisionEscalate {
			t.Fatalf("permutation %d: incomplete panel decision=%s", permutation, v.Decision)
		}
	}
}

func TestPanelCompletenessPendingMissingUnknownPark(t *testing.T) {
	tests := map[string]reviewpanel.Evidence{
		"pending": {
			SchemaVersion: 1, Subject: reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
			Declaration: reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
			Pending:     []string{"codex"},
		},
		"missing": {
			SchemaVersion: 1, Subject: reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
			Declaration: reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
			Missing:     []string{"codex"},
		},
		"unknown": {
			SchemaVersion: 1, Subject: reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
			Declaration: reviewpanel.Declaration{Path: ".ship.json", Expected: []string{"codex"}},
			Unknown:     []string{"codex"},
		},
		"absent declaration": {
			SchemaVersion: 1, Subject: reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
			Declaration: reviewpanel.Declaration{Path: ".ship.json"},
			Unknown:     []string{"declaration"},
		},
	}
	for name, panel := range tests {
		t.Run(name, func(t *testing.T) {
			v := panelVerdict(t, panel, Subject{Repo: "o/r", Number: 1, HeadSHA: "head"})
			if v.Decision != DecisionEscalate {
				t.Fatalf("decision=%s why=%s", v.Decision, v.Why)
			}
		})
	}
}

func TestPanelCompletenessHeadAdvanceParks(t *testing.T) {
	panel := panelEvidence([]string{"codex"}, 1, "old")
	v := panelVerdict(t, panel, Subject{Repo: "o/r", Number: 1, HeadSHA: "new"})
	if v.Decision != DecisionEscalate || !strings.Contains(v.Why, "exact judged head") {
		t.Fatalf("decision=%s why=%s", v.Decision, v.Why)
	}
}

func panelEvidence(required []string, mask int, head string) reviewpanel.Evidence {
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Revision: "blob", Expected: required},
	}
	for i, name := range required {
		if mask&(1<<i) != 0 {
			panel.Completed = append(panel.Completed, reviewpanel.Reviewer{
				Name: name, Actor: name + "[bot]", State: "COMMENTED",
				HeadSHA: head, ReviewID: int64(i + 1),
			})
			continue
		}
		panel.Missing = append(panel.Missing, name)
	}
	return panel
}

func panelEvidenceStates(required []string, permutation int, head string) reviewpanel.Evidence {
	panel := reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: head},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Revision: "blob", Expected: required},
	}
	for i, name := range required {
		state := permutation % 4
		permutation /= 4
		switch state {
		case 0:
			panel.Completed = append(panel.Completed, reviewpanel.Reviewer{
				Name: name, Actor: name + "[bot]", State: "COMMENTED",
				HeadSHA: head, ReviewID: int64(i + 1),
			})
		case 1:
			panel.Pending = append(panel.Pending, name)
		case 2:
			panel.Missing = append(panel.Missing, name)
		case 3:
			panel.Unknown = append(panel.Unknown, name)
		}
	}
	return panel
}

func panelVerdict(t *testing.T, panel reviewpanel.Evidence, subject Subject) Verdict {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := st.Append(state.KindEvidence, "run_t", nil, panel)
	if err != nil {
		t.Fatal(err)
	}
	art, err := PanelCompleteness(st, "run_t", ev.ID, subject)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
