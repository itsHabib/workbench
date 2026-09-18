package gym

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A team of teams. The founders cut a goal too big for one team into parts
// and mark each part as a team's job. For every such unit the harness forms
// a child team: two founders of its own, growth off its own list, its own
// store namespace, its own branch off the parent's. When the child's list
// is done and its branch is green, the parent merges the branch and marks
// the unit done. The layer above a team routes work down and results up and
// writes no code, which is the only kind of hierarchy the runs so far have
// justified.

// ChildResult is what a child team reports back into the parent's result.
type ChildResult struct {
	Unit   string     `json:"unit"`
	Branch string     `json:"branch"`
	Merged bool       `json:"merged"`
	Note   string     `json:"note,omitempty"`
	Result TeamResult `json:"result"`
}

// teamsController claims every --team unit as it appears and runs a child
// team for it. It is the parent's only management act.
func (r *teamRun) teamsController() {
	defer r.wg.Done()
	started := map[string]bool{}
	for r.ctx.Err() == nil {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
		for _, w := range r.workList() {
			if !w.Team || w.State != "open" || started[w.ID] {
				continue
			}
			if err := r.swarm("team-"+w.ID, "work", "claim", w.ID, "--ttl", r.wall.String()); err != nil {
				continue
			}
			started[w.ID] = true
			r.wg.Add(1)
			go r.childTeam(w)
		}
		if r.finished() {
			return
		}
	}
}

// childTeam forms, runs and lands one child team.
func (r *teamRun) childTeam(w workRow) {
	defer r.wg.Done()
	branch := "team/" + w.ID
	ctx, cancel := context.WithCancel(r.ctx)
	child := &teamRun{
		branch: branch, brief: w.Brief, originPath: r.origin(), childMax: r.childMax, shape: "swat", model: r.model,
		store: childStore(r.store, w.ID), out: filepath.Join(r.out, "teams", w.ID), goal: r.goal, module: r.module,
		self: r.self, seatCmd: r.seatCmd, wakes: r.wakes, turns: r.turns, wall: r.wall, ctx: ctx, cancel: cancel,
		start: time.Now(), reconcile: r.reconcile, verifyCmd: r.verifyCmd, deadline: r.deadline, lives: map[string]*liveSeat{}, childRuns: map[string]*teamRun{},
	}
	r.mu.Lock()
	r.childRuns[w.ID] = child
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.childRuns, w.ID)
		r.mu.Unlock()
	}()
	child.env = append(childEnv(filepath.Dir(r.self), filepath.Join(child.out, "fleet-state")), "SWARM_STORE="+child.store)
	_ = os.MkdirAll(filepath.Join(child.out, "logs"), 0o755)
	fmt.Printf("[%4.0fs] team %s forms on %s: %s\n", time.Since(r.start).Seconds(), w.ID, branch, w.Title)
	cr := ChildResult{Unit: w.ID, Branch: branch}
	if err := child.seedBranch(r); err != nil {
		cr.Note = "seed: " + err.Error()
	} else {
		cr.Result = child.run(r.childMax)
		cr.Merged, cr.Note = r.mergeChild(child)
	}
	fmt.Printf("[%4.0fs] team %s done: merged=%v %s\n", time.Since(r.start).Seconds(), w.ID, cr.Merged, cr.Note)
	r.mu.Lock()
	r.children = append(r.children, cr)
	r.mu.Unlock()
	result := "merged " + branch
	if !cr.Merged {
		result = "NOT MERGED: " + cr.Note
		r.mu.Lock()
		seats := append([]string{}, r.live...)
		r.mu.Unlock()
		for _, seat := range seats {
			_ = r.swarm("team-"+w.ID, "nudge", seat, "Team "+w.ID+" finished on branch "+branch+" but it could not be merged into "+r.branch+": "+cr.Note+". Merge it by hand (git fetch origin, git merge origin/"+branch+"), resolve, land, then mark "+w.ID+" done.")
		}
		_ = r.swarm("team-"+w.ID, "work", "drop", w.ID)
		return
	}
	_ = r.swarm("team-"+w.ID, "work", "done", w.ID, "--result", result)
}

// seedBranch starts the child's branch from the parent's current tip with
// the brief and the parent's decisions appended to the spec. The child
// shares the parent's origin; only its branch and its store differ.
func (c *teamRun) seedBranch(parent *teamRun) error {
	seed := filepath.Join(c.out, "seed")
	if err := hgit("", "clone", "-q", parent.origin(), seed); err != nil {
		return err
	}
	if err := hgit(seed, "checkout", "-q", "-b", c.branch, "origin/"+parent.branch); err != nil {
		return err
	}
	// The brief goes in a file of the team's own, never appended to SPEC.md:
	// every child branch touching the same lines of one file is a merge
	// conflict for every child but the first.
	decisions := parent.decisionsText()
	brief := fmt.Sprintf("# Your team's part\n\nYou are one team inside a team of teams. SPEC.md is the whole goal; build only this part, on branch `%s`. The rest is being built by other teams on their branches and merged into `%s` by the founders. Assume the shared foundation on `%s` as it is when you start, and pull it again before you land.\n\n%s\n", c.branch, parent.branch, parent.branch, c.brief)
	if decisions != "" {
		brief += "\n## Decisions the founders already made (binding)\n\n" + decisions
	}
	if err := os.WriteFile(filepath.Join(seed, "TEAM.md"), []byte(brief), 0o644); err != nil {
		return err
	}
	for _, step := range [][]string{{"add", "TEAM.md"}, {"commit", "-q", "-m", "team " + c.branch + ": brief"}, {"push", "-q", "origin", c.branch}} {
		if err := hgit(seed, step...); err != nil {
			return err
		}
	}
	return nil
}

func (r *teamRun) decisionsText() string {
	cmd := exec.Command(r.self, "decisions", "--json", "--as", "harness")
	cmd.Dir, cmd.Env = filepath.Join(r.out, "seed"), r.env
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	var rows []struct {
		Scope, Ruling string
	}
	_ = json.Unmarshal(data, &rows)
	var sb strings.Builder
	for _, d := range rows {
		fmt.Fprintf(&sb, "- %s: %s\n", d.Scope, d.Ruling)
	}
	return sb.String()
}

// mergeChild brings a finished child branch into the parent's branch. A
// child whose own suite is red is not merged; a merge conflict is reported,
// never resolved by the harness.
func (r *teamRun) mergeChild(c *teamRun) (bool, string) {
	if !c.lastGreen() {
		return false, "child branch is not green"
	}
	seed := filepath.Join(r.out, "seed")
	steps := [][]string{{"fetch", "-q", "origin"}, {"checkout", "-q", r.branch}, {"reset", "-q", "--hard", "origin/" + r.branch},
		{"merge", "-q", "--no-ff", "-m", "merge " + c.branch, "origin/" + c.branch}}
	for _, step := range steps {
		if err := hgit(seed, step...); err != nil {
			_ = hgit(seed, "merge", "--abort")
			return false, err.Error()
		}
	}
	ok, out := r.verifyHeadIn(seed)
	if !ok {
		_ = hgit(seed, "reset", "-q", "--hard", "origin/"+r.branch)
		return false, "merged tree is red: " + tail(out, 300)
	}
	if err := hgit(seed, "push", "-q", "origin", "HEAD:"+r.branch); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// lastGreen says whether the child's branch head passed verification, from
// the receipts its reconciler wrote or, without one, by verifying now.
func (c *teamRun) lastGreen() bool {
	head, _ := c.originHead()
	if head == "" {
		return false
	}
	for _, rc := range c.receipts {
		if rc.Head == head {
			return rc.Green
		}
	}
	ok, _ := c.verifyHead(head)
	return ok
}

func (r *teamRun) verifyHeadIn(dir string) (bool, string) {
	var log strings.Builder
	steps := [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, {"go", "test", "-count=1", "./..."}}
	if r.verifyCmd != "" {
		steps = [][]string{{"sh", "-c", r.verifyCmd}}
	}
	for _, step := range steps {
		ctx, cancel := context.WithTimeout(r.ctx, 10*time.Minute)
		cmd := exec.CommandContext(ctx, step[0], step[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		cancel()
		log.Write(out)
		if err != nil {
			return false, log.String()
		}
	}
	return true, ""
}

// childStore namespaces a child's store under its parent's.
func childStore(parent, id string) string {
	if strings.HasPrefix(parent, "file:") {
		return parent + "-" + id
	}
	return strings.TrimSuffix(parent, ":") + "-" + id + ":"
}
