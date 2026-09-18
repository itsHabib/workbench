package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/plane"
)

// Work is one unit of a goal the team broke down itself. It lives on the
// plane, so any seat on any machine can add one, and exactly one seat at a
// time holds it. Nobody assigns work: a seat claims what it will do.
type Work struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Files   []string  `json:"files,omitempty"`
	AddedBy string    `json:"added_by"`
	At      time.Time `json:"at"`
	Parent  string    `json:"parent,omitempty"`
	After   []string  `json:"after,omitempty"` // units this one waits on
	DoneAt  time.Time `json:"done_at,omitempty"`
	ClaimAt time.Time `json:"claim_at,omitempty"`
	Team    bool      `json:"team,omitempty"`  // a team's job, not one seat's
	Brief   string    `json:"brief,omitempty"` // what that team must build
	State   string    `json:"state"`           // open | claimed | done
	Holder  string    `json:"holder,omitempty"`
	Epoch   int       `json:"epoch,omitempty"`
	Until   time.Time `json:"until,omitempty"`
	Result  string    `json:"result,omitempty"`
	DoneBy  string    `json:"done_by,omitempty"`
	Head    string    `json:"head,omitempty"` // the commit the finisher says carries the unit
}

const kindWork = "work"

func workFromItem(it plane.Item) Work {
	var w Work
	_ = json.Unmarshal([]byte(it.Payload), &w)
	w.ID, w.State, w.Epoch = it.ID, "open", int(it.Epoch)
	switch {
	case it.State == plane.Done:
		w.State, w.DoneBy = "done", seatOf(it.DoneBy)
		w.DoneAt, _ = splitDoneAt(it.Result)
		w.Head, w.Result = splitHead(it.Result)
	case it.Owner != "" && Now().Before(it.Until):
		w.State, w.Holder, w.Until = "claimed", seatOf(it.Owner), it.Until
	}
	return w
}

// WorkAdd puts a unit of work on the plane. Adding an id that exists is a
// no-op, so two seats that think of the same task do not double it.
func (s *State) WorkAdd(id, title string, files []string, by, parent string, team bool, brief string, after []string) (*Work, error) {
	id = strings.TrimSpace(id)
	if id == "" || title == "" {
		return nil, refuse("bad_work", "work needs an id and --title")
	}
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	if it, err := st.Get(ctx, kindWork, id); err == nil {
		w := workFromItem(it)
		return &w, refuse("exists", "%s already exists (%s): %s", id, w.State, w.Title)
	}
	if team {
		// A team's job is named by the directories it owns, so two founders
		// who each add "the shop subsystem" under different ids collide here
		// instead of forming two teams that build the same thing.
		if len(files) == 0 {
			return nil, refuse("bad_work", "a --team unit needs --files naming the directories that team owns")
		}
		if other, path := s.teamOverlap(st, files); other != "" {
			return nil, refuse("overlaps", "%s already owns %s; claim or join that instead of adding another", other, path)
		}
	}
	w := Work{ID: id, Title: title, Files: files, AddedBy: by, At: Now(), Parent: parent, Team: team, Brief: brief, After: after}
	payload, _ := json.Marshal(w)
	if err := st.Put(ctx, plane.Item{Kind: kindWork, ID: id, Payload: string(payload)}); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "work_add", Seat: by, Detail: id})
	w.State = "open"
	return &w, nil
}

// WorkList lists every unit, oldest first.
func (s *State) WorkList() ([]Work, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	items, err := st.List(context.Background(), kindWork)
	if err != nil {
		return nil, err
	}
	out := make([]Work, 0, len(items))
	for _, it := range items {
		out = append(out, workFromItem(it))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// WIPLimit is how many units one seat may hold at once. SWARM_WIP overrides
// it. Two lets a seat line up its own follow-on while it lands the first;
// a larger number lets one seat hoard the list while the others idle.
func WIPLimit() int {
	if v, err := strconv.Atoi(os.Getenv("SWARM_WIP")); err == nil && v > 0 {
		return v
	}
	return 2
}

// WorkClaim leases a unit to seat. Claiming what you already hold renews
// it. A unit whose holder went away becomes claimable when its lease lapses.
func (s *State) WorkClaim(id, seat string, ttl time.Duration) (*Work, error) {
	if ttl <= 0 {
		ttl = 20 * time.Minute
	}
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	it, err := st.Get(ctx, kindWork, id)
	if err != nil {
		return nil, refuse("no_such_work", "%s", id)
	}
	owner := incarnation(seat)
	if it.Owner != owner {
		if blocked := s.blockedBy(st, workFromItem(it)); blocked != "" {
			return nil, refuse("blocked", "%s waits on %s, which is not done", id, blocked)
		}
		held, _ := s.heldBy(st, seat)
		if len(held) >= WIPLimit() {
			return nil, refuse("wip_full", "%s already holds %s; finish or drop one first", seat, strings.Join(held, ", "))
		}
	}
	var g plane.Grant
	if it.Owner == owner && Now().Before(it.Until) {
		g, err = st.Renew(ctx, plane.Grant{Kind: kindWork, ID: id, Epoch: it.Epoch, Owner: owner}, ttl)
	} else {
		g, err = st.Claim(ctx, kindWork, id, owner, ttl, "")
	}
	switch {
	case errors.Is(err, plane.ErrHeld):
		return nil, refuse("held_by_other", "%s is held by %s until %s", id, seatOf(it.Owner), it.Until.Format(time.RFC3339))
	case errors.Is(err, plane.ErrDone):
		return nil, refuse("already_done", "%s is done", id)
	case err != nil:
		return nil, err
	}
	s.appendEvent(Event{Kind: "work_claim", Seat: seat, Detail: id, Epoch: int(g.Epoch)})
	w := workFromItem(it)
	w.State, w.Holder, w.Epoch, w.Until = "claimed", seat, int(g.Epoch), g.Until
	return &w, nil
}

// WorkDone commits a unit under the caller's lease. A seat whose lease
// lapsed and was taken over is refused: its result is not the accepted one.
func (s *State) WorkDone(id, seat, result, head string) (*Work, error) {
	if head != "" {
		result = "head:" + head + "\n" + result
	}
	result = "done_at:" + Now().UTC().Format(time.RFC3339Nano) + "\n" + result
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	it, err := st.Get(ctx, kindWork, id)
	if err != nil {
		return nil, refuse("no_such_work", "%s", id)
	}
	owner := incarnation(seat)
	if it.State != plane.Done && it.Owner != owner {
		return nil, refuse("not_yours", "%s is not claimed by %s; claim it first", id, seat)
	}
	g := plane.Grant{Kind: kindWork, ID: id, Epoch: it.Epoch, Owner: owner}
	if _, err := st.Commit(ctx, g, result, "work:"+id+":"+owner, nil); err != nil {
		if errors.Is(err, plane.ErrDone) {
			return nil, refuse("already_done", "%s was already finished by %s", id, seatOf(it.DoneBy))
		}
		return nil, refuse("lease_lost", "%s changed hands while you worked; your result was not accepted", id)
	}
	s.appendEvent(Event{Kind: "work_done", Seat: seat, Detail: id})
	w := workFromItem(it)
	w.State, w.DoneBy = "done", seat
	w.Head, w.Result = splitHead(result)
	return &w, nil
}

// teamOverlap finds an existing team unit owning a path that contains, or
// is contained by, one of the given paths.
func (s *State) teamOverlap(st plane.Store, files []string) (string, string) {
	items, err := st.List(context.Background(), kindWork)
	if err != nil {
		return "", ""
	}
	for _, it := range items {
		w := workFromItem(it)
		if !w.Team {
			continue
		}
		for _, have := range w.Files {
			for _, want := range files {
				h, n := strings.Trim(have, "/")+"/", strings.Trim(want, "/")+"/"
				if strings.HasPrefix(h, n) || strings.HasPrefix(n, h) {
					return w.ID, have
				}
			}
		}
	}
	return "", ""
}

// blockedBy names the first unit this one waits on that is not done.
func (s *State) blockedBy(st plane.Store, w Work) string {
	for _, dep := range w.After {
		it, err := st.Get(context.Background(), kindWork, dep)
		if err != nil || it.State != plane.Done {
			return dep
		}
	}
	return ""
}

func (s *State) heldBy(st plane.Store, seat string) ([]string, error) {
	items, err := st.List(context.Background(), kindWork)
	if err != nil {
		return nil, err
	}
	var held []string
	for _, it := range items {
		if w := workFromItem(it); w.State == "claimed" && w.Holder == seat {
			held = append(held, w.ID)
		}
	}
	return held, nil
}

// WorkNext ranks the open units for a seat by how much they touch what
// the seat has already changed on the base branch. Context is the thing
// a seat has that a fresh one does not, so the list routes work to it.
func (s *State) WorkNext(seat, base string) ([]Work, error) {
	ws, err := s.WorkList()
	if err != nil {
		return nil, err
	}
	touched := map[string]bool{}
	if base == "" {
		base = "main"
	}
	for _, ref := range []string{"origin/" + base, base} {
		lines, err := gitLines(s.Repo, "log", "--author="+seat, "--name-only", "--format=", ref)
		if err != nil {
			continue
		}
		for _, l := range lines {
			if l != "" {
				touched[l] = true
				touched[filepath.Dir(l)] = true
			}
		}
		break
	}
	done := map[string]bool{}
	for _, w := range ws {
		done[w.ID] = w.State == "done"
	}
	var open []Work
	score := map[string]int{}
	for _, w := range ws {
		if w.State != "open" || !readyOf(w, done) {
			continue
		}
		for _, f := range w.Files {
			if touched[f] || touched[filepath.Dir(f)] {
				score[w.ID]++
			}
		}
		open = append(open, w)
	}
	sort.SliceStable(open, func(i, j int) bool { return score[open[i].ID] > score[open[j].ID] })
	return open, nil
}

// WorkDrop gives a unit back so another seat can take it. The epoch is not
// reused: the next claim fences anything the old holder still tries to land.
func (s *State) WorkDrop(id, seat string) (*Work, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	it, err := st.Get(ctx, kindWork, id)
	if err != nil {
		return nil, refuse("no_such_work", "%s", id)
	}
	owner := incarnation(seat)
	if it.Owner != owner {
		return nil, refuse("not_yours", "%s is not claimed by %s", id, seat)
	}
	if err := st.Release(ctx, plane.Grant{Kind: kindWork, ID: id, Epoch: it.Epoch, Owner: owner}); err != nil {
		return nil, refuse("lease_lost", "%s already changed hands", id)
	}
	s.appendEvent(Event{Kind: "work_drop", Seat: seat, Detail: id})
	w := workFromItem(it)
	w.State, w.Holder = "open", ""
	return &w, nil
}

// Idle blocks until there is something for seat to do: a note in its inbox,
// an open request it did not ask, or a unit of work it could claim. It
// returns a line saying which, or "" on timeout. A unit finishing while the
// seat waits also returns, so a seat blocked on someone else's unit wakes
// when it lands. It is how a headless seat
// waits without burning turns.
func (s *State) Idle(seat string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	doneAtStart := -1
	for {
		if notes, _ := s.Inbox(seat, false); len(notes) > 0 {
			return "you have notes: run `swarm inbox`"
		}
		if reqs, _ := s.Requests(true); len(reqs) > 0 {
			for _, r := range reqs {
				if r.From != seat && r.Status == "open" {
					return "an open request is waiting for a ruling: run `swarm requests`"
				}
			}
		}
		ws, _ := s.WorkList()
		open, done := 0, 0
		for _, w := range ws {
			switch w.State {
			case "open":
				open++
			case "done":
				done++
			}
		}
		if open > 0 {
			return "there is unclaimed work: run `swarm work list`"
		}
		if len(ws) > 0 && done == len(ws) {
			return "every unit of work is done"
		}
		if doneAtStart >= 0 && done > doneAtStart {
			return "a unit was finished while you waited: run `swarm work list` and `git pull --rebase origin main`"
		}
		doneAtStart = done
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(3 * time.Second)
	}
}

// splitHead separates the commit a result names from its prose. The head
// rides inside the committed result so it is fenced by the same epoch.
func splitHead(result string) (head, rest string) {
	_, result = splitDoneAt(result)
	line, after, _ := strings.Cut(result, "\n")
	if sha, ok := strings.CutPrefix(line, "head:"); ok {
		return sha, after
	}
	return "", result
}

func splitDoneAt(result string) (time.Time, string) {
	line, after, _ := strings.Cut(result, "\n")
	if ts, ok := strings.CutPrefix(line, "done_at:"); ok {
		t, _ := time.Parse(time.RFC3339Nano, ts)
		return t, after
	}
	return time.Time{}, result
}

// readyOf is true when every unit w waits on is done.
func readyOf(w Work, done map[string]bool) bool {
	for _, dep := range w.After {
		if !done[dep] {
			return false
		}
	}
	return true
}
