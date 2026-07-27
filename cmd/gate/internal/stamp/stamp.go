// Package stamp posts gate's provenance "stamp" onto a PR: a GitHub commit
// status (gate/authorized → success) that carries the deciding run id and the
// action artifact's chain hash, so the PR page shows a verifiable pointer back
// to gate's audit chain.
//
// This is mechanism, not policy. It is a legibility side effect of a decision
// already made — it never decides anything and never runs before a pass. The
// authorization stays the exit code plus the hash-chained log; the stamp is a
// legible pointer to that log, forgeable by anyone holding the same gh token
// and therefore never itself the authorization.
//
// It is a commit STATUS, deliberately never a GitHub review approval: an
// approval would manufacture the review-decision signal gate reads to judge
// readiness — the stamp gaming its own gate. A status does not feed that
// signal, so it is the honest rail.
package stamp

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Context is the commit-status context gate posts under. It is deliberately
// distinct from the "gate" context the enforcement workflow posts: readiness
// skips only its own exact "gate" context, and this one is always success, so
// it is never a check gate blocks itself on.
const Context = "gate/authorized"

// postTimeout bounds the status POST. The stamp is best-effort and strictly
// downstream of a finished decision, so a hung gh or a stalled GitHub must never
// hold the outcome/exit-code seam hostage: the deadline fires, Post returns an
// error the caller logs, and gate exits on its decision regardless.
const postTimeout = 10 * time.Second

// Authorized is one provenance stamp: the exact commit gate judged (HeadSHA),
// the decision's run id, and the deciding action artifact's chain hash.
type Authorized struct {
	Repo    string // owner/repo, as passed to gh
	HeadSHA string
	Run     string
	Hash    string
}

// Post publishes the stamp as a success commit status on HeadSHA. It is
// success-only by construction: the caller invokes it solely on a pass
// (exit 0), so a block/park/refuse posts nothing and a stale red stamp can
// never exist to deadlock gate's own readiness.
//
// Best-effort by contract: the decision is authoritative whether or not the
// status lands, so a caller treats a returned error as a warning, never as a
// reason to change the outcome.
func Post(a Authorized) error {
	if err := a.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), postTimeout)
	defer cancel()
	if _, err := exec.CommandContext(ctx, "gh", a.args()...).Output(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("stamp: gh status post timed out after %s", postTimeout)
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("stamp: gh status post: %s", ee.Stderr)
		}
		return fmt.Errorf("stamp: gh status post: %w", err)
	}
	return nil
}

// validate refuses an incomplete stamp: a status with no head SHA has nothing
// to bind to, and a stamp missing its run or hash is not verifiable — better to
// post nothing than a decorative marker that pins to no decision.
func (a Authorized) validate() error {
	if a.Repo == "" || a.HeadSHA == "" {
		return fmt.Errorf("stamp: repo and head sha required")
	}
	if a.Run == "" || a.Hash == "" {
		return fmt.Errorf("stamp: run and hash required for a verifiable stamp")
	}
	return nil
}

// args builds the gh invocation. Split out from Post so the payload wiring is
// unit-testable without a network or a gh binary.
func (a Authorized) args() []string {
	return []string{
		"api", "-X", "POST",
		"repos/" + a.Repo + "/statuses/" + a.HeadSHA,
		"-f", "state=success",
		"-f", "context=" + Context,
		"-f", "description=" + a.description(),
		"-f", "target_url=" + a.targetURL(),
	}
}

// description is the machine-verifiable payload a skeptic reads off the PR:
// run selects the artifact group (gate explain -run), hash is the tamper-
// evident anchor (gate audit). run_<16hex> + a sha256 hash fit well under the
// 140-char status-description ceiling.
func (a Authorized) description() string {
	return fmt.Sprintf("gate authorized · run=%s hash=%s", a.Run, a.Hash)
}

// targetURL points at the exact commit judged — a real, clickable page. The
// verifiable identifiers live in the description; this is the human landing
// spot, bound to the same head SHA the status is posted against.
func (a Authorized) targetURL() string {
	return fmt.Sprintf("https://github.com/%s/commit/%s", a.Repo, a.HeadSHA)
}
