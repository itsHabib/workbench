package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/observe"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// sweepSource names what taught gate the fact, recorded on every artifact the
// sweep writes so a reader knows the basis rather than inferring one. An
// open-PR list can prove a PR is NOT OPEN; it cannot say whether it merged or
// was abandoned, and the artifact must not imply otherwise.
const sweepSource = "gh-open-pr-list"

// cmdSweep reconciles the inbox against current PR state and RECORDS what it
// learns, so the offline projection stops recommending work on dead pull
// requests.
//
// It exists because gate authorizes and an executor acts: every action gate
// writes is dry_run/would_merge, so the log can prove a merge was ALLOWED and
// can never say the PR has since finished. `gate next -live` already discovers
// that on every invocation and throws it away — which is why the operator's
// inbox showed 164 rows against 3 live pull requests. This is the same read,
// persisted.
//
// It uses the open-PR seam `next -live` and `preflight` already share. Gate
// grows no second GitHub client here, and this verb reads no merge commits or
// actors: recording what LANDED, with the platform's own clock and actor, is
// `gate receipt`/`gate reconcile`'s claim to make, not a sweep's.
//
// It is a State writer, deliberately NOT folded into `next`. Observability views
// are read-only and storeless; a `next` that sometimes wrote would put a store
// mutation behind a display flag, on a path escalate serve shells under a hard
// budget. Like next/explain/audit it returns nil or an error — never a 0–3 a
// driver would misread as a decision.
func cmdSweep(args []string) error {
	fs := flag.NewFlagSet("sweep", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	asJSON := fs.Bool("json", false, "emit the sweep result as JSON")
	dryRun := fs.Bool("dry-run", false, "report what would be recorded without writing")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	res, err := runSweep(e.st, lookupOpenPRs, time.Now, *dryRun)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	renderSweep(os.Stdout, res)
	return nil
}

// SweepResult reports one sweep.
type SweepResult struct {
	Closed []SweepClosed `json:"closed"`
	// Unreadable names each repo whose open-PR read failed, with the reason. A
	// sweep that silently skipped a repo would read as "everything here is still
	// open", which is the opposite of what an unread repo means.
	Unreadable []SweepUnreadable `json:"unreadable,omitempty"`
	// Checked is how many live inbox rows the sweep examined, and DryRun whether
	// anything was actually written.
	Checked int  `json:"checked"`
	DryRun  bool `json:"dry_run,omitempty"`
	// Regated counts the subjects re-gated mid-sweep, whose closure was declined.
	Regated int `json:"regated,omitempty"`
}

// SweepClosed is one subject the sweep found is no longer open.
type SweepClosed struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Run    string `json:"run"`
	// Artifact is the recorded closure's id, or empty on a dry run or when the
	// terminal already carried one.
	Artifact string `json:"artifact,omitempty"`
	// Already reports that a closure for this terminal was already on the log, so
	// a repeated sweep is visibly a no-op rather than silently one.
	Already bool `json:"already,omitempty"`
	// Regated reports that the subject was re-gated while the sweep's GitHub read
	// was in flight, so nothing was recorded. It is a normal outcome, not an
	// error: the fresh terminal is the current truth and a later sweep can close
	// it on its own merits.
	Regated bool `json:"regated,omitempty"`
}

// SweepUnreadable is one repo the sweep could not read.
type SweepUnreadable struct {
	Repo   string `json:"repo"`
	Reason string `json:"reason"`
}

// runSweep is the whole verb, with both seams injected so it is testable without
// a network or a clock.
func runSweep(st *state.Store, fetch observe.OpenPRs, now func() time.Time, dryRun bool) (SweepResult, error) {
	arts, err := st.List(nil)
	if err != nil {
		return SweepResult{}, err
	}
	// Only LIVE rows are swept. A row the log has already discharged needs no
	// second closing fact, and re-reading merged PRs forever would make the
	// sweep's cost grow with history instead of with the queue.
	subjects := observe.LiveSubjects(arts)
	res := SweepResult{Checked: len(subjects), DryRun: dryRun}
	if len(subjects) == 0 {
		return res, nil
	}
	open, errs := observe.OpenSets(sweepRepos(subjects), fetch)
	for _, s := range subjects {
		if _, bad := errs[s.Repo]; bad {
			continue
		}
		if _, isOpen := open[s.Repo][s.Number]; isOpen {
			continue
		}
		closed, err := recordClosed(st, s, now, dryRun)
		if err != nil {
			return SweepResult{}, err
		}
		res.Closed = append(res.Closed, closed)
		if closed.Regated {
			res.Regated++
		}
	}
	res.Unreadable = unreadable(errs)
	return res, nil
}

// sweepRepos is the deduplicated repo set behind a work list.
func sweepRepos(subjects []observe.LiveSubject) []string {
	seen := make(map[string]bool, len(subjects))
	repos := make([]string, 0, len(subjects))
	for _, s := range subjects {
		if seen[s.Repo] {
			continue
		}
		seen[s.Repo] = true
		repos = append(repos, s.Repo)
	}
	return repos
}

// errTerminalMoved reports that the subject was re-gated while the sweep's
// GitHub read was in flight, so the closure no longer describes the log's
// current state.
var errTerminalMoved = errors.New("subject was re-gated during the sweep")

// recordClosed appends one closure, parented to the terminal the stale row
// stands on.
//
// Two guards, and they answer different questions. The store's absent-parent
// guard makes "one closure per terminal" structural, so a repeated sweep is a
// no-op rather than a second record of the same observation. The locked
// revalidation below closes the read-to-append race the first guard cannot see:
// the GitHub fetch takes seconds, and a PR reopened and re-gated inside that
// window gets a NEW terminal. The closure would then land AFTER it in the log
// and — since a closing fact settles by log order — moot the fresh park, while
// the absent-parent guard happily allowed it because it is keyed on the OLD
// terminal. Same defect the ordering rule fixed, arriving through a race
// instead of through history.
//
// The revalidation reduces through observe's shared fold rather than a second
// opinion about which terminal is current, and runs inside the store lock, so
// no terminal can land between the check and the append.
func recordClosed(st *state.Store, s observe.LiveSubject, now func() time.Time, dryRun bool) (SweepClosed, error) {
	out := SweepClosed{Repo: s.Repo, Number: s.Number, Run: s.Run}
	if dryRun {
		return out, nil
	}
	body := map[string]any{
		"repo":        s.Repo,
		"number":      s.Number,
		"state":       observe.ClosedNotOpen,
		"observed_at": now().UTC().Format(time.RFC3339),
		"source":      sweepSource,
	}
	stillNewest := func(audit state.AuditResult) error {
		newest, ok := observe.NewestTerminal(audit.All, s.Repo, s.Number)
		if !ok || newest != s.Terminal {
			return errTerminalMoved
		}
		return nil
	}
	a, err := st.AppendIfAbsentParentWhereAfterAudit(
		state.KindSubjectClosed, []string{state.KindSubjectClosed},
		s.Run, s.Terminal, []string{s.Terminal}, body, nil, stillNewest,
	)
	// The sentinel is wrapped by the store, so it is matched with errors.Is. A
	// direct comparison silently never fires, turning a deduplicated concurrent
	// sweep into a failed one.
	if errors.Is(err, state.ErrAlreadyExists) {
		out.Already = true
		return out, nil
	}
	if errors.Is(err, errTerminalMoved) {
		out.Regated = true
		return out, nil
	}
	if err != nil {
		return SweepClosed{}, fmt.Errorf("record closure for %s#%d: %w", s.Repo, s.Number, err)
	}
	out.Artifact = a.ID
	return out, nil
}

// unreadable renders the per-repo failures deterministically.
func unreadable(errs map[string]error) []SweepUnreadable {
	if len(errs) == 0 {
		return nil
	}
	out := make([]SweepUnreadable, 0, len(errs))
	for repo, err := range errs {
		out = append(out, SweepUnreadable{Repo: repo, Reason: err.Error()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// closedNote annotates a row the sweep did not actually write.
func closedNote(c SweepClosed) string {
	if c.Already {
		return "  (already recorded)"
	}
	if c.Regated {
		return "  (re-gated mid-sweep; not closed)"
	}
	return ""
}

func renderSweep(w io.Writer, res SweepResult) {
	verb := "recorded"
	if res.DryRun {
		verb = "would record"
	}
	fmt.Fprintf(w, "swept %d live row(s); %s %d closure(s)\n", res.Checked, verb, len(res.Closed)-res.Regated)
	for _, c := range res.Closed {
		fmt.Fprintf(w, "  %s#%d  %s%s\n", c.Repo, c.Number, c.Run, closedNote(c))
	}
	if res.Regated > 0 {
		fmt.Fprintln(w, "  a re-gated subject is left to the log's newer terminal, never closed behind it")
	}
	for _, u := range res.Unreadable {
		fmt.Fprintf(w, "  %s  UNREAD: %s\n", u.Repo, u.Reason)
	}
	if len(res.Unreadable) > 0 {
		fmt.Fprintln(w, "  an unread repo's rows are left alone, never assumed closed")
	}
}
