package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
)

// GrantCoverage is the inventory answer for one PR the inbox puts in front of
// the operator: does a live merge grant actually cover this repo, and would its
// tier / cycle ceiling park the next gate run before it ever reaches a decision.
// It exists because the ceiling used to be discovered at gate time — the inbox
// would recommend a PR whose repo had no grant at all, and the gap only
// surfaced when the operator ran `gate gate` and got a refusal.
//
// It is ADVISORY, like everything in observe: a projection of the log, never an
// authorization. `capability.Check` and the ladder remain the only authority —
// a "covered" row can still refuse (a grant this projection cannot verify the
// signature of, a head-bound grant, an expiry crossed between the two reads),
// and CyclesUsed is a best-effort count that skips outcomes whose parent
// verdict this projection cannot follow. Gate parks on the real numbers; this
// surface only saves the operator a round trip.
type GrantCoverage struct {
	// State is one of:
	//   covered — a live merge grant whose ceilings admit the next cycle
	//   ceiling — a live merge grant that would ceiling-park (tier or cycles)
	//   expired — the repo's merge grants have all lapsed
	//   absent  — the repo has never had a merge grant in this log
	State string `json:"state"`
	// Grant is the live grant this assessment read, when one exists.
	Grant     string `json:"grant,omitempty"`
	MaxTier   string `json:"max_tier,omitempty"`
	MaxCycles int    `json:"max_cycles,omitempty"`
	// VerdictTier is the run's composed tier — the monotone-max over its
	// verdicts, the same composition the ladder applies — compared against the
	// grant's tier ceiling. Empty when the run recorded no readable verdict.
	VerdictTier string `json:"verdict_tier,omitempty"`
	// CyclesUsed counts the review cycles this repo+PR has already consumed;
	// NextCycle is CyclesUsed+1, the number the next gate run would be checked
	// against.
	CyclesUsed    int    `json:"cycles_used"`
	NextCycle     int    `json:"next_cycle"`
	LastExpiredAt string `json:"last_expired_at,omitempty"`
	// Why states the gap in one line, and SuggestedMint is the paste-ready
	// `gate grant` that closes it. Both are empty when State is "covered".
	Why           string `json:"why,omitempty"`
	SuggestedMint string `json:"suggested_mint,omitempty"`
}

// coverageIndex is the one-pass read of the log the coverage assessment needs:
// live merge grants per repo, the most recent lapse per repo, consumed cycles
// per subject, and each run's composed verdict tier. Built once per projection
// so N rows cost one scan, not N.
type coverageIndex struct {
	liveGrants  map[string][]coverageGrant
	lastExpired map[string]time.Time
	cycles      map[string]int
	runTier     map[string]string
	subjectRun  map[string]string
	stateArg    string
}

// coverageGrant is the slice of a live merge grant the assessment compares
// against — the same deliberate copy of capability.Grant's signed shape the
// ledger reads, plus the artifact id so a row can name the grant it read.
type coverageGrant struct {
	id        string
	maxTier   string
	maxCycles int
	expiresAt time.Time
}

// verdictBody is the slice of a verdict artifact coverage reads: the tier it
// composed and the subject it names. A deliberate copy of verify's write shape,
// keeping the projection decoupled from the decision layer.
type verdictBody struct {
	Tier    string `json:"tier"`
	Subject struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	} `json:"subject"`
}

func subjectKey(repo string, number int) string { return fmt.Sprintf("%s#%d", repo, number) }

// buildCoverageIndex folds the log into the coverage index in a single pass over
// the artifacts, plus one pass to join outcomes to their parent verdicts.
func buildCoverageIndex(arts []state.Artifact, now time.Time, stateArg string) coverageIndex {
	idx := coverageIndex{
		liveGrants:  make(map[string][]coverageGrant),
		lastExpired: make(map[string]time.Time),
		cycles:      cyclesBySubject(arts),
		runTier:     runTiers(arts),
		subjectRun:  subjectRuns(arts),
		stateArg:    stateArg,
	}
	for _, a := range arts {
		g, ok := decodeMergeGrant(a)
		if !ok {
			continue
		}
		idx.addGrant(a.ID, g, now)
	}
	for repo := range idx.liveGrants {
		sortCoverageGrants(idx.liveGrants[repo])
	}
	return idx
}

// addGrant files one merge grant as live or lapsed. Expiry matches
// capability.Check exactly — expired strictly after the instant — so a grant at
// its expiry is still live here too.
func (idx coverageIndex) addGrant(id string, g grantBody, now time.Time) {
	if now.After(g.ExpiresAt) {
		if g.ExpiresAt.After(idx.lastExpired[g.Repo]) {
			idx.lastExpired[g.Repo] = g.ExpiresAt
		}
		return
	}
	idx.liveGrants[g.Repo] = append(idx.liveGrants[g.Repo], coverageGrant{
		id: id, maxTier: g.MaxTier, maxCycles: g.MaxCycles, expiresAt: g.ExpiresAt,
	})
}

func decodeMergeGrant(a state.Artifact) (grantBody, bool) {
	if a.Kind != state.KindGrant {
		return grantBody{}, false
	}
	var g grantBody
	if err := json.Unmarshal(a.Body, &g); err != nil {
		return grantBody{}, false
	}
	if g.Action != "merge" || g.Repo == "" {
		return grantBody{}, false
	}
	return g, true
}

// sortCoverageGrants orders a repo's live grants widest-first — highest tier
// ceiling, then the most cycle headroom (0 is unbounded, so it leads), then the
// latest expiry, then id for determinism. The widest grant is the one an
// operator would actually spend, so it is the one the row reports on.
func sortCoverageGrants(gs []coverageGrant) {
	sort.Slice(gs, func(i, j int) bool {
		a, b := gs[i], gs[j]
		if a.maxTier != b.maxTier {
			return tier.Rank(a.maxTier) > tier.Rank(b.maxTier)
		}
		if a.maxCycles != b.maxCycles {
			return cycleHeadroom(a.maxCycles) > cycleHeadroom(b.maxCycles)
		}
		if !a.expiresAt.Equal(b.expiresAt) {
			return a.expiresAt.After(b.expiresAt)
		}
		return a.id < b.id
	})
}

// cycleHeadroom maps a ceiling onto a comparable width: 0 (unbounded) is the
// widest there is, so it sorts above every finite ceiling.
func cycleHeadroom(maxCycles int) int {
	if maxCycles == 0 {
		return int(^uint(0) >> 1)
	}
	return maxCycles
}

// runTiers composes each run's tier the way the ladder does — monotone-max over
// the run's verdicts — so the coverage row compares the grant ceiling against
// the same number the gate run would.
func runTiers(arts []state.Artifact) map[string]string {
	tiers := make(map[string]string)
	for _, a := range arts {
		if a.Kind != state.KindVerdict {
			continue
		}
		var v verdictBody
		if err := json.Unmarshal(a.Body, &v); err != nil || !tier.Valid(v.Tier) {
			continue
		}
		if cur, ok := tiers[a.Run]; ok && tier.Rank(cur) >= tier.Rank(v.Tier) {
			continue
		}
		tiers[a.Run] = v.Tier
	}
	return tiers
}

// subjectRuns maps each repo#PR to its NEWEST gate run — the one that started
// last. The inbox already knows a row's run; a sweep inventory starts from a PR
// number instead, so it needs this join to ask the coverage question against the
// tier that PR's latest gate run actually composed.
//
// Recency is the run's FIRST artifact, never its last. Judging an older parked
// run appends a fresh verdict for that run at the end of the log, so ordering on
// the newest verdict would pick the most recently *updated* run and assess the
// PR against a superseded run's tier.
func subjectRuns(arts []state.Artifact) map[string]string {
	started := runStartOrder(arts)
	runs := make(map[string]string)
	for _, a := range arts {
		if a.Kind != state.KindVerdict {
			continue
		}
		var v verdictBody
		if err := json.Unmarshal(a.Body, &v); err != nil || v.Subject.Repo == "" {
			continue
		}
		key := subjectKey(v.Subject.Repo, v.Subject.Number)
		if cur, ok := runs[key]; ok && started[cur] >= started[a.Run] {
			continue
		}
		runs[key] = a.Run
	}
	return runs
}

// runStartOrder records where each run first appears in the log — the only
// stable proxy for "which run started later", since a run's artifacts continue
// to accrue long after a newer run has begun.
func runStartOrder(arts []state.Artifact) map[string]int {
	started := make(map[string]int)
	for order, a := range arts {
		if _, seen := started[a.Run]; !seen {
			started[a.Run] = order
		}
	}
	return started
}

// assessSubject is assess for a caller holding only a PR: it resolves the
// subject's latest verdict-bearing run itself. A PR gate has never run against
// resolves to no run, so its coverage carries no verdict tier — exactly the
// never-gated case a sweep preflight is asking about.
func (idx coverageIndex) assessSubject(repo string, number int) *GrantCoverage {
	return idx.assess(repo, number, idx.subjectRun[subjectKey(repo, number)])
}

// cyclesBySubject counts consumed review cycles per repo#PR, mirroring gate's
// own rule (main.go cycleCount): one counting outcome per distinct run is one
// cycle, authorization parks and capability refusals do not count, and the
// repo+PR join runs outcome → parent verdict → subject.
//
// It differs from the decision-path count in exactly one way, deliberately: an
// outcome whose parent verdict is missing or unreadable is SKIPPED rather than
// erroring the whole projection. Gate parks on an unreadable count; the inbox
// must still render, so this advisory number may undercount a corrupt log. It
// never overcounts.
func cyclesBySubject(arts []state.Artifact) map[string]int {
	byID := make(map[string]state.Artifact, len(arts))
	for _, a := range arts {
		byID[a.ID] = a
	}
	runsPerSubject := make(map[string]map[string]struct{})
	for _, a := range arts {
		key, ok := countingSubject(byID, a)
		if !ok {
			continue
		}
		if runsPerSubject[key] == nil {
			runsPerSubject[key] = make(map[string]struct{})
		}
		runsPerSubject[key][a.Run] = struct{}{}
	}
	counts := make(map[string]int, len(runsPerSubject))
	for key, runs := range runsPerSubject {
		counts[key] = len(runs)
	}
	return counts
}

// countingSubject returns the repo#PR key an outcome artifact consumes a cycle
// against, or false when the artifact is not a counting outcome or its subject
// cannot be resolved.
func countingSubject(byID map[string]state.Artifact, a state.Artifact) (string, bool) {
	if a.Kind != state.KindAction && a.Kind != state.KindEscalation {
		return "", false
	}
	var b outcomeCoverageBody
	if err := json.Unmarshal(a.Body, &b); err != nil {
		return "", false
	}
	if !countsAsCycleRow(a.Kind, b) || len(a.Parents) == 0 {
		return "", false
	}
	parent, ok := byID[a.Parents[0]]
	if !ok || parent.Kind != state.KindVerdict {
		return "", false
	}
	var v verdictBody
	if err := json.Unmarshal(parent.Body, &v); err != nil || v.Subject.Repo == "" {
		return "", false
	}
	return subjectKey(v.Subject.Repo, v.Subject.Number), true
}

// outcomeCoverageBody is the slice of an action/escalation body the cycle count
// reads — the outcome and the machine-readable park code.
type outcomeCoverageBody struct {
	Outcome string `json:"outcome"`
	Code    string `json:"code"`
}

// countsAsCycleRow mirrors main.go's countsAsCycle: an escalation counts only
// when it carries no authorization code (a content park, not a ceiling park),
// and an action counts unless it was a capability refusal.
func countsAsCycleRow(kind string, b outcomeCoverageBody) bool {
	if kind == state.KindEscalation {
		return b.Code == ""
	}
	return b.Outcome != "capability_refused"
}

// assess answers the inventory question for one row. It returns nil when there
// is nothing to say — the repo is covered and the next cycle fits — so a clean
// inbox stays quiet and only a real gap prints.
func (idx coverageIndex) assess(repo string, number int, run string) *GrantCoverage {
	if repo == "" || number == 0 {
		return nil
	}
	used := idx.cycles[subjectKey(repo, number)]
	c := GrantCoverage{
		VerdictTier: idx.runTier[run],
		CyclesUsed:  used,
		NextCycle:   used + 1,
	}
	live := idx.liveGrants[repo]
	if len(live) == 0 {
		return idx.uncoveredRow(c, repo)
	}
	g := live[0]
	c.Grant, c.MaxTier, c.MaxCycles = g.id, g.maxTier, g.maxCycles
	why := ceilingWhy(c, g)
	if why == "" {
		c.State = "covered"
		return &c
	}
	c.State = "ceiling"
	c.Why = why
	c.SuggestedMint = coverageMint(repo, idx.stateArg, wideTier(c), wideCycles(c))
	return &c
}

// uncoveredRow fills in the no-live-grant case, distinguishing a repo whose
// grants lapsed (name the lapse so the operator knows it is a re-mint) from one
// that never had one.
func (idx coverageIndex) uncoveredRow(c GrantCoverage, repo string) *GrantCoverage {
	c.State = "absent"
	c.Why = "no merge grant has ever been minted for " + repo
	if at, ok := idx.lastExpired[repo]; ok {
		c.State = "expired"
		c.LastExpiredAt = at.UTC().Format(time.RFC3339)
		c.Why = "the merge grant for " + repo + " expired " + c.LastExpiredAt
	}
	c.SuggestedMint = coverageMint(repo, idx.stateArg, wideTier(c), wideCycles(c))
	return &c
}

// ceilingWhy states which ceiling of a live grant would park the next run, or
// "" when neither would. Tier is reported first: it is the ceiling an operator
// can misread as a cycle problem.
func ceilingWhy(c GrantCoverage, g coverageGrant) string {
	if c.VerdictTier != "" && tier.Rank(c.VerdictTier) > tier.Rank(g.maxTier) {
		return fmt.Sprintf("verdict tier %s exceeds grant ceiling %s", c.VerdictTier, g.maxTier)
	}
	if g.maxCycles != 0 && c.NextCycle > g.maxCycles {
		return fmt.Sprintf("cycle %d would exceed grant ceiling %d", c.NextCycle, g.maxCycles)
	}
	return ""
}

// wideTier is the tier a suggested mint needs: the run's own composed tier when
// known, else `gate grant`'s default. It never suggests narrower than what the
// evidence already showed.
func wideTier(c GrantCoverage) string {
	if tier.Valid(c.VerdictTier) {
		return c.VerdictTier
	}
	return "T1"
}

// wideCycles is the cycle ceiling a suggested mint needs: enough to admit the
// next cycle, never below the house default of 3.
func wideCycles(c GrantCoverage) int {
	if c.NextCycle > 3 {
		return c.NextCycle
	}
	return 3
}

// coverageMint is the paste-ready mint for a gap, splicing stateArg the same way
// every other suggested command does so a copy targets the log this inbox read.
func coverageMint(repo, stateArg, maxTier string, maxCycles int) string {
	return fmt.Sprintf("gate grant%s -repo %s -action merge -max-tier %s -max-cycles %d -ttl 24h",
		stateArg, repo, maxTier, maxCycles)
}

// renderCoverage prints a row's coverage only when it names a gap. A covered row
// prints nothing: the inbox stays a queue of things to do, not a status board.
func renderCoverage(w io.Writer, c *GrantCoverage) {
	if c == nil || c.State == "covered" {
		return
	}
	fmt.Fprintf(w, "  grant %s: %s\n", c.State, c.Why)
	fmt.Fprintf(w, "  → %s\n", c.SuggestedMint)
}
