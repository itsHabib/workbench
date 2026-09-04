package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts"
)

// StrandedRun is a run whose judgment was recorded but whose authorization was
// not: a KindJudgment artifact exists, and the run's newest terminal is still
// the escalation that judgment resolves. A decision was taken and durably
// written, and then nothing followed it.
//
// It is a real, observed state, not a hypothetical. gate appends the judgment,
// the re-reduced verdict, and the action as separate artifacts, and a caller
// that runs gate as a subprocess under its own deadline (escalate's Slack
// ingress) kills it wherever the clock happens to expire. When that lands
// between the judgment and the action, the park still looks parked, the PR is
// still open, and the operator's approval has silently gone nowhere — while the
// Slack card reported success.
//
// The state is RECOVERABLE and always was: gate's judgment path resumes a
// persisted judgment rather than refusing it as a duplicate, re-reduces, and
// re-runs the outcome. What was missing is that nothing SAID so. A parked-run
// row sends the operator to judge a park whose judgment is already spent; this
// row names the state and carries the command that finishes it.
type StrandedRun struct {
	Run        string `json:"run"`
	Escalation string `json:"escalation"`
	Judgment   string `json:"judgment"`
	Repo       string `json:"repo,omitempty"`
	Number     int    `json:"number,omitempty"`
	// Decision is the judgment's recorded decision. The resume must re-assert
	// the SAME one — gate refuses a retry that contradicts what is already in the
	// log — so it is projected here rather than left for the operator to dig out.
	Decision string `json:"decision"`
	// Decider names who decided, or "unattributed" for a judgment recorded before
	// decisions carried one. A stranded decision is exactly when the question
	// "whose approval is this?" gets asked, so the row answers it.
	Decider    string `json:"decider"`
	Grant      string `json:"grant,omitempty"`
	JudgedAt   string `json:"judged_at"`
	ResumeHint string `json:"resume_hint"`
	// ResumeCommand completes the recorded decision. On resume gate loads the
	// PERSISTED judgment — -why and -who are not re-recorded, only -decision is
	// checked against it — so the command reasserts the decision and leaves the
	// original attribution intact. The NAME placeholder therefore does not become
	// the decider: the flag parser only requires it to be non-empty, and whoever
	// decided is already in the log.
	ResumeCommand string `json:"resume_command,omitempty"`
}

// judgmentBody is the slice of a judgment artifact this projection reads. It
// decodes as a verdict because that is what a judgment body is; the projection
// only displays, so it takes the tolerant read and never validates.
type judgmentBody = contracts.Verdict

// strandedRuns finds every run holding a judgment that produced no outcome.
//
// The reduction is deliberately narrow: a judgment exists, and the run's newest
// action-or-escalation is still the escalation that judgment names as a parent.
// A judgment followed by ANY action — would_merge, blocked, capability_refused —
// is not stranded, whatever that action decided.
//
// A run genuinely mid-judgment appears here for the fraction of a second between
// the two appends. That is honest rather than wrong: the log is read under one
// consistent snapshot, and at that instant the run really does hold a judgment
// with no outcome. The row is advisory and the resume is idempotent, so acting
// on a transient one costs nothing.
func strandedRuns(arts []state.Artifact, stateArg string) []StrandedRun {
	newest := make(map[string]state.Artifact)
	judgments := make(map[string]state.Artifact)
	facts := make(map[string]runFacts)
	for _, a := range arts {
		facts[a.Run] = mergeRunFacts(facts[a.Run], factsFromArtifact(a))
		if a.Kind == state.KindAction || a.Kind == state.KindEscalation {
			newest[a.Run] = a
		}
		if a.Kind == state.KindJudgment {
			judgments[a.Run] = a
		}
	}
	out := make([]StrandedRun, 0)
	for run, judgment := range judgments {
		terminal, ok := newest[run]
		if !ok || terminal.Kind != state.KindEscalation {
			continue
		}
		if !hasParent(judgment, terminal.ID) {
			continue
		}
		out = append(out, strandedFrom(run, judgment, terminal, facts[run], stateArg))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].JudgedAt != out[j].JudgedAt {
			return out[i].JudgedAt < out[j].JudgedAt
		}
		return out[i].Run < out[j].Run
	})
	return out
}

func hasParent(a state.Artifact, parent string) bool {
	for _, p := range a.Parents {
		if p == parent {
			return true
		}
	}
	return false
}

// strandedFrom renders one stranded run. Body decoding is best-effort like every
// other projection here: a drifted judgment body still surfaces its run rather
// than vanishing from the one view that would have caught it.
func strandedFrom(run string, judgment, escalation state.Artifact, facts runFacts, stateArg string) StrandedRun {
	var body judgmentBody
	_ = json.Unmarshal(judgment.Body, &body)
	s := StrandedRun{
		Run:        run,
		Escalation: escalation.ID,
		Judgment:   judgment.ID,
		Repo:       facts.Repo,
		Number:     facts.Number,
		Decision:   body.Decision,
		Decider:    deciderLine(body),
		Grant:      grantParent(judgment, escalation.ID),
		JudgedAt:   judgment.Time.UTC().Format("2006-01-02T15:04:05Z"),
		ResumeHint: "the decision is recorded but no outcome followed it; resuming re-reduces and records the authorization",
	}
	if s.Grant != "" && s.Decision != "" {
		s.ResumeCommand = fmt.Sprintf(
			"gate judge%s -run %s -grant %s -decision %s -why %q -who NAME",
			stateArg, run, s.Grant, s.Decision, "resume: complete the recorded judgment",
		)
	}
	return s
}

// deciderLine renders the judgment's attribution, naming the absence rather than
// leaving a blank. It duplicates verify's rendering deliberately: observe is
// read-only and storeless and must not import the package that owns the ladder
// law, so the two share the contract type and nothing else.
func deciderLine(body judgmentBody) string {
	if body.Decider == nil {
		return "unattributed"
	}
	return fmt.Sprintf("%s via %s at %s", body.Decider.Who, body.Decider.Method, body.Decider.At)
}

// grantParent picks the judgment's grant parent — every judgment is appended
// with [escalation, grant], so the non-escalation parent is the grant.
func grantParent(judgment state.Artifact, escalationID string) string {
	for _, p := range judgment.Parents {
		if p != escalationID {
			return p
		}
	}
	return ""
}

// renderStranded lists the runs holding a decision that never became an
// authorization. It is printed BEFORE the parked section: a stranded run also
// looks parked, and sending the operator to judge a park whose judgment is
// already spent is the failure this surface exists to stop.
func renderStranded(w io.Writer, rows []StrandedRun) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "judged but not authorized (%d) — a decision is recorded with no outcome; resume to finish it\n\n", len(rows))
	for _, r := range rows {
		subject := r.Run
		if r.Repo != "" && r.Number > 0 {
			subject = fmt.Sprintf("%s#%d  %s", r.Repo, r.Number, r.Run)
		}
		fmt.Fprintf(w, "  %s\n", subject)
		fmt.Fprintf(w, "    judged %s at %s by %s\n", r.Decision, r.JudgedAt, r.Decider)
		if r.ResumeCommand != "" {
			fmt.Fprintf(w, "    resume: %s\n", r.ResumeCommand)
		}
		fmt.Fprintln(w)
	}
}
