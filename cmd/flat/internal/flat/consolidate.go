package flat

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Consolidation is the outcome of the mechanical part of a theme merge.
type Consolidation struct {
	Into       string   `json:"into"`
	Merged     []string `json:"merged"`
	Conflicted []string `json:"conflicted,omitempty"` // merge conflict; left for a mind
	Failed     []string `json:"failed,omitempty"`     // merged cleanly but verification failed; undone
	Skipped    []string `json:"skipped,omitempty"`    // already contained
}

// Consolidate merges every landed branch into the branch checked out at
// worktree, in the ledger's order. A merge that applies cleanly and
// verifies costs no session a turn; a conflict or a red merge is undone
// and listed, and only those need a consolidator's attention.
func (s *State) Consolidate(worktree, verify string, opts BoardOptions) (*Consolidation, error) {
	into := CurrentBranch(worktree)
	if into == "" {
		return nil, refuse("detached", "consolidate needs a branch checked out")
	}
	seq, err := s.Order(opts)
	if err != nil {
		return nil, err
	}
	c := &Consolidation{Into: into}
	for _, br := range seq.Order {
		if br == into {
			continue
		}
		if isAncestor(worktree, br, "HEAD") {
			c.Skipped = append(c.Skipped, br)
			continue
		}
		if _, err := Git(worktree, "merge", "--no-ff", "-q", "-m", "merge "+br, br); err != nil {
			_, _ = Git(worktree, "merge", "--abort")
			c.Conflicted = append(c.Conflicted, br)
			continue
		}
		if verify != "" && !verifies(worktree, verify) {
			_, _ = Git(worktree, "reset", "-q", "--hard", "HEAD~1")
			c.Failed = append(c.Failed, br)
			continue
		}
		c.Merged = append(c.Merged, br)
	}
	s.appendEvent(Event{Kind: "consolidate", Seat: into, Detail: "merged " + joinMax(c.Merged, 40) + " conflicted " + joinMax(c.Conflicted, 40)})
	return c, nil
}

func verifies(dir, cmd string) bool {
	fields := strings.Fields(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	c.Dir = dir
	return c.Run() == nil
}
