// flare — the escalation/block routing sink (an Observability tool, not a
// plane). A pure sink: it tails the artifact logs producers emit and pushes a
// notification when something blocks or escalates. It never gates, never blocks,
// never writes into a producer — the inbound decision path is cmd/escalate, not
// flare (Amendment 3).
//
//	flare watch  [-config path] [-from-start]   poll loop (catch-up sweep first)
//	flare sweep  [-config path] [-from-start]   one catch-up pass, then exit
//	flare status [-config path]                 health as JSON; exit 1 when stale
//
// watch and sweep are single-instance: both take an exclusive lock on the state
// dir and exit 3 if another flare already holds it (two writers corrupt the
// journal + cursors). status is lock-free — it only reads.
//
// First run: a source with no cursor yet starts at the current tail of its log
// and journals that placement — a producer's history is not a page queue.
// -from-start opts in to delivering the whole history instead (dedupe still
// holds on later sweeps).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/cmd/flare/internal/journal"
	"github.com/itsHabib/workbench/cmd/flare/internal/notify"
	"github.com/itsHabib/workbench/cmd/flare/internal/route"
	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: flare watch|sweep|status [-config path] [-state dir]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	stateDir := fs.String("state", defaultStateDir(), "flare's own state dir (journal, cursors)")
	cfgPath := fs.String("config", "", "routes config (default <state>/routes.json)")
	fromStart := fs.Bool("from-start", false, "watch/sweep: a source with no cursor yet starts at offset 0 and delivers its whole history (default: start at the current tail)")
	fs.Parse(os.Args[2:])
	if *cfgPath == "" {
		*cfgPath = filepath.Join(*stateDir, "routes.json")
	}
	os.Exit(run(os.Args[1], *cfgPath, *stateDir, *fromStart))
}

func run(verb, cfgPath, stateDir string, fromStart bool) int {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	j, err := journal.Open(stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	switch verb {
	case "sweep":
		return sweep(cfg, j, fromStart)
	case "watch":
		return watch(cfg, j, fromStart)
	case "status":
		return status(cfg, j)
	}
	fmt.Fprintf(os.Stderr, "flare: unknown verb %q\n", verb)
	return 2
}

// courier is the delivery seam the cycle uses: post a card, and correct one
// already posted. It is a struct of two functions rather than an interface
// because the cycle needs exactly these two calls and nothing else — the
// narrowest input that does the job. liveCourier is the production wiring; a
// test substitutes its own to drive the orchestration without a live Slack.
type courier struct {
	send     func(config.Channel, event.Event) (notify.Ref, error)
	finalize func(config.Channel, notify.Ref, event.Event) error
}

func liveCourier() courier {
	return courier{send: notify.Send, finalize: notify.Finalize}
}

func watch(cfg config.Config, j *journal.Journal, fromStart bool) int {
	release, err := j.LockWatch()
	if err != nil {
		fmt.Fprintf(os.Stderr, "flare watch: %v\n", err)
		return 3
	}
	defer release()
	r := route.New(cfg, time.Now)
	for {
		if err := cycle(cfg, j, r, liveCourier(), fromStart); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		time.Sleep(time.Duration(cfg.PollSeconds) * time.Second)
	}
}

func sweep(cfg config.Config, j *journal.Journal, fromStart bool) int {
	release, err := j.LockWatch()
	if err != nil {
		fmt.Fprintf(os.Stderr, "flare sweep: %v\n", err)
		return 3
	}
	defer release()
	if err := cycle(cfg, j, route.New(cfg, time.Now), liveCourier(), fromStart); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// cycle is one poll: read every source from its cursor, route what's new,
// advance each cursor only when everything read from it settled (delivered,
// dropped, or throttled) — a failed delivery holds the cursor so the next
// cycle retries, and the journal's seen-set keeps retries from re-paging
// what already got through.
//
// A source with no cursor at all is placed first (see placeCursor): absent
// means flare has never looked at it, which is a different fact from a cursor
// deliberately reset to offset 0 (a resweep), and the two must not be confused
// — the first is fresh state, the second is recovery.
func cycle(cfg config.Config, j *journal.Journal, r *route.Router, co courier, fromStart bool) error {
	seen, err := j.Seen()
	if err != nil {
		return err
	}
	// The cards still standing in Slack, replayed from the journal so a restart
	// does not lose track of what the operator is looking at. It is mutated
	// through the cycle: a new park adds one, a terminal fact removes one.
	cards, err := j.LiveCards()
	if err != nil {
		return err
	}
	cur, err := recoverCorruptCursors(cfg, j, r, co)
	if err != nil {
		return err
	}
	var failed []string
	for _, src := range cfg.Sources {
		if err := placeCursor(j, src, cur, fromStart); err != nil {
			fmt.Fprintf(os.Stderr, "flare: %v\n", err)
			failed = append(failed, src.Name)
			continue
		}
		next, err := pollSource(cfg, j, r, co, src, cur.Sources[src.Name], seen, cards)
		if err != nil {
			fmt.Fprintf(os.Stderr, "flare: %v\n", err)
			failed = append(failed, src.Name)
			continue
		}
		cur.Sources[src.Name] = next
	}
	cur.LastPoll = time.Now()
	if err := j.SaveCursors(cur); err != nil {
		return err
	}
	// A source that failed to read is a swept-clean lie: surface it so `sweep`
	// exits non-zero (its CLI contract). The successful sources still advanced
	// and LastPoll still records liveness for the watch loop.
	if len(failed) > 0 {
		return fmt.Errorf("source(s) failed to poll: %s", strings.Join(failed, ", "))
	}
	return nil
}

// placeCursor gives a source that has no cursor yet its starting position, and
// journals that placement so the state explains itself. It is a no-op for a
// source that already has one (including a cursor deliberately reset to zero).
//
// The default is the current tail: a producer's log is history the operator
// already lived through, not a page queue, and a fresh flare that replays it
// pages every long-dead escalation (with live Approve/Block buttons) into
// Slack until it is rate-limited. -from-start opts in to the full history; the
// journal's seen-set then keeps any later sweep from re-paging it.
func placeCursor(j *journal.Journal, src config.Source, cur journal.Cursors, fromStart bool) error {
	if _, ok := cur.Sources[src.Name]; ok {
		return nil
	}
	start, note, err := firstCursor(src, fromStart)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "flare: %s: %s\n", src.Name, note)
	if err := j.Append(journal.Entry{Time: time.Now(), Kind: journal.CursorInit, Source: src.Name, Note: note}); err != nil {
		return err
	}
	cur.Sources[src.Name] = start
	// Persist the placement now, before this source is polled — not at the end
	// of the cycle. Left in memory, a crash or a failed end-of-cycle save would
	// make the next cycle see the source as unseen again and place it at a
	// NEWER tail, silently skipping everything appended in between (including
	// a delivery that errored and was owed a retry). Journal first, then save:
	// if the save fails the source retries next cycle and journals that
	// placement too, so the durable cursor always matches the last journaled
	// cursor-init.
	if err := j.SaveCursors(cur); err != nil {
		delete(cur.Sources, src.Name)
		return fmt.Errorf("source %s: persist first placement: %w", src.Name, err)
	}
	return nil
}

// firstCursor decides where a never-seen source starts and says why, in the
// words the journal keeps.
func firstCursor(src config.Source, fromStart bool) (source.Cursor, string, error) {
	if fromStart {
		return source.Cursor{}, "no cursor yet: -from-start, delivering the whole history from offset 0", nil
	}
	tail, err := source.Tail(src)
	if err != nil {
		return source.Cursor{}, "", err
	}
	note := fmt.Sprintf("no cursor yet: initialized at tail (offset %d", tail.Offset)
	if tail.LastHash != "" {
		note += fmt.Sprintf(", hash %.12s", tail.LastHash)
	}
	note += "); history before this point is not delivered — `flare sweep -from-start` pages it on purpose"
	return tail, note, nil
}

// recoverCorruptCursors self-heals a corrupt cursor file instead of wedging the
// loop. A corrupt cursors.json is *why* flare stopped delivering, so the
// recovery must be as loud as a source chain break: it PAGES the operator
// through the normal notify path (dispatch → catch-all → Slack), and only then
// quarantines the bad file and resweeps every configured source from offset 0
// (dedupe prevents re-paging). The reset is written as an explicit zero cursor
// per source, not an absent one: absent would read as fresh state and be
// placed at the tail, silently skipping whatever the corruption hid.
// If the page cannot be delivered, it holds the corrupt file and returns an
// error so the next cycle retries — the gap is never hidden behind a freshly
// healthy status, and a failed page never silently loses the alert.
func recoverCorruptCursors(cfg config.Config, j *journal.Journal, r *route.Router, co courier) (journal.Cursors, error) {
	cur, err := j.LoadCursors()
	if !errors.Is(err, journal.ErrCursorsCorrupt) {
		return cur, err
	}
	if !dispatch(cfg, j, r, co, corruptCursorsAlert(), map[string]journal.Card{}) {
		return journal.Cursors{}, fmt.Errorf("cursors.json corrupt: recovery alert undelivered, holding for retry")
	}
	if _, err := j.QuarantineCursors(); err != nil {
		return journal.Cursors{}, err
	}
	for _, src := range cfg.Sources {
		cur.Sources[src.Name] = source.Cursor{}
	}
	// Persist the zero cursors now, before the resweep runs: quarantine left no
	// cursors.json, and a crash mid-resweep would otherwise restart into fresh
	// state — tail placement — skipping exactly the records this recovery owes.
	// The remaining window is the rename→save gap between two syscalls, not the
	// whole resweep.
	if err := j.SaveCursors(cur); err != nil {
		return journal.Cursors{}, fmt.Errorf("cursors.json corrupt: persist reset cursors: %w", err)
	}
	return cur, nil
}

// corruptCursorsAlert is the page a corrupt cursor file raises. It carries no
// source route, so it lands on the catch-all channel like a source cursor-alert;
// severity escalate keeps a throttle from dropping it.
func corruptCursorsAlert() event.Event {
	return event.Event{
		Source:   "flare",
		ID:       "cursor-alert:flare:cursors-corrupt",
		Kind:     "cursor-alert",
		Time:     time.Now(),
		Severity: event.SevEscalate,
		Title:    "flare: cursors.json was corrupt — quarantined, resweeping",
		Body:     "flare's own cursor file did not parse. It has been set aside and the sources are being reswept from the start (dedupe prevents re-paging). Deliveries resume automatically; check for a delivery gap around this alert.",
		Fields:   map[string]string{},
	}
}

func pollSource(cfg config.Config, j *journal.Journal, r *route.Router, co courier, src config.Source, cur source.Cursor, seen map[string]bool, cards map[string]journal.Card) (source.Cursor, error) {
	// Read may return events AND an error together (a pending integrity alert
	// alongside a parse failure). Deliver what it produced before surfacing the
	// error, so an alert is never lost to the failure it arrived with.
	events, next, err := source.Read(src, cur)
	for _, ev := range events {
		if seen[journal.SeenKey(ev.Source, ev.ID)] {
			continue
		}
		if !dispatch(cfg, j, r, co, ev, cards) {
			return cur, err // delivery failed: hold the cursor, retry next cycle
		}
	}
	if err != nil {
		return cur, err // read failed: hold the cursor and surface it
	}
	return next, nil
}

// dispatch handles one event, journaling the outcome. Returns false when the
// work failed and the event must be retried.
//
// Two classes take two paths. A PAGE routes to a channel and becomes a
// notification. An UPDATE is not news — it is the terminal state of a park
// whose card is already in Slack — so it is applied to that card and never
// routed; routing it would page the operator for every judgment gate records.
func dispatch(cfg config.Config, j *journal.Journal, r *route.Router, co courier, ev event.Event, cards map[string]journal.Card) bool {
	if ev.Class == event.ClassUpdate {
		return applyUpdate(cfg, j, co, ev, cards)
	}
	entry := journal.Entry{
		Time:     time.Now(),
		Source:   ev.Source,
		EventID:  ev.ID,
		Severity: ev.Severity.String(),
		Note:     ev.Title,
	}
	d := r.Route(ev)
	entry.Channel = d.Channel
	if d.Throttled {
		entry.Kind = journal.Throttled
		return journalOK(j, entry)
	}
	if d.Channel == config.ChannelDrop || cfg.Channels[d.Channel].Type == config.ChannelDrop {
		entry.Kind = journal.Dropped
		return journalOK(j, entry)
	}
	// Close any card this event makes stale BEFORE posting the new one. A newer
	// park for the same PR supersedes the older in gate's inbox, so the older
	// card's Approve button is already dead — and doing this first keeps the
	// retry clean: nothing is journaled as delivered, so a failure here replays
	// the whole event rather than stranding a live-looking dead card.
	if !supersede(cfg, j, co, ev, cards) {
		return false
	}
	// Send before journaling: at-least-once by design. Journaling a delivery
	// before the send could record one that never happened — a real block
	// silently un-paged — which is the worse failure for a push sink. A
	// duplicate page (journal fails after a good send) is the safe way to err.
	ref, err := co.send(cfg.Channels[d.Channel], ev)
	if err != nil {
		entry.Kind = journal.Errored
		entry.Note = err.Error()
		journalOK(j, entry)
		return false
	}
	entry.Kind = journal.Delivered
	entry.Card = cardOf(d.Channel, ref, ev)
	if !journalOK(j, entry) {
		return false
	}
	if entry.Card != nil {
		cards[journal.SeenKey(ev.Source, ev.ID)] = *entry.Card
	}
	return true
}

// cardOf records a delivered message as a correctable card — but only for a
// PARK. A verdict or a cursor-alert never resolves into a terminal state a
// later artifact reports, so tracking it would grow the index for nothing.
func cardOf(channel string, ref notify.Ref, ev event.Event) *journal.Card {
	if ev.Kind != "escalation" || !ref.Updatable() {
		return nil
	}
	return &journal.Card{
		Channel:   channel,
		ChannelID: ref.ChannelID,
		TS:        ref.TS,
		Subject:   subjectOf(ev),
	}
}

// subjectOf is the card index key: the "repo#n" gate's inbox reduces parked
// runs by. Empty when the event names no PR — such a card is only ever closed
// by its own escalation id.
func subjectOf(ev event.Event) string {
	repo, number := ev.Fields["repo"], ev.Fields["number"]
	if repo == "" || number == "" {
		return ""
	}
	return repo + "#" + number
}

// applyUpdate corrects every live card the terminal fact closes: buttons off,
// outcome and decider on, and the outcome posted into the card's thread so a
// successful tap and a silently failed one stop looking identical.
//
// An update with no live card is settled, not an error: flare may have started
// after the park was paged, or the card may already have been corrected. The
// journal records that it was dealt with either way.
func applyUpdate(cfg config.Config, j *journal.Journal, co courier, ev event.Event, cards map[string]journal.Card) bool {
	entry := journal.Entry{
		Time:    time.Now(),
		Kind:    journal.CardUpdate,
		Source:  ev.Source,
		EventID: ev.ID,
		Note:    ev.Title,
	}
	targets := cardsFor(cards, ev)
	if len(targets) == 0 {
		entry.Note = "no live card for " + ev.Title
		return journalOK(j, entry)
	}
	if !finalizeCards(cfg, j, co, ev, cards, targets) {
		return false
	}
	return journalOK(j, entry)
}

// finalizeCards rewrites each target card and records it closed. A failure
// leaves the remaining cards for the retry: the ones already closed are gone
// from the index, so the retry finalizes only what is left.
func finalizeCards(cfg config.Config, j *journal.Journal, co courier, ev event.Event, cards map[string]journal.Card, targets map[string]journal.Card) bool {
	for key, card := range targets {
		ref := notify.Ref{ChannelID: card.ChannelID, TS: card.TS}
		if err := co.finalize(cfg.Channels[card.Channel], ref, ev); err != nil {
			fmt.Fprintf(os.Stderr, "flare: finalize card %s: %v\n", key, err)
			return false
		}
		delete(cards, key)
		if !journalOK(j, journal.Entry{
			Time: time.Now(), Kind: journal.CardFinal, Source: ev.Source,
			EventID: escIDFromKey(key), Channel: card.Channel, Note: ev.Fields["outcome"],
		}) {
			return false
		}
	}
	return true
}

// cardsFor selects the live cards a terminal fact closes. A fact naming an
// escalation closes that card; a fact naming a PR closes every live card for
// that PR, because gate's inbox reduces parked runs by subject — a merge or a
// re-park retires the older attempts too.
func cardsFor(cards map[string]journal.Card, ev event.Event) map[string]journal.Card {
	out := map[string]journal.Card{}
	esc := ev.Fields["escalation"]
	if esc != "" {
		if c, ok := cards[journal.SeenKey(ev.Source, esc)]; ok {
			out[journal.SeenKey(ev.Source, esc)] = c
		}
	}
	if ev.Fields["exact_card"] == "yes" {
		return out
	}
	subject := subjectOf(ev)
	if subject == "" {
		return out
	}
	for key, c := range cards {
		if c.Subject == subject {
			out[key] = c
		}
	}
	return out
}

// supersede closes the cards a NEW park makes stale. gate's inbox keeps only
// the latest terminal per PR, so the moment a fresh run parks the same PR every
// older card's Approve button resolves nothing — tapping one returns
// "escalation is not currently parked: esc_… not in gate inbox", which reads as
// a broken tool rather than a card that moved on. Two taps died that way on
// 2026-08-21.
func supersede(cfg config.Config, j *journal.Journal, co courier, ev event.Event, cards map[string]journal.Card) bool {
	subject := subjectOf(ev)
	if ev.Kind != "escalation" || subject == "" || ev.Fields["grant_request"] == "yes" {
		return true
	}
	stale := map[string]journal.Card{}
	for key, c := range cards {
		if c.Subject == subject && key != journal.SeenKey(ev.Source, ev.ID) {
			stale[key] = c
		}
	}
	if len(stale) == 0 {
		return true
	}
	return finalizeCards(cfg, j, co, supersededBy(ev), cards, stale)
}

// supersededBy is the terminal fact a newer park represents for the older ones.
func supersededBy(ev event.Event) event.Event {
	return event.Event{
		Class:  event.ClassUpdate,
		Source: ev.Source,
		ID:     ev.ID,
		Kind:   "card-update",
		Time:   ev.Time,
		Fields: map[string]string{
			"repo":    ev.Fields["repo"],
			"number":  ev.Fields["number"],
			"run":     ev.Fields["run"],
			"outcome": event.OutcomeSuperseded,
			"note":    "A newer gate run parked this PR. Resolve the current park from `gate next`.",
		},
	}
}

// escIDFromKey recovers the event id a SeenKey was built from.
func escIDFromKey(key string) string {
	if i := strings.IndexByte(key, 0); i >= 0 {
		return key[i+1:]
	}
	return key
}

func journalOK(j *journal.Journal, e journal.Entry) bool {
	if err := j.Append(e); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	return true
}

// status prints health as JSON. Exit 0 = a poll completed recently; 1 =
// stale or never ran (flare is best-effort push — a silent watcher must be
// visible where the operator already looks, so wire this into /health).
func status(cfg config.Config, j *journal.Journal) int {
	cur, err := j.LoadCursors()
	corrupt := errors.Is(err, journal.ErrCursorsCorrupt)
	if err != nil && !corrupt {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	stale := time.Duration(3*cfg.PollSeconds) * time.Second
	healthy := !corrupt && !cur.LastPoll.IsZero() && time.Since(cur.LastPoll) < stale
	tail, err := j.Tail(10)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	report := map[string]any{
		"healthy":   healthy,
		"last_poll": cur.LastPoll,
		"cursors":   cur.Sources,
		"recent":    tail,
	}
	// A corrupt cursor file is why the watcher can't advance — surface it here
	// (where /health looks) instead of erroring out with a raw parse failure.
	if corrupt {
		report["cursors_corrupt"] = true
	}
	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(out))
	if !healthy {
		return 1
	}
	return 0
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".flare"
	}
	return filepath.Join(home, ".flare")
}
