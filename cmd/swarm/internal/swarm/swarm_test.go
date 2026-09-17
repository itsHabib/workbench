package swarm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repo builds a hermetic repository with a main branch and returns its
// checkout. Every commit is unsigned and hook-free.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, "main")
	must(t, dir, "init", "-q", "--initial-branch=main", main)
	for _, kv := range [][2]string{{"user.name", "t"}, {"user.email", "t@example.invalid"}, {"commit.gpgsign", "false"}, {"core.hooksPath", filepath.Join(dir, "nohooks")}} {
		must(t, main, "config", kv[0], kv[1])
	}
	write(t, main, "a.go", "package a\n")
	write(t, main, "b.go", "package a\n")
	must(t, main, "add", "-A")
	must(t, main, "commit", "-q", "-m", "base")
	t.Setenv("SWARM_STATE", filepath.Join(dir, "state"))
	t.Setenv("SWARM_SESSION", "")
	return main
}

func must(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// branch creates a branch off main in its own worktree, with one commit
// touching files, and returns the worktree path.
func branch(t *testing.T, main, name string, files ...string) string {
	t.Helper()
	wt := filepath.Join(filepath.Dir(main), "wt", name)
	must(t, main, "worktree", "add", "-q", wt, "-b", name, "main")
	for _, f := range files {
		write(t, wt, f, "package a\n// "+name+"\n")
	}
	must(t, wt, "add", "-A")
	must(t, wt, "commit", "-q", "-m", "work "+name)
	return wt
}

func land(t *testing.T, wt, name string) {
	t.Helper()
	head := must(t, wt, "rev-parse", "HEAD")
	res, _ := json.Marshal(Result{HeadSHA: head, Claims: []string{"done"}})
	write(t, wt, "briefs/out/"+name+"/RESULT.json", string(res))
	must(t, wt, "add", "-A")
	must(t, wt, "commit", "-q", "-m", "land "+name)
}

func open(t *testing.T, dir string) *State {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rowOf(t *testing.T, b *Board, name string) Row {
	t.Helper()
	for _, r := range b.Rows {
		if r.Branch == name {
			return r
		}
	}
	t.Fatalf("no row %s in %+v", name, b.Rows)
	return Row{}
}

func TestPinCheckStates(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	working := branch(t, main, "w", "a.go")
	landed := branch(t, main, "l", "b.go")
	land(t, landed, "l")
	lying := branch(t, main, "x", "c.go")
	land(t, lying, "x")
	write(t, lying, "c.go", "package a\n// after\n")
	must(t, lying, "commit", "-q", "-am", "sneaky")
	bad := branch(t, main, "bad", "d.go")
	write(t, bad, "briefs/out/bad/RESULT.json", `{"head_sha":"0000000000000000000000000000000000000000"}`)
	must(t, bad, "add", "-A")
	must(t, bad, "commit", "-q", "-m", "bad pin")
	_ = working

	b, err := s.Board(BoardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"w": "working", "l": "landed", "x": "pin_violation", "bad": "pin_invalid"} {
		if got := rowOf(t, b, name).State; got != want {
			t.Errorf("%s: state %s, want %s", name, got, want)
		}
	}
	if extra := rowOf(t, b, "x").Extra; len(extra) != 1 || extra[0] != "c.go" {
		t.Errorf("x extra = %v", extra)
	}
}

func TestSilentAfterIdle(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00Z")
	branch(t, main, "w", "a.go")
	b, err := s.Board(BoardOptions{Idle: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if got := rowOf(t, b, "w").State; got != "silent" {
		t.Errorf("state %s, want silent", got)
	}
}

func TestContentionAndRuling(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	branch(t, main, "p", "a.go")
	branch(t, main, "q", "a.go", "b.go")
	b, err := s.Board(BoardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Contended) != 1 || b.Contended[0].File != "a.go" || b.Contended[0].Ruled {
		t.Fatalf("contended = %+v", b.Contended)
	}
	if ov := rowOf(t, b, "p").Overlaps; len(ov) != 1 || ov[0].With != "q" || ov[0].Ruled {
		t.Fatalf("overlaps = %+v", ov)
	}
	if err := s.Init(Tiers{Operator: []string{"op"}}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Ask("p", []string{"a.go"}, "who first?", []string{"p", "q"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rule(r.ID, "q", 0, "p lands first", "p started earlier", "", nil); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Board(BoardOptions{})
	if !b.Contended[0].Ruled {
		t.Fatalf("still unruled after ruling: %+v", b.Contended)
	}
	// A directory-scoped ruling covers files under it.
	r2, _ := s.Ask("p", []string{"pkg/x/y.go"}, "?", nil, "")
	if _, err := s.Rule(r2.ID, "q", 0, "ok", "", "", nil); err != nil {
		t.Fatal(err)
	}
	ds, _ := s.Lookup([]string{"pkg/x"})
	if len(ds) != 1 {
		t.Fatalf("lookup pkg/x = %d", len(ds))
	}
	if ds, _ = s.Lookup([]string{"pkg/xy"}); len(ds) != 0 {
		t.Fatalf("pkg/xy must not match pkg/x/y.go")
	}
}

func TestClaimFencing(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	r, _ := s.Ask("a", []string{"f"}, "?", nil, "")
	c1, err := s.ClaimRequest(r.ID, "b", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	c2, err := s.ClaimRequest(r.ID, "c", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Claim.Epoch != c1.Claim.Epoch+1 {
		t.Fatalf("epoch %d after %d", c2.Claim.Epoch, c1.Claim.Epoch)
	}
	if _, err := s.Rule(r.ID, "b", c1.Claim.Epoch, "late", "", "", nil); err == nil || !strings.Contains(err.Error(), "claim_fenced") {
		t.Fatalf("stale holder ruled: %v", err)
	}
	if _, err := s.ClaimRequest(r.ID, "d", time.Minute); err == nil || !strings.Contains(err.Error(), "claimed_by_other") {
		t.Fatalf("live claim taken: %v", err)
	}
	if _, err := s.Rule(r.ID, "c", c2.Claim.Epoch+7, "wrong epoch", "", "", nil); err == nil || !strings.Contains(err.Error(), "claim_fenced") {
		t.Fatalf("wrong epoch accepted: %v", err)
	}
	if _, err := s.Rule(r.ID, "c", c2.Claim.Epoch, "ok", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rule(r.ID, "c", 0, "again", "", "", nil); err == nil || !strings.Contains(err.Error(), "already_ruled") {
		t.Fatalf("double ruling: %v", err)
	}
}

func TestTiersAndTiebreak(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	if err := s.Init(Tiers{Operator: []string{"op"}, Lead: []string{"ld"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Ask("a", []string{"pkg/export"}, "format?", nil, TierOperator)
	if _, err := s.Rule(r.ID, "peer1", 0, "json", "", "", nil); err == nil || !strings.Contains(err.Error(), "tier_too_low") {
		t.Fatalf("peer ruled an operator request: %v", err)
	}
	if _, err := s.Rule(r.ID, "ld", 0, "json", "", "", nil); err == nil || !strings.Contains(err.Error(), "tier_too_low") {
		t.Fatalf("lead ruled an operator request: %v", err)
	}
	if _, err := s.Rule(r.ID, "op", 0, "jsonl", "intent", "", nil); err != nil {
		t.Fatal(err)
	}
	// Same scope, lower tier: outranked.
	r2, _ := s.Ask("b", []string{"pkg/export/x.go"}, "?", nil, "")
	if _, err := s.Rule(r2.ID, "peer1", 0, "csv", "", "", nil); err == nil || !strings.Contains(err.Error(), "outranked") {
		t.Fatalf("peer overrode operator: %v", err)
	}
}

func TestTiebreakAndEscalation(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	if err := s.Init(Tiers{Operator: []string{"op"}, Lead: []string{"ld"}}); err != nil {
		t.Fatal(err)
	}
	// Same tier, same scope: must supersede explicitly.
	r3, _ := s.Ask("b", []string{"a.go"}, "?", nil, "")
	d3, err := s.Rule(r3.ID, "peer1", 0, "first", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r4, _ := s.Ask("c", []string{"a.go"}, "?", nil, "")
	if _, err := s.Rule(r4.ID, "peer2", 0, "second", "", "", nil); err == nil || !strings.Contains(err.Error(), "must_supersede") {
		t.Fatalf("silent override at equal tier: %v", err)
	}
	if _, err := s.Rule(r4.ID, "peer2", 0, "second", "changed", d3.ID, nil); err != nil {
		t.Fatal(err)
	}
	eff, _ := s.Effective()
	for _, d := range eff {
		if d.ID == d3.ID {
			t.Fatal("superseded decision still effective")
		}
	}
	// Higher tier overrides silently and the ledger records the supersede.
	r5, _ := s.Ask("c", []string{"a.go"}, "?", nil, TierLead)
	d5, err := s.Rule(r5.ID, "ld", 0, "lead says", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d5.Supersedes == "" {
		t.Fatal("lead override did not record what it superseded")
	}
	// Escalation raises the bar and clears the claim.
	r6, _ := s.Ask("c", []string{"z.go"}, "?", nil, "")
	if _, err := s.ClaimRequest(r6.ID, "peer1", time.Minute); err != nil {
		t.Fatal(err)
	}
	e, err := s.Escalate(r6.ID, "peer1", TierOperator, "product")
	if err != nil {
		t.Fatal(err)
	}
	if e.Needs != TierOperator || e.Claim != nil || e.Status != "open" {
		t.Fatalf("escalated = %+v", e)
	}
	if _, err := s.Escalate(r6.ID, "peer1", TierLead, "down"); err == nil {
		t.Fatal("escalation went down")
	}
}

func TestLedgerChainDetectsTampering(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	r, _ := s.Ask("a", []string{"f"}, "?", nil, "")
	if _, err := s.Rule(r.ID, "b", 0, "one", "", "", nil); err != nil {
		t.Fatal(err)
	}
	r2, _ := s.Ask("a", []string{"g"}, "?", nil, "")
	if _, err := s.Rule(r2.ID, "b", 0, "two", "", "", nil); err != nil {
		t.Fatal(err)
	}
	p := s.path("decisions.jsonl")
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, bytes.Replace(data, []byte(`"one"`), []byte(`"uno"`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decisions(); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("tampering undetected: %v", err)
	}
}

func TestWaitReturnsRuling(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	r, _ := s.Ask("a", []string{"f"}, "?", nil, "")
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, _ = s.Rule(r.ID, "b", 0, "go", "", "", nil)
	}()
	_, d, err := s.Wait(r.ID, time.Second, 5*time.Millisecond)
	if err != nil || d == nil || d.Ruling != "go" {
		t.Fatalf("wait: %v %+v", err, d)
	}
	r2, _ := s.Ask("a", []string{"f"}, "?", nil, "")
	if _, _, err := s.Wait(r2.ID, 20*time.Millisecond, 5*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("no timeout: %v", err)
	}
}

func TestResourceLease(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	n := clock(t)
	if _, err := s.Take("db", "a", time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Take("db", "b", time.Minute); err == nil {
		t.Fatal("live lease taken")
	}
	*n = n.Add(2 * time.Second)
	r, err := s.Take("db", "b", time.Minute)
	if err != nil || r.Epoch != 2 {
		t.Fatalf("expired lease not taken over: %v %+v", err, r)
	}
	if err := s.Drop("db", "a", 0); err == nil {
		t.Fatal("non-holder dropped")
	}
	if err := s.Drop("db", "b", 0); err != nil {
		t.Fatal(err)
	}
	if s.Holder("db") != "" {
		t.Fatal("still held")
	}
}

func TestAdmit(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	branch(t, main, "w1", "a.go")
	branch(t, main, "w2", "b.go")
	a, err := s.Admit(AdmitOptions{Seats: 2})
	if err != nil {
		t.Fatal(err)
	}
	if a.Admitted || a.Active != 2 {
		t.Fatalf("seats not full: %+v", a)
	}
	t.Setenv("SWARM_DISK_FREE_BYTES", "1")
	a, _ = s.Admit(AdmitOptions{Seats: 10, DiskMin: 1 << 20})
	if a.Admitted || !strings.Contains(strings.Join(a.Reasons, ""), "disk_low") {
		t.Fatalf("disk floor ignored: %+v", a)
	}
	t.Setenv("SWARM_DISK_FREE_BYTES", "")
	if _, err := s.Take("db", "x", time.Minute); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Admit(AdmitOptions{Seats: 10, Resource: "db"})
	if a.Admitted {
		t.Fatalf("admitted onto a held resource: %+v", a)
	}
}

func TestWatchNudgesAndHookDelivers(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00Z")
	wt := branch(t, main, "w", "a.go")
	branch(t, main, "v", "a.go")
	t.Setenv("GIT_COMMITTER_DATE", "")
	_, alerts, err := s.WatchOnce(WatchOptions{Idle: time.Minute, UnclaimedAfter: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, a := range alerts {
		kinds[a.Kind]++
	}
	if kinds["builder_silent"] != 2 || kinds["overlap_unruled"] != 1 {
		t.Fatalf("alerts = %v", kinds)
	}
	notes, _ := s.Inbox("w", false)
	if len(notes) != 2 {
		t.Fatalf("w got %d notes, want silent + overlap", len(notes))
	}
	// A second pass within the renudge window adds nothing.
	_, _, _ = s.WatchOnce(WatchOptions{Idle: time.Minute, UnclaimedAfter: time.Hour})
	if notes, _ = s.Inbox("w", false); len(notes) != 2 {
		t.Fatalf("renudged too soon: %d", len(notes))
	}
	// The hook injects and consumes them; seat comes from the checked-out branch.
	t.Setenv("SWARM_SEAT", "")
	in := strings.NewReader(`{"hook_event_name":"PostToolUse","cwd":` + jsonString(wt) + `}`)
	var out bytes.Buffer
	if err := Hook(in, &out); err != nil {
		t.Fatal(err)
	}
	var resp map[string]map[string]string
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("hook output %q: %v", out.String(), err)
	}
	ctx := resp["hookSpecificOutput"]["additionalContext"]
	if !strings.Contains(ctx, "has not moved") || !strings.Contains(ctx, "no ruling") {
		t.Fatalf("context = %q", ctx)
	}
	if notes, _ = s.Inbox("w", false); len(notes) != 0 {
		t.Fatalf("notes not consumed: %d", len(notes))
	}
	// Ruling clears the overlap alert; the transition log records it.
	if err := s.Init(Tiers{Operator: []string{"op"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Ask("w", []string{"a.go"}, "?", nil, "")
	if _, err := s.Rule(r.ID, "v", 0, "w first", "", "", nil); err != nil {
		t.Fatal(err)
	}
	_, alerts, _ = s.WatchOnce(WatchOptions{Idle: time.Minute, UnclaimedAfter: time.Hour})
	for _, a := range alerts {
		if a.Kind == "overlap_unruled" {
			t.Fatal("overlap alert survived the ruling")
		}
	}
	data, _ := os.ReadFile(s.path("watch", "alerts.jsonl"))
	if !strings.Contains(string(data), `"cleared"`) {
		t.Fatal("no cleared transition logged")
	}
	if !strings.Contains(string(data), "ruled "+r.ID) && len(mustInbox(t, s, "w")) == 0 {
		t.Fatal("requester was not told about the ruling")
	}
}

func mustInbox(t *testing.T, s *State, seat string) []Note {
	t.Helper()
	n, err := s.Inbox(seat, false)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestStatsFromEvents(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	if err := s.Init(Tiers{Operator: []string{"op"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Ask("a", []string{"f"}, "?", nil, "")
	_, _ = s.Rule(r.ID, "b", 0, "x", "", "", nil)
	r2, _ := s.Ask("a", []string{"g"}, "?", nil, TierOperator)
	_, _ = s.Escalate(r2.ID, "b", TierOperator, "why")
	st, err := s.Stats(time.Hour, "main")
	if err != nil {
		t.Fatal(err)
	}
	if st.Requests != 2 || st.Ruled != 1 || st.Open != 1 || st.UnclaimedOver != 0 || st.ByNeeds[TierOperator] != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestAskRoutesByAffinity(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	branch(t, main, "p", "a.go")
	branch(t, main, "q", "a.go")
	branch(t, main, "r", "b.go")
	if err := s.Init(Tiers{Operator: []string{"op"}, Lead: []string{"ld"}}); err != nil {
		t.Fatal(err)
	}
	req, err := s.Ask("p", []string{"a.go"}, "order?", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := mustInbox(t, s, "q"); len(n) != 1 || n[0].Kind != "request" {
		t.Fatalf("q (affine) got %+v", n)
	}
	if n := mustInbox(t, s, "r"); len(n) != 0 {
		t.Fatalf("r (no affinity) got %+v", n)
	}
	if n := mustInbox(t, s, "p"); len(n) != 0 {
		t.Fatalf("asker nudged itself: %+v", n)
	}
	who := s.Affinity([]string{"a.go"}, "")
	if len(who) != 2 || who[0].Seat != "p" || who[1].Seat != "q" {
		t.Fatalf("who = %+v", who)
	}
	if _, err := s.Rule(req.ID, "q", 0, "p first", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if who = s.Affinity([]string{"a.go"}, "p"); len(who) != 1 || !strings.Contains(who[0].Why[1], "ruled") {
		t.Fatalf("ruler not indexed: %+v", who)
	}
	if _, err := s.Ask("r", []string{"z.go"}, "?", nil, TierLead); err != nil {
		t.Fatal(err)
	}
	if n := mustInbox(t, s, "ld"); len(n) != 1 {
		t.Fatalf("lead request not routed to lead: %+v", n)
	}
	st, _ := s.Stats(time.Hour, "main")
	if st.Routed != 2 || st.RuledByRouted != 1 {
		t.Fatalf("routing stats = routed %d ruled_by_routed %d", st.Routed, st.RuledByRouted)
	}
}

func TestHookRecordsTurnBoundaries(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	wt := branch(t, main, "w", "a.go")
	t.Setenv("SWARM_SEAT", "")
	send := func(event string) {
		in := strings.NewReader(`{"hook_event_name":"` + event + `","cwd":` + jsonString(wt) + `,"session_id":"sid-1"}`)
		if err := Hook(in, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	send("SessionStart")
	send("PreToolUse")
	if _, ok := s.Wakeable("w"); ok {
		t.Fatal("mid-turn seat reported wakeable")
	}
	send("Stop")
	rec, ok := s.Wakeable("w")
	if !ok || rec.SessionID != "sid-1" || rec.Cwd != wt {
		t.Fatalf("after Stop: ok=%v rec=%+v", ok, rec)
	}
	send("PostToolUse")
	if _, ok := s.Wakeable("w"); ok {
		t.Fatal("a new tool call after Stop still wakeable")
	}
	send("SessionEnd")
	if _, ok := s.Wakeable("w"); !ok {
		t.Fatal("ended session not wakeable")
	}
}

func TestRebaseOntoPeerIsInherited(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	p := branch(t, main, "p", "a.go")
	land(t, p, "p")
	q := branch(t, main, "q", "b.go")
	// q rebases onto p's tip, as a ruling "p lands first" tells it to.
	must(t, q, "rebase", "-q", "p")
	b, err := s.Board(BoardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	qr := rowOf(t, b, "q")
	if len(qr.Files) != 1 || qr.Files[0] != "b.go" {
		t.Fatalf("q files = %v, want only its own b.go", qr.Files)
	}
	if qr.State != "working" || qr.ResultPath != "" {
		t.Fatalf("q inherited p's landing: state=%s result=%s", qr.State, qr.ResultPath)
	}
	if len(b.Contended) != 0 {
		t.Fatalf("inherited files counted as contention: %+v", b.Contended)
	}
	land(t, q, "q")
	b, _ = s.Board(BoardOptions{})
	if got := rowOf(t, b, "q").State; got != "landed" {
		t.Fatalf("q after landing = %s", got)
	}
	if got := rowOf(t, b, "p").State; got != "landed" {
		t.Fatalf("p disturbed by q's rebase: %s", got)
	}
}

func TestDecideProactive(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	if err := s.Init(Tiers{Operator: []string{"op"}}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Decide("op", []string{"pkg/export"}, "json lines", "product", "", nil)
	if err != nil || d.Tier != TierOperator || d.Request != "" {
		t.Fatalf("decide: %v %+v", err, d)
	}
	r, _ := s.Ask("a", []string{"pkg/export/export.go"}, "format?", nil, "")
	if _, err := s.Rule(r.ID, "peer1", 0, "csv", "", "", nil); err == nil || !strings.Contains(err.Error(), "outranked") {
		t.Fatalf("peer overrode a proactive operator ruling: %v", err)
	}
	if _, err := s.Decide("op", []string{"pkg/export"}, "json array", "", "", nil); err == nil || !strings.Contains(err.Error(), "must_supersede") {
		t.Fatalf("same tier re-decided without superseding: %v", err)
	}
	if _, err := s.Decide("op", []string{"pkg/export"}, "json array", "changed", d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := trunc("héllo wörld", 6); got != "héllo…" {
		t.Fatalf("trunc = %q", got)
	}
}

func TestOrderFromLedger(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00Z")
	a := branch(t, main, "a", "x.go")
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-02T00:00:00Z")
	b := branch(t, main, "b", "x.go")
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-03T00:00:00Z")
	c := branch(t, main, "c", "y.go")
	t.Setenv("GIT_COMMITTER_DATE", "")
	land(t, a, "a")
	land(t, b, "b")
	land(t, c, "c")
	q, err := s.Order(BoardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(q.Order, ",") != "a,b,c" || len(q.Unruled) != 1 {
		t.Fatalf("by start time: %+v", q)
	}
	r, _ := s.Ask("a", []string{"x.go"}, "order?", nil, "")
	if _, err := s.Rule(r.ID, "b", 0, "b first", "", "", []string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.Order(BoardOptions{})
	if strings.Join(q.Order, ",") != "b,a,c" || len(q.Unruled) != 0 || len(q.Rulings) != 1 {
		t.Fatalf("ruled order: %+v", q)
	}
	if _, err := s.Decide("op", []string{"y.go"}, "cycle", "", "", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if q, _ = s.Order(BoardOptions{}); q.Conflict == "" {
		t.Fatalf("cycle not reported: %+v", q)
	}
	if _, err := s.Consolidate(a, "", BoardOptions{}); err == nil || !strings.Contains(err.Error(), "order_cycle") {
		t.Fatalf("consolidate ran over a contradictory ledger: %v", err)
	}
}

func TestVerifyReceipts(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	good := branch(t, main, "good", "a.go")
	land(t, good, "good")
	bad := branch(t, main, "bad", "b.go")
	write(t, bad, "FAIL", "1")
	must(t, bad, "add", "-A")
	must(t, bad, "commit", "-q", "-m", "marker")
	land(t, bad, "bad")
	// The verification command fails when a FAIL file exists at the head.
	b, _, err := s.WatchOnce(WatchOptions{Verify: "git ls-files --error-unmatch FAIL-absent"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range b.Rows {
		if r.State != "red" || r.Receipt == nil || r.Receipt.Pass {
			t.Fatalf("%s: state %s receipt %+v", r.Branch, r.State, r.Receipt)
		}
	}
	b, _, _ = s.WatchOnce(WatchOptions{Verify: "git ls-files --error-unmatch a.go"})
	// Receipts are per tip, so the first command's verdict sticks.
	if got := rowOf(t, b, "good").State; got != "red" {
		t.Fatalf("receipt not reused: %s", got)
	}
	if n, _ := s.Inbox("bad", false); len(n) == 0 {
		t.Fatal("red branch not nudged")
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), "swarm-verify-"+short(rowOf(t, b, "bad").Tip))); err == nil {
		t.Fatal("verify worktree left behind")
	}
}

func TestLoadMeanCountsClaims(t *testing.T) {
	l := &SeatLoad{}
	l.ClaimAvgMin, l.ClaimMaxMin = accumulate(l, 2)
	l.ClaimAvgMin, l.ClaimMaxMin = accumulate(l, 4)
	if l.ClaimAvgMin != 3 || l.ClaimMaxMin != 4 {
		t.Fatalf("two claims before any ruling: avg %v max %v", l.ClaimAvgMin, l.ClaimMaxMin)
	}
}

func TestLoad(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	branch(t, main, "p", "a.go")
	branch(t, main, "q", "a.go")
	if err := s.Init(Tiers{Operator: []string{"op"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Ask("p", []string{"a.go"}, "?", nil, "")
	_, _ = s.Ask("p", []string{"z.go"}, "format?", nil, TierOperator)
	ls, err := s.Load(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	find := func(seat string) SeatLoad {
		for _, l := range ls {
			if l.Seat == seat {
				return l
			}
		}
		t.Fatalf("no load row for %s in %+v", seat, ls)
		return SeatLoad{}
	}
	if q := find("q"); q.OpenRequests != 1 || q.PendingNotes != 1 {
		t.Fatalf("q load = %+v", q)
	}
	if o := find("operator"); o.OpenRequests != 1 {
		t.Fatalf("operator load = %+v", o)
	}
	if _, err := s.Rule(r.ID, "q", 0, "p first", "", "", nil); err != nil {
		t.Fatal(err)
	}
	ls, _ = s.Load(time.Hour)
	if q := find("q"); q.OpenRequests != 0 || q.RuledInWindow != 1 {
		t.Fatalf("q after ruling = %+v", q)
	}
}

func TestConsolidationMergeIsInherited(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	p := branch(t, main, "p", "a.go")
	land(t, p, "p")
	q := branch(t, main, "q", "b.go")
	land(t, q, "q")
	theme := branch(t, main, "theme", "DEMO.md")
	must(t, theme, "merge", "-q", "--no-ff", "p", "-m", "merge p")
	must(t, theme, "merge", "-q", "--no-ff", "q", "-m", "merge q")
	land(t, theme, "theme")
	b, err := s.Board(BoardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tr := rowOf(t, b, "theme")
	if strings.Join(tr.Files, ",") != "DEMO.md,briefs/out/theme/RESULT.json" {
		t.Fatalf("theme own files = %v", tr.Files)
	}
	if tr.State != "landed" || len(b.Contended) != 0 {
		t.Fatalf("theme state %s contended %+v", tr.State, b.Contended)
	}
	q2, _ := s.Order(BoardOptions{})
	if strings.Join(q2.Order, ",") != "p,q,theme" || len(q2.Unruled) != 0 {
		t.Fatalf("order = %+v", q2)
	}
}

func TestSplitQueuesChildren(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	write(t, main, "briefs/tasks.json", `[{"Branch":"t1","Title":"one","Card":"do one"}]`)
	kids := ParseChildren("t1b:key b|t1c:key c")
	d, err := s.Split("t1", main, kids, "scoped wrong")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ReadTasks(main)
	if err != nil || len(rows) != 3 || rows[1].Parent != "t1" || rows[2].Branch != "t1c" {
		t.Fatalf("tasks after split: %v %+v", err, rows)
	}
	if _, err := os.Stat(filepath.Join(main, "briefs", "tasks", "t1b.md")); err != nil {
		t.Fatal("child card not written")
	}
	if d.Scope[0] != "task:t1b" || !strings.Contains(d.Ruling, "t1b, t1c") {
		t.Fatalf("decision = %+v", d)
	}
	if n := mustInbox(t, s, "operator"); len(n) != 1 || n[0].Kind != "split" {
		t.Fatalf("operator not told: %+v", n)
	}
	if _, err := s.Split("t1", main, ParseChildren("t1b:again"), "dup"); err == nil {
		t.Fatal("duplicate child accepted")
	}
}

func TestIntentContendsBeforeAnyEdit(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	seat := func(name string) string {
		wt := filepath.Join(filepath.Dir(main), "wt", name)
		must(t, main, "worktree", "add", "-q", wt, "-b", name, "main")
		write(t, wt, "briefs/out/"+name+"/START.md", "start\n")
		must(t, wt, "add", "-A")
		must(t, wt, "commit", "-q", "-m", "start "+name)
		return wt
	}
	p, q := seat("p"), seat("q")
	if _, err := s.Intend(p, "p", []string{"pkg/x/x.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Intend(q, "q", []string{"pkg/x/x.go", "pkg/x/x_test.go"}); err != nil {
		t.Fatal(err)
	}
	b, err := s.Board(BoardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Contended) != 1 || b.Contended[0].File != "pkg/x/x.go" {
		t.Fatalf("intent did not contend: %+v", b.Contended)
	}
	// check must agree with the board: p sees q on the package from intent alone.
	if got := b.Touching("pkg/x", "p"); len(got) != 1 || !strings.HasPrefix(got[0], "q ") {
		t.Fatalf("Touching from intent = %v", got)
	}
	for _, c := range b.Contended {
		if strings.HasPrefix(c.File, "briefs/out/") {
			t.Fatalf("marker file contends: %+v", c)
		}
	}
	// q rebases onto p, then p moves on: p's old commits are still not q's.
	must(t, q, "rebase", "-q", "p")
	write(t, p, "later.go", "package a\n")
	must(t, p, "add", "-A")
	must(t, p, "commit", "-q", "-m", "p moves")
	b, _ = s.Board(BoardOptions{})
	for _, f := range rowOf(t, b, "q").Files {
		if strings.Contains(f, "/p/") {
			t.Fatalf("q owns p's marker after p moved: %v", rowOf(t, b, "q").Files)
		}
	}
}

func TestConsolidateMergesCleanAndListsConflicts(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	a := branch(t, main, "a", "a2.go")
	land(t, a, "a")
	bb := branch(t, main, "b", "shared.go")
	land(t, bb, "b")
	c := branch(t, main, "c", "shared.go")
	land(t, c, "c")
	theme := filepath.Join(filepath.Dir(main), "wt", "theme")
	must(t, main, "worktree", "add", "-q", theme, "-b", "theme", "main")
	res, err := s.Consolidate(theme, "git --version", BoardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Merged, ",") != "a,b" || strings.Join(res.Conflicted, ",") != "c" {
		t.Fatalf("consolidation = %+v", res)
	}
	if out := must(t, theme, "status", "--porcelain"); out != "" {
		t.Fatalf("conflict left the tree dirty: %q", out)
	}
	res, _ = s.Consolidate(theme, "", BoardOptions{})
	if len(res.Skipped) != 2 || len(res.Merged) != 0 {
		t.Fatalf("rerun = %+v", res)
	}
}
