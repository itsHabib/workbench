package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/cmd/flare/internal/journal"
	"github.com/itsHabib/workbench/cmd/flare/internal/preflight"
	"github.com/itsHabib/workbench/cmd/flare/internal/route"
	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

// `flare digest` is the standing answer to a question the per-event pages ask
// one at a time: across every repo, what is waiting on the operator's
// authority?
//
// A grant is the one thing an agent cannot mint for itself, and the operator
// cannot mint from a phone. So the useful moment to learn that a grant lapses
// in two hours — or that a repo has parked work and no grant at all — is while
// they are still at a keyboard, not after the work has stalled. Each individual
// refusal already pages (kind `grant-needed`); this rolls the standing pressure
// into one card with the exact mint beside each repo.
//
// It runs on demand or on a timer. It writes nothing anywhere but Slack and
// flare's own journal, and it decides nothing — every row is a reduction of
// artifacts gate already wrote.

// digestSource is the journal source the digest records under. It is FLARE's
// own name, not a watched source's: the digest reduces every gate source at
// once, so naming one of them would make the dedupe key depend on config
// ORDER — reorder the sources and an unchanged picture re-pages. Uniqueness
// comes from the content hash in the id; the source only scopes it.
const digestSource = "flare"

// digest builds and delivers the authority card. Exit 0 when there was nothing
// to say or it was delivered, 1 on a read or delivery failure, 3 when another
// flare holds the lock (it journals, so it takes the same single-instance lock
// as watch and sweep).
func digest(cfg config.Config, j *journal.Journal, co courier, within time.Duration) int {
	release, err := j.LockWatch()
	if err != nil {
		fmt.Fprintf(os.Stderr, "flare digest: %v\n", err)
		return 3
	}
	defer release()

	// One instant for the whole digest: liveness, the expiry window and the
	// dedupe read must all be answered as of the same moment, or a row can be
	// live for one question and lapsed for the next.
	now := time.Now()
	rows, err := authorityRows(cfg, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ev, ok := digestEvent(rows, now, within)
	if !ok {
		fmt.Println("flare digest: no authority pressure — nothing parked without a grant, nothing expiring soon")
		return 0
	}
	replayed, err := j.Load(now)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// An unchanged digest is not news. Its id is a hash of its own content, so
	// dedupe suppresses a repeat of the identical picture and passes the moment
	// anything in it moves.
	if replayed.Seen[journal.SeenKey(ev.Source, ev.ID)] {
		fmt.Println("flare digest: unchanged since the last delivered digest")
		return 0
	}
	rn := runner{cfg: cfg, j: j, r: route.New(cfg, time.Now), co: co}
	if !dispatch(rn, ev, &cycleState{cards: map[string]journal.Card{}, reasons: map[string]int{}}) {
		return 1
	}
	return 0
}

// authorityRow pairs a source's reduction with the state dir its mint command
// must pin.
type authorityRow struct {
	source.Authority
	state string
}

// authorityRows reduces every gate-log source. Ship receipts carry no grants,
// so they contribute nothing here.
func authorityRows(cfg config.Config, now time.Time) ([]authorityRow, error) {
	var rows []authorityRow
	for _, src := range cfg.Sources {
		if src.Kind != config.SourceGateLog {
			continue
		}
		found, err := source.Authorities(src, now)
		if err != nil {
			return nil, err
		}
		for _, a := range found {
			rows = append(rows, authorityRow{Authority: a, state: filepath.Dir(src.Path)})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Repo < rows[j].Repo })
	return rows, nil
}

// digestEvent renders the rows that need the operator into one card, or reports
// that none do. Only two situations qualify, and both are things only a mint
// fixes: parked work with NO live grant (a hard stop), and a live grant about
// to lapse under parked work (a stop that is coming). A repo running fine is
// left out — a digest that lists everything is another wall of text to skim.
func digestEvent(rows []authorityRow, now time.Time, within time.Duration) (event.Event, bool) {
	var blocked, expiring []string
	for _, r := range rows {
		line, urgent, ok := digestLine(r, now, within)
		if !ok {
			continue
		}
		if urgent {
			blocked = append(blocked, line)
			continue
		}
		expiring = append(expiring, line)
	}
	if len(blocked)+len(expiring) == 0 {
		return event.Event{}, false
	}
	detail := digestDetail(blocked, expiring)
	sev := event.SevInfo
	if len(blocked) > 0 {
		sev = event.SevEscalate
	}
	return event.Event{
		Source:   digestSource,
		ID:       fmt.Sprintf("authority-digest:%08x", hash32(detail)),
		Kind:     event.KindAuthorityDigest,
		Time:     now,
		Severity: sev,
		Title:    digestTitle(len(blocked), len(expiring)),
		Body:     digestTitle(len(blocked), len(expiring)),
		Fields:   map[string]string{"detail": detail},
	}, true
}

// digestLine renders one repo, and reports whether it is a hard stop. A repo
// with no parked work and no live grant is not pressure — nothing is waiting.
func digestLine(r authorityRow, now time.Time, within time.Duration) (string, bool, bool) {
	if r.Parked == 0 {
		return "", false, false
	}
	mint := "```" + preflight.Mint(r.Repo, r.state, r.ProposedTier, r.ProposedCycles) + "```"
	if !r.Live {
		return fmt.Sprintf("*%s* — %s parked, *no live grant*\n%s", r.Repo, plural(r.Parked, "PR"), mint), true, true
	}
	left := r.Grant.ExpiresAt.Sub(now)
	if left > within {
		return "", false, false
	}
	return fmt.Sprintf("*%s* — %s parked, grant %s expires in %s\n%s",
		r.Repo, plural(r.Parked, "PR"), r.Grant.MaxTier, roundDuration(left), mint), false, true
}

func digestDetail(blocked, expiring []string) string {
	var sections []string
	if len(blocked) > 0 {
		sections = append(sections, "*Stopped — nothing can proceed until you mint*\n"+strings.Join(blocked, "\n"))
	}
	if len(expiring) > 0 {
		sections = append(sections, "*Expiring soon, with work behind it*\n"+strings.Join(expiring, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

func digestTitle(blocked, expiring int) string {
	if blocked > 0 {
		return fmt.Sprintf("%s stopped for want of a grant; %d expiring soon", plural(blocked, "repo"), expiring)
	}
	return fmt.Sprintf("%s with parked work and a grant expiring soon", plural(expiring, "repo"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// roundDuration renders a remaining window the way a human reads a deadline:
// hours when there are hours left, minutes when it is nearly gone.
func roundDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func hash32(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}
