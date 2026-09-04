package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/ledger"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// ghTimeout bounds each GitHub read the ledger verbs make. They are reporting
// paths with no decision hanging off them, so a stalled call fails the command
// rather than degrading anything.
const ghTimeout = 20 * time.Second

// sourceExecutor / sourceReconcile name what wrote a receipt. Kept apart because
// a ledger whose receipts all came from reconciliation is a ledger whose
// executors are not reporting — a real operational fact, invisible if both paths
// wrote the same thing.
const (
	sourceExecutor  = "executor"
	sourceReconcile = "reconcile"
)

// cmdReceipt records what became of one authorization.
//
// The verb is deliberately SEPARATE from any merge. Gate authorizes, an executor
// runs the pinned command, and then discharges the authorization by calling this
// — the same plane separation the design rests on, which is why gate must never
// grow a merge of its own to make the writeback convenient.
//
// Nothing about the landing is taken from the caller. The merge commit, the
// actor, and the timestamp are read back from GitHub, so the record carries an
// independent clock and an independent actor rather than an executor's account
// of its own behavior — which is exactly the claim a receipt exists to check.
func cmdReceipt(args []string) error {
	fs := flag.NewFlagSet("receipt", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	run := fs.String("run", "", "the gate run whose authorization this receipt discharges")
	why := fs.String("why", "", "why the merge did not land (records a failed attempt on a still-open PR)")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if *run == "" {
		return errors.New("receipt: -run required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	arts, err := e.st.Run(*run)
	if err != nil {
		return err
	}
	if len(arts) == 0 {
		return fmt.Errorf("receipt: run %s not found in state dir %s", *run, absStateDir(e.stateDir))
	}
	auth, err := ledger.FindAuthorization(arts, *run)
	if err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	landing, err := lookupLanding(auth.Repo, auth.Number)
	if err != nil {
		return err
	}
	receipt, err := ledger.NewReceipt(auth, landing, sourceExecutor, *why, time.Now)
	if err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	artifact, err := appendReceipt(e, auth, receipt)
	if err != nil {
		return err
	}
	printJSON(struct {
		Receipt string          `json:"receipt"`
		Body    ledger.Receipt  `json:"body"`
		Hash    string          `json:"hash"`
		Note    json.RawMessage `json:"-"`
	}{Receipt: artifact.ID, Body: receipt, Hash: artifact.Hash})
	return nil
}

// appendReceipt writes the receipt parented to the action it discharges. The
// store's absent-parent guard, keyed on that action, is what makes "a receipt
// discharges exactly one action" structural: a second attempt is refused by the
// substrate, not by a check someone has to remember to write.
func appendReceipt(e env, auth ledger.Authorization, receipt ledger.Receipt) (state.Artifact, error) {
	artifact, err := e.st.AppendIfAbsentParentKinds(
		state.KindReceipt, []string{state.KindReceipt},
		auth.Run, auth.Action, []string{auth.Action}, receipt,
	)
	if errors.Is(err, state.ErrAlreadyExists) {
		return state.Artifact{}, fmt.Errorf("receipt_duplicate: action %s is already discharged by a receipt", auth.Action)
	}
	if err != nil {
		return state.Artifact{}, fmt.Errorf("receipt: append: %w", err)
	}
	return artifact, nil
}

// prView is the slice of GitHub's pull-request payload a landing needs.
type prView struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	Merged      bool   `json:"merged"`
	MergeCommit string `json:"merge_commit_sha"`
	MergedAt    string `json:"merged_at"`
	MergedBy    struct {
		Login string `json:"login"`
	} `json:"merged_by"`
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// lookupLanding reads one PR's landing facts from the GitHub API.
func lookupLanding(repo string, number int) (ledger.Landing, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	out, stderr, err := runGHAPI(ctx, fmt.Sprintf("repos/%s/pulls/%d", repo, number))
	if err != nil {
		return ledger.Landing{}, ghAPIError(ctx, "read pull request", repo, stderr, err)
	}
	var view prView
	if err := json.Unmarshal(out, &view); err != nil {
		return ledger.Landing{}, fmt.Errorf("receipt: parse pull request %s#%d: %w", repo, number, err)
	}
	return landingFrom(repo, view), nil
}

// landingFrom maps GitHub's payload onto the ledger's shape. State is
// upper-cased to gate's vocabulary, and merged is taken from the boolean rather
// than inferred from the state string — a closed PR and a merged one both report
// state "closed" on this endpoint, and conflating them would classify every
// merge as abandoned.
func landingFrom(repo string, view prView) ledger.Landing {
	l := ledger.Landing{
		Repo:        repo,
		Number:      view.Number,
		State:       strings.ToUpper(view.State),
		Merged:      view.Merged,
		HeadSHA:     view.Head.SHA,
		MergeCommit: view.MergeCommit,
		Actor:       view.MergedBy.Login,
		BaseRef:     view.Base.Ref,
	}
	if view.Merged {
		l.State = "MERGED"
	}
	if at, err := time.Parse(time.RFC3339, view.MergedAt); err == nil {
		l.MergedAt = at.UTC()
	}
	return l
}

// mergedPR is one row of `gh pr list --state merged`.
type mergedPR struct {
	Number      int    `json:"number"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	MergedAt    string `json:"mergedAt"`
	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	// MergedBy is who performed the merge — NOT the PR's author. On the bypass
	// rows this is the whole point of the field: "who merged this without
	// authorization" is a question about the merger, and on any repo where
	// authors do not self-merge, the author is simply a different person.
	MergedBy struct {
		Login string `json:"login"`
	} `json:"mergedBy"`
}

// lookupMergedPRs lists the pull requests merged into a repo, newest first.
//
// It queries merged PRs rather than walking the branch's commits because a PR is
// what carries the head SHA an authorization is keyed on — a bare commit has no
// head to join by. What that leaves out is stated on the artifact itself
// (ledger.BasisMergedPullRequests): a direct push to the protected branch is
// outside this claim, and belongs to branch protection rather than the ledger.
func lookupMergedPRs(repo string, limit int) ([]ledger.Landing, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "-R", repo,
		"--state", "merged", "--limit", strconv.Itoa(limit),
		"--json", "number,headRefOid,baseRefName,mergedAt,mergeCommit,mergedBy")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("reconcile: list merged PRs for %s: %s", repo, ee.Stderr)
		}
		return nil, fmt.Errorf("reconcile: list merged PRs for %s: %w", repo, err)
	}
	var rows []mergedPR
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("reconcile: parse merged PRs for %s: %w", repo, err)
	}
	landings := make([]ledger.Landing, 0, len(rows))
	for _, r := range rows {
		l := ledger.Landing{
			Repo: repo, Number: r.Number, State: "MERGED", Merged: true,
			HeadSHA: r.HeadRefOid, MergeCommit: r.MergeCommit.OID,
			Actor: r.MergedBy.Login, BaseRef: r.BaseRefName,
		}
		if at, err := time.Parse(time.RFC3339, r.MergedAt); err == nil {
			l.MergedAt = at.UTC()
		}
		landings = append(landings, l)
	}
	return landings, nil
}

// reconcileLimit bounds one sweep's PR fetch: generous enough for a long window
// on an active repo, bounded so a reconcile is one call rather than an unbounded
// pagination walk.
//
// The limit is RECORDED on the coverage artifact and printed, and a sweep that
// hits it says so. A cap that silently drops the oldest merges in a window would
// let a busy sprint produce a "0 bypasses" reading over half a window — a
// coverage report whose scope the reader cannot see is worse than none, which is
// the same reason the artifact states its basis.
const reconcileLimit = 500

// cmdReconcile answers the question no per-run artifact can: did anything merge
// AROUND the gate?
//
// Every other surface gate has reads its own decisions back and can only report
// what it did. Coverage reads the platform first and asks what gate can account
// for — so an absence of gate artifacts becomes visible as an absence, instead of
// as silence.
//
// Merges predating the control are classified separately and never counted as
// bypasses. That separation is the difference between a report someone acts on
// and a report that cries wolf at every historical merge until the reader learns
// to close it.
func cmdReconcile(args []string) error {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	repo := fs.String("repo", "", "owner/name")
	since := fs.String("since", "", "window start, YYYY-MM-DD (default: 30 days ago)")
	branch := fs.String("branch", "", "protected branch (default: the repo's default branch)")
	effectiveFrom := fs.String("effective-from", "", "YYYY-MM-DD the control took effect for this repo (default: derived from state)")
	asJSON := fs.Bool("json", false, "emit the coverage record as JSON")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if *repo == "" {
		return errors.New("reconcile: -repo required")
	}
	window, err := parseWindow(*since)
	if err != nil {
		return err
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	arts, err := e.st.List(nil)
	if err != nil {
		return err
	}
	effective, source, err := resolveEffectiveFrom(arts, *repo, *effectiveFrom)
	if err != nil {
		return err
	}
	protectedBranch, err := resolveBranch(*repo, *branch)
	if err != nil {
		return err
	}
	landings, err := lookupMergedPRs(*repo, reconcileLimit)
	if err != nil {
		return err
	}
	coverage := ledger.Reconcile(*repo, protectedBranch, window, effective, source,
		inWindow(landings, protectedBranch, window),
		ledger.Authorizations(arts, *repo), ledger.Receipts(arts), time.Now)
	coverage.Limit = reconcileLimit
	// gh returns newest-first, so a full page means older merges in the window may
	// be missing. Say so on the artifact rather than letting the reader assume the
	// sweep saw everything.
	coverage.Truncated = len(landings) >= reconcileLimit

	// The backstop. An executor that ran the pinned command and never called
	// `gate receipt` leaves an authorization the ledger cannot close; reconcile
	// has just proved, from the platform, that it landed at the authorized head,
	// so it writes the receipt the executor owed. Source distinguishes the two —
	// a ledger whose receipts all say "reconcile" is a ledger whose executors are
	// not reporting, which is worth being able to see rather than papering over.
	swept, sweepErr := sweepReceipts(e, coverage)
	artifact, err := e.st.Append(state.KindCoverage, state.NewRunID(), nil, coverage)
	if err != nil {
		return fmt.Errorf("reconcile: append coverage: %w", err)
	}
	if sweepErr != nil {
		fmt.Fprintf(os.Stderr, "gate: some receipts could not be swept (the coverage record stands): %v\n", sweepErr)
	}
	if *asJSON {
		printJSON(coverage)
		return nil
	}
	renderCoverage(os.Stdout, artifact.ID, coverage)
	if swept > 0 {
		fmt.Printf("swept %d receipt(s) for authorizations no executor discharged\n", swept)
	}
	return nil
}

// sweepReceipts writes a receipt for every authorization this sweep proved
// landed at its authorized head and that nothing had discharged.
//
// Ordering note: receipts are appended BEFORE the coverage artifact. If the
// coverage append then fails, the receipts are still durable and the sweep is
// simply re-runnable — the discharges it proved are not lost, and the second run
// finds them already present. The reverse order would risk a coverage record
// claiming discharges that were never written.
//
// It writes ONLY the merged-at-the-authorized-head class. A superseded or
// abandoned authorization is a judgement call about what an executor intended,
// and reconcile has no standing to make it — inventing an outcome is precisely
// what a receipt exists to prevent. Those stay open until someone says.
//
// The coverage rows are mutated in place so the artifact records the receipts
// this sweep created, rather than a snapshot that was already stale when written.
func sweepReceipts(e env, c ledger.Coverage) (int, error) {
	swept := 0
	var failures []string
	for i, row := range c.AuthorizedAndLanded {
		if row.Receipt != "" || row.Action == "" {
			continue
		}
		auth := ledger.Authorization{
			Action: row.Action, Run: row.Run, Repo: c.Repo, Number: row.Number,
			Head: row.Head, Outcome: "would_merge",
		}
		landing := ledger.Landing{
			Repo: c.Repo, Number: row.Number, State: "MERGED", Merged: true,
			HeadSHA: row.Head, MergeCommit: row.MergeCommit, Actor: row.Actor,
		}
		if at, err := time.Parse(time.RFC3339, row.MergedAt); err == nil {
			landing.MergedAt = at
		}
		receipt, err := ledger.NewReceipt(auth, landing, sourceReconcile, "", time.Now)
		if err != nil {
			failures = append(failures, fmt.Sprintf("#%d: %v", row.Number, err))
			continue
		}
		artifact, err := appendReceipt(e, auth, receipt)
		if err != nil {
			failures = append(failures, fmt.Sprintf("#%d: %v", row.Number, err))
			continue
		}
		c.AuthorizedAndLanded[i].Receipt = artifact.ID
		swept++
	}
	if len(failures) == 0 {
		return swept, nil
	}
	return swept, errors.New(strings.Join(failures, "; "))
}

// inWindow keeps the landings that belong to this reconciliation: merged into
// the protected branch, inside the window. Filtering on the base ref is what
// keeps a merge into some other branch from being counted as an unauthorized
// landing on the protected one.
func inWindow(landings []ledger.Landing, branch string, w ledger.Window) []ledger.Landing {
	out := make([]ledger.Landing, 0, len(landings))
	for _, l := range landings {
		if branch != "" && l.BaseRef != branch {
			continue
		}
		if l.MergedAt.Before(w.Since) || l.MergedAt.After(w.Until) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// defaultWindow is how far back a reconcile looks when no -since is given.
const defaultWindow = 30 * 24 * time.Hour

func parseWindow(since string) (ledger.Window, error) {
	now := time.Now().UTC()
	if since == "" {
		return ledger.Window{Since: now.Add(-defaultWindow), Until: now}, nil
	}
	at, err := time.Parse("2006-01-02", since)
	if err != nil {
		return ledger.Window{}, fmt.Errorf("reconcile: -since must be YYYY-MM-DD: %w", err)
	}
	return ledger.Window{Since: at.UTC(), Until: now}, nil
}

// resolveEffectiveFrom picks the adoption boundary: the operator's stated date
// when there is one, otherwise the first time gate ever ran on the repo — a fact
// state already holds, so the common case needs no configuration and cannot go
// stale. A repo gate has never touched has no boundary, and every merge on it is
// reported unauthorized, which is the honest answer.
func resolveEffectiveFrom(arts []state.Artifact, repo, flagValue string) (time.Time, string, error) {
	if flagValue != "" {
		at, err := time.Parse("2006-01-02", flagValue)
		if err != nil {
			return time.Time{}, "", fmt.Errorf("reconcile: -effective-from must be YYYY-MM-DD: %w", err)
		}
		return at.UTC(), "flag", nil
	}
	if at, ok := ledger.DerivedEffectiveFrom(arts, repo); ok {
		return at, "derived", nil
	}
	return time.Time{}, "", nil
}

// resolveBranch returns the branch to reconcile against, defaulting to the
// repo's own default branch.
func resolveBranch(repo, flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	return defaultBranch(ctx, repo, runGHAPI)
}

// renderCoverage prints one sweep. The two anomaly classes lead, because a
// coverage report whose headline is "47 merges accounted for" buries the one
// line that matters — and the pre-adoption count is stated explicitly rather
// than omitted, so a reader can see that history was excluded on purpose and is
// not being quietly counted as clean.
func renderCoverage(w io.Writer, artifactID string, c ledger.Coverage) {
	fmt.Fprintf(w, "coverage %s — %s (%s), %s .. %s\n", artifactID, c.Repo, c.Branch, c.Since, c.Until)
	fmt.Fprintf(w, "basis: %s (a direct push to %s has no PR and is outside this claim)\n", c.Basis, c.Branch)
	if c.Truncated {
		fmt.Fprintf(w, "INCOMPLETE: the sweep hit its %d-PR ceiling, so older merges in this window are NOT included — narrow -since and re-run\n", c.Limit)
	}
	if !c.Truncated && c.Limit > 0 {
		fmt.Fprintf(w, "sweep ceiling: %d merged PRs (not reached)\n", c.Limit)
	}
	if c.EffectiveFrom != "" {
		fmt.Fprintf(w, "control effective from: %s (%s)\n", c.EffectiveFrom, c.EffectiveFromSource)
	}
	if c.EffectiveFrom == "" {
		fmt.Fprintf(w, "control effective from: UNKNOWN — gate has never run on %s, so every merge below reads as unauthorized\n", c.Repo)
	}
	fmt.Fprintln(w)
	coverageSection(w, "MERGED WITHOUT AUTHORIZATION", c.LandedWithoutAuthorization)
	coverageSection(w, "authorized, never landed", c.AuthorizedNeverLanded)
	fmt.Fprintf(w, "authorized and landed: %d\n", len(c.AuthorizedAndLanded))
	fmt.Fprintf(w, "pre-adoption (excluded from the bypass count): %d\n", len(c.PreAdoption))
	if c.OutstandingOutsideWindow > 0 {
		fmt.Fprintf(w, "authorizations outstanding from BEFORE this window (not listed above): %d — widen -since to see them\n", c.OutstandingOutsideWindow)
	}
	unreceipted := 0
	for _, row := range c.AuthorizedAndLanded {
		if row.Receipt == "" {
			unreceipted++
		}
	}
	if unreceipted > 0 {
		fmt.Fprintf(w, "of those, %d have no receipt — the merge is accounted for, the writeback is not\n", unreceipted)
	}
}

func coverageSection(w io.Writer, title string, rows []ledger.CoverageRow) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "%s (%d)\n", title, len(rows))
	for _, r := range rows {
		fmt.Fprintf(w, "  #%d  head=%s", r.Number, shortSHA(r.Head))
		if r.MergedAt != "" {
			fmt.Fprintf(w, "  merged=%s", r.MergedAt)
		}
		if r.Actor != "" {
			fmt.Fprintf(w, "  by=%s", r.Actor)
		}
		if r.Action != "" {
			fmt.Fprintf(w, "  action=%s", r.Action)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
}
