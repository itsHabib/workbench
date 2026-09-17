package flat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Tiers order who may rule on what. There is no tree: a total order is enough
// to break every tie. Names map to tiers in tiers.json; anyone unnamed is a
// peer.
const (
	TierPeer     = "peer"
	TierLead     = "lead"
	TierOperator = "operator"
)

var tierLevel = map[string]int{TierPeer: 1, "builder": 1, TierLead: 2, "verifier": 2, TierOperator: 3}

// Level returns the rank of a tier name; unknown names rank as peer.
func Level(tier string) int {
	if l, ok := tierLevel[tier]; ok {
		return l
	}
	return 1
}

// Tiers is the operator-written map from names to tiers.
type Tiers struct {
	Operator []string `json:"operator"`
	Lead     []string `json:"lead,omitempty"`
	Verifier []string `json:"verifier,omitempty"`
}

// TierOf resolves a name to its tier. FLAT_TIER overrides for the caller
// only when it names a tier the map grants that name; it never elevates.
func (s *State) TierOf(name string) string {
	var t Tiers
	_ = readJSON(s.path("tiers.json"), &t)
	has := func(list []string) bool {
		for _, n := range list {
			if n == name {
				return true
			}
		}
		return false
	}
	switch {
	case has(t.Operator):
		return TierOperator
	case has(t.Lead):
		return TierLead
	case has(t.Verifier):
		return "verifier"
	}
	return TierPeer
}

// Init writes tiers.json.
func (s *State) Init(t Tiers) error {
	if len(t.Operator) == 0 {
		return errors.New("init needs at least one --operator name")
	}
	return writeJSON(s.path("tiers.json"), t)
}

// Claim is a lease on a request. Epoch fences: a ruling must carry the epoch
// it claimed under, so a holder that lost the claim cannot rule late.
type Claim struct {
	Holder string    `json:"holder"`
	Epoch  int       `json:"epoch"`
	At     time.Time `json:"at"`
	Until  time.Time `json:"until"`
}

// Escalation records a raise of the tier a request needs.
type Escalation struct {
	At   time.Time `json:"at"`
	By   string    `json:"by"`
	From string    `json:"from"`
	To   string    `json:"to"`
	Why  string    `json:"why"`
}

// Request is a decision a builder needs before it can proceed.
type Request struct {
	ID          string       `json:"id"`
	At          time.Time    `json:"at"`
	From        string       `json:"from"` // seat or branch, never a session id or title
	Scope       []string     `json:"scope"`
	Question    string       `json:"question"`
	Options     []string     `json:"options,omitempty"`
	Needs       string       `json:"needs"`  // tier required to rule
	Status      string       `json:"status"` // open | claimed | ruled
	Claim       *Claim       `json:"claim,omitempty"`
	Epoch       int          `json:"epoch"` // last epoch handed out
	Decision    string       `json:"decision,omitempty"`
	RuledAt     time.Time    `json:"ruled_at,omitempty"`
	Escalations []Escalation `json:"escalations,omitempty"`
}

// Decision is one ruling in the hash-chained ledger.
type Decision struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Request    string    `json:"request,omitempty"`
	Scope      []string  `json:"scope"`
	Ruling     string    `json:"ruling"`
	By         string    `json:"by"`
	Tier       string    `json:"tier"`
	Evidence   string    `json:"evidence,omitempty"`
	Supersedes string    `json:"supersedes,omitempty"`
	Prev       string    `json:"prev"`
	Hash       string    `json:"hash"`
}

// Refusal is a typed reason the substrate said no.
type Refusal struct {
	Code string
	Msg  string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Msg }

func refuse(code, format string, args ...any) error {
	return &Refusal{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func (s *State) requestPath(id string) string { return s.path("requests", id+".json") }

func (s *State) loadRequest(id string) (*Request, error) {
	var r Request
	if err := readJSON(s.requestPath(id), &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, refuse("no_such_request", "%s", id)
		}
		return nil, err
	}
	return &r, nil
}

func (s *State) saveRequest(r *Request) error { return writeJSON(s.requestPath(r.ID), r) }

// Ask files a request. needs defaults to peer.
func (s *State) Ask(from string, scope []string, question string, options []string, needs string) (*Request, error) {
	if from == "" {
		return nil, refuse("no_from", "who is asking? pass --as <seat> or set FLAT_SEAT")
	}
	if len(scope) == 0 || question == "" {
		return nil, refuse("bad_request", "a request needs --scope and --question")
	}
	if needs == "" {
		needs = TierPeer
	}
	if _, ok := tierLevel[needs]; !ok {
		return nil, refuse("bad_tier", "needs must be one of peer, lead, operator")
	}
	r := &Request{ID: NewID("req"), At: Now(), From: from, Scope: scope, Question: question, Options: options, Needs: needs, Status: "open"}
	if err := s.saveRequest(r); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "ask", Request: r.ID, By: from, Seat: from, Tier: needs, Detail: strings.Join(scope, ",")})
	s.route(r)
	return r, nil
}

// route fans a new request out to the seats most likely to hold the
// context for it: no leader picks; the index picks. Peer requests go to
// seats with affinity on the scope; lead and operator requests go to the
// names the tiers map grants. The watcher widens to every working seat if
// nobody claims within its threshold.
func (s *State) route(r *Request) {
	var seats []string
	switch r.Needs {
	case TierOperator:
		seats = []string{"operator"}
	case TierLead:
		var t Tiers
		_ = readJSON(s.path("tiers.json"), &t)
		seats = t.Lead
	default:
		for _, w := range s.Affinity(r.Scope, r.From) {
			seats = append(seats, w.Seat)
		}
	}
	text := fmt.Sprintf("request %s from %s on %s needs %s: %q. rule it if the evidence is yours: `flat rule %s --ruling '...' --evidence '...'`; escalate if it needs intent: `flat escalate %s --to operator --why '...'`.", r.ID, r.From, strings.Join(r.Scope, ","), r.Needs, r.Question, r.ID, r.ID)
	for _, seat := range seats {
		_, _ = s.Nudge(seat, "request", text)
	}
	s.appendEvent(Event{Kind: "route", Request: r.ID, Seat: r.From, Detail: strings.Join(seats, ",")})
}

// Who is one seat's claim to context on a scope.
type Who struct {
	Seat string   `json:"seat"`
	Why  []string `json:"why"`
}

// Affinity lists seats with context on scope, best first: branches whose
// files overlap it, seats that ruled on it before, holders of a resource in
// it. exclude drops the asker. This is the index; git and the ledger are
// its source, nobody registers.
func (s *State) Affinity(scope []string, exclude string) []Who {
	by := map[string]*Who{}
	var order []string
	add := func(seat, why string) {
		if seat == "" || seat == exclude {
			return
		}
		w := by[seat]
		if w == nil {
			w = &Who{Seat: seat}
			by[seat] = w
			order = append(order, seat)
		}
		w.Why = append(w.Why, why)
	}
	if b, err := s.Board(BoardOptions{}); err == nil {
		for _, row := range b.Rows {
			if row.State == "landed" || row.State == "silent" {
				continue
			}
			var hits []string
			for _, f := range row.Files {
				if isResultFile(f) || !scopesOverlap([]string{f}, scope) {
					continue
				}
				hits = append(hits, f)
			}
			if len(hits) > 0 {
				add(row.Branch, "changes "+strings.Join(hits, ","))
			}
		}
	}
	if ds, err := s.Lookup(scope); err == nil {
		for _, d := range ds {
			add(d.By, "ruled "+d.ID)
		}
	}
	for _, sc := range scope {
		if strings.HasPrefix(sc, "resource:") {
			add(s.Holder(strings.TrimPrefix(sc, "resource:")), "holds "+sc)
		}
	}
	out := make([]Who, 0, len(order))
	for _, seat := range order {
		out = append(out, *by[seat])
	}
	return out
}

// Requests lists requests, oldest first. open=true hides ruled ones.
func (s *State) Requests(openOnly bool) ([]Request, error) {
	entries, err := os.ReadDir(s.path("requests"))
	if err != nil {
		return nil, err
	}
	var out []Request
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var r Request
		if err := readJSON(filepath.Join(s.path("requests"), e.Name()), &r); err != nil {
			continue
		}
		if openOnly && r.Status == "ruled" {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// RequestSummary is the board's view of the queue.
type RequestSummary struct {
	Open              int            `json:"open"`
	Needs             map[string]int `json:"needs"`
	OldestOpenSeconds int64          `json:"oldest_open_s"`
	OldestOpen        string         `json:"oldest_open,omitempty"`
}

func (rs RequestSummary) byNeeds() string {
	var parts []string
	for _, t := range []string{TierPeer, TierLead, TierOperator} {
		if n := rs.Needs[t]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, t))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func (s *State) summarizeRequests() (RequestSummary, error) {
	rs := RequestSummary{Needs: map[string]int{}}
	reqs, err := s.Requests(true)
	if err != nil {
		return rs, err
	}
	now := Now()
	for _, r := range reqs {
		rs.Open++
		rs.Needs[r.Needs]++
		if age := int64(now.Sub(r.At).Seconds()); age > rs.OldestOpenSeconds {
			rs.OldestOpenSeconds, rs.OldestOpen = age, r.ID
		}
	}
	return rs, nil
}

// ClaimRequest leases a request to holder for ttl. A live claim by another
// holder refuses; an expired one is taken over with a new epoch.
func (s *State) ClaimRequest(id, holder string, ttl time.Duration) (*Request, error) {
	release, err := s.lock("requests", 5*time.Second, time.Minute)
	if err != nil {
		return nil, err
	}
	defer release()
	r, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	if r.Status == "ruled" {
		return nil, refuse("already_ruled", "%s was ruled by %s", id, r.Decision)
	}
	if Level(s.TierOf(holder)) < Level(r.Needs) {
		return nil, refuse("tier_too_low", "%s needs %s; %s is %s. escalate or leave it", id, r.Needs, holder, s.TierOf(holder))
	}
	now := Now()
	if c := r.Claim; c != nil && c.Holder != holder && now.Before(c.Until) {
		return nil, refuse("claimed_by_other", "%s is claimed by %s until %s", id, c.Holder, c.Until.Format(time.RFC3339))
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	r.Epoch++
	r.Claim = &Claim{Holder: holder, Epoch: r.Epoch, At: now, Until: now.Add(ttl)}
	r.Status = "claimed"
	if err := s.saveRequest(r); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "claim", Request: id, By: holder, Epoch: r.Epoch})
	return r, nil
}

// Rule appends a decision for a request. epoch 0 means "claim now if free";
// otherwise it must match the caller's live claim.
func (s *State) Rule(id, by string, epoch int, ruling, evidence, supersedes string) (*Decision, error) {
	if ruling == "" {
		return nil, refuse("bad_ruling", "a ruling needs --ruling text")
	}
	release, err := s.lock("requests", 5*time.Second, time.Minute)
	if err != nil {
		return nil, err
	}
	defer release()
	r, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	if r.Status == "ruled" {
		return nil, refuse("already_ruled", "%s was ruled by %s", id, r.Decision)
	}
	tier := s.TierOf(by)
	if Level(tier) < Level(r.Needs) {
		return nil, refuse("tier_too_low", "%s needs %s; %s is %s. use: flat escalate %s --to %s --why '...'", id, r.Needs, by, tier, id, r.Needs)
	}
	now := Now()
	switch c := r.Claim; {
	case c != nil && c.Holder != by && now.Before(c.Until):
		return nil, refuse("claimed_by_other", "%s is claimed by %s until %s", id, c.Holder, c.Until.Format(time.RFC3339))
	case c != nil && c.Holder == by && epoch != 0 && epoch != c.Epoch:
		return nil, refuse("claim_fenced", "your claim on %s is epoch %d, you passed %d; it changed hands", id, c.Epoch, epoch)
	case c != nil && c.Holder == by && epoch == 0 && now.After(c.Until):
		return nil, refuse("claim_expired", "your claim on %s expired at %s; claim again", id, c.Until.Format(time.RFC3339))
	case c == nil || c.Holder != by:
		r.Epoch++
		r.Claim = &Claim{Holder: by, Epoch: r.Epoch, At: now, Until: now.Add(time.Minute)}
		s.appendEvent(Event{Kind: "claim", Request: id, By: by, Epoch: r.Epoch, Detail: "implicit"})
	}

	// Tiebreak against an existing effective decision on the same scope.
	effective, err := s.Effective()
	if err != nil {
		return nil, err
	}
	for _, d := range effective {
		if !scopesOverlap(d.Scope, r.Scope) || d.ID == supersedes {
			continue
		}
		switch {
		case Level(d.Tier) > Level(tier):
			return nil, refuse("outranked", "%s already ruled on %s at tier %s; a %s cannot override it", d.ID, strings.Join(d.Scope, ","), d.Tier, tier)
		case Level(d.Tier) == Level(tier):
			return nil, refuse("must_supersede", "%s already ruled on %s; pass --supersedes %s and say why in --evidence", d.ID, strings.Join(d.Scope, ","), d.ID)
		default:
			supersedes = d.ID // a higher tier overrides silently, recorded
		}
	}

	d := &Decision{ID: NewID("dec"), At: now, Request: id, Scope: r.Scope, Ruling: ruling, By: by, Tier: tier, Evidence: evidence, Supersedes: supersedes}
	if err := s.appendDecision(d); err != nil {
		return nil, err
	}
	r.Status, r.Decision, r.RuledAt = "ruled", d.ID, now
	if err := s.saveRequest(r); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "rule", Request: id, By: by, Tier: tier, Epoch: r.Claim.Epoch, Detail: d.ID})
	s.notify(r.From, fmt.Sprintf("ruled %s by %s (%s): %s", id, by, tier, ruling))
	return d, nil
}

// Escalate raises the tier a request needs and releases any claim.
func (s *State) Escalate(id, by, to, why string) (*Request, error) {
	if _, ok := tierLevel[to]; !ok {
		return nil, refuse("bad_tier", "escalate --to must be lead or operator")
	}
	release, err := s.lock("requests", 5*time.Second, time.Minute)
	if err != nil {
		return nil, err
	}
	defer release()
	r, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	if r.Status == "ruled" {
		return nil, refuse("already_ruled", "%s was ruled by %s", id, r.Decision)
	}
	if Level(to) <= Level(r.Needs) {
		return nil, refuse("not_an_escalation", "%s already needs %s", id, r.Needs)
	}
	r.Escalations = append(r.Escalations, Escalation{At: Now(), By: by, From: r.Needs, To: to, Why: why})
	r.Needs = to
	r.Claim = nil
	r.Status = "open"
	if err := s.saveRequest(r); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "escalate", Request: id, By: by, Tier: to, Detail: why})
	return r, nil
}

// Decide records a ruling with no request, for a ruling made proactively
// (a lead or operator settling a scope before anyone asks).
func (s *State) Decide(by string, scope []string, ruling, evidence, supersedes string) (*Decision, error) {
	if len(scope) == 0 || ruling == "" {
		return nil, refuse("bad_ruling", "decide needs --scope and --ruling")
	}
	release, err := s.lock("requests", 5*time.Second, time.Minute)
	if err != nil {
		return nil, err
	}
	defer release()
	tier := s.TierOf(by)
	effective, err := s.Effective()
	if err != nil {
		return nil, err
	}
	for _, d := range effective {
		if !scopesOverlap(d.Scope, scope) || d.ID == supersedes {
			continue
		}
		switch {
		case Level(d.Tier) > Level(tier):
			return nil, refuse("outranked", "%s already ruled on %s at tier %s", d.ID, strings.Join(d.Scope, ","), d.Tier)
		case Level(d.Tier) == Level(tier):
			return nil, refuse("must_supersede", "%s already ruled on %s; pass --supersedes %s", d.ID, strings.Join(d.Scope, ","), d.ID)
		default:
			supersedes = d.ID
		}
	}
	d := &Decision{ID: NewID("dec"), At: Now(), Scope: scope, Ruling: ruling, By: by, Tier: tier, Evidence: evidence, Supersedes: supersedes}
	if err := s.appendDecision(d); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "decide", By: by, Tier: tier, Detail: d.ID})
	return d, nil
}

func (s *State) appendDecision(d *Decision) error {
	all, err := s.Decisions()
	if err != nil {
		return err
	}
	d.Prev = "genesis"
	if n := len(all); n > 0 {
		d.Prev = all[n-1].Hash
	}
	d.Hash = ""
	d.Hash = hashOf(d)
	return appendLine(s.path("decisions.jsonl"), d)
}

// Decisions reads the whole ledger and verifies the chain.
func (s *State) Decisions() ([]Decision, error) {
	var out []Decision
	err := readLines(s.path("decisions.jsonl"), func(line []byte) error {
		var d Decision
		if err := json.Unmarshal(line, &d); err != nil {
			return err
		}
		out = append(out, d)
		return nil
	})
	if err != nil {
		return nil, err
	}
	prev := "genesis"
	for i, d := range out {
		want := d.Hash
		d.Hash = ""
		if d.Prev != prev || hashOf(&d) != want {
			return nil, fmt.Errorf("decision ledger broken at line %d (%s)", i+1, want)
		}
		prev = want
	}
	return out, nil
}

// Effective returns decisions not superseded by a later one.
func (s *State) Effective() ([]Decision, error) {
	all, err := s.Decisions()
	if err != nil {
		return nil, err
	}
	dead := map[string]bool{}
	for _, d := range all {
		if d.Supersedes != "" {
			dead[d.Supersedes] = true
		}
	}
	var out []Decision
	for _, d := range all {
		if !dead[d.ID] {
			out = append(out, d)
		}
	}
	return out, nil
}

// Lookup returns effective decisions whose scope overlaps any of scope.
func (s *State) Lookup(scope []string) ([]Decision, error) {
	effective, err := s.Effective()
	if err != nil {
		return nil, err
	}
	var out []Decision
	for _, d := range effective {
		if scopesOverlap(d.Scope, scope) {
			out = append(out, d)
		}
	}
	return out, nil
}

func latestCovering(effective []Decision, file string) *Decision {
	var best *Decision
	for i := range effective {
		if scopesOverlap(effective[i].Scope, []string{file}) {
			best = &effective[i]
		}
	}
	return best
}

// scopesOverlap: equal entries, or one path a directory prefix of the other.
// resource:<name> entries only match exactly.
func scopesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if scopeMatch(x, y) {
				return true
			}
		}
	}
	return false
}

func scopeMatch(x, y string) bool {
	x, y = strings.TrimSuffix(x, "/"), strings.TrimSuffix(y, "/")
	if x == y {
		return true
	}
	if strings.HasPrefix(x, "resource:") || strings.HasPrefix(y, "resource:") {
		return false
	}
	return strings.HasPrefix(x, y+"/") || strings.HasPrefix(y, x+"/")
}

// Wait blocks until the request is ruled or the timeout passes.
func (s *State) Wait(id string, timeout, every time.Duration) (*Request, *Decision, error) {
	if every <= 0 {
		every = 2 * time.Second
	}
	deadline := Now().Add(timeout)
	for {
		r, err := s.loadRequest(id)
		if err != nil {
			return nil, nil, err
		}
		if r.Status == "ruled" {
			all, err := s.Decisions()
			if err != nil {
				return r, nil, err
			}
			for i := range all {
				if all[i].ID == r.Decision {
					return r, &all[i], nil
				}
			}
			return r, nil, nil
		}
		if Now().After(deadline) {
			return r, nil, refuse("timeout", "%s not ruled after %s (needs %s)", id, timeout, r.Needs)
		}
		time.Sleep(every)
	}
}
