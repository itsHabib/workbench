package swarm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TaskRow is one entry of briefs/tasks.json: the unit admission starts a
// seat for. Parent names the seat that split it off, if any.
type TaskRow struct {
	Branch   string   `json:"Branch"`
	Title    string   `json:"Title"`
	Card     string   `json:"Card"`
	Resource string   `json:"Resource,omitempty"`
	Fault    string   `json:"Fault,omitempty"`
	Parent   string   `json:"Parent,omitempty"`
	Files    []string `json:"Files,omitempty"`
	Added    string   `json:"Added,omitempty"`
}

func tasksPath(repo string) string { return filepath.Join(repo, "briefs", "tasks.json") }

// ReadTasks reads the task rows of a repository checkout.
func ReadTasks(repo string) ([]TaskRow, error) {
	data, err := os.ReadFile(tasksPath(repo))
	if err != nil {
		return nil, err
	}
	var rows []TaskRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Split records that a seat found its task to be several: it appends the
// children to tasks.json in the shared checkout, writes their cards, and
// files a ruling so the ledger shows who owns the parent. Admission picks
// the children up like any other task. No lead is consulted; the seat that
// discovered the scope holds the evidence.
func (s *State) Split(seat, repo string, children []TaskRow, why string) (*Decision, error) {
	if seat == "" || len(children) == 0 || why == "" {
		return nil, refuse("bad_split", "split needs a seat, at least one --into child, and --why")
	}
	release, err := s.lock("tasks", 5*time.Second, time.Minute)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := ReadTasks(repo)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	have := map[string]*TaskRow{}
	for i := range rows {
		have[rows[i].Branch] = &rows[i]
	}
	// Validate the whole batch before anything is written: no empty or
	// repeated names, and a name already queued must be this same split
	// being retried, not a collision.
	batch := map[string]bool{}
	var names, scope []string
	for i := range children {
		c := &children[i]
		if c.Branch == "" || batch[c.Branch] {
			return nil, refuse("bad_split", "child %q is empty or named twice in this split", c.Branch)
		}
		if prior := have[c.Branch]; prior != nil && prior.Parent != seat {
			return nil, refuse("bad_split", "child %q is already a task of %q", c.Branch, prior.Parent)
		}
		batch[c.Branch] = true
		names = append(names, c.Branch)
		scope = append(scope, "task:"+c.Branch)
	}
	// The ruling is the commit point and is idempotent: a retry of the same
	// split finds its own ruling and goes on to finish the queueing.
	d, err := s.splitRuling(seat, scope, names, why)
	if err != nil {
		return nil, err
	}
	for i := range children {
		c := &children[i]
		if have[c.Branch] != nil {
			continue // queued by the attempt this one is completing
		}
		c.Parent, c.Added = seat, Now().Format(time.RFC3339)
		if c.Card == "" {
			c.Card = c.Title
		}
		c.Card = fmt.Sprintf("%s\n\nSplit from %s: %s", c.Card, seat, why)
		rows = append(rows, *c)
	}
	data, _ := json.MarshalIndent(rows, "", "  ")
	if err := writeAtomic(tasksPath(repo), append(data, '\n')); err != nil {
		return nil, err
	}
	for _, c := range children {
		card := filepath.Join(repo, "briefs", "tasks", c.Branch+".md")
		_ = os.MkdirAll(filepath.Dir(card), 0o755)
		_ = writeAtomic(card, []byte(fmt.Sprintf("# %s\n\nbranch: %s\nparent: %s\n\n%s\n", c.Title, c.Branch, seat, c.Card)))
	}
	s.appendEvent(Event{Kind: "split", Seat: seat, Detail: strings.Join(names, ","), Tier: TierPeer})
	_, _ = s.Nudge("operator", "split", fmt.Sprintf("%s split its task into %s: %s. the children are queued for admission.", seat, strings.Join(names, ", "), why))
	return d, nil
}

// splitRuling records the split once. Its scope is the children, which are
// unique, so a second split by the same seat does not collide with the
// first, and the same split retried returns the ruling it already made.
func (s *State) splitRuling(seat string, scope, names []string, why string) (*Decision, error) {
	all, err := s.Decisions()
	if err != nil {
		return nil, err
	}
	want := strings.Join(scope, ",")
	for i := range all {
		if all[i].By == seat && strings.Join(all[i].Scope, ",") == want {
			return &all[i], nil
		}
	}
	return s.Decide(seat, scope, fmt.Sprintf("split into %s: %s", strings.Join(names, ", "), why), "the seat's own findings", "", nil)
}

// ParseChildren reads "branch:title|branch:title" into rows.
func ParseChildren(spec string) []TaskRow {
	var out []TaskRow
	for _, part := range strings.Split(spec, "|") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		branch, title, _ := strings.Cut(part, ":")
		out = append(out, TaskRow{Branch: strings.TrimSpace(branch), Title: strings.TrimSpace(title)})
	}
	return out
}
