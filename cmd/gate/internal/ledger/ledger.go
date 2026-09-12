// Package ledger closes the loop between what gate AUTHORIZED and what actually
// LANDED.
//
// Gate's log answers "was this merge authorized" completely and answers "did it
// merge" not at all: every action artifact ever written carries dry_run and the
// outcome would_merge, because gate authorizes and an executor acts. So the log
// can prove a merge was allowed and cannot prove one happened — and, worse,
// cannot prove the negative: that nothing merged AROUND the gate.
//
// Two artifacts close that. A RECEIPT discharges one action with the landing it
// produced, read back from GitHub so the merge commit, the actor, and the clock
// are the platform's facts rather than the executor's claim. A COVERAGE record
// is the periodic join over a window: what was authorized and landed, what was
// authorized and never landed, and what landed with no authorization at all.
//
// Policy only. Every GitHub read is injected, so the classification is pure and
// testable and this package never learns how to shell gh. It also never merges
// anything: gate authorizes, an executor acts, a receipt records. Collapsing
// those would put the decision and the effect in one process, which is the
// separation the whole design rests on.
package ledger

import (
	"errors"
	"fmt"
	"time"
)

// SchemaVersion tags both bodies. They are gate-internal today — gate is the
// only writer and the only reader — so they deliberately do NOT live in
// contracts/ yet. The promotion trigger is the repo's lazy-migration rule: the
// first time a SECOND tool writes or reads one of these, it earns a contracts
// package with an embedded schema, exactly as escalation.V1 did.
const (
	ReceiptVersion  = "receipt.v1"
	CoverageVersion = "coverage.v1"
)

// Landing outcomes — what became of an authorization.
const (
	// OutcomeMerged: the PR merged at the exact head the action authorized.
	OutcomeMerged = "merged"
	// OutcomeSuperseded: the PR merged, but at a DIFFERENT head than the one
	// authorized. The merge happened; this authorization did not cover it. It is
	// not an executor error — a push between authorization and merge produces it,
	// and --match-head-commit is what makes the pinned command refuse rather than
	// land unverified code — but it must never be recorded as a clean discharge.
	OutcomeSuperseded = "superseded"
	// OutcomeFailed: the pinned merge command ran and did not land the PR.
	OutcomeFailed = "failed"
	// OutcomeAbandoned: the PR closed without merging, so the authorization will
	// never be discharged by a landing.
	OutcomeAbandoned = "abandoned"
)

// Receipt records what became of one authorization. It is parented to the action
// it discharges, and the store's absent-parent guard is what makes "a receipt
// discharges exactly one action" structural rather than a convention.
type Receipt struct {
	SchemaVersion string `json:"schema_version"`
	Run           string `json:"run"`
	Repo          string `json:"repo"`
	Number        int    `json:"number"`
	// Action is the id of the action artifact this receipt discharges. It is also
	// the artifact's unique parent, so the id appears twice on purpose: the body
	// stays self-contained for a reader that has only the body.
	Action string `json:"action"`
	// AuthorizedHead is the exact commit the action authorized.
	AuthorizedHead string `json:"authorized_head"`
	Outcome        string `json:"outcome"`
	// LandedHead is the PR's head at merge time, present when it differs from
	// AuthorizedHead — the fact that makes a superseded landing legible.
	LandedHead string `json:"landed_head,omitempty"`
	// MergeCommit, Actor, and MergedAt are read back from the GitHub API, never
	// supplied by the caller. That is deliberate: it gives the record an
	// INDEPENDENT clock and an independent actor. An executor reporting its own
	// merge time and its own identity would be attesting to its own behavior,
	// which is precisely the claim a receipt exists to check. MergedAt is
	// GitHub's timestamp, not gate's, and the two can legitimately differ.
	MergeCommit string `json:"merge_commit,omitempty"`
	Actor       string `json:"actor,omitempty"`
	MergedAt    string `json:"merged_at,omitempty"`
	// Source names what wrote this receipt: the executor discharging an action it
	// just ran, or reconcile sweeping up an action nobody discharged. A ledger
	// where every receipt came from reconciliation is a ledger whose executors
	// are not reporting, and that is worth being able to see.
	Source string `json:"source"`
	// Why annotates a non-merged outcome. Required for OutcomeFailed, which is
	// the one outcome GitHub cannot explain on its own.
	Why        string `json:"why,omitempty"`
	RecordedAt string `json:"recorded_at"`
}

// Landing is what GitHub reports about one pull request. It is the injected
// mechanism's return shape, so this package reads facts and never fetches them.
type Landing struct {
	Repo   string
	Number int
	// State is GitHub's PR state: OPEN, MERGED, or CLOSED.
	State       string
	Merged      bool
	HeadSHA     string
	MergeCommit string
	Actor       string
	MergedAt    time.Time
	BaseRef     string
}

// Authorization is one action artifact reduced to what the join needs.
type Authorization struct {
	Action  string
	Run     string
	Repo    string
	Number  int
	Head    string
	Outcome string
	At      time.Time
	// Discharged is true when a receipt already names this action.
	Discharged bool
}

// ErrNotLanded is returned when a receipt is requested for an authorization
// whose PR is still open. There is nothing to record yet, and inventing an
// outcome for it would be the fabrication the receipt exists to prevent.
var ErrNotLanded = errors.New("receipt_not_landed")

// ErrNotAuthorizing is returned when the named run's newest action is not an
// authorization. A block or a refusal has no landing to discharge.
var ErrNotAuthorizing = errors.New("receipt_no_authorization")

// authorizingOutcomes are the action outcomes that authorize a merge. dry-run
// would_merge is included because it IS the live authorization today: gate emits
// the pinned command and an executor runs it.
var authorizingOutcomes = map[string]bool{
	"would_merge":           true,
	"merge_not_implemented": true,
	"merged":                true,
	"already_merged":        true,
}

// Authorizes reports whether an action outcome authorizes a merge.
func Authorizes(outcome string) bool { return authorizingOutcomes[outcome] }

// NewReceipt classifies one authorization against what GitHub says landed. It is
// the whole policy of the receipt verb, kept pure so the classification is
// testable without a network.
//
// The comparison that matters is head-to-head, not "did it merge": a PR that
// merged at a head this action never saw is a merge that happened outside this
// authorization, and recording it as a clean discharge would launder exactly the
// gap the receipt exists to expose.
func NewReceipt(auth Authorization, landing Landing, source, why string, now func() time.Time) (Receipt, error) {
	if !Authorizes(auth.Outcome) {
		return Receipt{}, fmt.Errorf("%w: action %s recorded %q", ErrNotAuthorizing, auth.Action, auth.Outcome)
	}
	r := Receipt{
		SchemaVersion:  ReceiptVersion,
		Run:            auth.Run,
		Repo:           auth.Repo,
		Number:         auth.Number,
		Action:         auth.Action,
		AuthorizedHead: auth.Head,
		Source:         source,
		Why:            why,
		RecordedAt:     now().UTC().Format(time.RFC3339),
	}
	outcome, err := classify(auth, landing, why)
	if err != nil {
		return Receipt{}, err
	}
	r.Outcome = outcome
	if !landing.Merged {
		return r, nil
	}
	r.MergeCommit = landing.MergeCommit
	r.Actor = landing.Actor
	if !landing.MergedAt.IsZero() {
		r.MergedAt = landing.MergedAt.UTC().Format(time.RFC3339)
	}
	if landing.HeadSHA != auth.Head {
		r.LandedHead = landing.HeadSHA
	}
	return r, nil
}

// classify maps a landing onto an outcome. An open PR yields ErrNotLanded unless
// the caller states that the merge command ran and failed — the one fact GitHub
// cannot report, because a command that never landed leaves no trace on the PR.
func classify(auth Authorization, landing Landing, why string) (string, error) {
	if landing.Merged {
		if landing.HeadSHA == auth.Head {
			return OutcomeMerged, nil
		}
		return OutcomeSuperseded, nil
	}
	if landing.State == "CLOSED" {
		return OutcomeAbandoned, nil
	}
	if why == "" {
		return "", fmt.Errorf("%w: %s#%d is still open; pass -why to record a failed merge attempt",
			ErrNotLanded, auth.Repo, auth.Number)
	}
	return OutcomeFailed, nil
}
