package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
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
	Run            string `json:"run"`
	Repo           string `json:"repo,omitempty"`
	Number         int    `json:"number,omitempty"`
	Title          string `json:"title,omitempty"`
	HeadSHA        string `json:"head_sha,omitempty"`
	URL            string `json:"url,omitempty"`
	PRState        string `json:"pr_state,omitempty"`
	PRStateReason  string `json:"pr_state_reason,omitempty"`
	Question       string `json:"question"`
	Code           string `json:"code,omitempty"`
	Grant          string `json:"grant,omitempty"`
	ParkedAt       string `json:"parked_at"`
	JudgeCommand   string `json:"judge_command"`
	ExplainCommand string `json:"explain_command"`
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

// escalationBody is the slice of an escalation body the inbox reads: the parked
// run's question and its machine-readable park code, the grant it ran under, and
// the PR subject when the escalation carried one.
type escalationBody struct {
	Question string `json:"question"`
	Code     string `json:"code"`
	Grant    string `json:"grant"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
}

// NextText renders the inbox as scannable text. stateArg is spliced into the
// paste-ready commands (empty for the ambient state dir; " -state <dir>" for an
// explicit one) so a copied command targets the same log this inbox read.
func NextText(w io.Writer, st *state.Store, now func() time.Time, stateArg string) error {
	in, err := collect(st, now, stateArg)
	if err != nil {
		return err
	}
	renderInbox(w, in)
	return nil
}

// NextJSON marshals the inbox projection as one JSON document — the console feed.
func NextJSON(w io.Writer, st *state.Store, now func() time.Time, stateArg string) error {
	in, err := collect(st, now, stateArg)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(in)
}

// PRLookup is the read-only mechanism used by the live inbox to check whether
// a projected subject is still open. Gate's command layer supplies the GitHub
// implementation; observe owns only the projection behavior.
type PRLookup func(repo string, number int) (LivePR, error)

// PRLister counts a repo's open PRs — the live enrichment seam for needs_grant[].
// Gate's command layer supplies the `gh pr list` implementation. It is called
// best-effort: a per-repo error drops that repo from needs_grant[] (see
// enrichNeedsGrant), never fails the whole projection.
type PRLister func(repo string) (int, error)

// LivePR is the small display/status slice returned by a live PR read.
type LivePR struct {
	State   string
	Title   string
	HeadSHA string
	URL     string
}

// NextJSONLive emits the console feed reconciled with current PR state. A
// failed lookup remains visible as unknown; only a confirmed non-open PR is
// removed from the attention queue.
func NextJSONLive(w io.Writer, st *state.Store, now func() time.Time, stateArg string, lookup PRLookup, lister PRLister) error {
	in, err := collect(st, now, stateArg)
	if err != nil {
		return err
	}
	in.Parked = reconcileLive(in.Parked, lookup)
	in.NeedsGrant = enrichNeedsGrant(in.NeedsGrant, lister)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(in)
}

// NextTextLive is the human-readable form of NextJSONLive.
func NextTextLive(w io.Writer, st *state.Store, now func() time.Time, stateArg string, lookup PRLookup, lister PRLister) error {
	in, err := collect(st, now, stateArg)
	if err != nil {
		return err
	}
	in.Parked = reconcileLive(in.Parked, lookup)
	in.NeedsGrant = enrichNeedsGrant(in.NeedsGrant, lister)
	renderInbox(w, in)
	return nil
}

// enrichNeedsGrant attaches a live open-PR count to each needs_grant row via the
// PRLister seam. It is best-effort and degrades a row rather than dropping it: a
// per-repo lookup failure KEEPS the row with OpenPRs left nil (omitted from
// JSON), mirroring how reconcileLive degrades a parked row to "unknown". The
// lapsed grant is the primary signal — dropping the whole row when gh is
// unreachable would reintroduce the very invisible needs-a-grant state this
// surface exists to kill. The projection never fails as a whole.
func enrichNeedsGrant(rows []NeedsGrantRow, lister PRLister) []NeedsGrantRow {
	out := make([]NeedsGrantRow, 0, len(rows))
	for _, r := range rows {
		n, err := lister(r.Repo)
		if err == nil {
			count := n
			r.OpenPRs = &count
		}
		out = append(out, r)
	}
	return out
}

type liveResult struct {
	index int
	pr    LivePR
	err   error
}

func reconcileLive(parked []ParkedRun, lookup PRLookup) []ParkedRun {
	results := make(chan liveResult, len(parked))
	jobs := make(chan int, len(parked))
	for i := range parked {
		jobs <- i
	}
	close(jobs)

	const maxWorkers = 8
	for range min(len(parked), maxWorkers) {
		go func() {
			for i := range jobs {
				p := parked[i]
				pr, err := lookup(p.Repo, p.Number)
				results <- liveResult{index: i, pr: pr, err: err}
			}
		}()
	}

	resolved := make([]liveResult, len(parked))
	for range parked {
		result := <-results
		resolved[result.index] = result
	}

	out := make([]ParkedRun, 0, len(parked))
	for i, p := range parked {
		result := resolved[i]
		if result.err != nil {
			p.PRState = "unknown"
			p.PRStateReason = result.err.Error()
			out = append(out, p)
			continue
		}
		state := strings.ToUpper(result.pr.State)
		p.PRState = state
		p = mergeLivePR(p, result.pr)
		if state == "OPEN" {
			out = append(out, p)
			continue
		}
		if state != "MERGED" && state != "CLOSED" {
			p.PRState = "unknown"
			p.PRStateReason = fmt.Sprintf("GitHub returned unrecognized PR state %q", result.pr.State)
			out = append(out, p)
		}
	}
	return out
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
func collect(st *state.Store, now func() time.Time, stateArg string) (Inbox, error) {
	arts, err := st.List(nil)
	if err != nil {
		return Inbox{}, err
	}
	return buildInbox(arts, now(), stateArg), nil
}

func buildInbox(arts []state.Artifact, now time.Time, stateArg string) Inbox {
	parked, unattributed := parkedRuns(arts, stateArg)
	return Inbox{
		Parked:       parked,
		Unattributed: unattributed,
		Grants:       grantLines(arts, now),
		NeedsGrant:   needsGrantRows(arts, now, stateArg),
	}
}

// parkedRuns finds every run whose latest terminal artifact is an escalation —
// the runs still awaiting judgment. A run parks by appending an escalation and
// resolves by appending an action (or a later escalation, if a judgment still
// left it over-ceiling), so the last terminal in log order is the run's current
// state. Output is oldest-park-first: age is a fact, not a priority call.
func parkedRuns(arts []state.Artifact, stateArg string) ([]ParkedRun, []ParkedRun) {
	last := make(map[string]terminalRun)
	facts := make(map[string]runFacts)
	for order, a := range arts {
		facts[a.Run] = mergeRunFacts(facts[a.Run], factsFromArtifact(a))
		if a.Kind == state.KindAction || a.Kind == state.KindEscalation {
			last[a.Run] = terminalRun{artifact: a, order: order}
		}
	}

	// A PR may be gated repeatedly, producing a fresh run each time. Reduce
	// those runs by subject so a later terminal action also resolves older
	// parked attempts for that PR.
	latest := make(map[string]terminalRun)
	var unattributed []ParkedRun
	for run, terminal := range last {
		f := facts[run]
		if f.Repo == "" || f.Number == 0 {
			if terminal.artifact.Kind == state.KindEscalation {
				unattributed = append(unattributed, parkedFromEscalation(terminal.artifact, f, stateArg))
			}
			continue
		}
		key := fmt.Sprintf("%s#%d", f.Repo, f.Number)
		terminal.facts = f
		current, ok := latest[key]
		if !ok || terminal.order > current.order {
			latest[key] = terminal
		}
	}

	parked := make([]ParkedRun, 0, len(latest))
	for _, terminal := range latest {
		if terminal.artifact.Kind == state.KindEscalation {
			parked = append(parked, parkedFromEscalation(terminal.artifact, terminal.facts, stateArg))
		}
	}
	sortParked(parked)
	sortParked(unattributed)
	return parked, unattributed
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
	var b escalationBody
	_ = json.Unmarshal(a.Body, &b)
	facts = mergeRunFacts(facts, runFacts{Repo: b.Repo, Number: b.Number})
	p := ParkedRun{
		Run:            a.Run,
		Repo:           facts.Repo,
		Number:         facts.Number,
		Title:          facts.Title,
		HeadSHA:        facts.HeadSHA,
		Question:       b.Question,
		Code:           b.Code,
		Grant:          b.Grant,
		ParkedAt:       a.Time.UTC().Format(time.RFC3339),
		JudgeCommand:   judgeCommand(a.Run, b.Grant, stateArg),
		ExplainCommand: fmt.Sprintf("gate explain%s -run %s -html", stateArg, a.Run),
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
	renderNeedsGrant(w, in.NeedsGrant)
	if len(in.Grants) == 0 {
		return
	}
	fmt.Fprintln(w, "grants")
	renderGrants(w, in.Grants)
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
	fmt.Fprintf(w, "  %s\n", head)
	if p.Question != "" {
		fmt.Fprintf(w, "  %q\n", p.Question)
	}
	if p.PRState == "unknown" {
		fmt.Fprintf(w, "  PR state unknown: %s\n", p.PRStateReason)
	}
	fmt.Fprintf(w, "  → %s\n", p.JudgeCommand)
	fmt.Fprintf(w, "  → %s\n\n", p.ExplainCommand)
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
