package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// AuditFindings are the accountability anomalies an audit reports alongside the
// chain check. The chain answers "was this log tampered with"; these answer "does
// this log account for what happened" — a different question, and the one the
// ledger could not previously ask at all.
//
// They are FINDINGS, not integrity failures, and the audit's exit code does not
// change for them. A tampered chain means the record cannot be trusted; an
// unreceipted authorization means the record is trustworthy and incomplete, and
// collapsing the two would make every operator's first audit look like an
// attack.
type AuditFindings struct {
	// AuthorizationWithoutReceipt: an authorized merge with nothing written back.
	// The count is the headline — with no receipt verb in use it is every
	// authorization ever recorded, which is the gap stated as a number.
	AuthorizationWithoutReceipt []AnomalyRow `json:"authorization_without_receipt,omitempty"`
	// MergeWithoutAuthorization: a merge with no action behind it, excluding
	// pre-adoption. Read from the newest coverage record per repo, because audit
	// is storeless and read-only and cannot call GitHub — the platform read
	// belongs to reconcile, and audit reports what reconcile recorded.
	MergeWithoutAuthorization []AnomalyRow `json:"merge_without_authorization,omitempty"`
	// UnattributedJudgments: a decision naming nobody. Recorded here because it is
	// the same class of question — can this log account for who did what — and
	// because it is the one anomaly answerable from artifacts alone.
	UnattributedJudgments []AnomalyRow `json:"unattributed_judgments,omitempty"`
	// ReconciledRepos names the repos whose coverage is being reported, and
	// StaleRepos those whose newest coverage predates the newest authorization —
	// a stale sweep whose "0 bypasses" answers a question about last week.
	ReconciledRepos []string `json:"reconciled_repos,omitempty"`
	StaleRepos      []string `json:"stale_repos,omitempty"`
}

// AnomalyRow is one finding, named by whatever identifies it.
type AnomalyRow struct {
	Artifact string `json:"artifact,omitempty"`
	Run      string `json:"run,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Number   int    `json:"number,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Empty reports whether the audit found nothing to say.
func (f AuditFindings) Empty() bool {
	return len(f.AuthorizationWithoutReceipt) == 0 &&
		len(f.MergeWithoutAuthorization) == 0 &&
		len(f.UnattributedJudgments) == 0
}

// sweepBody is the slice of a coverage artifact the audit reads. A local copy,
// like every other projection here: observe renders and never imports the policy
// layer that decides.
type sweepBody struct {
	Repo       string `json:"repo"`
	RecordedAt string `json:"recorded_at"`
	// Only the bypass class is read here. Authorization-without-receipt is
	// derived from the log directly rather than from a sweep, so it stays true
	// between reconciles instead of being as old as the last one.
	LandedWithoutAuthorization []sweepRowBody `json:"landed_without_authorization"`
}

type sweepRowBody struct {
	Number int    `json:"number"`
	Head   string `json:"head"`
	Actor  string `json:"actor"`
}

// Audit derives the accountability findings from the artifact log alone.
func Audit(arts []state.Artifact) AuditFindings {
	var f AuditFindings
	f.AuthorizationWithoutReceipt = undischargedActions(arts)
	f.UnattributedJudgments = unattributedJudgments(arts)
	f.MergeWithoutAuthorization, f.ReconciledRepos, f.StaleRepos = sweepFindings(arts)
	return f
}

// undischargedActions finds authorizations no receipt discharges. The join is on
// the parent link, which is the structural fact — a receipt is APPENDED against
// its action, so a body that drifted still discharges what it is parented to.
func undischargedActions(arts []state.Artifact) []AnomalyRow {
	discharged := make(map[string]bool)
	for _, a := range arts {
		if a.Kind != state.KindReceipt {
			continue
		}
		for _, p := range a.Parents {
			discharged[p] = true
		}
	}
	subjects := runFactsByRun(arts)
	rows := make([]AnomalyRow, 0)
	for _, a := range arts {
		if a.Kind != state.KindAction || discharged[a.ID] {
			continue
		}
		var body struct {
			Outcome string `json:"outcome"`
		}
		if err := json.Unmarshal(a.Body, &body); err != nil || !authorizingOutcome(body.Outcome) {
			continue
		}
		s := subjects[a.Run]
		rows = append(rows, AnomalyRow{
			Artifact: a.ID, Run: a.Run, Repo: s.Repo, Number: s.Number,
			Detail: "authorized " + body.Outcome + " with no receipt recording what landed",
		})
	}
	return rows
}

// authorizingOutcome duplicates the ledger's small vocabulary rather than
// importing it: observe is a read-only projection and must not depend on the
// package that decides what an authorization is. Four strings copied is cheaper
// than a dependency pointing the wrong way.
func authorizingOutcome(outcome string) bool {
	switch outcome {
	case "would_merge", "merge_not_implemented", "merged", "already_merged":
		return true
	}
	return false
}

// unattributedJudgments finds decisions naming nobody.
func unattributedJudgments(arts []state.Artifact) []AnomalyRow {
	subjects := runFactsByRun(arts)
	rows := make([]AnomalyRow, 0)
	for _, a := range arts {
		if a.Kind != state.KindJudgment {
			continue
		}
		var body judgmentBody
		if err := json.Unmarshal(a.Body, &body); err != nil || body.Decider != nil {
			continue
		}
		s := subjects[a.Run]
		rows = append(rows, AnomalyRow{
			Artifact: a.ID, Run: a.Run, Repo: s.Repo, Number: s.Number,
			Detail: "judged " + body.Decision + " with no decider recorded",
		})
	}
	return rows
}

// sweepFindings reads the NEWEST coverage record per repo and reports its
// bypasses, plus which repos' sweeps are stale.
//
// Only the newest counts: an older sweep's findings were either fixed or are
// restated by the newer one, and reporting every historical sweep would multiply
// one bypass by however many times anyone has reconciled since.
func sweepFindings(arts []state.Artifact) (rows []AnomalyRow, reconciled, stale []string) {
	newest := make(map[string]sweepBody)
	newestAt := make(map[string]string)
	lastAuth := make(map[string]string)
	subjects := runFactsByRun(arts)
	for _, a := range arts {
		if a.Kind == state.KindAction {
			s := subjects[a.Run]
			if s.Repo != "" {
				lastAuth[s.Repo] = a.Time.UTC().Format("2006-01-02T15:04:05Z")
			}
			continue
		}
		if a.Kind != state.KindCoverage {
			continue
		}
		var body sweepBody
		if err := json.Unmarshal(a.Body, &body); err != nil || body.Repo == "" {
			continue
		}
		if at, ok := newestAt[body.Repo]; ok && body.RecordedAt < at {
			continue
		}
		newest[body.Repo], newestAt[body.Repo] = body, body.RecordedAt
	}
	rows = make([]AnomalyRow, 0)
	for repo, body := range newest {
		reconciled = append(reconciled, repo)
		if at, ok := lastAuth[repo]; ok && at > body.RecordedAt {
			stale = append(stale, repo)
		}
		for _, r := range body.LandedWithoutAuthorization {
			rows = append(rows, AnomalyRow{
				Repo: repo, Number: r.Number,
				Detail: fmt.Sprintf("merged at %s by %s with no authorizing action", shortSHA12(r.Head), orUnknown(r.Actor)),
			})
		}
	}
	sort.Strings(reconciled)
	sort.Strings(stale)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Repo != rows[j].Repo {
			return rows[i].Repo < rows[j].Repo
		}
		return rows[i].Number < rows[j].Number
	})
	return rows, reconciled, stale
}

func shortSHA12(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown actor"
	}
	return s
}

// RenderAudit writes the findings under the chain result. maxRows caps each
// section: with no receipts written yet the first section is every authorization
// ever recorded, and a wall of ids teaches the reader to skip the whole surface.
// The count is the finding; the rows are a sample.
func RenderAudit(w io.Writer, f AuditFindings, maxRows int) {
	if f.Empty() {
		fmt.Fprintln(w, "accountability: no findings")
		return
	}
	section(w, "authorization without receipt", f.AuthorizationWithoutReceipt, maxRows,
		"gate authorized these merges and nothing recorded what landed — run `gate receipt -run <id>` after the pinned merge, or `gate reconcile -repo <r>` to sweep")
	section(w, "merge without authorization", f.MergeWithoutAuthorization, maxRows,
		"these landed on the protected branch with no action artifact behind them (pre-adoption merges excluded)")
	section(w, "unattributed judgments", f.UnattributedJudgments, maxRows,
		"decisions recorded before judgments named a decider; new judgments are refused without one")
	if len(f.ReconciledRepos) == 0 {
		fmt.Fprintln(w, "note: no repo has been reconciled, so merge-without-authorization is UNMEASURED, not zero.")
		fmt.Fprintln(w, "      run `gate reconcile -repo <owner/name>` to measure it.")
		return
	}
	fmt.Fprintf(w, "reconciled repos: %v\n", f.ReconciledRepos)
	if len(f.StaleRepos) > 0 {
		fmt.Fprintf(w, "STALE coverage (authorizations recorded since the last sweep): %v\n", f.StaleRepos)
	}
}

func section(w io.Writer, title string, rows []AnomalyRow, maxRows int, hint string) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s (%d)\n", title, len(rows))
	fmt.Fprintf(w, "  %s\n", hint)
	for i, r := range rows {
		if i >= maxRows {
			fmt.Fprintf(w, "  ... and %d more\n", len(rows)-maxRows)
			break
		}
		fmt.Fprintf(w, "  %s\n", anomalyLine(r))
	}
}

func anomalyLine(r AnomalyRow) string {
	subject := r.Artifact
	if r.Repo != "" && r.Number > 0 {
		subject = fmt.Sprintf("%s#%d", r.Repo, r.Number)
		if r.Artifact != "" {
			subject += "  " + r.Artifact
		}
	}
	if r.Detail == "" {
		return subject
	}
	return subject + " — " + r.Detail
}

// runFactsByRun folds each run's PR subject from its artifacts, reusing the
// inbox's own fact merge so the two views cannot disagree about which PR a run
// is about.
func runFactsByRun(arts []state.Artifact) map[string]runFacts {
	out := make(map[string]runFacts, len(arts))
	for _, a := range arts {
		out[a.Run] = mergeRunFacts(out[a.Run], factsFromArtifact(a))
	}
	return out
}
