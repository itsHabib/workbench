package verify

import (
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

// refreshPanel is a panel whose only reviewer reviewed "old", declared
// diff-equivalent to the judged head "new".
func refreshPanel() reviewpanel.Evidence {
	return reviewpanel.Evidence{
		SchemaVersion: 1,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 1, HeadSHA: "new"},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Revision: "blob", Expected: []string{"codex"}},
		Completed: []reviewpanel.Reviewer{{
			Name: "codex", Actor: "codex[bot]", State: "COMMENTED",
			HeadSHA: "old", ReviewID: 1,
		}},
		Equivalence: &reviewpanel.Equivalence{ReviewedHeadSHA: "old", DiffDigest: "sha256:abc"},
	}
}

func TestPanelCompletenessDiffEquivalentRefreshPasses(t *testing.T) {
	v := panelVerdict(t, refreshPanel(), Subject{Repo: "o/r", Number: 1, HeadSHA: "new"})
	if v.Decision != DecisionPass {
		t.Fatalf("decision=%s why=%s", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "diff-equivalent refresh") || !strings.Contains(v.Why, "1 carried from old") {
		t.Fatalf("verdict does not state the carry-forward: %s", v.Why)
	}
}

func TestPanelCompletenessRefreshParksWhenUnused(t *testing.T) {
	panel := refreshPanel()
	panel.Completed[0].HeadSHA = "new"
	v := panelVerdict(t, panel, Subject{Repo: "o/r", Number: 1, HeadSHA: "new"})
	if v.Decision != DecisionEscalate || !strings.Contains(v.Why, "no completed reviewer relies on") {
		t.Fatalf("decision=%s why=%s", v.Decision, v.Why)
	}
}

func TestPanelCompletenessRefreshParksOnUnreadableDigest(t *testing.T) {
	panel := refreshPanel()
	panel.Equivalence.DiffDigest = "md5:abc"
	v := panelVerdict(t, panel, Subject{Repo: "o/r", Number: 1, HeadSHA: "new"})
	if v.Decision != DecisionEscalate || !strings.Contains(v.Why, "unreadable diff digest") {
		t.Fatalf("decision=%s why=%s", v.Decision, v.Why)
	}
}

// A refresh licenses only the reviewers it actually carries. One still absent
// at both heads parks exactly as before — the cap-friendly path is "no new
// review cycle for a no-op refresh", never "a refresh clears the panel".
func TestPanelCompletenessRefreshDoesNotClearRemainingReviewers(t *testing.T) {
	panel := refreshPanel()
	panel.Declaration.Expected = []string{"codex", "cursor"}
	panel.Missing = []string{"cursor"}
	v := panelVerdict(t, panel, Subject{Repo: "o/r", Number: 1, HeadSHA: "new"})
	if v.Decision != DecisionEscalate || !strings.Contains(v.Why, "missing=[cursor]") {
		t.Fatalf("decision=%s why=%s", v.Decision, v.Why)
	}
}
