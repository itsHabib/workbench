// Package render turns folded role state into the two read surfaces the org
// runtime serves: the boot index a session starts from, and the board an
// operator glances at.
//
// The boot index is an INDEX, not a context dump. Every line is a pointer plus
// just enough of a hook to decide whether to follow it; the depth — full
// charter prose, task bodies, old checkpoints — stays behind the pointers and
// is read lazily by whoever needs it. That is what keeps the standing cost of
// re-entry (bytes injected into every turn of every session) decoupled from
// how much a role knows.
package render

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/workbench/contracts/org"
)

// Boot is the boot index for one role: what a fresh incarnation must see
// before its first action.
type Boot struct {
	Tenant string    `json:"tenant"`
	Role   string    `json:"role"`
	Phase  org.Phase `json:"phase"`
	Seq    int64     `json:"seq"`
	Tip    string    `json:"tip"`
	Holder string    `json:"holder,omitempty"`
	Terms  org.Terms `json:"terms"`
	Active string    `json:"active,omitempty"`
	// Dangling is an inherited, unresolved claim. It renders first: the kernel
	// refuses every new claim until it is discharged, so a session that skims
	// only one line must skim this one.
	Dangling        string           `json:"dangling,omitempty"`
	Held            []org.Assignment `json:"held,omitempty"`
	OpenIntents     []string         `json:"open_intents,omitempty"`
	OpenEscalations []string         `json:"open_escalations,omitempty"`
	NextDue         string           `json:"next_due,omitempty"`
	Late            bool             `json:"late,omitempty"`
	Degraded        bool             `json:"degraded,omitempty"`
	// LastWord is the most recent advisory body on the chain — the previous
	// incarnation's distilled conclusion, or its mechanical mark.
	LastWord *LastWord `json:"last_word,omitempty"`
}

// LastWord locates and excerpts the newest advisory record with a body.
type LastWord struct {
	Kind   string `json:"kind"`
	Seq    int64  `json:"seq"`
	At     string `json:"at"`
	Digest string `json:"body_digest"`
	// Excerpt is the head of the body, cut to the boot byte budget. Erased
	// bodies render as erased rather than silently vanishing.
	Excerpt string `json:"excerpt,omitempty"`
	Erased  bool   `json:"erased,omitempty"`
}

// BlobReader resolves a body digest to its content. It reports found=false for
// an erased blob, which is a legitimate state, not an error.
type BlobReader interface {
	Blob(digest string) (body []byte, found bool, err error)
}

// NewBoot assembles the boot index from a folded state and its chain.
func NewBoot(state org.RoleState, records []org.Record, blobs BlobReader, now time.Time) (Boot, error) {
	b := Boot{
		Tenant: state.Tenant, Role: state.Role,
		Phase: state.Phase, Seq: state.Seq, Tip: state.Tip,
		Holder: state.Holder, Terms: state.Terms,
		Active: state.Active, Dangling: state.Dangling,
		Held:        state.Held,
		OpenIntents: state.OpenIntents, OpenEscalations: state.OpenEscalations,
		NextDue: state.NextDue, Degraded: state.Degraded,
	}
	b.Late = late(state.NextDue, now)
	last, err := lastWord(records, blobs)
	if err != nil {
		return Boot{}, err
	}
	b.LastWord = last
	return b, nil
}

// late reports whether a declared next-append deadline has passed. An
// unparseable deadline is treated as late: a writer that garbles its own
// liveness declaration should read as dead, not as immortal.
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

// lastWord scans the chain backwards for the newest advisory record carrying a
// body, skipping structural records: a claim terminal is a fact, not a word.
func lastWord(records []org.Record, blobs BlobReader) (*LastWord, error) {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.KindClass != org.ClassAdvisory || r.BodyDigest == "" {
			continue
		}
		w := &LastWord{Kind: r.Kind, Seq: r.Seq, At: r.At, Digest: r.BodyDigest}
		body, found, err := blobs.Blob(r.BodyDigest)
		if err != nil {
			return nil, err
		}
		if !found {
			w.Erased = true
			return w, nil
		}
		w.Excerpt = string(body)
		return w, nil
	}
	return nil, nil
}

// JSON renders the boot index as one JSON document.
func (b Boot) JSON() ([]byte, error) { return json.MarshalIndent(b, "", "  ") }

// Text renders the boot index for injection, within a byte budget.
//
// The budget is enforced by shedding depth, not by cutting mid-line: first the
// last-word excerpt shrinks, then the held list collapses to a count. The
// headline, the dangling obligation, and the charter line are never shed —
// they are the lines whose absence changes what a session does.
func (b Boot) Text(budget int) string {
	if budget <= 0 {
		budget = 2048
	}
	for {
		s := b.text()
		if len(s) <= budget {
			return s
		}
		if !b.shed() {
			return s[:budget]
		}
	}
}

// shed drops the cheapest remaining depth, reporting false when nothing more
// can go.
func (b *Boot) shed() bool {
	switch {
	// The cut must land strictly below its own guard, or shedding stops making
	// progress and Text spins: 160 bytes + a 3-byte ellipsis is 163.
	case b.LastWord != nil && len(b.LastWord.Excerpt) > 163:
		b.LastWord.Excerpt = b.LastWord.Excerpt[:160] + "…"
	case b.LastWord != nil && b.LastWord.Excerpt != "":
		b.LastWord.Excerpt = ""
	case len(b.Held) > 0:
		b.Held = nil
	default:
		return false
	}
	return true
}

func (b Boot) text() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# baton boot — %s @ %s\n", b.Role, b.Tenant)
	fmt.Fprintf(&sb, "phase: %s · seq %d · holder %s\n", b.Phase, b.Seq, shortOr(b.Holder, "none"))
	fmt.Fprintf(&sb, "charter: tier %s · scope %s · supervisors %s\n",
		orDash(b.Terms.Tier), joinOr(b.Terms.Scope, "-"), joinOr(b.Terms.Supervisors, "-"))
	if b.Terms.Retire != "" {
		fmt.Fprintf(&sb, "retire-when: %s\n", b.Terms.Retire)
	}
	if b.Dangling != "" {
		fmt.Fprintf(&sb, "OBLIGATION: a predecessor's claim on %s is unresolved — yield, complete or abandon it before claiming anything\n", b.Dangling)
	}
	if b.Active != "" {
		fmt.Fprintf(&sb, "active: %s\n", b.Active)
	}
	if len(b.Held) > 0 {
		works := make([]string, 0, len(b.Held))
		for _, a := range b.Held {
			works = append(works, a.Work)
		}
		fmt.Fprintf(&sb, "held (%d): %s\n", len(works), strings.Join(works, " · "))
	}
	if len(b.OpenIntents) > 0 {
		fmt.Fprintf(&sb, "open effects (%d): %s — resolve before claiming\n", len(b.OpenIntents), strings.Join(b.OpenIntents, " · "))
	}
	if len(b.OpenEscalations) > 0 {
		fmt.Fprintf(&sb, "open escalations (%d): %s\n", len(b.OpenEscalations), strings.Join(shortAll(b.OpenEscalations), " · "))
	}
	if b.NextDue != "" {
		mark := ""
		if b.Late {
			mark = "  (LATE — the last writer is past its own deadline)"
		}
		fmt.Fprintf(&sb, "next-due: %s%s\n", b.NextDue, mark)
	}
	if b.LastWord != nil {
		fmt.Fprintf(&sb, "last word: %s @ seq %d, %s", b.LastWord.Kind, b.LastWord.Seq, b.LastWord.At)
		switch {
		case b.LastWord.Erased:
			sb.WriteString(" (body erased)\n")
		case b.LastWord.Excerpt != "":
			fmt.Fprintf(&sb, "\n> %s\n", strings.ReplaceAll(strings.TrimSpace(b.LastWord.Excerpt), "\n", "\n> "))
		default:
			fmt.Fprintf(&sb, " (body %s — read it with: org blob %s)\n", short(b.LastWord.Digest), short(b.LastWord.Digest))
		}
	}
	if b.Degraded {
		sb.WriteString("degraded: the tip is a mechanical mark, not a distilled checkpoint — this resume is thinner than it looks\n")
	}
	return sb.String()
}

// Row is one role on the board.
type Row struct {
	Tenant string    `json:"tenant"`
	Role   string    `json:"role"`
	Phase  org.Phase `json:"phase"`
	Active string    `json:"active,omitempty"`
	Held   int       `json:"held"`
	Open   int       `json:"open"`
	Late   bool      `json:"late,omitempty"`
	Seq    int64     `json:"seq"`
}

// NewRow summarizes one folded role for the board.
func NewRow(state org.RoleState, now time.Time) Row {
	open := len(state.OpenIntents) + len(state.OpenEscalations)
	if state.Dangling != "" {
		open++
	}
	return Row{
		Tenant: state.Tenant, Role: state.Role, Phase: state.Phase,
		Active: state.Active, Held: len(state.Held), Open: open,
		Late: late(state.NextDue, now), Seq: state.Seq,
	}
}

// Board renders rows as an aligned text table.
func Board(rows []Row) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-8s  %-32s  %-9s  %-36s  %4s  %4s  %s\n",
		"TENANT", "ROLE", "PHASE", "ACTIVE", "HELD", "OPEN", "LIVENESS")
	for _, r := range rows {
		liveness := "ok"
		if r.Late {
			liveness = "LATE"
		}
		fmt.Fprintf(&sb, "%-8s  %-32s  %-9s  %-36s  %4d  %4d  %s\n",
			r.Tenant, r.Role, r.Phase, orDash(r.Active), r.Held, r.Open, liveness)
	}
	return sb.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func joinOr(ss []string, empty string) string {
	if len(ss) == 0 {
		return empty
	}
	return strings.Join(ss, ", ")
}

func shortOr(s, empty string) string {
	if s == "" {
		return empty
	}
	return short(s)
}

func shortAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, short(s))
	}
	return out
}

func short(digest string) string {
	if len(digest) <= 14 {
		return digest
	}
	return digest[:14] + "…"
}
