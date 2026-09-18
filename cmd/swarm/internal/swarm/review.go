package swarm

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/plane"
)

// Review is one seat's judgment of one exact commit. It is an item on the
// store, never a note: a review that lives only in a session did not
// happen as far as any later reader is concerned.
type Review struct {
	Tip     string    `json:"tip"`
	By      string    `json:"by"`
	Verdict string    `json:"verdict"` // pass | fail
	Why     string    `json:"why,omitempty"`
	At      time.Time `json:"at"`
}

const kindReview = "review"

// Review records a verdict. One per reviewer per commit; a second from the
// same seat is refused so a changed mind is a new commit's review, not a
// rewrite.
func (s *State) Review(tip, by, verdict, why string) (*Review, error) {
	if tip == "" || by == "" {
		return nil, refuse("bad_review", "review needs a commit and a reviewing seat")
	}
	if verdict != "pass" && verdict != "fail" {
		return nil, refuse("bad_review", "--verdict pass|fail")
	}
	if !remote() {
		return nil, refuse("no_store", "reviews live on a shared store; set SWARM_STORE")
	}
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	id := tip + "~" + by
	if _, err := st.Get(ctx, kindReview, id); err == nil {
		return nil, refuse("review_exists", "%s already reviewed %s", by, short(tip))
	}
	r := &Review{Tip: tip, By: by, Verdict: verdict, Why: why, At: Now()}
	payload, _ := json.Marshal(r)
	if err := st.Put(ctx, plane.Item{Kind: kindReview, ID: id, Payload: string(payload)}); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "review", Seat: by, Detail: verdict + " " + short(tip)})
	return r, nil
}

// Reviews lists reviews, for one commit when tip is given, oldest first.
func (s *State) Reviews(tip string) ([]Review, error) {
	if !remote() {
		return nil, refuse("no_store", "reviews live on a shared store; set SWARM_STORE")
	}
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	items, err := st.List(context.Background(), kindReview)
	if err != nil {
		return nil, err
	}
	var out []Review
	for _, it := range items {
		var r Review
		if json.Unmarshal([]byte(it.Payload), &r) == nil && (tip == "" || r.Tip == tip) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}
