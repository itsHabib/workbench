package observe

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// Discharge names why an inbox row no longer needs the operator. It is the one
// vocabulary the parked projection, the ready-to-merge projection, and the audit
// metric all share.
//
// Sharing it is the point. "Is this park still open" used to be re-derived
// independently by each caller — `cmdResolve`'s pre-check, the locked check, and
// `parkedRuns` — and the three drifted, which is the whole of the follow-up this
// closes. One reduction, three consumers.
type Discharge string

const (
	// DischargeNone is the live case: nothing in the log closes this row, so it
	// is genuinely awaiting the operator.
	DischargeNone Discharge = ""
	// DischargeSuperseded means a LATER terminal for the same subject (repo#PR)
	// already answered the question this row asks. A PR gated more than once
	// produces a run per attempt; only the newest one's terminal is the subject's
	// current state, and the earlier attempts are history, not work.
	DischargeSuperseded Discharge = "superseded"
	// DischargeMoot means the pull request itself is finished — merged or closed
	// — so no judgment against it can change anything. This is the class the
	// inbox could not previously see at all: every action gate writes is
	// dry_run/would_merge, so once the operator ran the emitted merge command and
	// the PR landed, nothing in the log ever said so and the row stood forever.
	DischargeMoot Discharge = "moot"
	// DischargeStale means the pull request is still OPEN but has moved past the
	// head the row's pinned command authorizes, so that command would refuse. It
	// is deliberately not moot: the PR still needs work — a re-gate — and folding
	// it into "finished" would report owed work as done.
	DischargeStale Discharge = "stale"
)

// liveMootWhy explains a row discharged by the live open-PR read. It says what
// gate actually observed — an absence from the repo's OPEN set — rather than
// claiming a merge it never read back.
const liveMootWhy = "the PR is absent from its repo's open set (merged or closed)"

// notOpenState is the PR state a live read can assert from an open-PR list.
const notOpenState = "NOT_OPEN"

// Closing states — what the log knows about a finished subject. They are kept
// distinct from Discharge because the discharge is the same either way and the
// REASON is not: a reader deciding whether to trust the row deserves to know
// whether gate read a merge back or merely observed an absence.
const (
	// ClosedNotOpen is what a batched open-PR read can honestly report: the PR
	// was absent from its repo's OPEN set. That is exactly the predicate the
	// inbox needs — a row is actionable only while its PR is open — but it is
	// NOT a merge, and recording it as one would invent a fact gate never read.
	ClosedNotOpen = "not_open"
	// ClosedMerged is a merge gate read back: an already_merged refusal, or a
	// receipt/coverage record written from the platform's own account.
	ClosedMerged = "merged"
	// ClosedAbandoned is a pull request closed without merging.
	ClosedAbandoned = "abandoned"
)

// Receipt and coverage are PR #249's artifact kinds. They are named here as
// literals rather than through state.Kind* because #249 has not landed yet and
// this projection must compile without it. Both are best-effort inputs: an
// artifact that does not decode simply teaches the index nothing.
//
// Rebase task when #249 merges: replace these two constants with
// state.KindReceipt / state.KindCoverage. TestClosureReadsReceiptAndCoverage
// pins the body shapes this decoder expects, so a drift in either fails loudly
// rather than silently emptying the moot class.
const (
	kindReceipt  = "receipt"
	kindCoverage = "coverage"
)

// outcomeAlreadyMerged mirrors the command layer's action outcome for a run
// refused because its PR had already merged. Duplicated rather than imported for
// the same reason actionBody is: the projection reads bodies off the log and
// never depends on the package that writes them.
const outcomeAlreadyMerged = "already_merged"

// Closure counts one surface's withheld rows by reason. The counts are always
// projected, even when the rows are not: an inbox that quietly shrank from 164
// rows to 3 is indistinguishable from one that lost them, and the operator has
// been quoted the parked count as if it were real work.
type Closure struct {
	Superseded int `json:"superseded"`
	Moot       int `json:"moot"`
	Stale      int `json:"stale"`
}

// Total is how many rows this surface withheld.
func (c Closure) Total() int { return c.Superseded + c.Moot + c.Stale }

// Discharged summarises what the whole projection withheld, per surface.
type Discharged struct {
	Parked       Closure `json:"parked"`
	ReadyToMerge Closure `json:"ready_to_merge"`
}

// Total is how many rows the projection withheld across every surface.
func (d Discharged) Total() int { return d.Parked.Total() + d.ReadyToMerge.Total() }

// closingFact is what the log knows about one subject being finished: the state
// gate observed, which artifact taught it, when that observation was made, and
// its position in the log.
//
// The position is load-bearing. A pull request can be CLOSED AND REOPENED — this
// repo's own review-cycle rule says a PR past its cap is "closed and re-opened
// fresh" — and the re-gated PR then parks again AFTER the closure was recorded.
// Without the ordering, that stale closing fact would moot the fresh park
// forever: a live merge-authorization question silently hidden, which is the
// exact failure this whole reduction exists to prevent, running backwards.
type closingFact struct {
	State  string
	Source string
	At     string
	// order is the closing artifact's index in the log. It only settles a
	// terminal it POSTDATES.
	order int
}

// why renders the fact as the sentence a row carries.
func (f closingFact) why() string {
	switch f.State {
	case ClosedMerged:
		return fmt.Sprintf("the PR merged (recorded by %s)", f.Source)
	case ClosedAbandoned:
		return fmt.Sprintf("the PR closed without merging (recorded by %s)", f.Source)
	default:
		return fmt.Sprintf("the PR is no longer open (recorded by %s)", f.Source)
	}
}

// closureIndex answers "does the log already know this pull request is
// finished", keyed by subject.
type closureIndex map[string]closingFact

// buildClosureIndex folds every closing fact the log carries into one lookup.
// Later artifacts overwrite earlier ones: the log is append-only, so the newest
// observation of a subject is the current one.
//
// It reads only artifacts that are already provenance — an already_merged
// refusal, a sweep record, a receipt, a coverage sweep. None of them is a
// decision, so consulting them here cannot move authorization; the index makes
// a row invisible, never mergeable.
func buildClosureIndex(arts []state.Artifact, facts map[string]runFacts) closureIndex {
	idx := make(closureIndex)
	for order, a := range arts {
		idx.absorb(a, facts, order)
	}
	return idx
}

// absorb records every subject one artifact proves finished.
func (idx closureIndex) absorb(a state.Artifact, facts map[string]runFacts, order int) {
	switch a.Kind {
	case state.KindAction:
		idx.absorbAlreadyMerged(a, facts, order)
	case state.KindSubjectClosed:
		idx.absorbSubjectClosed(a, order)
	case kindReceipt:
		idx.absorbReceipt(a, order)
	case kindCoverage:
		idx.absorbCoverage(a, order)
	}
}

// absorbAlreadyMerged reads the one closing fact main already produces: gate
// refusing a run because the PR had merged before it ran. The subject comes from
// the run's folded facts, since the refusal body carries the subject the same
// way every other action does.
func (idx closureIndex) absorbAlreadyMerged(a state.Artifact, facts map[string]runFacts, order int) {
	var b actionBody
	if err := json.Unmarshal(a.Body, &b); err != nil {
		return
	}
	if b.Outcome != outcomeAlreadyMerged {
		return
	}
	f := facts[a.Run]
	if f.Repo == "" || f.Number == 0 {
		return
	}
	idx[subjectKey(f.Repo, f.Number)] = closingFact{
		State:  ClosedMerged,
		Source: "an already_merged refusal",
		At:     a.Time.UTC().Format(time.RFC3339),
		order:  order,
	}
}

// subjectClosedBody is the body `gate sweep` writes. It is a deliberate copy of
// the command layer's write shape, kept here so the projection stays decoupled
// from it — the same split actionBody already makes.
type subjectClosedBody struct {
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	State      string `json:"state"`
	ObservedAt string `json:"observed_at"`
	Source     string `json:"source"`
}

func (idx closureIndex) absorbSubjectClosed(a state.Artifact, order int) {
	var b subjectClosedBody
	if err := json.Unmarshal(a.Body, &b); err != nil {
		return
	}
	if b.Repo == "" || b.Number == 0 {
		return
	}
	idx[subjectKey(b.Repo, b.Number)] = closingFact{
		State:  b.State,
		Source: "an open-PR sweep",
		At:     b.ObservedAt,
		order:  order,
	}
}

// receiptBody is the slice of PR #249's receipt this index reads.
type receiptBody struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	Outcome string `json:"outcome"`
}

// receiptClosings maps a receipt outcome onto the closing state it proves.
// "failed" is deliberately absent: a pinned merge command that ran and did not
// land leaves the PR OPEN, so treating it as closed would hide a row that still
// needs the operator — the exact failure this whole change exists to stop, run
// in reverse.
var receiptClosings = map[string]string{
	"merged":     ClosedMerged,
	"superseded": ClosedMerged,
	"abandoned":  ClosedAbandoned,
}

func (idx closureIndex) absorbReceipt(a state.Artifact, order int) {
	var b receiptBody
	if err := json.Unmarshal(a.Body, &b); err != nil {
		return
	}
	closed, ok := receiptClosings[b.Outcome]
	if !ok || b.Repo == "" || b.Number == 0 {
		return
	}
	idx[subjectKey(b.Repo, b.Number)] = closingFact{
		State:  closed,
		Source: "a receipt",
		At:     a.Time.UTC().Format(time.RFC3339),
		order:  order,
	}
}

// coverageBody is the slice of PR #249's coverage sweep this index reads. Only
// the LANDED classes are read: authorized_never_landed is a list of
// authorizations, not of merges, and a PR on it may still be open.
type coverageBody struct {
	Repo                       string        `json:"repo"`
	AuthorizedAndLanded        []coverageRow `json:"authorized_and_landed"`
	LandedWithoutAuthorization []coverageRow `json:"landed_without_authorization"`
	PreAdoption                []coverageRow `json:"pre_adoption"`
}

type coverageRow struct {
	Number   int    `json:"number"`
	MergedAt string `json:"merged_at"`
}

func (idx closureIndex) absorbCoverage(a state.Artifact, order int) {
	var b coverageBody
	if err := json.Unmarshal(a.Body, &b); err != nil {
		return
	}
	if b.Repo == "" {
		return
	}
	landed := [][]coverageRow{b.AuthorizedAndLanded, b.LandedWithoutAuthorization, b.PreAdoption}
	for _, rows := range landed {
		idx.absorbCoverageRows(b.Repo, rows, order)
	}
}

func (idx closureIndex) absorbCoverageRows(repo string, rows []coverageRow, order int) {
	for _, r := range rows {
		if r.Number == 0 {
			continue
		}
		idx[subjectKey(repo, r.Number)] = closingFact{
			State:  ClosedMerged,
			Source: "a coverage sweep",
			At:     r.MergedAt,
			order:  order,
		}
	}
}

// settles reports whether the log holds a closing fact for this subject that
// POSTDATES the given terminal.
//
// The ordering check is what makes a reopened pull request work: a closure
// recorded before the terminal describes a PR that has since been gated again,
// and treating it as current would hide a live park. A closing fact never
// settles a question the log asked after it.
func (idx closureIndex) settles(repo string, number, terminalOrder int) (closingFact, bool) {
	if repo == "" || number == 0 {
		return closingFact{}, false
	}
	f, ok := idx[subjectKey(repo, number)]
	if !ok || f.order <= terminalOrder {
		return closingFact{}, false
	}
	return f, true
}

// subjectTerminals is the log folded once into, per subject, the terminal that
// is its CURRENT state and the terminals a newer one displaced.
//
// Every inbox surface needs this same fold. Before it was extracted, the parked
// and ready projections each built their own copy — the duplication a follow-up
// named as the reason gate's three "is this park still open" notions drift apart
// (cmdResolve's pre-check, the locked check, and the reducer). One fold, and the
// audit metric reads it too.
type subjectTerminals struct {
	// newest is each subject's current state: the highest-ordered action or
	// escalation across every run that ever gated it.
	newest map[string]terminalRun
	// superseded holds every terminal a later one displaced, oldest first.
	superseded map[string][]terminalRun
	// unattributed is every run whose terminal carries no resolvable subject. It
	// cannot be reduced by subject at all, so it is neither newest nor superseded
	// and no supersession or closure claim can be made about it.
	unattributed []terminalRun
}

// foldSubjectTerminals reduces the log to each run's last terminal, then reduces
// runs by subject. It returns the fold and the per-run facts it built on the way,
// because the closure index needs those same facts to attribute a refusal.
func foldSubjectTerminals(arts []state.Artifact) (subjectTerminals, map[string]runFacts) {
	last := make(map[string]terminalRun)
	facts := make(map[string]runFacts)
	for order, a := range arts {
		facts[a.Run] = mergeRunFacts(facts[a.Run], factsFromArtifact(a))
		if a.Kind == state.KindAction || a.Kind == state.KindEscalation {
			last[a.Run] = terminalRun{artifact: a, order: order}
		}
	}

	terms := subjectTerminals{
		newest:     make(map[string]terminalRun, len(last)),
		superseded: make(map[string][]terminalRun),
	}
	for run, t := range last {
		t.facts = facts[run]
		terms.place(t)
	}
	terms.sortSuperseded()
	return terms, facts
}

// place files one run's terminal as its subject's newest, or as a superseded
// one. A terminal with no resolvable subject goes to unattributed: reducing it
// by subject would silently merge unrelated runs under an empty key.
func (t *subjectTerminals) place(term terminalRun) {
	f := term.facts
	if f.Repo == "" || f.Number == 0 {
		t.unattributed = append(t.unattributed, term)
		return
	}
	key := subjectKey(f.Repo, f.Number)
	current, ok := t.newest[key]
	if !ok {
		t.newest[key] = term
		return
	}
	if term.order > current.order {
		t.newest[key] = term
		t.superseded[key] = append(t.superseded[key], current)
		return
	}
	t.superseded[key] = append(t.superseded[key], term)
}

// sortSuperseded orders each subject's displaced terminals oldest-first, so the
// discharged view reads as the history it is.
func (t *subjectTerminals) sortSuperseded() {
	for _, terms := range t.superseded {
		sort.Slice(terms, func(i, j int) bool { return terms[i].order < terms[j].order })
	}
}

// subjects lists every subject key the fold resolved, sorted so callers iterate
// deterministically rather than in map order.
func (t subjectTerminals) subjects() []string {
	keys := make([]string, 0, len(t.newest))
	for k := range t.newest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// add tallies one row's discharge.
func (c *Closure) add(d Discharge) {
	switch d {
	case DischargeSuperseded:
		c.Superseded++
	case DischargeMoot:
		c.Moot++
	case DischargeStale:
		c.Stale++
	}
}

// summary renders one surface's withheld counts, naming every reason including
// the zeroes. A reason that vanishes when it is zero cannot be distinguished
// from a reason the reader has never heard of.
func (c Closure) summary() string {
	return fmt.Sprintf("%d (%d moot, %d superseded, %d stale)", c.Total(), c.Moot, c.Superseded, c.Stale)
}

// LiveSubject is one pull request the inbox still holds open, together with the
// terminal artifact its row stands on. The terminal is what a recorded closure
// is parented to, which is what makes "one closure per terminal" structural.
type LiveSubject struct {
	Repo     string
	Number   int
	Run      string
	Terminal string
}

// LiveSubjects lists every subject the offline projection still considers
// actionable — a live park or a live ready-to-merge row.
//
// It is the sweep's work list, and scoping the sweep to it is deliberate: a row
// the log has already discharged needs no second closing fact, so the cost grows
// with the queue rather than with history. It reduces through exactly the fold
// and closure index the inbox renders from — the same two functions, not a
// parallel derivation — so the sweep can never disagree with the surface it is
// fixing. It deliberately skips the inbox's grant and coverage enrichment, which
// needs a clock and which a work list has no use for.
func LiveSubjects(arts []state.Artifact) []LiveSubject {
	terms, facts := foldSubjectTerminals(arts)
	closed := buildClosureIndex(arts, facts)
	parked, _, _ := parkedRuns(terms, closed, "")
	ready, _ := readyToMergeRuns(terms, closed)

	seen := make(map[string]bool, len(parked)+len(ready))
	out := make([]LiveSubject, 0, len(parked)+len(ready))
	for _, p := range parked {
		out = appendLiveSubject(out, seen, terms, p.Repo, p.Number)
	}
	for _, r := range ready {
		out = appendLiveSubject(out, seen, terms, r.Repo, r.Number)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Number < out[j].Number
	})
	return out
}

func appendLiveSubject(out []LiveSubject, seen map[string]bool, terms subjectTerminals, repo string, number int) []LiveSubject {
	if repo == "" || number == 0 {
		return out
	}
	key := subjectKey(repo, number)
	if seen[key] {
		return out
	}
	t, ok := terms.newest[key]
	if !ok {
		return out
	}
	seen[key] = true
	return append(out, LiveSubject{Repo: repo, Number: number, Run: t.artifact.Run, Terminal: t.artifact.ID})
}

// OpenSets reads every named repo's open-PR set ONCE, in parallel, and reports
// which repos could not be read. It is the same batched machinery the live
// inbox reconcile runs on, exported so `gate sweep` shares it rather than
// growing a second, sequential copy of the fan-out.
//
// A repo appears in exactly one of the two maps, so a caller can never mistake
// "read, and nothing is open" for "could not read".
func OpenSets(repos []string, fetch OpenPRs) (open map[string]map[int]LivePR, errs map[string]error) {
	live := resolveRepos(repos, fetch)
	return live.open, live.errs
}
