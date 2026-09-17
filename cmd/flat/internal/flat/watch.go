package flat

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// WatchOptions is the lead's loop, as policy for a process instead of a
// prompt for a session.
type WatchOptions struct {
	Interval       time.Duration
	Base           string
	Fetch          bool
	Idle           time.Duration // silent after this much tip age
	UnclaimedAfter time.Duration // a request open this long is an alert
	Renudge        time.Duration // repeat a standing alert's nudge this often
	DiskMin        uint64
	Once           bool
	Out            io.Writer
	Wake           bool // resume seats that have notes and are between turns
	WakeOpts       WakeOptions
}

func (o *WatchOptions) defaults() {
	if o.Interval == 0 {
		o.Interval = 30 * time.Second
	}
	if o.Idle == 0 {
		o.Idle = 20 * time.Minute
	}
	if o.UnclaimedAfter == 0 {
		o.UnclaimedAfter = 10 * time.Minute
	}
	if o.Renudge == 0 {
		o.Renudge = 10 * time.Minute
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
}

// Alert is a standing condition the watcher tracks by key. It is raised
// once, renudged on a cadence, and cleared when the condition goes away.
type Alert struct {
	Key        string    `json:"key"`
	Kind       string    `json:"kind"`
	Subject    string    `json:"subject"`
	Seats      []string  `json:"seats,omitempty"` // who gets the nudge
	Text       string    `json:"text"`
	Since      time.Time `json:"since"`
	LastNudged time.Time `json:"last_nudged"`
}

type alertTransition struct {
	At     time.Time `json:"at"`
	Change string    `json:"change"` // raised | cleared | renudged
	Alert  Alert     `json:"alert"`
}

// alertSet collects the alerts one pass derives.
type alertSet struct {
	now     time.Time
	current map[string]*Alert
}

func (as *alertSet) add(a Alert) {
	a.Since = as.now
	as.current[a.Key] = &a
}

// Watch loops until the process ends, or once.
func (s *State) Watch(opts WatchOptions) error {
	opts.defaults()
	for {
		b, alerts, err := s.WatchOnce(opts)
		if err != nil {
			fmt.Fprintf(opts.Out, "watch: %v\n", err)
		} else {
			fmt.Fprintf(opts.Out, "%s rows=%d alerts=%d open=%d\n", b.At.Format("15:04:05"), len(b.Rows), len(alerts), b.Requests.Open)
		}
		if opts.Once {
			opts.WakeOpts.defaults()
			if left := WaitWakes(opts.WakeOpts.Wall + time.Minute); len(left) > 0 {
				fmt.Fprintf(opts.Out, "watch: wakes still running past their wall: %s\n", strings.Join(left, ", "))
			}
			return err
		}
		time.Sleep(opts.Interval)
	}
}

// WatchOnce derives the board, computes alerts, applies transitions, nudges
// seats, wakes seats that can take a turn, and writes the renders under
// watch/.
func (s *State) WatchOnce(opts WatchOptions) (*Board, []Alert, error) {
	opts.defaults()
	b, err := s.Board(BoardOptions{Base: opts.Base, Fetch: opts.Fetch, Idle: opts.Idle})
	if err != nil {
		return nil, nil, err
	}
	as := &alertSet{now: b.At, current: map[string]*Alert{}}
	rowAlerts(b, as)
	reqs, err := s.Requests(true)
	if err != nil {
		return nil, nil, err
	}
	var t Tiers
	_ = readJSON(s.path("tiers.json"), &t)
	s.requestAlerts(reqs, b, t, opts, as)
	resourceAlerts(b, as)
	if opts.DiskMin > 0 {
		if free, err := diskFree(s.Repo); err == nil && free < opts.DiskMin {
			as.add(Alert{Key: "disk:low", Kind: "disk_low", Subject: s.Repo, Seats: []string{"operator"},
				Text: fmt.Sprintf("disk under the repository is at %s free, floor %s. admission is refusing new builders.", human(free), human(opts.DiskMin))})
		}
	}
	if err := s.applyTransitions(as, opts); err != nil {
		return nil, nil, err
	}
	if opts.Wake {
		for _, seat := range s.WakePending(opts.WakeOpts) {
			fmt.Fprintf(opts.Out, "waking %s\n", seat)
		}
	}
	alerts := make([]Alert, 0, len(as.current))
	for _, a := range as.current {
		alerts = append(alerts, *a)
	}
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].Key < alerts[j].Key })
	_ = writeAtomic(s.path("watch", "board.jsonl"), []byte(b.JSONL()))
	_ = writeAtomic(s.path("watch", "board.md"), []byte(b.Markdown()))
	_ = writeAtomic(s.path("watch", "phone.txt"), []byte(b.Phone()))
	_ = writeAtomic(s.path("watch", "digest.md"), []byte(s.Digest(b, alerts)))
	return b, alerts, nil
}

func rowAlerts(b *Board, as *alertSet) {
	for _, r := range b.Rows {
		switch r.State {
		case "silent":
			as.add(Alert{Key: "silent:" + r.Branch, Kind: "builder_silent", Subject: r.Branch, Seats: []string{r.Branch},
				Text: fmt.Sprintf("your branch %s has not moved for %s. push WIP now, or write RESULT.json if you are done, or `flat ask` if you are blocked.", r.Branch, ageString(r.AgeSeconds))})
		case "pin_violation":
			as.add(Alert{Key: "pin:" + r.Branch, Kind: "pin_violation", Subject: r.Branch, Seats: []string{r.Branch},
				Text: fmt.Sprintf("branch %s changed after RESULT.json (%s). a landing is a commit whose RESULT.json pins its own head; either revert those commits or rewrite RESULT.json with the new head_sha as the last commit.", r.Branch, strings.Join(r.Extra, ", "))})
		case "pin_invalid":
			as.add(Alert{Key: "pininvalid:" + r.Branch, Kind: "pin_invalid", Subject: r.Branch, Seats: []string{r.Branch},
				Text: fmt.Sprintf("branch %s has a RESULT.json whose head_sha is missing or not on the branch. fix head_sha to the commit before the RESULT commit.", r.Branch)})
		}
	}
	for _, c := range b.Contended {
		if c.Ruled {
			continue
		}
		as.add(Alert{Key: "overlap:" + c.File, Kind: "overlap_unruled", Subject: c.File, Seats: c.Branches,
			Text: fmt.Sprintf("%s is changed by %s with no ruling on order. one of you: `flat ask --scope %s --question 'which lands first?' --options '<a>|<b>'`, then `flat wait`.", c.File, strings.Join(c.Branches, " and "), c.File)})
	}
}

func (s *State) requestAlerts(reqs []Request, b *Board, t Tiers, opts WatchOptions, as *alertSet) {
	var working []string
	for _, r := range b.Rows {
		if r.State == "working" {
			working = append(working, r.Branch)
		}
	}
	for _, r := range reqs {
		age := as.now.Sub(r.At)
		if r.Status == "claimed" && r.Claim != nil && as.now.After(r.Claim.Until) {
			as.add(Alert{Key: "staleclaim:" + r.ID, Kind: "claim_stale", Subject: r.ID, Seats: []string{r.Claim.Holder},
				Text: fmt.Sprintf("your claim on %s expired at %s without a ruling. rule it (`flat rule %s ...`) or let it go.", r.ID, r.Claim.Until.Format("15:04"), r.ID)})
		}
		if r.Status != "open" || age <= opts.UnclaimedAfter {
			continue
		}
		as.add(Alert{Key: "unclaimed:" + r.ID, Kind: "request_unclaimed", Subject: r.ID, Seats: audience(r, t, working),
			Text: fmt.Sprintf("request %s from %s has waited %s (needs %s): %q on %s. if it is in your reach: `flat rule %s --ruling '...' --evidence '...'`; if it needs product intent: `flat escalate %s --to operator --why '...'`.", r.ID, r.From, ageString(int64(age.Seconds())), r.Needs, r.Question, strings.Join(r.Scope, ","), r.ID, r.ID)})
	}
}

// audience is who hears about a request nobody claimed: the operator, the
// leads, or every working seat but the asker.
func audience(r Request, t Tiers, working []string) []string {
	switch r.Needs {
	case TierOperator:
		return []string{"operator"}
	case TierLead:
		return t.Lead
	}
	var seats []string
	for _, w := range working {
		if w != r.From {
			seats = append(seats, w)
		}
	}
	return seats
}

func resourceAlerts(b *Board, as *alertSet) {
	for _, res := range b.Resources {
		if as.now.After(res.Until) {
			as.add(Alert{Key: "resource:" + res.Name, Kind: "resource_expired", Subject: res.Name, Seats: []string{res.Holder},
				Text: fmt.Sprintf("your lease on %s expired at %s. `flat take %s` to renew or `flat drop %s`.", res.Name, res.Until.Format("15:04"), res.Name, res.Name)})
		}
	}
}

// applyTransitions compares this pass with the last, nudges on raise and on
// the renudge cadence, logs raised/cleared/renudged, and saves the set.
func (s *State) applyTransitions(as *alertSet, opts WatchOptions) error {
	prev := map[string]*Alert{}
	_ = readJSON(s.path("watch", "alerts.json"), &prev)
	log := s.path("watch", "alerts.jsonl")
	for key, a := range as.current {
		p, seen := prev[key]
		if !seen {
			s.deliver(a, as.now)
			_ = appendLine(log, alertTransition{At: as.now, Change: "raised", Alert: *a})
			continue
		}
		a.Since, a.LastNudged = p.Since, p.LastNudged
		if as.now.Sub(a.LastNudged) >= opts.Renudge {
			s.deliver(a, as.now)
			_ = appendLine(log, alertTransition{At: as.now, Change: "renudged", Alert: *a})
		}
	}
	for key, p := range prev {
		if _, ok := as.current[key]; !ok {
			_ = appendLine(log, alertTransition{At: as.now, Change: "cleared", Alert: *p})
		}
	}
	return writeJSON(s.path("watch", "alerts.json"), as.current)
}

func (s *State) deliver(a *Alert, now time.Time) {
	a.LastNudged = now
	for _, seat := range a.Seats {
		_, _ = s.Nudge(seat, a.Kind, a.Text)
	}
}

// Digest is the coalesced report: twenty landings become one table, and
// every standing alert appears once under its kind.
func (s *State) Digest(b *Board, alerts []Alert) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Digest · %s\n\n", b.At.Format("2006-01-02 15:04 MST"))
	digestRows(b, &sb)
	digestAlerts(b.At, alerts, &sb)
	s.digestRequests(b.At, &sb)
	s.digestRulings(b.At, &sb)
	digestResources(b, &sb)
	return sb.String()
}

func digestRows(b *Board, sb *strings.Builder) {
	byState := map[string][]Row{}
	for _, r := range b.Rows {
		byState[r.State] = append(byState[r.State], r)
	}
	for _, st := range []string{"landed", "working", "silent", "blocked", "pin_violation", "pin_invalid"} {
		rows := byState[st]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(sb, "## %s (%d)\n\n", st, len(rows))
		for _, r := range rows {
			fmt.Fprintf(sb, "- %s · %s · %d files%s\n", r.Branch, ageString(r.AgeSeconds), len(r.Files), rowNote(r))
		}
		sb.WriteString("\n")
	}
}

func rowNote(r Row) string {
	switch r.State {
	case "pin_violation":
		return " · after RESULT: " + strings.Join(r.Extra, ", ")
	case "blocked":
		return " · on " + r.Result.Blocked
	case "landed":
		return " · " + short(r.Tip)
	}
	return ""
}

func digestAlerts(now time.Time, alerts []Alert, sb *strings.Builder) {
	if len(alerts) == 0 {
		return
	}
	byKind := map[string][]Alert{}
	for _, a := range alerts {
		byKind[a.Kind] = append(byKind[a.Kind], a)
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Fprintf(sb, "## alerts (%d)\n\n", len(alerts))
	for _, k := range kinds {
		for _, a := range byKind[k] {
			fmt.Fprintf(sb, "- %s · %s · since %s\n", k, a.Subject, ageString(int64(now.Sub(a.Since).Seconds())))
		}
	}
	sb.WriteString("\n")
}

func (s *State) digestRequests(now time.Time, sb *strings.Builder) {
	reqs, _ := s.Requests(true)
	if len(reqs) == 0 {
		return
	}
	fmt.Fprintf(sb, "## requests open (%d)\n\n| id | from | needs | age | status | question |\n|---|---|---|---|---|---|\n", len(reqs))
	for _, r := range reqs {
		fmt.Fprintf(sb, "| %s | %s | %s | %s | %s | %s |\n", r.ID, r.From, r.Needs, ageString(int64(now.Sub(r.At).Seconds())), r.Status, r.Question)
	}
	sb.WriteString("\n")
}

func (s *State) digestRulings(now time.Time, sb *strings.Builder) {
	all, err := s.Decisions()
	if err != nil {
		return
	}
	var recent []Decision
	for _, d := range all {
		if now.Sub(d.At) <= time.Hour {
			recent = append(recent, d)
		}
	}
	if len(recent) == 0 {
		return
	}
	fmt.Fprintf(sb, "## rulings in the last hour (%d)\n\n", len(recent))
	for _, d := range recent {
		fmt.Fprintf(sb, "- %s · %s (%s) on %s: %s\n", d.At.Format("15:04"), d.By, d.Tier, strings.Join(d.Scope, ","), d.Ruling)
	}
	sb.WriteString("\n")
}

func digestResources(b *Board, sb *strings.Builder) {
	if len(b.Resources) == 0 {
		return
	}
	sb.WriteString("## resources\n\n")
	for _, r := range b.Resources {
		state := "held"
		if b.At.After(r.Until) {
			state = "EXPIRED"
		}
		fmt.Fprintf(sb, "- %s · %s by %s until %s\n", r.Name, state, r.Holder, r.Until.Format("15:04"))
	}
}

// ReadDigest returns the last digest the watcher wrote.
func (s *State) ReadDigest() (string, error) {
	data, err := os.ReadFile(s.path("watch", "digest.md"))
	return string(data), err
}
