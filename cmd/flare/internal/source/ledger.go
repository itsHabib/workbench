package source

import (
	"encoding/json"
	"time"

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
}

// verdictRef is the slice of a verdict the joins need: the subject a cycle
// count matches on, and the tier a grant ceiling is compared against.
type verdictRef struct {
	repo   string
	number int
	tier   string
}

// outcomeRef is one artifact that may have consumed a review cycle: the run it
// belongs to, the parent verdict naming its subject, and whether it counts.
type outcomeRef struct {
	run    string
	parent string
	counts bool
}

// grantBody / outcomeBody are the small local reads of bodies contracts does
// not type. Both are gate's own persisted shapes; a field flare does not read
// is ignored, so an additive gate field can never break this decode.
type grantBody struct {
	Repo      string    `json:"repo"`
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
	lg := &ledger{
		grants:   map[string]preflight.Grant{},
		verdicts: map[string]verdictRef{},
	}
	lines, _ := completeLines(raw)
	for _, l := range lines {
		var env contracts.Envelope
		if err := json.Unmarshal([]byte(l), &env); err != nil {
			continue
		}
		lg.index(env)
	}
	return lg
}

func (l *ledger) index(env contracts.Envelope) {
	switch env.Kind {
	case contracts.KindGrant:
		l.indexGrant(env)
	case contracts.KindVerdict:
		l.indexVerdict(env)
	case contracts.KindAction, contracts.KindEscalation:
		l.indexOutcome(env)
	}
}

func (l *ledger) indexGrant(env contracts.Envelope) {
	var b grantBody
	if err := json.Unmarshal(env.Body, &b); err != nil {
		return
	}
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
	l.verdicts[env.ID] = verdictRef{repo: v.Subject.Repo, number: v.Subject.Number, tier: v.Tier}
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
