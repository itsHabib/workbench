package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Intend declares, in the seat's own branch, the paths it is about to
// change, and commits the declaration. The board reads it from git, so
// contention is visible before any of those paths has a commit: two seats
// that start together and both find the package clear still collide on the
// board at once, not after both have landed.
func (s *State) Intend(worktree, seat string, paths []string) ([]string, error) {
	if seat == "" || len(paths) == 0 {
		return nil, refuse("bad_intent", "intend needs a seat and at least one path")
	}
	file := filepath.Join(worktree, filepath.FromSlash(markerDir(seat)), "INTENT.json")
	var in struct {
		Paths []string `json:"paths"`
	}
	_ = readJSON(file, &in)
	seen := map[string]bool{}
	for _, p := range append(in.Paths, paths...) {
		seen[filepath.ToSlash(p)] = true
	}
	in.Paths = make([]string, 0, len(seen))
	for p := range seen {
		in.Paths = append(in.Paths, p)
	}
	sort.Strings(in.Paths)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(in, "", "  ")
	if err := writeAtomic(file, append(data, '\n')); err != nil {
		return nil, err
	}
	rel := markerDir(seat) + "INTENT.json"
	if _, err := Git(worktree, "add", "--", rel); err != nil {
		return nil, err
	}
	if _, err := Git(worktree, "commit", "-q", "-m", "intent "+seat, "--", rel); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "intend", Seat: seat, Detail: joinMax(in.Paths, 6)})
	return in.Paths, nil
}

func joinMax(xs []string, n int) string {
	out := ""
	for i, x := range xs {
		if i == n {
			return out + ",…"
		}
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}
