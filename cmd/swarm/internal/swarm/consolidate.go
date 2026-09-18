package swarm

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
	HeadRed    string   `json:"head_red,omitempty"`   // the head found on entry failed verification; nothing was merged on top of it
}

// verify commands are split on spaces; quoted arguments are not supported.

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
	if seq.Conflict != "" {
		return nil, refuse("order_cycle", "%s; supersede one of the order rulings before consolidating", seq.Conflict)
	}
	c := &Consolidation{Into: into}
	// A merge that exists is not a merge that was verified. If an earlier
	// attempt died between merging and verifying, the head has no passing
	// receipt; verify it now, and refuse to build on a red head.
	if verify != "" {
		head, err := Git(worktree, "rev-parse", "HEAD")
		if err != nil {
			return nil, err
		}
		if !s.headVerified(worktree, head, into, verify) {
			c.HeadRed = head
			s.appendEvent(Event{Kind: "consolidate", Seat: into, Detail: "head " + short(head) + " failed verification; nothing merged"})
			return c, nil
		}
	}
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
		if verify != "" {
			head, _ := Git(worktree, "rev-parse", "HEAD")
			if !s.headVerified(worktree, head, into, verify) {
				_, _ = Git(worktree, "reset", "-q", "--hard", "HEAD~1")
				c.Failed = append(c.Failed, br)
				continue
			}
		}
		c.Merged = append(c.Merged, br)
	}
	s.appendEvent(Event{Kind: "consolidate", Seat: into, Detail: "merged " + joinMax(c.Merged, 40) + " conflicted " + joinMax(c.Conflicted, 40)})
	return c, nil
}

// headVerified answers from the receipt for this exact head, running the
// verification and recording a receipt when there is none. The receipt is
// what makes a verified merge durable across a crash and a retry.
func (s *State) headVerified(worktree, head, branch, verify string) bool {
	if rec := s.Receipt(head); rec != nil && rec.Cmd == verify {
		return rec.Pass
	}
	pass := verifies(worktree, verify)
	_ = s.PutReceipt(&Receipt{Tip: head, Branch: branch, Cmd: verify, Pass: pass})
	return pass
}

func verifies(dir, cmd string) bool {
	fields := strings.Fields(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	c.Dir = dir
	return c.Run() == nil
}
