// Package preflight answers one question about a parked escalation: could a
// human's Approve possibly land, or is it already guaranteed to fail?
//
// It exists because flare paints an Approve button on a Slack card and a tap
// on that button is ONE-SHOT — `gate judge` records a judgment that cannot be
// re-run. On 2026-08-21 a tap on workbench#242 recorded a judgment and then
// died on `grant_tier_exceeded: verdict tier T3 exceeds grant ceiling T1`. The
// tap burned the operator's approval on a decision that was arithmetically
// doomed before the card was ever rendered: the park artifact named both its
// verdict (tier T3) and its grant (ceiling T1), and both artifacts were already
// in the log flare was tailing.
//
// This is NOT flare gating (Amendment 3). flare decides nothing and writes
// nothing: it reads facts gate already recorded and declines to paint a button
// for an outcome gate has already determined. The rules below are a faithful
// mirror of the ceilings gate enforces in `act` — expiry, then the tier
// ceiling, then the review-cycle ceiling — deliberately re-stated here rather
// than imported, because importing another tool's decision code is the one
// forbidden import (workbench boundary law).
//
// The failure direction is asymmetric and deliberate. Withholding a button the
// operator needed is worse than painting one that fails, because the phone is
// the only surface they have when away from a keyboard. So every uncertainty —
// a grant flare could not find, a tier it does not recognize, a cycle count it
// could not join — resolves to Unknown, and an Unknown renders exactly the card
// flare renders today. A button is withheld only on a proof.
//
// Policy only: it reads no files, shells nothing, and holds no state. The
// joins that feed it are the caller's (mechanism).
package preflight

import (
	"fmt"
	"time"
)

// Grant is the slice of a gate grant a ceiling check reads. Zero values mean
// "not recorded": a zero ExpiresAt never expires the park, and a zero
// MaxCycles is gate's own back-compat reading of an unbounded cycle ceiling.
type Grant struct {
	ID        string
	Repo      string
	MaxTier   string
	MaxCycles int
	ExpiresAt time.Time
}

// Park is everything the check knows about one parked escalation, already
// joined from the log by the caller. Found and CyclesKnown distinguish "the
// fact says no" from "flare could not read the fact" — the difference between
// a proof and a guess, and the whole reason this check can fail open.
type Park struct {
	// Grant is the grant the run parked under; Found reports whether the
	// grant artifact was actually located in the log.
	Grant Grant
	Found bool
	// VerdictTier is the tier of the reduced verdict the park stands on. Empty
	// when the verdict artifact was not found.
	VerdictTier string
	// Cycles is how many review cycles this PR has already consumed, counted
	// the way gate counts them; CyclesKnown reports whether that join
	// succeeded. A count flare could not derive is never treated as zero.
	Cycles      int
	CyclesKnown bool
}

// Result is what the card should do. Known false means the facts did not
// support a verdict either way, and the caller must render as if this package
// did not exist. Approve is meaningful only when Known.
type Result struct {
	Known   bool
	Approve bool
	// Blocker is the plain sentence naming why an approval cannot land, in the
	// same vocabulary gate uses when it refuses ("verdict tier T3 exceeds grant
	// ceiling T1"). Empty when Approve.
	Blocker string
	// Needs names the authority that would clear the blocker, in words an
	// operator reading a phone can act on. Empty when Approve.
	Needs string
	// MintTier and MintCycles carry the ceilings a clearing grant must have, so
	// a caller can render a paste-ready mint command without re-deriving them.
	MintTier   string
	MintCycles int
}

// tiers is the risk-tier ordering gate composes and bounds with. It is
// re-stated rather than imported (boundary law); the values are the wire
// contract, pinned by a test against the tiers gate's grants actually carry.
var tiers = map[string]int{"T0": 0, "T1": 1, "T2": 2, "T3": 3}

// Check reports whether approving this park could land. It mirrors gate's own
// order — a grant is checked for expiry when it is loaded, then the tier
// ceiling, then the review-cycle ceiling — so the first blocker flare reports
// is the first blocker gate would hit.
//
// Every unreadable fact short-circuits to Unknown, so this function can only
// ever remove a button flare has PROVEN cannot work.
func Check(p Park, now time.Time) Result {
	if !p.Found || p.VerdictTier == "" {
		return Result{}
	}
	if res, blocked := expired(p.Grant, now); blocked {
		return res
	}
	if res, blocked := overTier(p.Grant, p.VerdictTier); blocked {
		return res
	}
	if res, blocked := overCycles(p); blocked {
		return res
	}
	return Result{Known: true, Approve: true}
}

// expired reports a grant whose window has already closed. gate refuses such a
// grant at load with grant_expired, so no judgment recorded against it can
// reach a merge — the tap is spent for nothing.
func expired(g Grant, now time.Time) (Result, bool) {
	if g.ExpiresAt.IsZero() || !now.After(g.ExpiresAt) {
		return Result{}, false
	}
	return Result{
		Known:      true,
		Blocker:    fmt.Sprintf("grant %s expired %s", g.ID, g.ExpiresAt.UTC().Format(time.RFC3339)),
		Needs:      fmt.Sprintf("a fresh grant for %s", g.Repo),
		MintTier:   g.MaxTier,
		MintCycles: g.MaxCycles,
	}, true
}

// overTier reports a verdict the grant's ceiling cannot authorize. This is the
// exact failure that burned the workbench#242 tap, and it is the one ceiling a
// judgment provably cannot launder: gate composes tiers monotone-max, so a
// judgment (tier T0) leaves the reduced tier where it was and the same ceiling
// check refuses again on the judged run.
//
// An unrecognized tier on either side is Unknown, not blocked: gate fails
// closed there, but flare failing closed would silently delete the operator's
// only remote path over a vocabulary drift.
func overTier(g Grant, verdictTier string) (Result, bool) {
	want, ok := tiers[verdictTier]
	ceiling, ceilingOK := tiers[g.MaxTier]
	if !ok || !ceilingOK || want <= ceiling {
		return Result{}, false
	}
	return Result{
		Known:      true,
		Blocker:    fmt.Sprintf("verdict tier %s exceeds grant ceiling %s", verdictTier, g.MaxTier),
		Needs:      fmt.Sprintf("a %s grant for %s — a judgment cannot lower the tier", verdictTier, g.Repo),
		MintTier:   verdictTier,
		MintCycles: g.MaxCycles,
	}, true
}

// overCycles reports a PR that has already spent its grant's review-cycle
// budget. gate checks cycle n+1 against the ceiling — the cycle this run would
// consume — so the boundary is the same one gate refuses at. A zero ceiling is
// unbounded and never blocks; an underivable count is Unknown, because gate
// parks such a run rather than failing it and the park is still worth showing.
func overCycles(p Park) (Result, bool) {
	g := p.Grant
	if g.MaxCycles == 0 || !p.CyclesKnown || p.Cycles+1 <= g.MaxCycles {
		return Result{}, false
	}
	return Result{
		Known:      true,
		Blocker:    fmt.Sprintf("cycle %d exceeds grant ceiling %d (%d of %d consumed)", p.Cycles+1, g.MaxCycles, p.Cycles, g.MaxCycles),
		Needs:      fmt.Sprintf("a wider -max-cycles grant for %s", g.Repo),
		MintTier:   g.MaxTier,
		MintCycles: p.Cycles + 1,
	}, true
}

// MintCommand renders the paste-ready `gate grant` an operator runs to clear a
// blocker. The operator mints from a keyboard — never a phone — so the one
// thing the card owes them is a single copy-paste. Minting stays entirely
// theirs: flare prints the string, it never runs it and never could.
//
// stateDir is the caller's; flare has no opinion about where gate keeps state
// (watched paths are config, never derived in code).
func MintCommand(repo, stateDir string, r Result) string {
	if r.Known && r.Approve {
		return ""
	}
	tier := r.MintTier
	if _, ok := tiers[tier]; !ok {
		tier = "T2"
	}
	cmd := fmt.Sprintf("gate grant -repo %s -max-tier %s", repo, tier)
	if r.MintCycles > 0 {
		cmd += fmt.Sprintf(" -max-cycles %d", r.MintCycles)
	}
	cmd += " -ttl 24h"
	if stateDir != "" {
		cmd += fmt.Sprintf(" -state %s", stateDir)
	}
	return cmd
}
