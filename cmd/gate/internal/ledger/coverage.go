package ledger

import (
	"sort"
	"time"
)

// Coverage classes — the three questions a control has to be able to answer
// about itself, plus the one that keeps the answer honest.
const (
	// ClassAuthorizedAndLanded: an action authorized this exact head and the PR
	// merged at it. The control worked.
	ClassAuthorizedAndLanded = "authorized_and_landed"
	// ClassAuthorizedNeverLanded: an authorization no landing ever discharged.
	// Not necessarily a fault — a PR can be superseded or abandoned — but an
	// authorization outstanding forever is an unclosed loop, and the log could
	// not previously see one at all.
	ClassAuthorizedNeverLanded = "authorized_never_landed"
	// ClassLandedWithoutAuthorization: a merge with no action artifact behind it.
	// The bypass. This is the number the whole exercise exists to produce, and
	// gate could not previously produce it at any value, including zero.
	ClassLandedWithoutAuthorization = "landed_without_authorization"
	// ClassPreAdoption: a merge that landed before the control was in force for
	// this repo. Structurally identical to a bypass and morally nothing like one.
	// Reporting the two together would drown a real bypass in history and train
	// the reader to ignore the surface — so they are separated at the source, and
	// the boundary is recorded on the artifact rather than left to a reader's
	// memory of when adoption happened.
	ClassPreAdoption = "pre_adoption"
)

// BasisMergedPullRequests names what the join was computed over, recorded on the artifact so a
// reader knows the boundary of the claim rather than inferring one.
//
// The join is over MERGED PULL REQUESTS, because a PR is what carries the head
// SHA an action is keyed on. A direct push to the protected branch has no PR and
// no head to join by, so it is outside this claim entirely — that is branch
// protection's job, not the ledger's. Stating it is the point: a coverage report
// that quietly omitted direct pushes while implying it covered "everything that
// landed" would be worse than no report.
const BasisMergedPullRequests = "merged-pull-requests"

// Coverage is one reconciliation over one repo and window: the join between what
// gate authorized and what actually landed on the protected branch.
type Coverage struct {
	SchemaVersion string `json:"schema_version"`
	Repo          string `json:"repo"`
	Branch        string `json:"branch"`
	Basis         string `json:"basis"`
	Since         string `json:"since"`
	Until         string `json:"until"`
	// EffectiveFrom is when the control began operating for this repo. Merges
	// before it are classified pre-adoption, never as bypasses.
	EffectiveFrom string `json:"effective_from,omitempty"`
	// EffectiveFromSource is how that boundary was established: "flag" when the
	// operator stated it, "derived" when gate read it from state as the first
	// time it ever ran on this repo. Derived is the default because it needs no
	// config file and no custody: "the control took effect when the control first
	// ran" is a fact the log already holds, and one nobody has to remember.
	EffectiveFromSource string `json:"effective_from_source,omitempty"`

	AuthorizedAndLanded        []CoverageRow `json:"authorized_and_landed"`
	AuthorizedNeverLanded      []CoverageRow `json:"authorized_never_landed"`
	LandedWithoutAuthorization []CoverageRow `json:"landed_without_authorization"`
	PreAdoption                []CoverageRow `json:"pre_adoption"`
	RecordedAt                 string        `json:"recorded_at"`
}

// CoverageRow is one PR in one class.
type CoverageRow struct {
	Number      int    `json:"number"`
	Head        string `json:"head,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	MergedAt    string `json:"merged_at,omitempty"`
	Actor       string `json:"actor,omitempty"`
	// Action and Run name the authorization, when there is one.
	Action string `json:"action,omitempty"`
	Run    string `json:"run,omitempty"`
	// Receipt names the receipt discharging this action, when one exists. Its
	// absence on an authorized-and-landed row is the authorization-without-receipt
	// anomaly: the merge is provably fine, but nothing wrote it back, so the
	// ledger learned it from a sweep instead of from the executor.
	Receipt string `json:"receipt,omitempty"`
	Why     string `json:"why,omitempty"`
}

// Window bounds a reconciliation.
type Window struct {
	Since time.Time
	Until time.Time
}

// Reconcile joins landings to authorizations and classifies each side. It is
// pure: the caller fetches the landings and reads the authorizations from state,
// and everything here is a decision over those two lists.
//
// The join key is (number, head). Number alone would call a merge authorized
// because SOME earlier run once authorized that PR at a head that is not what
// landed — the exact laundering --match-head-commit exists to prevent, rebuilt
// one layer up in the reporting.
func Reconcile(repo, branch string, w Window, effectiveFrom time.Time, effectiveSource string, landings []Landing, auths []Authorization, receipts map[string]string, now func() time.Time) Coverage {
	c := Coverage{
		SchemaVersion:              CoverageVersion,
		Repo:                       repo,
		Branch:                     branch,
		Basis:                      BasisMergedPullRequests,
		Since:                      w.Since.UTC().Format(time.RFC3339),
		Until:                      w.Until.UTC().Format(time.RFC3339),
		EffectiveFromSource:        effectiveSource,
		AuthorizedAndLanded:        []CoverageRow{},
		AuthorizedNeverLanded:      []CoverageRow{},
		LandedWithoutAuthorization: []CoverageRow{},
		PreAdoption:                []CoverageRow{},
		RecordedAt:                 now().UTC().Format(time.RFC3339),
	}
	if !effectiveFrom.IsZero() {
		c.EffectiveFrom = effectiveFrom.UTC().Format(time.RFC3339)
	}

	byHead := make(map[headKey]Authorization, len(auths))
	for _, a := range auths {
		if !Authorizes(a.Outcome) {
			continue
		}
		byHead[headKey{a.Number, a.Head}] = a
	}

	matched := make(map[string]bool, len(landings))
	for _, l := range landings {
		row := landingRow(l)
		auth, ok := byHead[headKey{l.Number, l.HeadSHA}]
		if ok {
			matched[auth.Action] = true
			row.Action, row.Run, row.Receipt = auth.Action, auth.Run, receipts[auth.Action]
			c.AuthorizedAndLanded = append(c.AuthorizedAndLanded, row)
			continue
		}
		// The boundary is on merged_at, not on the reconcile's own clock: a merge
		// is pre-adoption if it happened before the control was in force, whenever
		// anyone gets around to asking.
		if !effectiveFrom.IsZero() && l.MergedAt.Before(effectiveFrom) {
			row.Why = "merged before the control was in force for this repo"
			c.PreAdoption = append(c.PreAdoption, row)
			continue
		}
		row.Why = "no action artifact authorizes this repo#number at this head"
		c.LandedWithoutAuthorization = append(c.LandedWithoutAuthorization, row)
	}

	for _, a := range auths {
		if !Authorizes(a.Outcome) || matched[a.Action] {
			continue
		}
		if a.At.Before(w.Since) || a.At.After(w.Until) {
			continue
		}
		c.AuthorizedNeverLanded = append(c.AuthorizedNeverLanded, CoverageRow{
			Number: a.Number, Head: a.Head, Action: a.Action, Run: a.Run,
			Receipt: receipts[a.Action],
			Why:     "authorized, with no merge at this head in the window",
		})
	}

	sortRows(c.AuthorizedAndLanded)
	sortRows(c.AuthorizedNeverLanded)
	sortRows(c.LandedWithoutAuthorization)
	sortRows(c.PreAdoption)
	return c
}

type headKey struct {
	number int
	head   string
}

func landingRow(l Landing) CoverageRow {
	row := CoverageRow{
		Number:      l.Number,
		Head:        l.HeadSHA,
		MergeCommit: l.MergeCommit,
		Actor:       l.Actor,
	}
	if !l.MergedAt.IsZero() {
		row.MergedAt = l.MergedAt.UTC().Format(time.RFC3339)
	}
	return row
}

func sortRows(rows []CoverageRow) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Number < rows[j].Number })
}

// Anomalies is the pair of findings a coverage record surfaces to audit. They
// are counted separately because they mean opposite things: one says the control
// ran and nobody wrote back, the other says the control did not run at all.
type Anomalies struct {
	// AuthorizationWithoutReceipt: authorized and landed, but no receipt
	// discharges the action. The merge is accounted for; the writeback is not.
	AuthorizationWithoutReceipt []CoverageRow
	// MergeWithoutAuthorization: landed with no authorization behind it, and not
	// pre-adoption. The bypass.
	MergeWithoutAuthorization []CoverageRow
}

// Findings extracts the two anomalies from a coverage record. Pre-adoption rows
// are deliberately absent: they are history, and reporting history as a finding
// is how a real finding gets ignored.
func (c Coverage) Findings() Anomalies {
	var a Anomalies
	for _, row := range c.AuthorizedAndLanded {
		if row.Receipt == "" {
			a.AuthorizationWithoutReceipt = append(a.AuthorizationWithoutReceipt, row)
		}
	}
	a.MergeWithoutAuthorization = append(a.MergeWithoutAuthorization, c.LandedWithoutAuthorization...)
	return a
}
