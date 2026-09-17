package swarm

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// onPlane points the state's requests, rulings and notes at a store, the
// way seats on different machines would share one.
func onPlane(t *testing.T) *State {
	t.Helper()
	main := repo(t)
	t.Setenv("SWARM_STORE", "file:"+filepath.Join(t.TempDir(), "store"))
	return open(t, main)
}

func TestPlaneAskRuleWaitAndNote(t *testing.T) {
	s := onPlane(t)
	r, err := s.Ask("asker", []string{"pkg/x"}, "order?", []string{"a", "b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if open, _ := s.Requests(true); len(open) != 1 || open[0].Status != "open" {
		t.Fatalf("open requests = %+v", open)
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		_, _ = s.Rule(r.ID, "peer", 0, "a first", "a started earlier", "", []string{"a", "b"})
	}()
	_, d, err := s.Wait(r.ID, 2*time.Second, 10*time.Millisecond)
	if err != nil || d == nil || strings.Join(d.Order, ",") != "a,b" {
		t.Fatalf("wait: %v %+v", err, d)
	}
	notes, _ := s.Inbox("asker", true)
	if len(notes) != 1 || notes[0].Kind != "ruling" {
		t.Fatalf("the ruling did not reach the asker: %+v", notes)
	}
	if again, _ := s.Inbox("asker", true); len(again) != 0 {
		t.Fatalf("note delivered twice: %+v", again)
	}
	if open, _ := s.Requests(true); len(open) != 0 {
		t.Fatalf("ruled request still open: %+v", open)
	}
	same, err := s.Rule(r.ID, "peer", 0, "changed my mind", "", "", nil)
	if err != nil || same.ID != d.ID {
		t.Fatalf("a retry did not return the original ruling: %v %+v", err, same)
	}
	_, err = s.Rule(r.ID, "other", 0, "b first", "", "", nil)
	refused(t, err, "already_ruled")
}

func TestPlaneFencingAndTiers(t *testing.T) {
	s := onPlane(t)
	if err := s.Init(Tiers{Operator: []string{"op"}}); err != nil {
		t.Fatal(err)
	}
	n := clock(t)
	r, _ := s.Ask("a", []string{"f.go"}, "?", nil, "")
	c1, err := s.ClaimRequest(r.ID, "b", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimRequest(r.ID, "c", time.Minute)
	refused(t, err, "claimed_by_other")
	*n = n.Add(2 * time.Second)
	_, err = s.Rule(r.ID, "b", c1.Claim.Epoch, "late", "", "", nil)
	refused(t, err, "claim_expired")
	c2, err := s.ClaimRequest(r.ID, "c", time.Minute)
	if err != nil || c2.Claim.Epoch <= c1.Claim.Epoch {
		t.Fatalf("takeover: %v %+v", err, c2)
	}
	_, err = s.Rule(r.ID, "b", c1.Claim.Epoch, "stale", "", "", nil)
	refused(t, err, "claim_fenced")
	if _, err := s.Rule(r.ID, "c", c2.Claim.Epoch, "ok", "", "", nil); err != nil {
		t.Fatal(err)
	}
	// Tiers and the tiebreak hold on the plane as they do in files.
	d, err := s.Decide("op", []string{"pkg/export"}, "jsonl", "product", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := s.Ask("a", []string{"pkg/export/x.go"}, "format?", nil, "")
	_, err = s.Rule(q.ID, "peer", 0, "csv", "", d.ID, nil)
	refused(t, err, "outranked")
	if reqs, _ := s.Requests(false); len(reqs) != 2 {
		t.Fatalf("a proactive ruling was listed as a request: %d", len(reqs))
	}
}

func TestPlaneEscalationMovesTheQuestionInOneStep(t *testing.T) {
	s := onPlane(t)
	if err := s.Init(Tiers{Operator: []string{"operator"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Ask("a", []string{"pkg/export"}, "csv or jsonl?", nil, "")
	next, err := s.Escalate(r.ID, "peer", TierOperator, "product decision")
	if err != nil || next.Needs != TierOperator || next.ID == r.ID {
		t.Fatalf("escalate: %v %+v", err, next)
	}
	_, err = s.Rule(next.ID, "peer", 0, "csv", "", "", nil)
	refused(t, err, "tier_too_low")
	if n, _ := s.Inbox("operator", false); len(n) != 1 {
		t.Fatalf("operator not told: %+v", n)
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		_, _ = s.Rule(next.ID, "operator", 0, "jsonl", "intent", "", nil)
	}()
	// The asker waits on the question it asked and hears the operator's answer.
	_, d, err := s.Wait(r.ID, 2*time.Second, 10*time.Millisecond)
	if err != nil || d == nil || d.Tier != TierOperator {
		t.Fatalf("wait did not follow the escalation: %v %+v", err, d)
	}
}

// Many peers race to rule one request, and overlapping scopes race the
// tiebreak: exactly one ruling per request, and never two effective
// same-tier rulings on one scope.
func TestPlaneConcurrentRulers(t *testing.T) {
	s := onPlane(t)
	r, _ := s.Ask("a", []string{"pkg/x"}, "?", nil, "")
	var wg sync.WaitGroup
	wins := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			by := "peer" + string(rune('A'+i))
			if d, err := s.Rule(r.ID, by, 0, "mine", "", "", nil); err == nil && d.By == by {
				wins <- by
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	if len(wins) != 1 {
		t.Fatalf("%d rulers won one request", len(wins))
	}
	a, _ := s.Ask("a", []string{"pkg/y/a.go"}, "?", nil, "")
	b, _ := s.Ask("b", []string{"pkg/y"}, "?", nil, "")
	var ok int
	var mu sync.Mutex
	for _, p := range [][2]string{{a.ID, "p1"}, {b.ID, "p2"}} {
		wg.Add(1)
		go func(id, by string) {
			defer wg.Done()
			if _, err := s.Rule(id, by, 0, "x", "", "", nil); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(p[0], p[1])
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d same-tier rulings landed on overlapping scope without superseding", ok)
	}
}
