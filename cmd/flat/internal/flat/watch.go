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
			WaitWakes()
			return err
		}
		time.Sleep(opts.Interval)
	}
}

// WatchOnce derives the board, computes alerts, applies transitions, nudges
// seats, and writes the renders under watch/.
func (s *State) WatchOnce(opts WatchOptions) (*Board, []Alert, error) {
	opts.defaults()
	b, err := s.Board(BoardOptions{Base: opts.Base, Fetch: opts.Fetch, Idle: opts.Idle})
	if err != nil {
		return nil, nil, err
	}
	now := b.At
	current := map[string]*Alert{}
	add := func(a Alert) { a.Since = now; current[a.Key] = &a }

	var working []string
	for _, r := range b.Rows {
		if r.State == "working" {
			working = append(working, r.Branch)
		}
	}
	for _, r := range b.Rows {
		switch r.State {
		case "silent":
			add(Alert{Key: "silent:" + r.Branch, Kind: "builder_silent", Subject: r.Branch, Seats: []string{r.Branch},
				Text: fmt.Sprintf("your branch %s has not moved for %s. push WIP now, or write RESULT.json if you are done, or `flat ask` if you are blocked.", r.Branch, ageString(r.AgeSeconds))})
		case "pin_violation":
			add(Alert{Key: "pin:" + r.Branch, Kind: "pin_violation", Subject: r.Branch, Seats: []string{r.Branch},
				Text: fmt.Sprintf("branch %s changed after RESULT.json (%s). a landing is a commit whose RESULT.json pins its own head; either revert those commits or rewrite RESULT.json with the new head_sha as the last commit.", r.Branch, strings.Join(r.Extra, ", "))})
		case "pin_invalid":
			add(Alert{Key: "pininvalid:" + r.Branch, Kind: "pin_invalid", Subject: r.Branch, Seats: []string{r.Branch},
				Text: fmt.Sprintf("branch %s has a RESULT.json whose head_sha is missing or not on the branch. fix head_sha to the commit before the RESULT commit.", r.Branch)})
		}
	}
	for _, c := range b.Contended {
		if c.Ruled {
			continue
		}
		add(Alert{Key: "overlap:" + c.File, Kind: "overlap_unruled", Subject: c.File, Seats: c.Branches,
			Text: fmt.Sprintf("%s is changed by %s with no ruling on order. one of you: `flat ask --scope %s --question 'which lands first?' --options '<a>|<b>'`, then `flat wait`.", c.File, strings.Join(c.Branches, " and "), c.File)})
	}
	reqs, err := s.Requests(true)
	if err != nil {
		return nil, nil, err
	}
	var t Tiers
	_ = readJSON(s.path("tiers.json"), &t)
	for _, r := range reqs {
		age := now.Sub(r.At)
		if r.Status == "claimed" && r.Claim != nil && now.After(r.Claim.Until) {
			add(Alert{Key: "staleclaim:" + r.ID, Kind: "claim_stale", Subject: r.ID, Seats: []string{r.Claim.Holder},
				Text: fmt.Sprintf("your claim on %s expired at %s without a ruling. rule it (`flat rule %s ...`) or let it go.", r.ID, r.Claim.Until.Format("15:04"), r.ID)})
		}
		if r.Status == "open" && age > opts.UnclaimedAfter {
			var seats []string
			switch r.Needs {
			case TierOperator:
				seats = append(seats, "operator")
			case TierLead:
				seats = append(seats, t.Lead...)
			default:
				for _, w := range working {
					if w != r.From {
						seats = append(seats, w)
					}
				}
			}
			add(Alert{Key: "unclaimed:" + r.ID, Kind: "request_unclaimed", Subject: r.ID, Seats: seats,
				Text: fmt.Sprintf("request %s from %s has waited %s (needs %s): %q on %s. if it is in your reach: `flat rule %s --ruling '...' --evidence '...'`; if it needs product intent: `flat escalate %s --to operator --why '...'`.", r.ID, r.From, ageString(int64(age.Seconds())), r.Needs, r.Question, strings.Join(r.Scope, ","), r.ID, r.ID)})
		}
	}
	for _, res := range b.Resources {
		if now.After(res.Until) {
			add(Alert{Key: "resource:" + res.Name, Kind: "resource_expired", Subject: res.Name, Seats: []string{res.Holder},
				Text: fmt.Sprintf("your lease on %s expired at %s. `flat take %s` to renew or `flat drop %s`.", res.Name, res.Until.Format("15:04"), res.Name, res.Name)})
		}
	}
	if opts.DiskMin > 0 {
		if free, err := diskFree(s.Repo); err == nil && free < opts.DiskMin {
			add(Alert{Key: "disk:low", Kind: "disk_low", Subject: s.Repo, Seats: []string{"operator"},
				Text: fmt.Sprintf("disk under the repository is at %s free, floor %s. admission is refusing new builders.", human(free), human(opts.DiskMin))})
		}
	}

	// Transitions against the previous pass.
	prev := map[string]*Alert{}
	_ = readJSON(s.path("watch", "alerts.json"), &prev)
	for key, a := range current {
		if p, ok := prev[key]; ok {
			a.Since = p.Since
			a.LastNudged = p.LastNudged
			if now.Sub(a.LastNudged) >= opts.Renudge {
				s.deliver(a, now)
				_ = appendLine(s.path("watch", "alerts.jsonl"), alertTransition{At: now, Change: "renudged", Alert: *a})
			}
			continue
		}
		s.deliver(a, now)
		_ = appendLine(s.path("watch", "alerts.jsonl"), alertTransition{At: now, Change: "raised", Alert: *a})
	}
	for key, p := range prev {
		if _, ok := current[key]; !ok {
			_ = appendLine(s.path("watch", "alerts.jsonl"), alertTransition{At: now, Change: "cleared", Alert: *p})
		}
	}
	if err := writeJSON(s.path("watch", "alerts.json"), current); err != nil {
		return nil, nil, err
	}
	if opts.Wake {
		for _, seat := range s.WakePending(opts.WakeOpts) {
			fmt.Fprintf(opts.Out, "waking %s\n", seat)
		}
	}

	alerts := make([]Alert, 0, len(current))
	for _, a := range current {
		alerts = append(alerts, *a)
	}
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].Key < alerts[j].Key })
	_ = writeAtomic(s.path("watch", "board.jsonl"), []byte(b.JSONL()))
	_ = writeAtomic(s.path("watch", "board.md"), []byte(b.Markdown()))
	_ = writeAtomic(s.path("watch", "phone.txt"), []byte(b.Phone()))
	_ = writeAtomic(s.path("watch", "digest.md"), []byte(s.Digest(b, alerts)))
	return b, alerts, nil
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
	byState := map[string][]Row{}
	for _, r := range b.Rows {
		byState[r.State] = append(byState[r.State], r)
	}
	for _, st := range []string{"landed", "working", "silent", "blocked", "pin_violation", "pin_invalid"} {
		rows := byState[st]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "## %s (%d)\n\n", st, len(rows))
		for _, r := range rows {
			extra := ""
			switch st {
			case "pin_violation":
				extra = " · after RESULT: " + strings.Join(r.Extra, ", ")
			case "blocked":
				extra = " · on " + r.Result.Blocked
			case "landed":
				extra = " · " + short(r.Tip)
			}
			fmt.Fprintf(&sb, "- %s · %s · %d files%s\n", r.Branch, ageString(r.AgeSeconds), len(r.Files), extra)
		}
		sb.WriteString("\n")
	}
	if len(alerts) > 0 {
		byKind := map[string][]Alert{}
		for _, a := range alerts {
			byKind[a.Kind] = append(byKind[a.Kind], a)
		}
		kinds := make([]string, 0, len(byKind))
		for k := range byKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		fmt.Fprintf(&sb, "## alerts (%d)\n\n", len(alerts))
		for _, k := range kinds {
			for _, a := range byKind[k] {
				fmt.Fprintf(&sb, "- %s · %s · since %s\n", k, a.Subject, ageString(int64(b.At.Sub(a.Since).Seconds())))
			}
		}
		sb.WriteString("\n")
	}
	reqs, _ := s.Requests(true)
	if len(reqs) > 0 {
		fmt.Fprintf(&sb, "## requests open (%d)\n\n| id | from | needs | age | status | question |\n|---|---|---|---|---|---|\n", len(reqs))
		for _, r := range reqs {
			fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n", r.ID, r.From, r.Needs, ageString(int64(b.At.Sub(r.At).Seconds())), r.Status, r.Question)
		}
		sb.WriteString("\n")
	}
	if all, err := s.Decisions(); err == nil {
		var recent []Decision
		for _, d := range all {
			if b.At.Sub(d.At) <= time.Hour {
				recent = append(recent, d)
			}
		}
		if len(recent) > 0 {
			fmt.Fprintf(&sb, "## rulings in the last hour (%d)\n\n", len(recent))
			for _, d := range recent {
				fmt.Fprintf(&sb, "- %s · %s (%s) on %s: %s\n", d.At.Format("15:04"), d.By, d.Tier, strings.Join(d.Scope, ","), d.Ruling)
			}
			sb.WriteString("\n")
		}
	}
	if len(b.Resources) > 0 {
		sb.WriteString("## resources\n\n")
		for _, r := range b.Resources {
			state := "held"
			if b.At.After(r.Until) {
				state = "EXPIRED"
			}
			fmt.Fprintf(&sb, "- %s · %s by %s until %s\n", r.Name, state, r.Holder, r.Until.Format("15:04"))
		}
	}
	return sb.String()
}

// ReadDigest returns the last digest the watcher wrote.
func (s *State) ReadDigest() (string, error) {
	data, err := os.ReadFile(s.path("watch", "digest.md"))
	return string(data), err
}
