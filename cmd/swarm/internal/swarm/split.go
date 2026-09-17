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
	have := map[string]bool{}
	for _, r := range rows {
		have[r.Branch] = true
	}
	var names []string
	for i := range children {
		c := &children[i]
		if c.Branch == "" || have[c.Branch] {
			return nil, refuse("bad_split", "child %q is empty or already a task", c.Branch)
		}
		c.Parent, c.Added = seat, Now().Format(time.RFC3339)
		if c.Card == "" {
			c.Card = c.Title
		}
		c.Card = fmt.Sprintf("%s\n\nSplit from %s: %s", c.Card, seat, why)
		rows = append(rows, *c)
		names = append(names, c.Branch)
		card := filepath.Join(repo, "briefs", "tasks", c.Branch+".md")
		_ = os.MkdirAll(filepath.Dir(card), 0o755)
		_ = os.WriteFile(card, []byte(fmt.Sprintf("# %s\n\nbranch: %s\nparent: %s\n\n%s\n", c.Title, c.Branch, seat, c.Card)), 0o644)
	}
	data, _ := json.MarshalIndent(rows, "", "  ")
	if err := writeAtomic(tasksPath(repo), append(data, '\n')); err != nil {
		return nil, err
	}
	d, err := s.Decide(seat, []string{"task:" + seat}, fmt.Sprintf("split into %s: %s", strings.Join(names, ", "), why), "the seat's own findings", "", nil)
	if err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "split", Seat: seat, Detail: strings.Join(names, ","), Tier: TierPeer})
	_, _ = s.Nudge("operator", "split", fmt.Sprintf("%s split its task into %s: %s. the children are queued for admission.", seat, strings.Join(names, ", "), why))
	return d, nil
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
