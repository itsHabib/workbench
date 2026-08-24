package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/escalation"
)

// Inbox is a read-only projection of everything currently awaiting the operator,
// derived from the artifact log alone: gate runs parked for judgment, and the
// grant ledger (live grants, plus grants expired within the recent window so a
// re-mint is one glance away). Like every observe view it renders; it never
// decides — nothing here is scored or ranked by anything but age and expiry.
type Inbox struct {
	Parked       []ParkedRun     `json:"parked"`
	Unattributed []ParkedRun     `json:"unattributed"`
	Grants       []GrantLine     `json:"grants"`
	NeedsGrant   []NeedsGrantRow `json:"needs_grant,omitempty"`
	ReadyToMerge []ReadyRow      `json:"ready_to_merge,omitempty"`
	// Discharged counts the rows this projection withheld, per surface. It is
	// ALWAYS emitted, including when the rows themselves are not: an inbox that
	// shrank from 164 rows to 3 must be able to say so, or a reader cannot tell
	// a working reduction from a lost queue.
	Discharged Discharged `json:"discharged"`
	// DischargedRows carries those withheld rows themselves, populated only when
	// the caller asked for them. They are history and diagnostics, never work —
	// each one is stripped of its paste-ready commands.
	DischargedRows *DischargedRows `json:"discharged_rows,omitempty"`
}

// DischargedRows holds the withheld rows for a caller that asked to see them.
type DischargedRows struct {
	Parked       []ParkedRun `json:"parked,omitempty"`
	ReadyToMerge []ReadyRow  `json:"ready_to_merge,omitempty"`
}

// ReadyRow is one PR whose latest terminal artifact is a would_merge action:
// judged clean under its grant, the dry-run merge command written and waiting
// for the operator to run it. MergeCommand is that paste-ready `gh pr merge`
// (the action body's own command), so landing a ready PR is one copy away. A
// row appears only while the would_merge action is still the subject's newest
// terminal — a later park/block/newer run supersedes it — and, on the live
// path, only while the PR is still open.
type ReadyRow struct {
	Run          string `json:"run"`
	Repo         string `json:"repo,omitempty"`
	Number       int    `json:"number,omitempty"`
	Title        string `json:"title,omitempty"`
	HeadSHA      string `json:"head_sha,omitempty"`
	URL          string `json:"url,omitempty"`
	MergeCommand string `json:"merge_command"`
	// Discharge is empty while the row is live, and otherwise names why the log
	// has already settled it. DischargeWhy is the same fact as a sentence.
	Discharge    Discharge `json:"discharge,omitempty"`
	DischargeWhy string    `json:"discharge_why,omitempty"`
	// Coverage answers "is this repo actually covered by a live grant, and would
	// its ceilings park the next run" for the PR the inbox is recommending —
	// the inventory check that used to happen only at gate time. Advisory; see
	// GrantCoverage.
	Coverage *GrantCoverage `json:"grant_coverage,omitempty"`
}

// NeedsGrantRow is one repo whose gate runs were refused for want of a live
// grant, surfaced so a re-mint is one paste away. It appears only for a repo
// with no currently-live merge grant: a live grant — even one about to expire —
// suppresses it, so this surface never trains the operator to re-mint a grant
// that already covers the repo. SuggestedMint is a paste-ready `gate grant`,
// carrying the same -state arg the inbox read under. OpenPRs is populated only
// on the live path; the non-live projection omits it.
type NeedsGrantRow struct {
	Repo          string `json:"repo"`
	GrantState    string `json:"grant_state"` // "expired" | "absent"
	LastExpiredAt string `json:"last_expired_at,omitempty"`
	OpenPRs       *int   `json:"open_prs,omitempty"`
	SuggestedMint string `json:"suggested_mint"`
}

// ParkedRun is one gate run stopped on an escalation, waiting for the operator's
// judgment. JudgeCommand and ExplainCommand are paste-ready: the grant id is the
// one the run parked under, read from the escalation itself, so resolving a park
// never means hunting an id out of the log.
type ParkedRun struct {
	Run string `json:"run"`
	// Escalation is the id of the escalation artifact this park stands on — the
	// key a resolution is driven against (`gate resolve -escalation`). It is
	// projected so a remote resolver (a Slack callback, a future gate UI) holding
	// only the escalation id can join it to the grant the run parked under,
	// without hunting the id out of the log. Empty only when the body predates
	// the artifact id being carried.
	Escalation string `json:"escalation,omitempty"`
	// Discharge is empty while the park is live, and otherwise names why the log
	// has already settled it: a later terminal for the same PR answered it, or
	// the PR itself finished. DischargeWhy is the same fact as a sentence.
	//
	// A discharged park carries no JudgeCommand and no ResolveCommand. Handing
	// one over would invite the operator to spend a one-shot judgment on a
	// question that is no longer live.
	Discharge     Discharge `json:"discharge,omitempty"`
	DischargeWhy  string    `json:"discharge_why,omitempty"`
	Repo          string    `json:"repo,omitempty"`
	Number        int       `json:"number,omitempty"`
	Title         string    `json:"title,omitempty"`
	HeadSHA       string    `json:"head_sha,omitempty"`
	URL           string    `json:"url,omitempty"`
	PRState       string    `json:"pr_state,omitempty"`
	PRStateReason string    `json:"pr_state_reason,omitempty"`
	Question      string    `json:"question"`
	Code          string    `json:"code,omitempty"`
	Grant         string    `json:"grant,omitempty"`
	ParkedAt      string    `json:"parked_at"`
	// CyclesUsed and CyclesMax are the review-cycle budget this park sits
	// under: the cycles its repo#PR has consumed (this park's own run included
	// — a content park is a ladder decision) against the -max-cycles ceiling
	// of the grant the run parked under, the same grant JudgeCommand names.
	// CyclesMax is 0 when that grant is unbounded or not in this log. Rendered
	// as "cycles N/M" on the row so a driver sees the budget before acting: a
	// judgment of this run spends no new cycle, but a fresh `gate gate` would
	// be cycle N+1, and refuses pre-flight once N+1 exceeds M.
	CyclesUsed int `json:"cycles_used"`
	CyclesMax  int `json:"cycles_max"`
	// Escape is the route sealed into the escalation artifact, when one was
	// recorded. On a ceiling park it replaces JudgeCommand: judging under the
	// same grant re-applies the ceiling, so the stored route (inspect, then
	// mint wider authority) is the only next step that can move the run.
	Escape         *escalation.Escape `json:"escape,omitempty"`
	JudgeCommand   string             `json:"judge_command,omitempty"`
	ExplainCommand string             `json:"explain_command"`
	// ResolveCommand is the paste-ready `escalate resolve` line for this park —
	// the human-decision route alongside JudgeCommand's automated one. Empty when
	// the park carries no escalation id or no grant (escalate's ingest refuses an
	// empty grant), and on a ceiling park, which needs a wider grant rather than a
	// decision.
	ResolveCommand string `json:"resolve_command,omitempty"`
	// Coverage is the same inventory answer the ready rows carry: judging a park
	// clean is wasted work if no live grant covers the repo the merge needs, or
	// if the grant's ceiling would re-park the run straight afterwards.
	Coverage *GrantCoverage `json:"grant_coverage,omitempty"`
}

// GrantLine is one grant in the ledger with its expiry resolved against now.
// Remaining is a compact human span: "in 5h49m" while live, "16h ago" once
// expired.
type GrantLine struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	Action    string `json:"action"`
	MaxTier   string `json:"max_tier"`
	MaxCycles int    `json:"max_cycles"`
	ExpiresAt string `json:"expires_at"`
	Expired   bool   `json:"expired"`
	Remaining string `json:"remaining,omitempty"`
}

// recentlyExpired bounds how long an expired grant lingers in the ledger: long
// enough that a just-lapsed grant is still visible to re-mint from, short enough
// that the ledger doesn't accrete every grant ever minted.
const recentlyExpired = 24 * time.Hour

// grantBody is the slice of a grant artifact the inbox reads. It is a small,
// deliberate copy of capability.Grant's shape rather than an import: the ledger
// only displays grants, so the projection layer stays decoupled from the policy
// layer that mints and checks them. The grant body's field names are signed
// field-by-field in capability, so this shape is a stable contract.
type grantBody struct {
	Repo      string    `json:"repo"`
	Action    string    `json:"action"`
	MaxTier   string    `json:"max_tier"`
	MaxCycles int       `json:"max_cycles"`
	ExpiresAt time.Time `json:"expires_at"`
}

// The inbox reads the parked run's escalation body — question, park code, grant,
// PR subject — through the shared contract (escalation.DecodeBody), the same
// tolerant read that used to run against a locally-redeclared struct. This is
// one of the three readers the typed contract collapsed onto a single source of
// truth; the projection still renders best-effort, so a drifted body never drops
// a park from the inbox.

// NextRequest carries everything a projection needs beyond the store and the
// clock. It follows preflight's shape rather than growing the four positional
// entry points that preceded it: the offline/live and default/all axes multiply,
// and a bare bool pair at a call site says nothing about which is which.
type NextRequest struct {
	// StateArg is the `-state` argument the inbox read under, threaded into every
	// paste-ready command so a copied line runs against the same store.
	StateArg string
	// Fetch is the live open-PR seam. Nil means the offline projection: gate
	// reduces from the log alone and makes no network call.
	//
	// It stays nil by default on purpose. `gate next -json` is on `escalate
	// serve`'s Slack path under a hard budget, and putting one gh subprocess per
	// distinct repo there would trade a stale queue for a stranded interaction.
	Fetch OpenPRs
	// IncludeDischarged asks for the withheld rows themselves. The COUNTS are
	// always projected regardless.
	IncludeDischarged bool
}

// NextText renders the console feed for a human.
func NextText(w io.Writer, st *state.Store, now func() time.Time, req NextRequest) error {
	in, err := next(st, now, req)
	if err != nil {
		return err
	}
	renderInbox(w, in)
	return nil
}

// NextJSON emits the console feed as JSON.
func NextJSON(w io.Writer, st *state.Store, now func() time.Time, req NextRequest) error {
	in, err := next(st, now, req)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(in)
}

// next builds the projection and, when a live seam was supplied, reconciles it
// against current PR state.
func next(st *state.Store, now func() time.Time, req NextRequest) (Inbox, error) {
	in, err := collect(st, now, req)
	if err != nil {
		return Inbox{}, err
	}
	if req.Fetch == nil {
		return in, nil
	}
	return reconcileInbox(in, req.Fetch), nil
}

// OpenPRs is the one live seam the inbox reconciles against: it returns every
// OPEN PR for a repo, keyed by number, in a single call. Gate's command layer
// supplies the `gh pr list` implementation. It is the whole batching win — one
// subprocess per DISTINCT repo instead of one `gh pr view` per row — and it
// serves all three live surfaces at once: the parked reconcile, the
// ready-to-merge reconcile, and the needs_grant open-PR count. It is called
// best-effort per repo: a repo whose fetch errors degrades that repo's rows
// (parked→unknown, ready→kept, needs_grant→nil count) but never fails the whole
// projection.
type OpenPRs func(repo string) (map[int]LivePR, error)

// LivePR is the small display/status slice carried for one open PR. On the live
// path State is always "OPEN" — a PR present in a repo's open set is open by
// construction; a PR absent from it (with no fetch error) is not open and its
// row drops.
type LivePR struct {
	State   string
	Title   string
	HeadSHA string
	URL     string
}

// liveRepos is the result of the batched per-repo fetch: the open-PR map for
// each repo whose fetch succeeded, plus the error for each repo whose fetch
// failed. A repo appears in exactly one of the two maps. It is the single
// snapshot every live surface reconciles against, so parked, ready, and
// needs_grant can never disagree about which repos were reachable.
type liveRepos struct {
	open map[string]map[int]LivePR
	errs map[string]error
}

// lookup resolves one subject against the batched snapshot: the fetch error for
// its repo (keep-with-reason), or whether the PR is in the repo's open set
// (present→open+facts, absent→not open→drop).
func (l liveRepos) lookup(repo string, number int) (pr LivePR, open bool, err error) {
	if e := l.errs[repo]; e != nil {
		return LivePR{}, false, e
	}
	pr, open = l.open[repo][number]
	return pr, open, nil
}

// reconcileInbox is the one live pass: fetch each distinct repo's open PRs ONCE
// (in parallel), then reconcile every row LOCALLY against that snapshot. The
// cost is O(distinct repos), never O(rows) — the whole point of the batched
// seam. All three surfaces reconcile against the same snapshot.
//
// A row the snapshot proves finished is discharged as moot, exactly as a closing
// fact in the log would discharge it, and counted the same way. The live path
// and the offline path therefore reach the same classification by different
// evidence, and `gate sweep` is what turns the former into the latter.
func reconcileInbox(in Inbox, fetch OpenPRs) Inbox {
	live := resolveRepos(distinctRepos(in), fetch)
	parked, parkedOut := reconcileLive(in.Parked, live)
	ready, readyOut := reconcileReadyLive(in.ReadyToMerge, live)
	in.Parked = parked
	in.ReadyToMerge = ready
	in.NeedsGrant = enrichNeedsGrant(in.NeedsGrant, live)
	for _, r := range parkedOut {
		in.Discharged.Parked.add(r.Discharge)
	}
	for _, r := range readyOut {
		in.Discharged.ReadyToMerge.add(r.Discharge)
	}
	if in.DischargedRows != nil {
		in.DischargedRows.Parked = append(in.DischargedRows.Parked, parkedOut...)
		in.DischargedRows.ReadyToMerge = append(in.DischargedRows.ReadyToMerge, readyOut...)
		sortParked(in.DischargedRows.Parked)
		sortReady(in.DischargedRows.ReadyToMerge)
	}
	return in
}

// distinctRepos collects the deduplicated set of repos across every live
// surface, so each repo is fetched exactly once even when many rows share it.
func distinctRepos(in Inbox) []string {
	seen := make(map[string]struct{})
	for _, p := range in.Parked {
		if p.Repo != "" {
			seen[p.Repo] = struct{}{}
		}
	}
	for _, r := range in.ReadyToMerge {
		if r.Repo != "" {
			seen[r.Repo] = struct{}{}
		}
	}
	for _, r := range in.NeedsGrant {
		if r.Repo != "" {
			seen[r.Repo] = struct{}{}
		}
	}
	repos := make([]string, 0, len(seen))
	for repo := range seen {
		repos = append(repos, repo)
	}
	return repos
}

type repoResult struct {
	repo string
	prs  map[int]LivePR
	err  error
}

// resolveRepos fetches each repo's open-PR map ONCE, fanned over a bounded
// worker pool, and folds the results into one snapshot indexed by repo. One
// fetch per repo is the batching invariant the tests pin.
func resolveRepos(repos []string, fetch OpenPRs) liveRepos {
	n := len(repos)
	results := make(chan repoResult, n)
	jobs := make(chan string, n)
	for _, repo := range repos {
		jobs <- repo
	}
	close(jobs)

	const maxWorkers = 8
	for range min(n, maxWorkers) {
		go func() {
			for repo := range jobs {
				prs, err := fetch(repo)
				results <- repoResult{repo: repo, prs: prs, err: err}
			}
		}()
	}

	live := liveRepos{open: make(map[string]map[int]LivePR, n), errs: make(map[string]error, n)}
	for range n {
		res := <-results
		if res.err != nil {
			live.errs[res.repo] = res.err
			continue
		}
		live.open[res.repo] = res.prs
	}
	return live
}

// reconcileLive reconciles the parked rows against the batched snapshot: a repo
// fetch error keeps the row visible as unknown (the failed-lookup default); a PR
// present in its repo's open set is OPEN and enriched; a PR absent from the set
// (no fetch error) is not open — merged or closed — and is discharged as moot
// rather than dropped, so the shrinkage stays countable.
func reconcileLive(parked []ParkedRun, live liveRepos) (out, discharged []ParkedRun) {
	for _, p := range parked {
		pr, open, err := live.lookup(p.Repo, p.Number)
		if err != nil {
			p.PRState = "unknown"
			p.PRStateReason = err.Error()
			out = append(out, p)
			continue
		}
		if !open {
			p.PRState = notOpenState
			discharged = append(discharged, dischargeParked(p, DischargeMoot, liveMootWhy))
			continue
		}
		p.PRState = "OPEN"
		out = append(out, mergeLivePR(p, pr))
	}
	return out, discharged
}

// reconcileReadyLive enforces the ready-row freshness law against the batched
// snapshot. A row is discharged on a confirmed non-mergeable fact, and the two
// such facts are NOT the same fact:
//
//   - the PR is absent from its repo's open set (since MERGED or CLOSED) — the
//     row is MOOT and nothing more is owed;
//   - its head has MOVED past the SHA the would_merge command pins (both SHAs
//     non-empty and differing) — the row is STALE, and the PR is still open and
//     still needs re-gating. Counting that as moot would report a PR that needs
//     work as one that is finished.
//
// A row is KEPT on anything ambiguous — a repo fetch error, or an empty live SHA
// — because the safe default is never to silently hide a possibly-mergeable PR.
func reconcileReadyLive(rows []ReadyRow, live liveRepos) (out, discharged []ReadyRow) {
	for _, r := range rows {
		pr, open, err := live.lookup(r.Repo, r.Number)
		if err != nil {
			out = append(out, r)
			continue
		}
		if !open {
			discharged = append(discharged, dischargeReady(r, DischargeMoot, liveMootWhy))
			continue
		}
		if headMoved(r.HeadSHA, pr.HeadSHA) {
			discharged = append(discharged, dischargeReady(r, DischargeStale, staleWhy(r.HeadSHA, pr.HeadSHA)))
			continue
		}
		out = append(out, mergeLiveReady(r, pr))
	}
	return out, discharged
}

// staleWhy explains a ready row whose PR moved past the head gate authorized.
func staleWhy(authorized, live string) string {
	return fmt.Sprintf("the PR head moved to %s; the pinned command authorizes %s and would refuse", short(live), short(authorized))
}

// short trims a SHA to the width a human reads.
func short(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

// enrichNeedsGrant attaches a live open-PR count to each needs_grant row from
// the batched snapshot — len of the repo's open set, no extra `gh` call. It is
// best-effort and degrades a row rather than dropping it: a repo whose fetch
// failed KEEPS the row with OpenPRs left nil (omitted from JSON). The lapsed
// grant is the primary signal — dropping the whole row when gh is unreachable
// would reintroduce the very invisible needs-a-grant state this surface exists
// to kill. The projection never fails as a whole.
func enrichNeedsGrant(rows []NeedsGrantRow, live liveRepos) []NeedsGrantRow {
	out := make([]NeedsGrantRow, 0, len(rows))
	for _, r := range rows {
		if live.errs[r.Repo] != nil {
			out = append(out, r)
			continue
		}
		count := len(live.open[r.Repo])
		r.OpenPRs = &count
		out = append(out, r)
	}
	return out
}

// headMoved reports whether the live head has confirmably moved past the SHA the
// ready row's merge command pins. Only a confirmed mismatch — both SHAs present
// and differing — counts; an empty live SHA is not a confirmation and keeps the
// row (per the keep-on-ambiguous default).
func headMoved(recorded, live string) bool {
	return recorded != "" && live != "" && recorded != live
}

// mergeLiveReady refreshes a ready row's display facts from the live PR read,
// preferring the live value when present — the same enrichment mergeLivePR does
// for a parked row.
func mergeLiveReady(r ReadyRow, live LivePR) ReadyRow {
	if live.Title != "" {
		r.Title = live.Title
	}
	if live.HeadSHA != "" {
		r.HeadSHA = live.HeadSHA
	}
	if live.URL != "" {
		r.URL = live.URL
	}
	return r
}

func mergeLivePR(p ParkedRun, live LivePR) ParkedRun {
	if live.Title != "" {
		p.Title = live.Title
	}
	if live.HeadSHA != "" {
		p.HeadSHA = live.HeadSHA
	}
	if live.URL != "" {
		p.URL = live.URL
	}
	return p
}

// collect reads the log once and folds it into the inbox projection. The single
// read is deliberate: parked runs and the grant ledger are two views of one
// snapshot, never two scans that could disagree under a concurrent append.
func collect(st *state.Store, now func() time.Time, req NextRequest) (Inbox, error) {
	arts, err := st.List(nil)
	if err != nil {
		return Inbox{}, err
	}
	return buildInbox(arts, now(), req), nil
}

func buildInbox(arts []state.Artifact, now time.Time, req NextRequest) Inbox {
	// One fold of the log, then one closure index over it: every surface below
	// reduces against the same two, so parked, ready, and the audit metric can
	// never disagree about which terminal is current or which PR is finished.
	terms, facts := foldSubjectTerminals(arts)
	closed := buildClosureIndex(arts, facts)
	parked, parkedOut, unattributed := parkedRuns(terms, closed, req.StateArg)
	ready, readyOut := readyToMergeRuns(terms, closed)
	// One index, both surfaces: the coverage question is asked per row but the
	// log is read once, so N recommended PRs cost one scan.
	idx := buildCoverageIndex(arts, now, req.StateArg)
	for i := range parked {
		parked[i].Coverage = idx.assessParked(parked[i])
		parked[i].CyclesUsed, parked[i].CyclesMax = idx.budget(parked[i].Repo, parked[i].Number, parked[i].Grant)
	}
	for i := range ready {
		ready[i].Coverage = idx.assess(ready[i].Repo, ready[i].Number, ready[i].Run)
	}
	in := Inbox{
		Parked:       parked,
		Unattributed: unattributed,
		Grants:       grantLines(arts, now),
		NeedsGrant:   needsGrantRows(arts, now, req.StateArg),
		ReadyToMerge: ready,
		Discharged:   Discharged{Parked: count(parkedOut), ReadyToMerge: countReady(readyOut)},
	}
	if req.IncludeDischarged {
		in.DischargedRows = &DischargedRows{Parked: parkedOut, ReadyToMerge: readyOut}
	}
	return in
}

// count tallies discharged parks by reason.
func count(rows []ParkedRun) Closure {
	var c Closure
	for _, r := range rows {
		c.add(r.Discharge)
	}
	return c
}

// countReady tallies discharged ready rows by reason.
func countReady(rows []ReadyRow) Closure {
	var c Closure
	for _, r := range rows {
		c.add(r.Discharge)
	}
	return c
}

// readyToMergeRuns splits every subject whose current terminal is a would_merge
// action into the rows still worth landing and the rows the log has already
// discharged. It reads the shared subject fold, so "which terminal is current"
// is decided in exactly one place for every surface.
//
// A subject surfaces as live only when its newest terminal is a would_merge
// action AND no closing fact says the PR is finished. The second half is the
// one that was missing: every action gate writes is dry_run/would_merge, so a
// row survived its own merge and stood forever recommending a landed PR.
func readyToMergeRuns(terms subjectTerminals, closed closureIndex) (live, discharged []ReadyRow) {
	for _, key := range terms.subjects() {
		l, d := readySubject(terms, closed, key)
		live = append(live, l...)
		discharged = append(discharged, d...)
	}
	sortReady(live)
	sortReady(discharged)
	return live, discharged
}

// readySubject classifies one subject's current and displaced would_merge
// actions.
func readySubject(terms subjectTerminals, closed closureIndex, key string) (live, discharged []ReadyRow) {
	for _, t := range terms.superseded[key] {
		row, ok := readyRowFromTerminal(t)
		if !ok {
			continue
		}
		discharged = append(discharged, dischargeReady(row, DischargeSuperseded, supersededBy(terms.newest[key])))
	}
	row, ok := readyRowFromTerminal(terms.newest[key])
	if !ok {
		return live, discharged
	}
	fact, finished := closed.settles(row.Repo, row.Number, terms.newest[key].order)
	if finished {
		return live, append(discharged, dischargeReady(row, DischargeMoot, fact.why()))
	}
	return append(live, row), discharged
}

func dischargeReady(r ReadyRow, d Discharge, why string) ReadyRow {
	r.Discharge = d
	r.DischargeWhy = why
	return r
}

// supersededBy names the terminal that displaced a row, so a discharged row
// explains itself instead of merely disappearing.
func supersededBy(newest terminalRun) string {
	if newest.artifact.Run == "" {
		return "a later run for the same PR reached a terminal outcome"
	}
	return fmt.Sprintf("run %s reached a later terminal for the same PR", newest.artifact.Run)
}

// actionBody is the slice of a KindAction body the ready-to-merge projection
// reads: the outcome that classifies the action and, for a would_merge, the
// paste-ready merge command gate wrote. A deliberate copy of main.go's write
// shape, kept here so the projection stays decoupled from the command layer.
type actionBody struct {
	Outcome string `json:"outcome"`
	Command string `json:"command"`
}

// readyRowFromTerminal returns a ready row when the subject's newest terminal is
// a would_merge action, and false for any other terminal (an escalation, or an
// action whose outcome is blocked / capability_refused / merge_not_implemented).
func readyRowFromTerminal(t terminalRun) (ReadyRow, bool) {
	if t.artifact.Kind != state.KindAction {
		return ReadyRow{}, false
	}
	var b actionBody
	if err := json.Unmarshal(t.artifact.Body, &b); err != nil {
		return ReadyRow{}, false
	}
	if b.Outcome != "would_merge" {
		return ReadyRow{}, false
	}
	row := ReadyRow{
		Run:          t.artifact.Run,
		Repo:         t.facts.Repo,
		Number:       t.facts.Number,
		Title:        t.facts.Title,
		HeadSHA:      t.facts.HeadSHA,
		MergeCommand: b.Command,
	}
	if row.Repo != "" && row.Number != 0 {
		row.URL = fmt.Sprintf("https://github.com/%s/pull/%d", row.Repo, row.Number)
	}
	return row, true
}

// sortReady orders ready rows deterministically across runs: repo → number →
// run — ready rows carry no age to rank by.
func sortReady(rows []ReadyRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Repo != rows[j].Repo {
			return rows[i].Repo < rows[j].Repo
		}
		if rows[i].Number != rows[j].Number {
			return rows[i].Number < rows[j].Number
		}
		return rows[i].Run < rows[j].Run
	})
}

// parkedRuns splits every park into the ones genuinely awaiting the operator and
// the ones the log has already discharged.
//
// A run parks by appending an escalation and resolves by appending an action (or
// a later escalation, if a judgment still left it over-ceiling), so a run's last
// terminal is its current state. Reducing runs by subject then discharges the
// earlier attempts at a PR gated more than once. What the reduction could not
// see — and what left a 164-row inbox carrying 3 live rows — is the PR itself
// ending: a merged or closed pull request keeps its newest terminal forever, and
// an escalation there is a question nobody can answer any more.
//
// Discharged rows are classified and returned, never dropped. The counts are
// what make the shrinkage legible, and `-all` is what makes the rows themselves
// inspectable; silently hiding 161 of 164 rows would replace one unreadable
// surface with another.
//
// Output is oldest-park-first: age is a fact, not a priority call.
func parkedRuns(terms subjectTerminals, closed closureIndex, stateArg string) (live, discharged, unattributed []ParkedRun) {
	for _, key := range terms.subjects() {
		l, d := parkedSubject(terms, closed, key, stateArg)
		live = append(live, l...)
		discharged = append(discharged, d...)
	}
	for _, t := range terms.unattributed {
		if t.artifact.Kind != state.KindEscalation {
			continue
		}
		unattributed = append(unattributed, parkedFromEscalation(t.artifact, t.facts, stateArg))
	}
	sortParked(live)
	sortParked(discharged)
	sortParked(unattributed)
	return live, discharged, unattributed
}

// parkedSubject classifies one subject's current and displaced parks.
func parkedSubject(terms subjectTerminals, closed closureIndex, key, stateArg string) (live, discharged []ParkedRun) {
	for _, t := range terms.superseded[key] {
		if t.artifact.Kind != state.KindEscalation {
			continue
		}
		row := parkedFromEscalation(t.artifact, t.facts, stateArg)
		discharged = append(discharged, dischargeParked(row, DischargeSuperseded, supersededBy(terms.newest[key])))
	}
	newest := terms.newest[key]
	if newest.artifact.Kind != state.KindEscalation {
		return live, discharged
	}
	row := parkedFromEscalation(newest.artifact, newest.facts, stateArg)
	fact, finished := closed.settles(row.Repo, row.Number, newest.order)
	if finished {
		return live, append(discharged, dischargeParked(row, DischargeMoot, fact.why()))
	}
	return append(live, row), discharged
}

// dischargeParked stamps a row with why it is no longer actionable and strips
// the paste-ready commands. A discharged park must never hand the operator a
// `gate judge` line: the one-shot judgment it would spend is spent against a
// question the log has already answered.
func dischargeParked(p ParkedRun, d Discharge, why string) ParkedRun {
	p.Discharge = d
	p.DischargeWhy = why
	p.JudgeCommand = ""
	p.ResolveCommand = ""
	p.Escape = nil
	return p
}

type terminalRun struct {
	artifact state.Artifact
	facts    runFacts
	order    int
}

type runFacts struct {
	Repo    string
	Number  int
	Title   string
	HeadSHA string
}

type artifactFactsBody struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	Subject struct {
		Repo    string `json:"repo"`
		Number  int    `json:"number"`
		HeadSHA string `json:"head_sha"`
	} `json:"subject"`
	PR struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	} `json:"pr"`
	Data struct {
		Title      string `json:"title"`
		HeadRefOID string `json:"headRefOid"`
	} `json:"data"`
}

func factsFromArtifact(a state.Artifact) runFacts {
	var body artifactFactsBody
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return runFacts{}
	}
	f := runFacts{Repo: body.Repo, Number: body.Number, Title: body.Data.Title, HeadSHA: body.Data.HeadRefOID}
	if body.Subject.Repo != "" {
		f.Repo = body.Subject.Repo
		f.Number = body.Subject.Number
		f.HeadSHA = body.Subject.HeadSHA
	}
	if body.PR.Repo != "" {
		f.Repo = body.PR.Repo
		f.Number = body.PR.Number
	}
	return f
}

func mergeRunFacts(old, next runFacts) runFacts {
	if next.Repo != "" {
		old.Repo = next.Repo
	}
	if next.Number != 0 {
		old.Number = next.Number
	}
	if next.Title != "" {
		old.Title = next.Title
	}
	if next.HeadSHA != "" {
		old.HeadSHA = next.HeadSHA
	}
	return old
}

func sortParked(parked []ParkedRun) {
	sort.Slice(parked, func(i, j int) bool {
		if parked[i].ParkedAt != parked[j].ParkedAt {
			return parked[i].ParkedAt < parked[j].ParkedAt
		}
		return parked[i].Run < parked[j].Run
	})
}

func parkedFromEscalation(a state.Artifact, facts runFacts, stateArg string) ParkedRun {
	// Best-effort decode: an escalation with an unreadable body still lists its
	// run, so a park is never silently dropped just because its body drifted.
	b, _ := escalation.DecodeBody(a.Body)
	facts = mergeRunFacts(facts, runFacts{Repo: b.Repo, Number: b.Number})
	p := ParkedRun{
		Run:            a.Run,
		Escalation:     a.ID,
		Repo:           facts.Repo,
		Number:         facts.Number,
		Title:          facts.Title,
		HeadSHA:        facts.HeadSHA,
		Question:       b.Question,
		Code:           b.Code,
		Grant:          b.Grant,
		ParkedAt:       a.Time.UTC().Format(time.RFC3339),
		Escape:         b.Escape,
		JudgeCommand:   judgeCommand(a.Run, b.Grant, stateArg),
		ExplainCommand: fmt.Sprintf("gate explain%s -run %s -html", stateArg, a.Run),
		ResolveCommand: resolveCommand(a.ID, b.Grant, stateArg),
	}
	if ceilingPark(b.Code) {
		p.JudgeCommand = ""
		p.ResolveCommand = ""
		p.Escape = ceilingEscape(a.Run, stateArg, b.Escape)
	}
	if p.Repo != "" && p.Number != 0 {
		p.URL = fmt.Sprintf("https://github.com/%s/pull/%d", p.Repo, p.Number)
	}
	return p
}

func judgeCommand(run, grant, stateArg string) string {
	if grant == "" {
		grant = "grt_..."
	}
	return fmt.Sprintf("gate judge%s -run %s -grant %s -decision <pass|block> -why \"...\"", stateArg, run, grant)
}

// resolveCommand is the paste-ready `escalate resolve` line for a park: the
// human route through the back-channel, next to judgeCommand's automated one.
// Unlike judgeCommand it substitutes no placeholder grant — escalate's ingest
// refuses an empty grant, so a grantless park (or one whose body predates the
// artifact id) gets no line at all rather than one guaranteed to fail.
func resolveCommand(escalationID, grant, stateArg string) string {
	if escalationID == "" || grant == "" {
		return ""
	}
	return fmt.Sprintf("escalate resolve%s -escalation %s -grant %s -decision <pass|block> -who <you> -why \"...\"",
		stateArg, escalationID, grant)
}

// ceilingPark reports whether the park is an authorization ceiling — a code a
// judgment cannot clear, because applyJudgment re-applies the grant ceiling.
func ceilingPark(code string) bool {
	return code == escalation.CodeTierExceeded || code == escalation.CodeCycleExceeded
}

// ceilingEscape rebuilds the ceiling route against the state path this
// projection was invoked with. The sealed escape carries the writer's absolute
// -state, which goes stale when the ledger is copied or mounted elsewhere, and
// legacy ceiling parks predate sealed escapes entirely — so the command is
// always derived here, and only the sealed prose is kept.
func ceilingEscape(run, stateArg string, sealed *escalation.Escape) *escalation.Escape {
	why := "the operator must mint a wider grant; inspect the recorded ceiling before requesting new authority"
	if sealed != nil && sealed.Why != "" {
		why = sealed.Why
	}
	return &escalation.Escape{
		Why:  why,
		Next: fmt.Sprintf("gate explain%s -run %s", stateArg, run),
	}
}

// datedGrant pairs a ledger row with its expiry instant so the ledger can sort
// on the instant (below), not on the second-precision string GrantLine carries.
type datedGrant struct {
	line GrantLine
	at   time.Time
}

// grantLines projects the grant ledger: every live grant, soonest-to-expire
// first (the ones nearest needing a re-mint lead), followed by grants expired
// within the recent window, most-recently-expired first. Grants expired longer
// ago are omitted — neither spendable nor worth re-minting from.
func grantLines(arts []state.Artifact, now time.Time) []GrantLine {
	var live, expired []datedGrant
	for _, a := range arts {
		if a.Kind != state.KindGrant {
			continue
		}
		var g grantBody
		if err := json.Unmarshal(a.Body, &g); err != nil {
			// An unreadable grant body can't be spent anyway; skip it rather than
			// surface a half-decoded ledger row.
			continue
		}
		line := GrantLine{
			ID:        a.ID,
			Repo:      g.Repo,
			Action:    g.Action,
			MaxTier:   g.MaxTier,
			MaxCycles: g.MaxCycles,
			ExpiresAt: g.ExpiresAt.UTC().Format(time.RFC3339),
		}
		// Expiry matches capability.Check exactly: expired strictly after the
		// instant, so a grant at its expiry is still live.
		if now.After(g.ExpiresAt) {
			since := now.Sub(g.ExpiresAt)
			if since > recentlyExpired {
				continue
			}
			line.Expired = true
			line.Remaining = shortDur(since) + " ago"
			expired = append(expired, datedGrant{line, g.ExpiresAt})
			continue
		}
		line.Remaining = "in " + shortDur(g.ExpiresAt.Sub(now))
		live = append(live, datedGrant{line, g.ExpiresAt})
	}
	// Sort on the instant, not the rendered second-precision string, so grants
	// minted within the same second keep a stable, id-tiebroken order run to run.
	sort.Slice(live, func(i, j int) bool { return grantBefore(live[i], live[j]) })
	sort.Slice(expired, func(i, j int) bool { return grantBefore(expired[j], expired[i]) })
	out := make([]GrantLine, 0, len(live)+len(expired))
	for _, d := range live {
		out = append(out, d.line)
	}
	for _, d := range expired {
		out = append(out, d.line)
	}
	return out
}

// grantBefore orders two ledger rows by expiry instant, breaking exact ties on
// id so the order is fully deterministic. Expired rows pass their args swapped
// to get the reverse (most-recently-expired first).
func grantBefore(a, b datedGrant) bool {
	if !a.at.Equal(b.at) {
		return a.at.Before(b.at)
	}
	return a.line.ID < b.line.ID
}

// grantNeededBody is the slice of a grant_needed artifact the inbox reads: the
// repo the refused run targeted, the machine-readable reason, and the refusal
// timestamp. A deliberate copy of the record main.go writes, kept here so the
// projection stays decoupled from the command layer's write shape.
type grantNeededBody struct {
	Repo   string `json:"repo"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

// needsGrantAgg folds one repo's latest refusal facts (across dedup) into a row.
type needsGrantAgg struct {
	reason string
	at     time.Time
}

// needsGrantRows folds the grant_needed artifacts into one row per repo whose
// merge grant has genuinely lapsed. The dedup laws it enforces are the
// correctness core of this surface — a false "needs a grant" for a covered repo
// trains the operator to ignore it:
//   - A repo with a currently-LIVE merge grant (not expired at now, matching
//     capability.Check exactly) is SUPPRESSED — even if that grant is close to
//     expiring, it still covers the repo now.
//   - A repo with only expired/absent grants shows exactly ONE row, folding
//     multiple refusal records into the most-recent one (latest timestamp wins
//     the grant_state and last_expired_at).
//
// Rows carry no open-PR count here; the live path enriches them via PRLister.
func needsGrantRows(arts []state.Artifact, now time.Time, stateArg string) []NeedsGrantRow {
	live := liveMergeGrantRepos(arts, now)
	latest := make(map[string]needsGrantAgg)
	for _, a := range arts {
		if a.Kind != state.KindGrantNeeded {
			continue
		}
		var b grantNeededBody
		if err := json.Unmarshal(a.Body, &b); err != nil || b.Repo == "" {
			continue
		}
		// A pre-flight cycle refusal is recorded under the same kind, but the
		// grant it refused against is live, just spent: the budget shows on the
		// parked row itself, and folding it here would print a lapse that
		// never happened.
		if b.Reason == escalation.CodeCycleExceeded {
			continue
		}
		at := grantNeededAt(b.At, a.Time)
		cur, seen := latest[b.Repo]
		// Most-recent wins; on an equal timestamp the later log-order record wins
		// (RFC3339 is second-precision, so two same-second refusals must not fold
		// to the earlier one). Artifacts arrive in log order, so `at.Before(cur.at)`
		// — strict — keeps the last-seen on ties.
		if seen && at.Before(cur.at) {
			continue
		}
		latest[b.Repo] = needsGrantAgg{reason: b.Reason, at: at}
	}

	rows := make([]NeedsGrantRow, 0, len(latest))
	for repo, ag := range latest {
		if live[repo] {
			continue
		}
		rows = append(rows, newNeedsGrantRow(repo, ag, stateArg))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Repo < rows[j].Repo })
	return rows
}

// liveMergeGrantRepos is the set of repos with a currently-live merge grant. It
// reuses grantLines' expiry rule verbatim — expired strictly after the instant,
// so a grant at exactly its expiry is still live — so the suppression test can
// never drift from what capability.Check would authorize.
func liveMergeGrantRepos(arts []state.Artifact, now time.Time) map[string]bool {
	live := make(map[string]bool)
	for _, a := range arts {
		if a.Kind != state.KindGrant {
			continue
		}
		var g grantBody
		if err := json.Unmarshal(a.Body, &g); err != nil {
			continue
		}
		if g.Action != "merge" || now.After(g.ExpiresAt) {
			continue
		}
		live[g.Repo] = true
	}
	return live
}

// newNeedsGrantRow builds a row from a repo's folded refusal facts. last_expired_at
// is set only for an expired grant (an absent grant never had an expiry to name).
func newNeedsGrantRow(repo string, ag needsGrantAgg, stateArg string) NeedsGrantRow {
	gstate := "expired"
	if ag.reason == "grant_absent" {
		gstate = "absent"
	}
	row := NeedsGrantRow{
		Repo:          repo,
		GrantState:    gstate,
		SuggestedMint: suggestedMint(repo, stateArg),
	}
	if gstate == "expired" {
		row.LastExpiredAt = ag.at.UTC().Format(time.RFC3339)
	}
	return row
}

// grantNeededAt prefers the record's own timestamp field, falling back to the
// artifact's log time when the body carried none (or an unparseable one).
func grantNeededAt(bodyAt string, artTime time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, bodyAt); err == nil {
		return t
	}
	return artTime
}

// suggestedMint is the paste-ready re-mint command for a lapsed repo. It splices
// stateArg the same way judgeCommand does, so a copied command targets the very
// state dir this inbox read. Tier and TTL mirror `gate grant`'s own defaults.
func suggestedMint(repo, stateArg string) string {
	return fmt.Sprintf("gate grant%s -repo %s -action merge -max-tier T1 -ttl 24h", stateArg, repo)
}

// shortDur renders d as a compact span using its largest one or two units:
// "45m", "5h49m", "2d3h". Sub-minute spans collapse to "<1m" so a grant seconds
// from expiry doesn't read as "0m".
func shortDur(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func renderInbox(w io.Writer, in Inbox) {
	if len(in.Parked) == 0 {
		fmt.Fprintln(w, "nothing awaits judgment.")
	} else {
		fmt.Fprintf(w, "awaiting judgment (%d)\n\n", len(in.Parked))
		for _, p := range in.Parked {
			renderParked(w, p)
		}
	}
	if len(in.Unattributed) > 0 {
		fmt.Fprintf(w, "legacy parked runs without a PR subject (%d)\n\n", len(in.Unattributed))
		for _, p := range in.Unattributed {
			renderParked(w, p)
		}
	}
	renderReadyToMerge(w, in.ReadyToMerge)
	renderNeedsGrant(w, in.NeedsGrant)
	renderDischarged(w, in)
	if len(in.Grants) == 0 {
		return
	}
	fmt.Fprintln(w, "grants")
	renderGrants(w, in.Grants)
}

// renderDischarged reports what the projection withheld. It prints whenever
// anything was withheld, even though the rows themselves are hidden by default:
// the count is the difference between an inbox that reduced correctly and one
// that lost its queue, and the operator has been quoted the un-reduced number as
// if it were real work.
func renderDischarged(w io.Writer, in Inbox) {
	total := in.Discharged.Total()
	if total == 0 {
		return
	}
	fmt.Fprintf(w, "discharged (%d) — already settled, not shown\n", total)
	fmt.Fprintf(w, "  parked          %s\n", in.Discharged.Parked.summary())
	fmt.Fprintf(w, "  ready to merge  %s\n", in.Discharged.ReadyToMerge.summary())
	if in.DischargedRows == nil {
		fmt.Fprintf(w, "  → gate next -all to see them\n\n")
		return
	}
	fmt.Fprintln(w)
	renderDischargedRows(w, *in.DischargedRows)
}

// renderDischargedRows lists the withheld rows themselves. They are history, so
// each prints its reason and nothing to act on.
func renderDischargedRows(w io.Writer, rows DischargedRows) {
	for _, p := range rows.Parked {
		fmt.Fprintf(w, "  %s  %s  [%s]\n", subjectOrRun(p.Repo, p.Number, p.Run), p.ParkedAt, p.Discharge)
		fmt.Fprintf(w, "    %s\n", p.DischargeWhy)
	}
	for _, r := range rows.ReadyToMerge {
		fmt.Fprintf(w, "  %s  ready  [%s]\n", subjectOrRun(r.Repo, r.Number, r.Run), r.Discharge)
		fmt.Fprintf(w, "    %s\n", r.DischargeWhy)
	}
	fmt.Fprintln(w)
}

// subjectOrRun labels a row by its PR when it has one, and by its run when it
// does not — the same fallback every other surface uses.
func subjectOrRun(repo string, number int, run string) string {
	if repo == "" || number == 0 {
		return run
	}
	return fmt.Sprintf("%s#%d  %s", repo, number, run)
}

// renderReadyToMerge lists the PRs gate has judged clean and is ready to land,
// each with its paste-ready merge command — symmetric with the parked section.
func renderReadyToMerge(w io.Writer, rows []ReadyRow) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "ready to merge (%d)\n\n", len(rows))
	for _, r := range rows {
		head := r.Run
		if r.Repo != "" {
			head = fmt.Sprintf("%s#%d  %s", r.Repo, r.Number, r.Run)
		}
		fmt.Fprintf(w, "  %s\n", head)
		if r.Title != "" {
			fmt.Fprintf(w, "  %q\n", r.Title)
		}
		if r.HeadSHA != "" {
			fmt.Fprintf(w, "  head %s\n", r.HeadSHA)
		}
		fmt.Fprintf(w, "  → %s\n", r.MergeCommand)
		renderCoverage(w, r.Coverage)
		fmt.Fprintln(w)
	}
}

// renderNeedsGrant lists the repos whose merge grant has lapsed, each with its
// paste-ready re-mint — symmetric with the grants ledger below it.
func renderNeedsGrant(w io.Writer, rows []NeedsGrantRow) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "needs a grant (%d)\n\n", len(rows))
	for _, r := range rows {
		head := fmt.Sprintf("  %s  %s", r.Repo, r.GrantState)
		if r.OpenPRs != nil {
			head += fmt.Sprintf("  %d open PR(s)", *r.OpenPRs)
		}
		fmt.Fprintln(w, head)
		if r.LastExpiredAt != "" {
			fmt.Fprintf(w, "  last expired %s\n", r.LastExpiredAt)
		}
		fmt.Fprintf(w, "  → %s\n\n", r.SuggestedMint)
	}
}

func renderParked(w io.Writer, p ParkedRun) {
	head := p.Run
	if p.Repo != "" {
		head = fmt.Sprintf("%s#%d  %s", p.Repo, p.Number, p.Run)
	}
	if p.Code != "" {
		head += "  " + p.Code
	}
	if p.Repo != "" {
		head += "  " + cyclesLabel(p.CyclesUsed, p.CyclesMax)
	}
	fmt.Fprintf(w, "  %s\n", head)
	if p.Question != "" {
		fmt.Fprintf(w, "  %q\n", p.Question)
	}
	if p.PRState == "unknown" {
		fmt.Fprintf(w, "  PR state unknown: %s\n", p.PRStateReason)
	}
	if p.JudgeCommand != "" {
		fmt.Fprintf(w, "  \u2192 %s\n", p.JudgeCommand)
	}
	if p.ResolveCommand != "" {
		fmt.Fprintf(w, "  \u2192 %s\n", p.ResolveCommand)
	}
	if p.JudgeCommand == "" && p.Escape != nil {
		fmt.Fprintf(w, "  %s\n", p.Escape.Why)
		fmt.Fprintf(w, "  \u2192 %s\n", p.Escape.Next)
	}
	fmt.Fprintf(w, "  \u2192 %s\n", p.ExplainCommand)
	renderCoverage(w, p.Coverage)
	fmt.Fprintln(w)
}

// cyclesLabel renders a review-cycle budget as "cycles N/M"; an unbounded (or
// unknown) ceiling reads "cycles N/∞".
func cyclesLabel(used, maxCycles int) string {
	if maxCycles == 0 {
		return fmt.Sprintf("cycles %d/∞", used)
	}
	return fmt.Sprintf("cycles %d/%d", used, maxCycles)
}

func renderGrants(w io.Writer, grants []GrantLine) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, g := range grants {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", g.ID, g.Repo, g.Action, g.MaxTier, grantWhen(g))
	}
	tw.Flush()
}

func grantWhen(g GrantLine) string {
	if g.Expired {
		return "expired " + g.Remaining
	}
	return g.Remaining
}

// NextInbox returns the projection itself instead of rendering it. It is the
// seam a caller needs when the inbox is an INPUT — `gate sweep` reducing its
// work list, a test asserting on the reduction — rather than something to show.
func NextInbox(st *state.Store, now func() time.Time, req NextRequest) (Inbox, error) {
	return next(st, now, req)
}
