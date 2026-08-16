package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
)

// Preflight is the batch inventory a sweep runs BEFORE it starts working: every
// open PR it intends to touch, grouped by repo, each with the coverage question
// `gate next` asks for the single PR it recommends — plus, once per repo, the
// branch-protection shape the merge will land against and the ONE paste-ready
// mint that covers the whole repo for the whole sweep.
//
// It exists because grants were being discovered one repo at a time, mid-sweep:
// a clean, green PR sat unmerged purely for want of a grant, and the default
// cycle ceiling ceiling-parked PRs whose historical review cycles had already
// spent it — both facts knowable up front, both previously learned only at the
// park. Everything here is ADVISORY and read-only, like the rest of observe.
//
// The hard boundary: this projection PRINTS mint commands. Minting is the
// operator's, and no code path here executes `gate grant`.
type Preflight struct {
	Repos []RepoPlan `json:"repos"`
	// Mints is the whole sweep's mint batch, one command per repo that needs
	// one, in repo order — the block the operator runs once instead of being
	// interrupted per repo. Empty when every inventoried PR is already covered.
	Mints []string `json:"mints,omitempty"`
	// Excluded lists the denylisted repos and PRs this inventory skipped, echoed
	// back so a silently-dropped PR is never mistaken for a clean sweep.
	Excluded []string `json:"excluded,omitempty"`
}

// RepoPlan is one repo's slice of the sweep: its open PRs, the protection shape
// their merges land against, and the single mint that covers them all.
type RepoPlan struct {
	Repo string `json:"repo"`
	// Protection is the branch-protection read, when it succeeded.
	// ProtectionError carries the read failure otherwise — a repo whose
	// protection cannot be read is still swept, just without the refresh-cost
	// warning.
	Protection      *Protection    `json:"protection,omitempty"`
	ProtectionError string         `json:"protection_error,omitempty"`
	PRs             []PreflightPR  `json:"prs"`
	PRsError        string         `json:"prs_error,omitempty"`
	Grant           *GrantSnapshot `json:"grant,omitempty"`
	// Mint is the paste-ready `gate grant` covering every PR below, and Why
	// states what forced its ceilings. Both empty when the repo is already
	// covered for the whole sweep.
	Mint string `json:"mint,omitempty"`
	Why  string `json:"why,omitempty"`
}

// Protection is the slice of a repo's branch protection a sweep needs: whether a
// BEHIND PR must be refreshed before it can merge (strict), and the checks that
// must be green. Strict is the expensive bit — a refresh costs a CI re-run and a
// fresh gate judgment, since a judgment binds to the head it was made against.
// Protected is false for a repo whose default branch carries no protection at
// all, where gate and the guard are the only boundary.
type Protection struct {
	Protected bool     `json:"protected"`
	Strict    bool     `json:"strict"`
	Contexts  []string `json:"contexts,omitempty"`
	// ConversationResolution records whether unresolved review threads block the
	// merge — a sweep that leaves a thread open on such a repo stalls at the end.
	ConversationResolution bool `json:"conversation_resolution,omitempty"`
}

// GrantSnapshot is the live merge grant covering a repo at preflight time, when
// one exists — the widest one, the same choice the coverage assessment makes.
type GrantSnapshot struct {
	ID        string `json:"id"`
	MaxTier   string `json:"max_tier"`
	MaxCycles int    `json:"max_cycles"`
}

// PreflightPR is one open PR in the sweep with its coverage answer attached.
// Coverage carries the historical cycle count, so a PR whose cycles have already
// spent the house default ceiling is visible here rather than at the park.
type PreflightPR struct {
	Number   int            `json:"number"`
	Title    string         `json:"title,omitempty"`
	HeadSHA  string         `json:"head_sha,omitempty"`
	URL      string         `json:"url,omitempty"`
	Coverage *GrantCoverage `json:"grant_coverage,omitempty"`
}

// Protections is the second live seam, alongside OpenPRs: it returns one repo's
// default-branch protection shape. Gate's command layer supplies the `gh api`
// implementation. Like OpenPRs it is called once per repo and best-effort — a
// repo whose read fails keeps its rows and records the error.
type Protections func(repo string) (Protection, error)

// PreflightRequest is the inventory's inputs. Repos is the sweep's scope; when
// empty the scope is every repo the log already knows about. Deny excludes
// either a whole repo ("owner/repo") or a single PR ("owner/repo#12").
type PreflightRequest struct {
	Repos    []string
	Deny     []string
	Fetch    OpenPRs
	Protect  Protections
	StateArg string
	Now      func() time.Time
}

// PreflightText renders the inventory for a human about to start a sweep.
func PreflightText(w io.Writer, st *state.Store, req PreflightRequest) error {
	p, err := collectPreflight(st, req)
	if err != nil {
		return err
	}
	renderPreflight(w, p)
	return nil
}

// PreflightJSON emits the same inventory as one JSON document, for a sweep
// driver that wants to branch on it rather than read it.
func PreflightJSON(w io.Writer, st *state.Store, req PreflightRequest) error {
	p, err := collectPreflight(st, req)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func collectPreflight(st *state.Store, req PreflightRequest) (Preflight, error) {
	arts, err := st.List(nil)
	if err != nil {
		return Preflight{}, err
	}
	return buildPreflight(arts, req), nil
}

// buildPreflight folds the log once — the same single-scan coverage index the
// inbox builds — then fans one open-PR fetch and one protection read per repo
// over that snapshot. Cost is O(repos), never O(PRs).
func buildPreflight(arts []state.Artifact, req PreflightRequest) Preflight {
	now := req.Now()
	idx := buildCoverageIndex(arts, now, req.StateArg)
	deny := parseDenylist(req.Deny)
	repos := scopeRepos(req.Repos, idx, arts, deny)
	live := resolveRepos(repos, req.Fetch)

	p := Preflight{Excluded: deny.list()}
	for _, repo := range repos {
		p.Repos = append(p.Repos, repoPlan(repo, idx, live, deny, req.Protect))
	}
	for _, plan := range p.Repos {
		if plan.Mint != "" {
			p.Mints = append(p.Mints, plan.Mint)
		}
	}
	return p
}

// denylist is the parsed exclusion set: whole repos, and individual PRs.
type denylist struct {
	repos    map[string]struct{}
	prs      map[string]struct{}
	original []string
}

func parseDenylist(entries []string) denylist {
	d := denylist{repos: make(map[string]struct{}), prs: make(map[string]struct{})}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		d.original = append(d.original, entry)
		repo, number, ok := splitSubject(entry)
		if !ok {
			d.repos[entry] = struct{}{}
			continue
		}
		d.prs[subjectKey(repo, number)] = struct{}{}
	}
	sort.Strings(d.original)
	return d
}

// splitSubject parses an "owner/repo#12" denylist entry. A bare repo, or a
// suffix that is not a number, is not a subject — the caller treats it as a
// whole-repo exclusion rather than guessing at a PR.
func splitSubject(entry string) (string, int, bool) {
	repo, num, found := strings.Cut(entry, "#")
	if !found {
		return "", 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return repo, n, true
}

func (d denylist) repoExcluded(repo string) bool {
	_, ok := d.repos[repo]
	return ok
}

func (d denylist) prExcluded(repo string, number int) bool {
	_, ok := d.prs[subjectKey(repo, number)]
	return ok
}

func (d denylist) list() []string { return d.original }

// scopeRepos resolves the sweep's repo scope. An explicit -repo list wins; with
// none, the scope is every repo the log has ever seen — grants minted, refusals
// recorded, PRs gated — which is the set a portfolio sweep would walk anyway.
// Denylisted repos are dropped here, before any network call is spent on them.
func scopeRepos(explicit []string, idx coverageIndex, arts []state.Artifact, deny denylist) []string {
	seen := make(map[string]struct{})
	for _, repo := range explicit {
		if r := strings.TrimSpace(repo); r != "" {
			seen[r] = struct{}{}
		}
	}
	if len(seen) == 0 {
		for _, repo := range knownRepos(idx, arts) {
			seen[repo] = struct{}{}
		}
	}
	repos := make([]string, 0, len(seen))
	for repo := range seen {
		if deny.repoExcluded(repo) {
			continue
		}
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

// knownRepos is every repo the log names: live grants, lapsed grants, refusals
// recorded for want of one, and the subjects of past runs.
func knownRepos(idx coverageIndex, arts []state.Artifact) []string {
	seen := make(map[string]struct{})
	for repo := range idx.liveGrants {
		seen[repo] = struct{}{}
	}
	for repo := range idx.lastExpired {
		seen[repo] = struct{}{}
	}
	for key := range idx.cycles {
		addSubjectRepo(seen, key)
	}
	for key := range idx.subjectRun {
		addSubjectRepo(seen, key)
	}
	for _, a := range arts {
		if a.Kind != state.KindGrantNeeded {
			continue
		}
		var b grantNeededBody
		if err := json.Unmarshal(a.Body, &b); err == nil && b.Repo != "" {
			seen[b.Repo] = struct{}{}
		}
	}
	repos := make([]string, 0, len(seen))
	for repo := range seen {
		repos = append(repos, repo)
	}
	return repos
}

func addSubjectRepo(seen map[string]struct{}, key string) {
	if repo, _, ok := splitSubject(key); ok {
		seen[repo] = struct{}{}
	}
}

// repoPlan assembles one repo's row: its protection shape, its open PRs with
// coverage, and the single mint that would cover the lot.
func repoPlan(repo string, idx coverageIndex, live liveRepos, deny denylist, protect Protections) RepoPlan {
	plan := RepoPlan{Repo: repo, PRs: preflightPRs(repo, idx, live, deny)}
	if err := live.errs[repo]; err != nil {
		plan.PRsError = err.Error()
	}
	plan.Protection, plan.ProtectionError = readProtection(repo, protect)
	if g, ok := idx.widestGrant(repo); ok {
		plan.Grant = &GrantSnapshot{ID: g.id, MaxTier: g.maxTier, MaxCycles: g.maxCycles}
	}
	plan.Mint, plan.Why = repoMint(repo, idx, plan.PRs)
	return plan
}

// readProtection calls the protection seam once, tolerating both a nil seam (the
// offline projection) and a read failure.
func readProtection(repo string, protect Protections) (*Protection, string) {
	if protect == nil {
		return nil, ""
	}
	p, err := protect(repo)
	if err != nil {
		return nil, err.Error()
	}
	return &p, ""
}

func (idx coverageIndex) widestGrant(repo string) (coverageGrant, bool) {
	live := idx.liveGrants[repo]
	if len(live) == 0 {
		return coverageGrant{}, false
	}
	return live[0], true
}

// preflightPRs assesses every open PR of a repo, skipping the denylisted ones.
// Order is by number, so a sweep walks a repo the way a human reads it.
func preflightPRs(repo string, idx coverageIndex, live liveRepos, deny denylist) []PreflightPR {
	open := live.open[repo]
	rows := make([]PreflightPR, 0, len(open))
	for number, pr := range open {
		if deny.prExcluded(repo, number) {
			continue
		}
		rows = append(rows, PreflightPR{
			Number:   number,
			Title:    pr.Title,
			HeadSHA:  pr.HeadSHA,
			URL:      pr.URL,
			Coverage: idx.assessSubject(repo, number),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Number < rows[j].Number })
	return rows
}

// repoMint composes the ONE grant that covers a repo for the whole sweep: wide
// enough for the highest tier any of its PRs has already shown, and for the
// cycle number the most-reviewed of them would next consume. This is the batch
// answer to the per-PR mints `gate next` suggests — an operator mints once per
// repo, not once per park.
//
// It returns nothing when a live grant already admits every PR: the preflight is
// a list of things to do, not a status board, and suggesting a re-mint over a
// covering grant would train the operator to ignore the block.
func repoMint(repo string, idx coverageIndex, prs []PreflightPR) (string, string) {
	need, why := mintNeed(prs)
	if why == "" {
		return "", ""
	}
	// Never narrow what the repo already holds: a re-mint that lowers a live
	// ceiling would park PRs the current grant admits.
	if g, ok := idx.widestGrant(repo); ok {
		need = need.widen(g)
	}
	return coverageMint(repo, idx.stateArg, need.maxTier, need.maxCycles), why
}

// mintCeilings is the widest ceiling pair a repo's PRs require.
type mintCeilings struct {
	maxTier   string
	maxCycles int
}

// widen raises a composed ceiling to at least what a live grant already carries.
// A zero cycle ceiling is unbounded, so it wins over any finite one.
func (m mintCeilings) widen(g coverageGrant) mintCeilings {
	if tier.Valid(g.maxTier) && tier.Rank(g.maxTier) > tier.Rank(m.maxTier) {
		m.maxTier = g.maxTier
	}
	if g.maxCycles == 0 || cycleHeadroom(g.maxCycles) > cycleHeadroom(m.maxCycles) {
		m.maxCycles = g.maxCycles
	}
	return m
}

// mintNeed folds a repo's PR coverage into one ceiling pair plus the reason a
// mint is needed at all. The reason is empty — no mint — only when every PR is
// covered. Ceilings are composed across ALL the repo's PRs, covered ones
// included: one grant serves the whole sweep, so it must admit the PR with the
// longest review history even if that PR is fine under today's grant.
func mintNeed(prs []PreflightPR) (mintCeilings, string) {
	need := mintCeilings{maxTier: "T1", maxCycles: 3}
	var reasons []string
	for _, pr := range prs {
		c := pr.Coverage
		if c == nil {
			continue
		}
		need = need.raise(*c)
		if c.State == "covered" {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("#%d: %s", pr.Number, c.Why))
	}
	if len(reasons) == 0 {
		return need, ""
	}
	return need, strings.Join(reasons, "; ")
}

// raise widens the composed ceilings to admit one PR: the tier its evidence
// already showed, and the cycle its next gate run would consume.
func (m mintCeilings) raise(c GrantCoverage) mintCeilings {
	if t := wideTier(c); tier.Rank(t) > tier.Rank(m.maxTier) {
		m.maxTier = t
	}
	if n := wideCycles(c); n > m.maxCycles {
		m.maxCycles = n
	}
	return m
}

func renderPreflight(w io.Writer, p Preflight) {
	if len(p.Repos) == 0 {
		fmt.Fprintln(w, "no repos in scope.")
		return
	}
	for _, plan := range p.Repos {
		renderRepoPlan(w, plan)
	}
	renderExcluded(w, p.Excluded)
	renderMints(w, p.Mints)
}

func renderRepoPlan(w io.Writer, plan RepoPlan) {
	fmt.Fprintf(w, "%s (%d open PR(s))\n", plan.Repo, len(plan.PRs))
	if plan.PRsError != "" {
		fmt.Fprintf(w, "  open PRs unreadable: %s\n", plan.PRsError)
	}
	renderProtection(w, plan)
	if plan.Grant != nil {
		fmt.Fprintf(w, "  grant %s  max-tier %s  max-cycles %d\n", plan.Grant.ID, plan.Grant.MaxTier, plan.Grant.MaxCycles)
	}
	for _, pr := range plan.PRs {
		renderPreflightPR(w, pr)
	}
	if plan.Mint != "" {
		fmt.Fprintf(w, "  mint needed — %s\n", plan.Why)
	}
	fmt.Fprintln(w)
}

func renderProtection(w io.Writer, plan RepoPlan) {
	if plan.ProtectionError != "" {
		fmt.Fprintf(w, "  protection unreadable: %s\n", plan.ProtectionError)
		return
	}
	if plan.Protection == nil {
		return
	}
	fmt.Fprintf(w, "  protection: %s\n", protectionLine(*plan.Protection))
}

// protectionLine states the merge-shape facts a sweep plans around: strict is
// the expensive one — a BEHIND PR there costs a refresh, a CI re-run, and a
// fresh gate judgment.
func protectionLine(p Protection) string {
	if !p.Protected {
		return "none — gate and the guard are the only boundary"
	}
	parts := []string{"not strict — a BEHIND PR merges without a refresh"}
	if p.Strict {
		parts = []string{"STRICT — a BEHIND PR costs refresh + CI re-run + a fresh gate judgment"}
	}
	if len(p.Contexts) > 0 {
		parts = append(parts, "checks "+strings.Join(p.Contexts, ", "))
	}
	if p.ConversationResolution {
		parts = append(parts, "conversation resolution required")
	}
	return strings.Join(parts, "; ")
}

func renderPreflightPR(w io.Writer, pr PreflightPR) {
	head := fmt.Sprintf("  #%d", pr.Number)
	if pr.Title != "" {
		head += fmt.Sprintf("  %q", pr.Title)
	}
	fmt.Fprintln(w, head)
	c := pr.Coverage
	if c == nil {
		return
	}
	fmt.Fprintf(w, "    cycles used %d, next %d\n", c.CyclesUsed, c.NextCycle)
	if c.State == "covered" {
		return
	}
	fmt.Fprintf(w, "    grant %s: %s\n", c.State, c.Why)
}

func renderExcluded(w io.Writer, excluded []string) {
	if len(excluded) == 0 {
		return
	}
	fmt.Fprintf(w, "excluded (%d): %s\n\n", len(excluded), strings.Join(excluded, ", "))
}

// renderMints prints the whole sweep's mint batch last, as one block. The point
// of the surface: the operator mints every grant the sweep needs once, up front,
// instead of being interrupted per repo. Gate never runs these.
func renderMints(w io.Writer, mints []string) {
	if len(mints) == 0 {
		fmt.Fprintln(w, "every repo in scope is covered — no mints needed.")
		return
	}
	fmt.Fprintf(w, "mint before the sweep (%d) — operator only; gate never runs these\n\n", len(mints))
	for _, m := range mints {
		fmt.Fprintf(w, "  %s\n", m)
	}
}
