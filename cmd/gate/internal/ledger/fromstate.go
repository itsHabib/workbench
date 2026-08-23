package ledger

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// actionBody is the slice of an action artifact the ledger reads. Deliberately a
// small local copy rather than an import of the writer's map: the ledger only
// joins actions to landings, so it stays decoupled from whatever else an action
// body grows.
type actionBody struct {
	Outcome string `json:"outcome"`
}

// receiptBody is the slice of a receipt this package re-reads when indexing.
type receiptBody struct {
	Action string `json:"action"`
}

// subjectBody covers the two shapes a run's PR subject appears in across
// artifact kinds — a verdict's nested subject and an escalation's flat pair.
type subjectBody struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	Subject struct {
		Repo    string `json:"repo"`
		Number  int    `json:"number"`
		HeadSHA string `json:"head_sha"`
	} `json:"subject"`
}

// Authorizations reads every action artifact that authorizes a merge, resolving
// each one's PR subject and exact head from its own run.
//
// The head comes from the run's REDUCED VERDICT, not from the action body, and
// that is load-bearing: the action carries the pinned merge argv, but the head
// gate actually judged travels on the verdict's subject. Joining on anything
// else would let a report claim a merge was authorized at a commit gate never
// read.
func Authorizations(arts []state.Artifact, repo string) []Authorization {
	subjects := runSubjects(arts)
	out := make([]Authorization, 0)
	for _, a := range arts {
		if a.Kind != state.KindAction {
			continue
		}
		var body actionBody
		if err := json.Unmarshal(a.Body, &body); err != nil {
			continue
		}
		if !Authorizes(body.Outcome) {
			continue
		}
		s := subjects[a.Run]
		if s.Repo != repo || s.Number == 0 || s.HeadSHA == "" {
			continue
		}
		out = append(out, Authorization{
			Action: a.ID, Run: a.Run, Repo: s.Repo, Number: s.Number,
			Head: s.HeadSHA, Outcome: body.Outcome, At: a.Time.UTC(),
		})
	}
	return out
}

// Receipts indexes existing receipts by the action each discharges, so a caller
// can tell an undischarged authorization from a discharged one without a second
// scan.
func Receipts(arts []state.Artifact) map[string]string {
	out := make(map[string]string)
	for _, a := range arts {
		if a.Kind != state.KindReceipt {
			continue
		}
		var body receiptBody
		if err := json.Unmarshal(a.Body, &body); err == nil && body.Action != "" {
			out[body.Action] = a.ID
			continue
		}
		// A body that will not parse still discharges its parent action: the
		// parent link is the structural fact, the body is the description.
		for _, p := range a.Parents {
			out[p] = a.ID
		}
	}
	return out
}

// runSubject is one run's PR subject, folded from whichever artifacts carry it.
type runSubject struct {
	Repo    string
	Number  int
	HeadSHA string
}

// runSubjects folds each run's repo, number, and judged head from its artifacts.
// Later artifacts win on the head, because a run's reduced verdict — the last
// thing written before the action — carries the head the decision was made
// against.
func runSubjects(arts []state.Artifact) map[string]runSubject {
	out := make(map[string]runSubject)
	for _, a := range arts {
		var body subjectBody
		if err := json.Unmarshal(a.Body, &body); err != nil {
			continue
		}
		s := out[a.Run]
		s.Repo = firstNonEmpty(body.Subject.Repo, body.Repo, s.Repo)
		s.Number = firstNonZero(body.Subject.Number, body.Number, s.Number)
		if body.Subject.HeadSHA != "" {
			s.HeadSHA = body.Subject.HeadSHA
		}
		out[a.Run] = s
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// FindAuthorization returns the newest authorizing action for a run, which is
// what a receipt discharges. A run whose newest action is a block or a refusal
// has nothing to discharge and is refused by name.
func FindAuthorization(arts []state.Artifact, run string) (Authorization, error) {
	// Keyed on the run, not the repo: a receipt discharges one run's action, and
	// that run's subject is whatever it is.
	subjects := runSubjects(arts)
	var newest state.Artifact
	var body actionBody
	for _, a := range arts {
		if a.Kind != state.KindAction || a.Run != run {
			continue
		}
		var b actionBody
		if err := json.Unmarshal(a.Body, &b); err != nil {
			continue
		}
		newest, body = a, b
	}
	if newest.ID == "" {
		return Authorization{}, fmt.Errorf("%w: run %s recorded no action", ErrNotAuthorizing, run)
	}
	if !Authorizes(body.Outcome) {
		return Authorization{}, fmt.Errorf("%w: run %s recorded %q, which authorizes no merge", ErrNotAuthorizing, run, body.Outcome)
	}
	s := subjects[run]
	if s.Repo == "" || s.Number == 0 || s.HeadSHA == "" {
		return Authorization{}, fmt.Errorf("%w: run %s has no resolvable PR subject and exact head", ErrNotAuthorizing, run)
	}
	return Authorization{
		Action: newest.ID, Run: run, Repo: s.Repo, Number: s.Number,
		Head: s.HeadSHA, Outcome: body.Outcome, At: newest.Time.UTC(),
	}, nil
}

// DerivedEffectiveFrom is when the control began operating for a repo, read from
// state as the earliest artifact naming it.
//
// This is the default boundary because it needs no config file, no new custody
// surface, and nothing for anyone to remember: "the control took effect for this
// repo when the control first ran on it" is a fact the log already holds, and it
// cannot drift out of date the way a hand-maintained date would. An operator who
// needs a different boundary states one; that is the only case worth a flag.
func DerivedEffectiveFrom(arts []state.Artifact, repo string) (time.Time, bool) {
	var earliest time.Time
	for _, a := range arts {
		var body subjectBody
		if err := json.Unmarshal(a.Body, &body); err != nil {
			continue
		}
		if body.Repo != repo && body.Subject.Repo != repo {
			continue
		}
		if earliest.IsZero() || a.Time.Before(earliest) {
			earliest = a.Time.UTC()
		}
	}
	return earliest, !earliest.IsZero()
}
