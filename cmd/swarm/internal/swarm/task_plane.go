package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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
	State   string    `json:"state"` // open | claimed | done
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
		w.Head, w.Result = splitHead(it.Result)
	case it.Owner != "" && Now().Before(it.Until):
		w.State, w.Holder, w.Until = "claimed", seatOf(it.Owner), it.Until
	}
	return w
}

// WorkAdd puts a unit of work on the plane. Adding an id that exists is a
// no-op, so two seats that think of the same task do not double it.
func (s *State) WorkAdd(id, title string, files []string, by, parent string) (*Work, error) {
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
	w := Work{ID: id, Title: title, Files: files, AddedBy: by, At: Now(), Parent: parent}
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
	line, after, _ := strings.Cut(result, "\n")
	if sha, ok := strings.CutPrefix(line, "head:"); ok {
		return sha, after
	}
	return "", result
}
