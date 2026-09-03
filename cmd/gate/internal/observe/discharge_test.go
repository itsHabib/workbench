package observe

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// judgmentOf builds a judgment parented to the escalation it answers. The parent
// link IS the record of the answer, so a fixture without it is not a judged park.
func judgmentOf(run, id, escalation string, at time.Time) state.Artifact {
	a := art(state.KindJudgment, run, id, at, map[string]any{"decision": "pass"})
	a.Parents = []string{escalation}
	return a
}

// TestParkDischargeClassifiesEveryPark pins the audit metric's classes and,
// crucially, their PRECEDENCE. A park that was judged is credited to judgment
// even though a later run also displaced it: crediting it to supersession would
// understate how well the loop is working, and the whole point of the ratio is
// that it is read as a health signal.
func TestParkDischargeClassifiesEveryPark(t *testing.T) {
	arts := []state.Artifact{
		// widget#7: parked, judged, then re-parked and still live.
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "q1", "", "o/widget", 7)),
		judgmentOf("run_1", "jdg_1", "esc_1", inboxBase.Add(time.Minute)),
		art(state.KindAction, "run_1", "act_1", inboxBase.Add(2*time.Minute), map[string]any{"outcome": "blocked", "subject": map[string]any{"repo": "o/widget", "number": 7}}),
		art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(3*time.Minute), esc("grt_a", "q2", "", "o/widget", 7)),

		// api#12: parked and never answered; a later run overtook it.
		art(state.KindEscalation, "run_3", "esc_3", inboxBase.Add(4*time.Minute), esc("grt_a", "q3", "", "o/api", 12)),
		art(state.KindAction, "run_4", "act_4", inboxBase.Add(5*time.Minute), map[string]any{"outcome": "blocked", "subject": map[string]any{"repo": "o/api", "number": 12}}),

		// docs#3: parked, nothing superseded it, and the PR has since closed.
		art(state.KindEscalation, "run_5", "esc_5", inboxBase.Add(6*time.Minute), esc("grt_a", "q5", "", "o/docs", 3)),
		art(state.KindSubjectClosed, "run_5", "sbc_5", inboxBase.Add(7*time.Minute), map[string]any{"repo": "o/docs", "number": 3, "state": ClosedNotOpen}),

		// A park with no resolvable subject: no supersession claim is possible.
		art(state.KindEscalation, "run_6", "esc_6", inboxBase.Add(8*time.Minute), map[string]any{"outcome": "parked_for_judgment", "question": "orphan"}),
	}

	got := ParkDischargeReport(arts)

	want := ParkDischarge{Total: 5, ByJudgment: 1, BySupersession: 1, Moot: 1, Live: 1, Unattributed: 1}
	if got != want {
		t.Fatalf("park discharge = %+v, want %+v", got, want)
	}
}

// TestParkDischargeAgreesWithTheInbox pins the shared-reduction property that
// motivated extracting the fold: the metric's Live count and the inbox's parked
// count are the same number, derived from the same fold. They used to be
// independent derivations, which is how they drifted.
func TestParkDischargeAgreesWithTheInbox(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "q1", "", "o/widget", 7)),
		art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(time.Minute), esc("grt_a", "q2", "", "o/widget", 7)),
		art(state.KindEscalation, "run_3", "esc_3", inboxBase.Add(2*time.Minute), esc("grt_a", "q3", "", "o/api", 12)),
		art(state.KindEscalation, "run_4", "esc_4", inboxBase.Add(3*time.Minute), esc("grt_a", "q4", "", "o/docs", 3)),
		art(state.KindSubjectClosed, "run_4", "sbc_4", inboxBase.Add(4*time.Minute), map[string]any{"repo": "o/docs", "number": 3, "state": ClosedNotOpen}),
	}

	rep := ParkDischargeReport(arts)
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{})

	if rep.Live != len(in.Parked) {
		t.Fatalf("metric Live=%d but inbox shows %d parked — the two reductions disagree", rep.Live, len(in.Parked))
	}
	if rep.Moot != in.Discharged.Parked.Moot {
		t.Fatalf("metric Moot=%d but inbox counted %d", rep.Moot, in.Discharged.Parked.Moot)
	}
}

// TestRenderParkDischargeStaysQuietOnAnEmptyLog pins that audit says nothing
// when there is nothing to say. A ratio over zero parks is noise, and an audit
// that prints noise trains the reader to skim past its real findings.
func TestRenderParkDischargeStaysQuietOnAnEmptyLog(t *testing.T) {
	var buf bytes.Buffer
	RenderParkDischarge(&buf, ParkDischargeReport(nil))
	if buf.Len() != 0 {
		t.Fatalf("want no output for a log with no parks, got %q", buf.String())
	}
}

// TestRenderParkDischargeReportsTheRatio pins the line the operator reads.
func TestRenderParkDischargeReportsTheRatio(t *testing.T) {
	var buf bytes.Buffer
	RenderParkDischarge(&buf, ParkDischarge{Total: 10, ByJudgment: 6, BySupersession: 3, Moot: 1})

	out := buf.String()
	for _, want := range []string{"6 by judgment", "3 by supersession", "1 moot", "supersession share 30%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("audit line missing %q:\n%s", want, out)
		}
	}
}
