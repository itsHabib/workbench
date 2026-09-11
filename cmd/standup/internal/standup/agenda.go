package standup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaAgenda is the agenda file's schema id.
const SchemaAgenda = "standup-agenda.v1"

// Source is one read and what it said. A source that could not be read is ok:false
// with the tool's own words; absence of evidence is unknown, never failure.
type Source struct {
	Name  string           `json:"name"`
	OK    bool             `json:"ok"`
	Error string           `json:"error,omitempty"`
	Rows  []map[string]any `json:"rows"`
}

// Agenda is what the standup is made against. Projection is the stable view of the
// sources (identity and outcome, never timestamps or liveness flaps) and Digest is
// its hash; apply refuses a record whose digest no longer matches the live world.
type Agenda struct {
	Schema      string     `json:"schema"`
	ID          string     `json:"id"`
	Lead        string     `json:"lead"`
	GeneratedAt string     `json:"generated_at"`
	Sources     []Source   `json:"sources"`
	PrevRecord  string     `json:"prev_record,omitempty"`
	Deferred    []Deferred `json:"deferred"`
	Projection  []string   `json:"projection"`
	Digest      string     `json:"digest"`
}

// AgendaPath is where an agenda with this id lives.
func (e Env) AgendaPath(id string) string {
	return filepath.Join(e.Dir, "agenda", id+".json")
}

// NextID mints the next id for today: standup-<date>-<nnn>, n counting agendas
// already written for that date, zero-padded so lexical order is chronological.
func (e Env) NextID() (string, error) {
	date := e.Now().UTC().Format("2006-01-02")
	prefix := "standup-" + date + "-"
	n := 1
	ents, err := os.ReadDir(filepath.Join(e.Dir, "agenda"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("agenda directory: %w", err)
	}
	for _, ent := range ents {
		name := strings.TrimSuffix(ent.Name(), ".json")
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		var k int
		if _, err := fmt.Sscanf(strings.TrimPrefix(name, prefix), "%d", &k); err == nil && k >= n {
			n = k + 1
		}
	}
	return fmt.Sprintf("%s%03d", prefix, n), nil
}

// Build reads every source once and returns the agenda, digested. It never fails
// on a source: the tool's refusal is recorded where its rows would be.
func (e Env) Build(cfg Config, id string) (*Agenda, error) {
	a := &Agenda{Schema: SchemaAgenda, ID: id, Lead: cfg.Lead, GeneratedAt: e.Now().UTC().Format("2006-01-02T15:04:05Z")}
	a.Sources = append(a.Sources,
		e.jsonSource("fleet work", e.LeadDir, e.Fleet, "work", "--json"),
		e.jsonSource("fleet receipts", e.LeadDir, e.Fleet, "receipts", "--since", "24h", "--json"),
		e.jsonSource("fleet mail", e.LeadDir, e.Fleet, "mail", "--for", cfg.Lead, "--unacked", "--json"),
		e.orgSource(),
	)
	for _, repo := range cfg.Repos {
		a.Sources = append(a.Sources, e.jsonSource("gh pr list "+repo, e.LeadDir, e.GH, "pr", "list", "-R", repo, "--state", "open",
			"--json", "number,title,headRefName,isDraft,mergeable,reviewDecision,updatedAt,url", "--limit", "50"))
	}
	prev, err := e.LatestRecord()
	if err != nil {
		return nil, err
	}
	a.Deferred = []Deferred{}
	if prev != nil {
		a.PrevRecord = prev.ID
		a.Deferred = append(a.Deferred, prev.Deferred...)
	}
	a.Projection = Project(a)
	a.Digest = Digest(a.Projection)
	return a, nil
}

// jsonSource runs a tool that speaks JSON and keeps its rows, or its refusal.
func (e Env) jsonSource(name, dir, bin string, args ...string) Source {
	s := Source{Name: name, Rows: []map[string]any{}}
	res := e.Run.Run(dir, bin, args...)
	if res.Err != nil {
		s.Error = res.Err.Error()
		return s
	}
	if res.Code != 0 {
		s.Error = strings.TrimSpace(res.Stderr + res.Stdout)
		if s.Error == "" {
			s.Error = fmt.Sprintf("exit %d", res.Code)
		}
		return s
	}
	rows, err := parseRows(res.Stdout)
	if err != nil {
		s.Error = fmt.Sprintf("unreadable output: %v", err)
		return s
	}
	s.OK, s.Rows = true, rows
	return s
}

// orgSource is `org status -json` with the rows that are not a lane dropped: the
// tool prints placeholders with an empty tenant for lines it could not read.
func (e Env) orgSource() Source {
	s := e.jsonSource("org status", e.LeadDir, e.Org, "status", "-json")
	if !s.OK {
		return s
	}
	kept := s.Rows[:0]
	for _, r := range s.Rows {
		if str(r, "tenant") != "" && str(r, "phase") != "retired" {
			kept = append(kept, r)
		}
	}
	s.Rows = kept
	return s
}

func parseRows(out string) ([]map[string]any, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return []map[string]any{}, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err == nil {
		return rows, nil
	}
	var one map[string]any
	if err := json.Unmarshal([]byte(out), &one); err != nil {
		return nil, err
	}
	return []map[string]any{one}, nil
}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%v", v)
	case bool:
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// Project renders the sources as stable lines: what exists and how it ended, never
// when it was touched or whether hands are live this second. Two agendas built a
// minute apart against an unchanged world project identically. Deferrals are the
// standup's own choice, not the world, so they are carried but not projected; receipts
// are a sliding 24h window of history, so they are shown but not projected either (a
// row's done state already carries what a receipt proved).
func Project(a *Agenda) []string {
	var lines []string
	for _, s := range a.Sources {
		if !s.OK {
			lines = append(lines, "source "+s.Name+" unavailable")
			continue
		}
		for _, r := range s.Rows {
			if line, ok := projectRow(s.Name, r); ok {
				lines = append(lines, line)
			}
		}
	}
	sort.Strings(lines)
	return lines
}

// projectRow is one row's identity and outcome. A Fleet row is (repo, change,
// relationship): the relationship is part of the identity, so a row replaced by
// another relationship for the same change projects differently.
func projectRow(source string, r map[string]any) (string, bool) {
	switch {
	case source == "fleet work":
		return fmt.Sprintf("row %s %s %s for=%s", str(r, "key"), str(r, "relationship"), outcome(str(r, "state")), str(r, "for")), true
	case source == "fleet mail":
		return fmt.Sprintf("mail %s %s from=%s", str(r, "id"), str(r, "kind"), str(r, "from")), true
	case source == "org status":
		return fmt.Sprintf("lane %s %s held=%s open=%s", str(r, "role"), str(r, "phase"), str(r, "held"), str(r, "open")), true
	case strings.HasPrefix(source, "gh pr list "):
		repo := strings.TrimPrefix(source, "gh pr list ")
		return fmt.Sprintf("pr %s#%s %s draft=%s mergeable=%s review=%s", repo, str(r, "number"), str(r, "headRefName"), str(r, "isDraft"), str(r, "mergeable"), str(r, "reviewDecision")), true
	}
	return "", false
}

// outcome folds the observed row states into what a plan cares about: a row that is
// finished, one whose hands died, or one still open. working/idle/late/dispatched
// all read as open; late is the watcher's to report, not a reason to re-plan.
func outcome(state string) string {
	switch state {
	case "done", "dead":
		return state
	}
	return "open"
}

// Digest hashes the projection.
func Digest(lines []string) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Save writes the agenda file.
func (a *Agenda) Save(path string) error { return writeJSON(path, a) }

// LoadAgenda reads one.
func LoadAgenda(path string) (*Agenda, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Agenda
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if a.Schema != SchemaAgenda {
		return nil, fmt.Errorf("%s: schema %q, want %q", path, a.Schema, SchemaAgenda)
	}
	if got := Digest(a.Projection); got != a.Digest {
		return nil, errors.New(path + ": digest does not match its projection; the file was edited")
	}
	return &a, nil
}

// maxTextBytes bounds the rendered agenda so it can ride in a prompt.
const maxTextBytes = 24 * 1024

// Text renders the agenda for a person or a model: one section per source, one line
// per row, the deferrals last. Bounded; a cut says so.
func (a *Agenda) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "agenda %s for %s at %s (digest %s)\n", a.ID, a.Lead, a.GeneratedAt, short(a.Digest))
	for _, s := range a.Sources {
		if !s.OK {
			fmt.Fprintf(&b, "\n## %s: unavailable\n%s\n", s.Name, s.Error)
			continue
		}
		fmt.Fprintf(&b, "\n## %s (%d)\n", s.Name, len(s.Rows))
		for _, r := range s.Rows {
			b.WriteString(rowLine(s.Name, r))
		}
	}
	if len(a.Deferred) > 0 {
		fmt.Fprintf(&b, "\n## deferred from %s (%d)\n", a.PrevRecord, len(a.Deferred))
		for _, d := range a.Deferred {
			fmt.Fprintf(&b, "- %s: %s\n", d.Subject, d.Why)
		}
	}
	out := b.String()
	if len(out) > maxTextBytes {
		out = out[:maxTextBytes] + "\n[agenda cut at 24 KiB; the file has the rest]\n"
	}
	return out
}

func rowLine(source string, r map[string]any) string {
	switch {
	case source == "fleet work":
		return fmt.Sprintf("- %s %s/%s for %s (hands %s, due %s, slot %s)\n  %s\n", str(r, "state"), str(r, "repo"), str(r, "change"), orDash(str(r, "for")), orDash(shortID(str(r, "hands"))), when(r["due"]), orDash(str(r, "slot")), str(r, "brief"))
	case source == "fleet receipts":
		return fmt.Sprintf("- %s %s %s by %s: %s\n", shortID(str(r, "head")), str(r, "kind"), str(r, "verdict"), str(r, "role"), str(r, "observable"))
	case source == "fleet mail":
		return fmt.Sprintf("- %s %s from %s: %s\n", str(r, "id"), str(r, "kind"), str(r, "from"), str(r, "subject"))
	case source == "org status":
		return fmt.Sprintf("- %s %s held=%s open=%s active=%s\n", str(r, "role"), str(r, "phase"), str(r, "held"), str(r, "open"), str(r, "active"))
	case strings.HasPrefix(source, "gh pr list "):
		draft := ""
		if str(r, "isDraft") == "true" {
			draft = " draft"
		}
		return fmt.Sprintf("- #%s %s%s mergeable=%s review=%s updated %s\n  %s\n", str(r, "number"), str(r, "headRefName"), draft, str(r, "mergeable"), str(r, "reviewDecision"), str(r, "updatedAt"), str(r, "title"))
	}
	j, _ := json.Marshal(r)
	return "- " + string(j) + "\n"
}

// when renders an epoch-seconds number as a UTC timestamp; anything else as itself.
func when(v any) string {
	switch t := v.(type) {
	case float64:
		if t > 0 {
			return time.Unix(int64(t), 0).UTC().Format("2006-01-02T15:04Z")
		}
	case string:
		return orDash(t)
	}
	return "-"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
