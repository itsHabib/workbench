package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	Parked []ParkedRun `json:"parked"`
	Grants []GrantLine `json:"grants"`
}

// ParkedRun is one gate run stopped on an escalation, waiting for the operator's
// judgment. JudgeCommand and ExplainCommand are paste-ready: the grant id is the
// one the run parked under, read from the escalation itself, so resolving a park
// never means hunting an id out of the log.
type ParkedRun struct {
	Run            string `json:"run"`
	Repo           string `json:"repo,omitempty"`
	Number         int    `json:"number,omitempty"`
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
	return Inbox{
		Parked: parkedRuns(arts, stateArg),
		Grants: grantLines(arts, now),
	}
}

// parkedRuns finds every run whose latest terminal artifact is an escalation —
// the runs still awaiting judgment. A run parks by appending an escalation and
// resolves by appending an action (or a later escalation, if a judgment still
// left it over-ceiling), so the last terminal in log order is the run's current
// state. Output is oldest-park-first: age is a fact, not a priority call.
func parkedRuns(arts []state.Artifact, stateArg string) []ParkedRun {
	last := make(map[string]state.Artifact)
	for _, a := range arts {
		if a.Kind == state.KindAction || a.Kind == state.KindEscalation {
			last[a.Run] = a
		}
	}
	var parked []state.Artifact
	for _, a := range last {
		if a.Kind == state.KindEscalation {
			parked = append(parked, a)
		}
	}
	sort.Slice(parked, func(i, j int) bool { return parked[i].Time.Before(parked[j].Time) })
	out := make([]ParkedRun, 0, len(parked))
	for _, a := range parked {
		out = append(out, parkedFromEscalation(a, stateArg))
	}
	return out
}

func parkedFromEscalation(a state.Artifact, stateArg string) ParkedRun {
	// Best-effort decode: an escalation with an unreadable body still lists its
	// run, so a park is never silently dropped just because its body drifted.
	var b escalationBody
	_ = json.Unmarshal(a.Body, &b)
	return ParkedRun{
		Run:            a.Run,
		Repo:           b.Repo,
		Number:         b.Number,
		Question:       b.Question,
		Code:           b.Code,
		Grant:          b.Grant,
		ParkedAt:       a.Time.UTC().Format(time.RFC3339),
		JudgeCommand:   judgeCommand(a.Run, b.Grant, stateArg),
		ExplainCommand: fmt.Sprintf("gate explain%s -run %s -html", stateArg, a.Run),
	}
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
	if len(in.Grants) == 0 {
		return
	}
	fmt.Fprintln(w, "grants")
	renderGrants(w, in.Grants)
}

func renderParked(w io.Writer, p ParkedRun) {
	head := p.Run
	if p.Repo != "" {
		head = fmt.Sprintf("%s  %s#%d", p.Run, p.Repo, p.Number)
	}
	if p.Code != "" {
		head += "  " + p.Code
	}
	fmt.Fprintf(w, "  %s\n", head)
	if p.Question != "" {
		fmt.Fprintf(w, "  %q\n", p.Question)
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
