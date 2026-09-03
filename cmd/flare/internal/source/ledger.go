package source

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/preflight"
	"github.com/itsHabib/workbench/contracts"
)

// A parked escalation names the grant it parked under and the verdict it stands
// on by ID ONLY — the ceilings that decide whether approving it can land live
// in those other artifacts. The ledger is the read-only join that resolves
// those ids, plus the review-cycle count, out of the same gate log flare is
// already tailing.
//
// Mechanism, deliberately: it decodes and indexes, it decides nothing. Whether
// a grant can authorize a verdict is preflight's policy; whether to paint a
// button is notify's rendering. Nothing here writes, locks, or shells — it is
// the same tolerant read of a producer's append-only log the rest of this
// package does, over bytes Read already holds in memory, so it costs no I/O.
//
// It is TOLERANT where Read is strict, and the asymmetry is the point. Read
// fails a corrupt line loudly, because a corrupt log must not read as quiet.
// The ledger skips what it cannot decode and reports a miss, because its only
// consumer withholds a button on a proof and renders normally otherwise: a
// ledger that guessed could delete the operator's one remote path over a line
// it merely failed to parse.

// ledger indexes one gate log by artifact id.
type ledger struct {
	grants   map[string]preflight.Grant
	verdicts map[string]verdictRef
	outcomes []outcomeRef
	// grantOrder is the log order of indexed grants, so "the repo's most
	// recent grant" is a fact of the log rather than of map iteration.
	grantOrder []string
	// lifecycle indexes (see the section at the bottom of this file)
	escSubjects map[string]verdictRef
	runSHA      map[string]string
	escWho      map[string]string
}

// verdictRef is the slice of a verdict the joins need: the subject a cycle
// count matches on, and the tier a grant ceiling is compared against.
type verdictRef struct {
	repo   string
	number int
	tier   string
	head   string
}

// outcomeRef is one artifact that may have consumed a review cycle: the run it
// belongs to, the parent verdict naming its subject, and whether it counts.
type outcomeRef struct {
	id     string
	kind   string
	run    string
	parent string
	counts bool
}

// grantBody / outcomeBody are the small local reads of bodies contracts does
// not type. Both are gate's own persisted shapes; a field flare does not read
// is ignored, so an additive gate field can never break this decode.
type grantBody struct {
	Repo      string    `json:"repo"`
	Action    string    `json:"action"`
	MaxTier   string    `json:"max_tier"`
	MaxCycles int       `json:"max_cycles"`
	ExpiresAt time.Time `json:"expires_at"`
}

type outcomeBody struct {
	Outcome string `json:"outcome"`
	Code    string `json:"code"`
}

// lazyLedger defers the index until something actually needs it. Most polls
// carry no parked escalation at all, and a full re-index of a growing log every
// minute for nothing is waste; the first lookup pays for it, later lookups in
// the same read reuse it.
type lazyLedger struct {
	raw   []byte
	built *ledger
}

func newLazyLedger(raw []byte) *lazyLedger { return &lazyLedger{raw: raw} }

func (l *lazyLedger) get() *ledger {
	if l.built == nil {
		l.built = buildLedger(l.raw)
	}
	return l.built
}

// buildLedger indexes every artifact in raw. Undecodable lines are skipped:
// see the tolerance note above.
func buildLedger(raw []byte) *ledger {
	lg, _ := indexLines(raw, false)
	return lg
}

// strictLedger indexes raw the way Read reads it: a complete line that does
// not decode fails the whole build. It serves the digest, whose consumer is
// the operator's authority picture — a skipped grant or terminal there reads
// as "nothing parked" or "no live grant", the quiet wrong answer a corrupt log
// must never produce. The torn final line is left alone, as Read leaves it.
func strictLedger(raw []byte) (*ledger, error) {
	return indexLines(raw, true)
}

func indexLines(raw []byte, strict bool) (*ledger, error) {
	lg := &ledger{
		grants:      map[string]preflight.Grant{},
		verdicts:    map[string]verdictRef{},
		escSubjects: map[string]verdictRef{},
		runSHA:      map[string]string{},
		escWho:      map[string]string{},
	}
	lines, _ := completeLines(raw)
	for i, l := range lines {
		var env contracts.Envelope
		err := json.Unmarshal([]byte(l), &env)
		if err != nil && strict {
			return nil, fmt.Errorf("gate log line %d: corrupt artifact: %w", i+1, err)
		}
		if err != nil {
			continue
		}
		lg.index(env)
	}
	return lg, nil
}

func (l *ledger) index(env contracts.Envelope) {
	switch env.Kind {
	case contracts.KindGrant:
		l.indexGrant(env)
	case contracts.KindVerdict:
		l.indexVerdict(env)
	case contracts.KindAction, contracts.KindEscalation:
		l.indexOutcome(env)
		l.indexLifecycle(env)
	case contracts.KindResolution:
		l.indexResolution(env)
	}
}

// indexResolution records the VERIFIED human identity a resolution carries,
// keyed on the escalation it closed.
//
// It exists because gate writes a park's terminal artifacts as one transaction
// — judgment, verdict, action, resolution — and the judgment lands first. Its
// body names only the producer ("operator"), while the resolution beside it
// names the actual human Slack authenticated. Reading the judgment alone would
// close the card with the weaker name and, since a closed card is never closed
// twice, the real one would never appear. Indexing the whole file lets either
// artifact render the same, better answer.
func (l *ledger) indexResolution(env contracts.Envelope) {
	if len(env.Parents) == 0 {
		return
	}
	var b struct {
		Who string `json:"who"`
	}
	if err := json.Unmarshal(env.Body, &b); err != nil || b.Who == "" {
		return
	}
	l.escWho[env.Parents[0]] = b.Who
}

// resolvedWho returns the verified identity recorded for an escalation, or ""
// when nothing recorded one.
func (l *ledger) resolvedWho(escID string) string { return l.escWho[escID] }

// indexLifecycle records the joins a card update needs: which subject a
// terminal artifact is about, and the head sha gate pinned a run's merge to.
//
// The subject is read off BOTH kinds. A park names its PR directly; so does
// gate's already_merged action, whose parent is view evidence rather than a
// verdict — resolving that one only through its parent would drop it, leave
// the older park as the subject's latest terminal, and keep paging for a PR
// that is already merged.
func (l *ledger) indexLifecycle(env contracts.Envelope) {
	l.indexParkSubject(env)
	if env.Kind == contracts.KindAction {
		l.indexMergeSHA(env)
	}
}

func (l *ledger) indexParkSubject(env contracts.Envelope) {
	var b struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}
	if err := json.Unmarshal(env.Body, &b); err != nil || b.Repo == "" || b.Number == 0 {
		return
	}
	l.escSubjects[env.ID] = verdictRef{repo: b.Repo, number: b.Number}
}

func (l *ledger) indexMergeSHA(env contracts.Envelope) {
	var b struct {
		Argv    []string `json:"argv"`
		Outcome string   `json:"outcome"`
	}
	if err := json.Unmarshal(env.Body, &b); err != nil || b.Outcome != "would_merge" {
		return
	}
	if sha := matchHeadCommit(b.Argv); sha != "" {
		l.runSHA[env.Run] = sha
	}
}

// matchHeadCommit lifts the sha gate pinned the merge to out of the recorded
// argv. The flag is the whole point of gate's merge line — it makes the merge
// refuse if the head moved after verification — so the value beside it is the
// exact revision the authorization covers.
func matchHeadCommit(argv []string) string {
	for i, a := range argv {
		if a == "--match-head-commit" && i+1 < len(argv) {
			return argv[i+1]
		}
		if after, ok := strings.CutPrefix(a, "--match-head-commit="); ok {
			return after
		}
	}
	return ""
}

// indexGrant keeps MERGE grants only. gate mints per action, and a grant for
// some other action neither covers parked merge work nor is evidence of the
// ceilings a merge grant should carry — indexing it would report a repo as
// covered and copy a foreign ceiling into a proposed mint. An absent action is
// the pre-action grant shape and reads as merge.
func (l *ledger) indexGrant(env contracts.Envelope) {
	var b grantBody
	if err := json.Unmarshal(env.Body, &b); err != nil {
		return
	}
	if b.Action != "" && b.Action != "merge" {
		return
	}
	l.grantOrder = append(l.grantOrder, env.ID)
	l.grants[env.ID] = preflight.Grant{
		ID:        env.ID,
		Repo:      b.Repo,
		MaxTier:   b.MaxTier,
		MaxCycles: b.MaxCycles,
		ExpiresAt: b.ExpiresAt,
	}
}

func (l *ledger) indexVerdict(env contracts.Envelope) {
	v, ok, err := env.Verdict()
	if !ok || err != nil {
		return
	}
	l.verdicts[env.ID] = verdictRef{repo: v.Subject.Repo, number: v.Subject.Number, tier: v.Tier, head: v.Subject.HeadSHA}
}

// indexOutcome records an action or escalation as a candidate consumed cycle,
// mirroring gate's own rule: an escalation counts only when it carries NO park
// code (a coded park is authorization exhaustion, not review work), and an
// action counts unless it was a capability refusal. Counting the excluded ones
// would make a re-mint self-defeating — every failed retry would burn the cycle
// the wider grant was minted to free.
func (l *ledger) indexOutcome(env contracts.Envelope) {
	if len(env.Parents) == 0 {
		return
	}
	var b outcomeBody
	if err := json.Unmarshal(env.Body, &b); err != nil {
		return
	}
	l.outcomes = append(l.outcomes, outcomeRef{
		id:     env.ID,
		kind:   env.Kind,
		run:    env.Run,
		parent: env.Parents[0],
		counts: countsAsCycle(env.Kind, b),
	})
}

func countsAsCycle(kind string, b outcomeBody) bool {
	if kind == contracts.KindEscalation {
		return b.Code == ""
	}
	return b.Outcome != "capability_refused"
}

// park joins one escalation body to the ceilings that decide whether approving
// it could land. A miss anywhere yields a Park whose Found / CyclesKnown say so,
// and preflight turns that into Unknown rather than a refusal.
func (l *ledger) park(grantID, verdictID, repo string, number int, curRun string) preflight.Park {
	g, found := l.grants[grantID]
	p := preflight.Park{Grant: g, Found: found}
	if v, ok := l.verdicts[verdictID]; ok {
		p.VerdictTier = v.tier
	}
	p.Cycles, p.CyclesKnown = l.cycles(repo, number, curRun)
	return p
}

// cycles counts the distinct prior runs that consumed a review cycle for this
// repo+PR, the way gate counts them: each counting outcome joins to its parent
// verdict for the subject, and the run being judged is excluded so a park does
// not count itself.
//
// It reports known=false when an outcome's parent verdict is missing from the
// index. gate treats that as an error and parks; flare treats it as "cannot
// say", because an undercount here would withhold a working button.
//
// KNOWN LIMITATION, accepted deliberately: one dangling parent anywhere in the
// log disables the cycle pre-flight for EVERY subject, not just the one it
// belongs to — the scan cannot tell whose outcome it was without the parent it
// is missing. The direction is safe (uncertainty renders the ordinary card, and
// gate re-applies the ceiling at judgment either way), and the measured join
// rate on the live ledger is 100% of 332 parks, so it is a fail-open on a case
// that does not currently occur. Narrowing it means indexing each outcome's
// subject at write time; recorded in docs/FOLLOWUPS.md rather than guessed at
// here.
func (l *ledger) cycles(repo string, number int, curRun string) (int, bool) {
	if repo == "" || number == 0 {
		return 0, false
	}
	runs := map[string]struct{}{}
	for _, o := range l.outcomes {
		if !o.counts || o.run == curRun {
			continue
		}
		v, ok := l.verdicts[o.parent]
		if !ok {
			return 0, false
		}
		if v.repo == repo && v.number == number {
			runs[o.run] = struct{}{}
		}
	}
	return len(runs), true
}

// --- lifecycle: what closed a park -------------------------------------------
//
// A Slack card is a snapshot, and a snapshot of a park is stale the moment the
// park resolves. gate's inbox reduces parked runs BY SUBJECT — only the latest
// terminal artifact per repo#PR is still "parked" — so a re-park, a merge, or a
// keyboard `gate judge` silently drops an older park out of the inbox while its
// Approve button stays live in Slack. Tapping one then fails with "escalation
// is not currently parked: esc_… not in gate inbox".
//
// These indexes are the read that lets flare close that card instead. Same
// posture as the rest of this file: read-only, tolerant, decides nothing.

// subjectOf maps an escalation artifact id to the PR its card was rendered for,
// so a terminal artifact can be applied to every live card for that subject —
// matching gate's own subject reduction rather than guessing.
func (l *ledger) subjectOf(escID string) (string, int) {
	v := l.escSubjects[escID]
	return v.repo, v.number
}

// mergeSHA returns the head sha gate pinned the merge to for a run, taken from
// the recorded `--match-head-commit` argument. gate's action is the
// AUTHORIZATION to merge, not a receipt that one landed (`dry_run` is true —
// the caller runs the emitted command), so callers must present this as the
// authorized head, not a confirmed merge.
func (l *ledger) mergeSHA(run string) string { return l.runSHA[run] }

// headOf returns the head sha a verdict was gathered against, or "".
func (l *ledger) headOf(verdictID string) string { return l.verdicts[verdictID].head }

// Authorities reads one gate log and reduces it to the authority rows a digest
// renders. It is the package's one exported read beyond Read/Tail, and it is
// read-only like the rest: open, index, reduce, close.
func Authorities(src config.Source, now time.Time) ([]Authority, error) {
	raw, err := os.ReadFile(src.Path)
	if err != nil {
		return nil, fmt.Errorf("source %s: read %s: %w", src.Name, src.Path, err)
	}
	lg, err := strictLedger(raw)
	if err != nil {
		return nil, fmt.Errorf("source %s: %s: %w", src.Name, src.Path, err)
	}
	return lg.authority(now), nil
}

// subjectFromVerdict resolves an artifact's parent verdict to the PR it names.
func (l *ledger) subjectFromVerdict(verdictID string) (string, int) {
	v, ok := l.verdicts[verdictID]
	if !ok {
		return "", 0
	}
	return v.repo, v.number
}

// --- authority: who is waiting on the operator's mint --------------------------
//
// A grant is the only thing an agent cannot mint for itself, so "your authority
// is the bottleneck" is the one alert that must never be silent. gate already
// records it — a `grant_needed` artifact per refusal — and flare dropped them.
//
// The digest below answers the question those artifacts pose one at a time:
// across every repo, what is parked, which grants are about to lapse, and where
// is work waiting with no live grant at all.

// Authority is the read a digest renders: per repo, how much is parked behind
// the operator and what grant (if any) is standing.
type Authority struct {
	Repo string
	// Parked is how many PRs are awaiting judgment for this repo.
	Parked int
	// Grant is the longest-lived grant still live for the repo; Live reports
	// whether there is one at all. A repo with parked work and no live grant is
	// the hard stop — nothing can proceed until the operator mints.
	Grant preflight.Grant
	Live  bool
	// ProposedTier / ProposedCycles are the ceilings a re-mint should carry: the
	// live grant's when there is one, else those of the MOST RECENT grant the
	// repo held — one real grant's tuple, never a composite of several.
	// Carried explicitly so the digest proposes exactly what a single refusal
	// card proposes — a repo that has always run at T3 must not be told T2 on
	// one surface and T3 on the other. flare proposes what the operator already
	// judged appropriate; it never widens, and it never mints.
	ProposedTier   string
	ProposedCycles int
}

// authority reduces the log to one row per repo that has parked work or a live
// grant. Parked counting mirrors gate's inbox: a PR is parked when its LATEST
// terminal artifact is an escalation — a later action (or a later park) retires
// the earlier attempt.
func (l *ledger) authority(now time.Time) []Authority {
	rows := map[string]*Authority{}
	for repo, n := range l.parkedByRepo() {
		rows[repo] = &Authority{Repo: repo, Parked: n}
	}
	for repo, g := range l.liveGrants(now) {
		row, ok := rows[repo]
		if !ok {
			row = &Authority{Repo: repo}
			rows[repo] = row
		}
		row.Grant, row.Live = g, true
	}
	out := make([]Authority, 0, len(rows))
	for _, r := range rows {
		r.ProposedTier, r.ProposedCycles = l.lastCeilingsFor(r.Repo)
		if r.Live {
			r.ProposedTier, r.ProposedCycles = r.Grant.MaxTier, r.Grant.MaxCycles
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// parkedByRepo counts the PRs whose latest terminal artifact is an escalation.
func (l *ledger) parkedByRepo() map[string]int {
	latest := map[string]outcomeRef{}
	for _, o := range l.outcomes {
		if sub := l.outcomeSubject(o); sub.repo != "" {
			latest[subjectKey(sub)] = o
		}
	}
	counts := map[string]int{}
	for key, o := range latest {
		if o.kind != contracts.KindEscalation {
			continue
		}
		counts[repoOf(key)]++
	}
	return counts
}

// outcomeSubject resolves an outcome artifact to the PR it is about: from the
// subject its own body names when it names one (every park, and the actions
// gate writes with a subject, such as already_merged), else through its parent
// verdict.
func (l *ledger) outcomeSubject(o outcomeRef) verdictRef {
	if v, ok := l.escSubjects[o.id]; ok && v.repo != "" {
		return v
	}
	return l.verdicts[o.parent]
}

// liveGrants keeps, per repo, the grant that stands longest — the one an agent
// would actually run under.
func (l *ledger) liveGrants(now time.Time) map[string]preflight.Grant {
	live := map[string]preflight.Grant{}
	for _, g := range l.grants {
		if g.Repo == "" || !g.ExpiresAt.After(now) {
			continue
		}
		if cur, ok := live[g.Repo]; ok && !g.ExpiresAt.After(cur.ExpiresAt) {
			continue
		}
		live[g.Repo] = g
	}
	return live
}

func subjectKey(v verdictRef) string { return v.repo + "#" + strconv.Itoa(v.number) }

func repoOf(subjectKey string) string {
	if i := strings.LastIndex(subjectKey, "#"); i >= 0 {
		return subjectKey[:i]
	}
	return subjectKey
}

// lastCeilingsFor recovers the ceilings of the most recent grant a repo was
// minted — one tuple the operator actually signed, in log order. It must never
// compose across grants: the tier of one and the cycles of another is a grant
// nobody minted, and presenting it as a safe re-mint widens authority. It is a
// proposal, never an authorization: only the operator mints.
func (l *ledger) lastCeilingsFor(repo string) (string, int) {
	for i := len(l.grantOrder) - 1; i >= 0; i-- {
		g := l.grants[l.grantOrder[i]]
		if g.Repo == repo {
			return g.MaxTier, g.MaxCycles
		}
	}
	return "", 0
}
