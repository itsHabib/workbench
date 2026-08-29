// Package survey counts continuity health from role chains.
//
// It answers the question the whole substrate is a bet on: does a session
// actually leave something the next one can inherit? That is measurable
// without asking anyone, because the chain records the difference between a
// session that distilled a conclusion (a checkpoint) and one the host merely
// observed ending (a mark), and between a claim that was closed with a
// terminal record and one a dying session orphaned.
//
// The counting is a REPLAY, not a scan: records are folded one at a time
// through the kernel, so a transition is counted from the state change it
// caused rather than from a kind name that might not have been admissible.
// Orphaned claims are only visible this way — a takeover's own record does not
// say which claim it stranded; the fold does.
//
// Every number here is derived from records the host wrote mechanically. None
// depends on an agent having cooperated, which is the property that makes the
// result evidence rather than self-report.
package survey

import (
	"sort"
	"time"

	"github.com/itsHabib/workbench/contracts/org"
)

// Role is one role's continuity health.
type Role struct {
	Tenant string    `json:"tenant"`
	Role   string    `json:"role"`
	Phase  org.Phase `json:"phase"`
	// Records is the number of successfully decoded records supplied to replay.
	// A decoded record that stops the fold is included; an undecodable JSON line
	// is excluded, Err names it, and Records ends at the last decodable prefix.
	// Incarnations counts the sessions that ever held the role (an attach or a
	// takeover each mint one).
	Records      int `json:"records"`
	Incarnations int `json:"incarnations"`
	// Claims opened, and Terminals recorded against them. A claim still active
	// is legitimately open and is not owed a terminal yet.
	Claims    int `json:"claims"`
	Terminals int `json:"terminals"`
	// Orphaned counts claims a displaced holder left unresolved; Discharged
	// counts how many of those a successor then closed. The gap between them
	// is work that silently stopped — the failure this substrate exists to
	// make unrepresentable.
	Orphaned   int `json:"orphaned"`
	Discharged int `json:"discharged"`
	// Checkpoints are distilled session conclusions; Marks are the host's
	// mechanical observation that a session ended. The ratio is the real
	// question: a chain of marks means re-entry inherits nothing but facts.
	Checkpoints int `json:"checkpoints"`
	Marks       int `json:"marks"`
	// Degraded reports the tip is a mark: the last session ended without
	// anyone distilling what it concluded.
	Degraded bool `json:"degraded,omitempty"`
	// Dangling names an obligation outstanding right now.
	Dangling        string `json:"dangling,omitempty"`
	OpenIntents     int    `json:"open_intents"`
	OpenEscalations int    `json:"open_escalations"`
	// LastAt is the tip's timestamp; Late reports the last writer is past the
	// deadline it declared for itself.
	LastAt string `json:"last_at,omitempty"`
	Late   bool   `json:"late,omitempty"`
	// Held is the last valid folded ownership set. It is carried only so a
	// sweep across role chains can detect cross-role assignment conflicts; the
	// top-level conflict list is the public JSON shape, so rows do not duplicate
	// every work URI.
	Held []string `json:"-"`
	// Err names a chain that does not fold, so a broken chain is reported as a
	// finding rather than failing the whole sweep.
	Err string `json:"error,omitempty"`
}

// terminalKinds are the three ways a claim ends.
var terminalKinds = map[string]bool{
	org.KindYield: true, org.KindComplete: true, org.KindAbandon: true,
}

// Of replays one role's chain and counts what happened along the way.
//
// A chain that stops folding is reported with Err and the counts accumulated
// up to that record: a corrupt tail should not erase the evidence in the head,
// and a sweep that refuses to report anything about a broken chain is exactly
// the sweep nobody runs twice.
func Of(tenant, role string, records []org.Record, now time.Time) Role {
	r := Role{Tenant: tenant, Role: role, Records: len(records)}
	var state org.RoleState
	for _, rec := range records {
		next, err := org.Advance(state, rec)
		if err != nil {
			// Carry the last state that DID fold. An obligation outstanding at
			// the break is the most urgent thing on a broken chain, and a
			// reader that sees only BROKEN would have to guess whether work
			// was stranded behind it.
			r.Err = err.Error()
			return withState(r, state, now)
		}
		count(&r, state, next, rec)
		// Stamped per record rather than from the tip, so a BROKEN row reports
		// when the chain last held valid state instead of nothing at all —
		// state carries no timestamp, so withState cannot recover it later.
		r.LastAt = rec.At
		state = next
	}
	return withState(r, state, now)
}

// withState copies the folded state's reportable fields onto a row. It is one
// function rather than two copies because the broken path and the healthy path
// must report the same fields — a divergence there is exactly how a dangling
// obligation goes missing from a sweep.
func withState(r Role, state org.RoleState, now time.Time) Role {
	r.Phase, r.Dangling, r.Degraded = state.Phase, state.Dangling, state.Degraded
	r.OpenIntents, r.OpenEscalations = len(state.OpenIntents), len(state.OpenEscalations)
	r.Late = late(state.NextDue, now)
	r.Held = heldWorks(state.Held)
	return r
}

func heldWorks(assignments []org.Assignment) []string {
	works := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		works = append(works, assignment.Work)
	}
	return works
}

// AssignConflict is one work URI held by more than one role in a tenant.
//
// This is deliberately a finding, not an admission result: no single-chain
// fold can prove global uniqueness. Roles names the chains whose last valid
// folded state still holds the work.
type AssignConflict struct {
	Tenant string   `json:"tenant"`
	Work   string   `json:"work"`
	Roles  []string `json:"roles"`
}

// AssignConflicts detects cross-role ownership for one tenant by exact work
// URI. URI schemes stay opaque, and requiring the tenant keeps the new work-URI
// projection inside the caller's configured partition.
func AssignConflicts(tenant string, roles []Role) []AssignConflict {
	owners := make(map[string]map[string]struct{})
	for _, role := range roles {
		if role.Tenant != tenant {
			continue
		}
		for _, work := range role.Held {
			addOwner(owners, work, role.Role)
		}
	}

	conflicts := make([]AssignConflict, 0)
	for work, roles := range owners {
		if len(roles) < 2 {
			continue
		}
		conflicts = append(conflicts, AssignConflict{
			Tenant: tenant,
			Work:   work,
			Roles:  sortedOwners(roles),
		})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].Work < conflicts[j].Work
	})
	return conflicts
}

func addOwner(owners map[string]map[string]struct{}, work, role string) {
	roles, ok := owners[work]
	if !ok {
		roles = make(map[string]struct{})
		owners[work] = roles
	}
	roles[role] = struct{}{}
}

func sortedOwners(owners map[string]struct{}) []string {
	roles := make([]string, 0, len(owners))
	for role := range owners {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

// count folds one record's contribution, reading transitions from the state
// change rather than the kind alone.
func count(r *Role, before, after org.RoleState, rec org.Record) {
	switch {
	case rec.Kind == org.KindAttach || rec.Kind == org.KindTakeover:
		r.Incarnations++
	case rec.Kind == org.KindClaim:
		r.Claims++
	case terminalKinds[rec.Kind]:
		r.Terminals++
	case rec.Kind == org.KindCheckpoint:
		r.Checkpoints++
	case rec.Kind == org.KindMark:
		r.Marks++
	}
	// An orphan is not a kind — it is a claim that was active before a
	// displacement and dangling after it. Only the fold knows.
	if before.Dangling == "" && after.Dangling != "" {
		r.Orphaned++
	}
	if before.Dangling != "" && after.Dangling == "" {
		r.Discharged++
	}
}

// late reports whether a declared next-append deadline has passed. An
// unparseable deadline reads as late: a writer that garbles its own liveness
// declaration should not thereby become immortal.
func late(nextDue string, now time.Time) bool {
	if nextDue == "" {
		return false
	}
	due, err := time.Parse(time.RFC3339, nextDue)
	if err != nil {
		return true
	}
	return now.After(due)
}

// Totals aggregates a sweep across roles, plus the two rates the whole bet
// rests on.
type Totals struct {
	Roles        int `json:"roles"`
	Records      int `json:"records"`
	Incarnations int `json:"incarnations"`
	Claims       int `json:"claims"`
	Terminals    int `json:"terminals"`
	Orphaned     int `json:"orphaned"`
	Discharged   int `json:"discharged"`
	Checkpoints  int `json:"checkpoints"`
	Marks        int `json:"marks"`
	Dangling     int `json:"dangling"`
	Late         int `json:"late"`
	Broken       int `json:"broken"`
	// DistillRate is checkpoints / (checkpoints + marks): how often a session
	// end left a conclusion rather than only the fact that it ended. -1 when
	// no session has ended yet, so "no data" never renders as 0%.
	DistillRate float64 `json:"distill_rate"`
	// DischargeRate is discharged / orphaned: how often an inherited
	// obligation was actually closed. -1 when nothing was ever orphaned.
	DischargeRate float64 `json:"discharge_rate"`
}

// Sum aggregates per-role surveys.
func Sum(roles []Role) Totals {
	t := Totals{Roles: len(roles), DistillRate: -1, DischargeRate: -1}
	for _, r := range roles {
		t.Records += r.Records
		t.Incarnations += r.Incarnations
		t.Claims += r.Claims
		t.Terminals += r.Terminals
		t.Orphaned += r.Orphaned
		t.Discharged += r.Discharged
		t.Checkpoints += r.Checkpoints
		t.Marks += r.Marks
		if r.Dangling != "" {
			t.Dangling++
		}
		if r.Late {
			t.Late++
		}
		if r.Err != "" {
			t.Broken++
		}
	}
	if ends := t.Checkpoints + t.Marks; ends > 0 {
		t.DistillRate = float64(t.Checkpoints) / float64(ends)
	}
	if t.Orphaned > 0 {
		t.DischargeRate = float64(t.Discharged) / float64(t.Orphaned)
	}
	return t
}
