package standup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SchemaRecord is the record's schema id. A record with any other value is refused.
const SchemaRecord = "standup.v1"

// Record is the whole output of one standup. Confirm stays nil until the operator's
// phrase sets it; Applied is written by apply, one entry per verb, and is the only
// ledger the standup keeps.
type Record struct {
	Schema       string     `json:"schema"`
	ID           string     `json:"id"`
	Tenant       string     `json:"tenant"`
	Lead         string     `json:"lead"`
	At           string     `json:"at"`
	Agenda       string     `json:"agenda"`
	AgendaDigest string     `json:"agenda_digest"`
	Roles        []Role     `json:"roles"`
	Cards        []Card     `json:"cards"`
	Decisions    []Decision `json:"decisions"`
	Deferred     []Deferred `json:"deferred"`
	Confirm      *Confirm   `json:"confirm"`
	Next         string     `json:"next,omitempty"`
	Applied      []Applied  `json:"applied"`
}

// Role is a seat pool the standup wants to exist: a kind with a manifest, a checkout
// to pool beside, and how many seats. Instructions and terms ride in every card brief
// for that role until Fleet's per-role definition store has a reader.
type Role struct {
	Role         string         `json:"role"`
	Kind         string         `json:"kind"`
	Checkout     string         `json:"checkout"`
	Seats        int            `json:"seats"`
	Instructions string         `json:"instructions,omitempty"`
	Terms        map[string]any `json:"terms,omitempty"`
}

// Card is one work item: a Fleet dispatch row plus the mail that carries its brief.
// Change is a branch or #<n> in Repo; As is the receipt kind that means done; For is
// the accountable role; Seat is where it runs (its checkout is looked up in roles.map)
// unless Checkout names one directly.
type Card struct {
	ID       string `json:"id"`
	Repo     string `json:"repo"`
	Change   string `json:"change"`
	As       string `json:"as"`
	For      string `json:"for"`
	Seat     string `json:"seat,omitempty"`
	Checkout string `json:"checkout,omitempty"`
	Due      string `json:"due"`
	Brief    string `json:"brief"`
}

// Decision is a `fleet decide` row: every session sees it at its next turn.
type Decision struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// Deferred is a subject the standup chose not to act on, with the reason. It is
// re-raised verbatim in the next agenda until decided or dropped.
type Deferred struct {
	Subject string `json:"subject"`
	Why     string `json:"why"`
}

// Confirm is set by code when the operator's phrase matched, never by the model.
// PlanDigest binds it to the plan that was read back: an edit after confirm is a
// different plan, and apply refuses it until the operator confirms again.
type Confirm struct {
	By         string `json:"by"`
	Phrase     string `json:"phrase"`
	At         string `json:"at"`
	Surface    string `json:"surface"`
	PlanDigest string `json:"plan_digest"`
}

// Applied is one verb's receipt: what ran, where, and what it said.
type Applied struct {
	Step   string   `json:"step"`
	Verb   string   `json:"verb"`
	Dir    string   `json:"dir,omitempty"`
	Args   []string `json:"args"`
	Code   int      `json:"code"`
	Output string   `json:"output"`
	At     string   `json:"at"`
	Skip   string   `json:"skip,omitempty"`
}

var (
	relationshipRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	dueRe          = regexp.MustCompile(`^[1-9][0-9]*[mhd]$`)
	repoRe         = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	roleRe         = regexp.MustCompile(`^[a-z][a-z0-9-]*:[A-Za-z0-9_.-]+$`)
	idRe           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Validate is every structural rule the record must meet before apply reads it. It
// says every failure at once so the model fixes the draft in one pass.
func (r *Record) Validate() error {
	var errs []string
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if r.Schema != SchemaRecord {
		bad("schema must be %q, got %q", SchemaRecord, r.Schema)
	}
	if !idRe.MatchString(r.ID) {
		bad("id %q: letters, digits, . _ - only", r.ID)
	}
	if r.Lead == "" {
		bad("lead is required")
	}
	if r.Agenda == "" || r.AgendaDigest == "" {
		bad("agenda and agenda_digest are required: a record is made against one agenda")
	}
	r.validateCards(bad)
	r.validateRoles(bad)
	r.validateDecisions(bad)
	if c := r.Confirm; c != nil && (c.By == "" || c.Phrase == "" || c.At == "" || c.PlanDigest == "") {
		bad("confirm, when set, needs by, phrase, at and plan_digest")
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "\n"))
}

func (r *Record) validateCards(bad func(string, ...any)) {
	seen := map[string]bool{}
	for i, c := range r.Cards {
		at := fmt.Sprintf("cards[%d]", i)
		switch {
		case !idRe.MatchString(c.ID):
			bad("%s id %q: letters, digits, . _ - only", at, c.ID)
		case seen[c.ID]:
			bad("%s id %q repeats", at, c.ID)
		}
		seen[c.ID] = true
		if !repoRe.MatchString(c.Repo) {
			bad("%s repo %q: owner/name", at, c.Repo)
		}
		if strings.TrimSpace(c.Change) == "" {
			bad("%s change is required: a branch or #<n>", at)
		}
		if !relationshipRe.MatchString(c.As) {
			bad("%s as %q: a short lowercase word, the receipt kind that means done", at, c.As)
		}
		if !roleRe.MatchString(c.For) {
			bad("%s for %q: an accountable role like author:ivy", at, c.For)
		}
		switch {
		case c.Seat == "" && c.Checkout == "":
			bad("%s needs a seat (from roles.map) or a checkout to dispatch from", at)
		case c.Seat != "" && c.Checkout != "":
			bad("%s names both a seat and a checkout; Fleet places the row in the seat, so name one", at)
		}
		if !dueRe.MatchString(c.Due) {
			bad("%s due %q: <n>m, <n>h or <n>d", at, c.Due)
		}
		if strings.TrimSpace(c.Brief) == "" || strings.ContainsAny(c.Brief, "\n\r") || len(c.Brief) > 400 {
			bad("%s brief: one line, 1–400 characters", at)
		}
	}
}

func (r *Record) validateRoles(bad func(string, ...any)) {
	for i, ro := range r.Roles {
		at := fmt.Sprintf("roles[%d]", i)
		switch {
		case !roleRe.MatchString(ro.Role):
			bad("%s role %q: kind:name", at, ro.Role)
		case !strings.HasPrefix(ro.Role, ro.Kind+":"):
			bad("%s kind %q must prefix role %q", at, ro.Kind, ro.Role)
		}
		if !filepath.IsAbs(ro.Checkout) {
			bad("%s checkout %q must be an absolute path", at, ro.Checkout)
		}
		if ro.Seats < 0 {
			bad("%s seats must be ≥ 0", at)
		}
	}
}

func (r *Record) validateDecisions(bad func(string, ...any)) {
	for i, d := range r.Decisions {
		switch d.Kind {
		case "drop", "park", "ignore", "rule":
		default:
			bad("decisions[%d] kind %q: drop, park, ignore or rule", i, d.Kind)
		}
		if strings.TrimSpace(d.Subject) == "" || strings.TrimSpace(d.Text) == "" {
			bad("decisions[%d] needs subject and text", i)
		}
	}
	for i, d := range r.Deferred {
		if strings.TrimSpace(d.Subject) == "" || strings.TrimSpace(d.Why) == "" {
			bad("deferred[%d] needs subject and why", i)
		}
	}
}

// RecordPath is where a record with this id lives.
func (e Env) RecordPath(id string) string {
	return filepath.Join(e.Dir, "records", id+".json")
}

// ResolveRecord accepts an id or a path and returns the path.
func (e Env) ResolveRecord(ref string) string {
	if strings.HasSuffix(ref, ".json") || strings.ContainsAny(ref, "/\\") {
		return ref
	}
	return e.RecordPath(ref)
}

// LoadRecord reads and validates.
func LoadRecord(path string) (*Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%s:\n%s", path, err)
	}
	return &r, nil
}

// Save writes the record atomically. Slices are never null in the file so a model
// editing it sees where to add entries.
func (r *Record) Save(path string) error {
	if r.Roles == nil {
		r.Roles = []Role{}
	}
	if r.Cards == nil {
		r.Cards = []Card{}
	}
	if r.Decisions == nil {
		r.Decisions = []Decision{}
	}
	if r.Deferred == nil {
		r.Deferred = []Deferred{}
	}
	if r.Applied == nil {
		r.Applied = []Applied{}
	}
	return writeJSON(path, r)
}

// PlanDigest hashes what the operator hears in the readback: the agenda the plan
// was made against, roles, cards, decisions, deferrals and next. Confirm stores
// it; apply recomputes it. Re-pointing a confirmed record at a fresh agenda is an
// edit like any other.
func (r *Record) PlanDigest() string {
	b, _ := json.Marshal(struct {
		Agenda       string     `json:"agenda"`
		AgendaDigest string     `json:"agenda_digest"`
		Roles        []Role     `json:"roles"`
		Cards        []Card     `json:"cards"`
		Decisions    []Decision `json:"decisions"`
		Deferred     []Deferred `json:"deferred"`
		Next         string     `json:"next"`
	}{r.Agenda, r.AgendaDigest, r.Roles, r.Cards, r.Decisions, r.Deferred, r.Next})
	return Digest([]string{string(b)})
}

// CarryOver copies the plan (roles, cards, decisions, deferrals, next) from a
// previous draft into this one, re-keying card ids to this record. Confirm and
// applied are never carried: the new record is unconfirmed against its own agenda.
// This is how a refusal for a moved world costs one readback, not a retyped plan.
func (r *Record) CarryOver(prev *Record) {
	r.Roles = append([]Role{}, prev.Roles...)
	r.Cards = make([]Card, 0, len(prev.Cards))
	for i, c := range prev.Cards {
		c.ID = fmt.Sprintf("%s-c%d", r.ID, i+1)
		r.Cards = append(r.Cards, c)
	}
	r.Decisions = append([]Decision{}, prev.Decisions...)
	r.Deferred = append([]Deferred{}, prev.Deferred...)
	r.Next = prev.Next
}

// LatestRecord returns the newest record, or nil when there is none. Newest is by
// date then numeric sequence, so an unpadded id from before zero-padding still
// orders correctly beside a padded one.
func (e Env) LatestRecord() (*Record, error) {
	ents, err := os.ReadDir(filepath.Join(e.Dir, "records"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, ent := range ents {
		if strings.HasSuffix(ent.Name(), ".json") {
			names = append(names, strings.TrimSuffix(ent.Name(), ".json"))
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Slice(names, func(i, j int) bool { return idLess(names[i], names[j]) })
	b, err := os.ReadFile(filepath.Join(e.Dir, "records", names[len(names)-1]+".json"))
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// idLess orders standup ids by date, then by numeric sequence; anything that is
// not of that shape sorts lexically before everything that is.
func idLess(a, b string) bool {
	da, na, oka := splitID(a)
	db, nb, okb := splitID(b)
	switch {
	case oka != okb:
		return !oka
	case !oka:
		return a < b
	case da != db:
		return da < db
	}
	return na < nb
}

func splitID(id string) (string, int, bool) {
	rest, ok := strings.CutPrefix(id, "standup-")
	if !ok || len(rest) < 12 {
		return "", 0, false
	}
	date, seq := rest[:10], strings.TrimPrefix(rest[10:], "-")
	var n int
	if _, err := fmt.Sscanf(seq, "%d", &n); err != nil {
		return "", 0, false
	}
	return date, n, true
}

// Readback renders the draft as the prose the lead reads back before confirm. It
// is the whole record, in the order the operator hears it: roles, cards, decisions,
// deferrals, then what confirm will do.
func (r *Record) Readback() string {
	var b strings.Builder
	fmt.Fprintf(&b, "standup %s for %s (agenda %s)\n", r.ID, r.Lead, short(r.AgendaDigest))
	if len(r.Roles) == 0 {
		b.WriteString("roles: none new\n")
	}
	for _, ro := range r.Roles {
		fmt.Fprintf(&b, "role %s: kind %s, %d seat(s) beside %s\n", ro.Role, ro.Kind, ro.Seats, ro.Checkout)
		if ro.Instructions != "" {
			fmt.Fprintf(&b, "  instructions: %s\n", ro.Instructions)
		}
		if len(ro.Terms) > 0 {
			t, _ := json.Marshal(ro.Terms)
			fmt.Fprintf(&b, "  terms: %s\n", t)
		}
	}
	if len(r.Cards) == 0 {
		b.WriteString("cards: none\n")
	}
	for _, c := range r.Cards {
		where := c.Seat
		if where == "" {
			where = c.Checkout
		}
		fmt.Fprintf(&b, "card %s: %s %s → %s in %s, done at %s, due %s\n  %s\n", c.ID, c.Repo, c.Change, c.For, where, c.As, c.Due, c.Brief)
	}
	for _, d := range r.Decisions {
		fmt.Fprintf(&b, "decide %s %s: %s\n", d.Kind, d.Subject, d.Text)
	}
	for _, d := range r.Deferred {
		fmt.Fprintf(&b, "defer %s: %s\n", d.Subject, d.Why)
	}
	if r.Next != "" {
		fmt.Fprintf(&b, "next standup: %s\n", r.Next)
	}
	switch {
	case r.Confirm == nil:
		b.WriteString("confirm: not yet. Nothing is written until the phrase is said.\n")
	default:
		fmt.Fprintf(&b, "confirmed by %s at %s (%s)\n", r.Confirm.By, r.Confirm.At, r.Confirm.Surface)
	}
	if n := len(r.Applied); n > 0 {
		fmt.Fprintf(&b, "applied: %d step(s)\n", n)
	}
	return b.String()
}

func short(digest string) string {
	s := strings.TrimPrefix(digest, "sha256:")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
