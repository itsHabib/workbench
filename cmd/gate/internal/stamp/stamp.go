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
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Context is the commit-status context gate posts under. It is deliberately
// distinct from the "gate" context the enforcement workflow posts: readiness
// skips only its own exact "gate" context, and this one is always success, so
// it is never a check gate blocks itself on.
const Context = "gate/authorized"

// postTimeout bounds the WHOLE stamp attempt — the head→PR resolution and the
// status POST together, not each in turn. One shared budget, and a small one,
// because the stamp is a decoration hanging off the tail of a decision that is
// already durable, and gate is routinely run as a subprocess under a caller's
// own deadline (escalate's ingress is the live example). A per-call budget lets
// two sequential gh calls spend twice the number written here, which is how a
// decoration comes to own most of a parent's budget and gets the whole process
// killed for it. The decision never depends on this succeeding; the exit code
// is already decided when Post is called.
const postTimeout = 6 * time.Second

// Authorized is one provenance stamp: the PR gate evaluated (Repo + Number),
// the exact commit it judged (HeadSHA), the decision's run id, and the deciding
// action artifact's chain hash.
type Authorized struct {
	Repo    string // owner/repo, as passed to gh
	Number  int    // the PR gate evaluated — the stamp posts only if it is the sole open PR on the head
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
	// One budget for both gh calls below, started here. See postTimeout.
	ctx, cancel := context.WithTimeout(context.Background(), postTimeout)
	defer cancel()
	// A commit status is commit-scoped, but gate authorized exactly one PR. Post
	// only when the evaluated PR is the SOLE open PR on this head. Two ways this
	// fails, both to a PR gate never evaluated: more than one open PR shares the
	// head (ambiguous), or the head's one open PR is a DIFFERENT PR because the
	// evaluated one merged/closed. Fail closed on either — and on an unresolvable
	// head — so the same-head guard the workflow rail documents (gate.yml) holds
	// here too, and is not fooled by a merged evaluated PR.
	open, err := a.openPRNumbers(ctx)
	if err != nil {
		return err
	}
	if len(open) != 1 || open[0] != a.Number {
		return fmt.Errorf("stamp: head %s does not map uniquely to evaluated PR #%d (open PRs on head: %v); refusing", a.HeadSHA, a.Number, open)
	}
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

// openPRNumbers resolves the numbers of the OPEN PRs that share this head SHA.
// The stamp posts only when this is exactly the evaluated PR. A merged/closed PR
// on the same head is excluded: it can take no live stamp, and its presence must
// not stand in for the open PR the guard actually checks.
func (a Authorized) openPRNumbers(ctx context.Context) ([]int, error) {
	// Resolve gh before invoking it so an unresolvable binary reports as itself —
	// the launchd case, where the daemon's PATH is /usr/bin:/bin:/usr/sbin:/sbin
	// and gh lives under /opt/homebrew/bin. `exec: "gh": executable file not
	// found in $PATH` names the symptom; this names the fix.
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("stamp: gh is not resolvable on PATH=%q: %w", os.Getenv("PATH"), err)
	}
	out, err := exec.CommandContext(ctx, "gh", a.prsArgs()...).Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("stamp: resolve PRs for head %s timed out after %s", a.HeadSHA, postTimeout)
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("stamp: resolve PRs for head %s: %s", a.HeadSHA, ee.Stderr)
		}
		return nil, fmt.Errorf("stamp: resolve PRs for head %s: %w", a.HeadSHA, err)
	}
	var nums []int
	for _, f := range strings.Fields(string(out)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("stamp: parse PR number %q for head %s: %w", f, a.HeadSHA, err)
		}
		nums = append(nums, n)
	}
	return nums, nil
}

// prsArgs builds the gh query for the head's open PR numbers. Split out so the
// guard's wiring is unit-testable without a network or a gh binary.
func (a Authorized) prsArgs() []string {
	return []string{
		"api", "repos/" + a.Repo + "/commits/" + a.HeadSHA + "/pulls",
		"--jq", `.[] | select(.state=="open") | .number`,
	}
}

// validate refuses an incomplete stamp: a status with no head SHA has nothing
// to bind to, and a stamp missing its run or hash is not verifiable — better to
// post nothing than a decorative marker that pins to no decision.
func (a Authorized) validate() error {
	if a.Repo == "" || a.HeadSHA == "" {
		return fmt.Errorf("stamp: repo and head sha required")
	}
	if a.Number <= 0 {
		return fmt.Errorf("stamp: a positive PR number is required to scope the stamp")
	}
	// Hash is the ACTION artifact's chain hash, and it exists only once that
	// artifact has been durably appended. Requiring it is therefore not just a
	// verifiability check: it is the structural guarantee that no GitHub call on
	// the stamp path can precede the authorization it decorates. A caller cannot
	// stamp an authorization that is not yet in the log, because it has nothing
	// to put here.
	if a.Run == "" || a.Hash == "" {
		return fmt.Errorf("stamp: run and hash required for a verifiable stamp (the hash exists only after the action artifact is durable)")
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

// description is the machine-verifiable payload a skeptic reads off the PR: the
// PR gate evaluated names itself (so a viewer on any PR sharing the head knows
// which one was authorized), run selects the artifact group (gate explain -run),
// and hash is the tamper-evident anchor (gate audit). #num + run_<16hex> + a
// sha256 hash fit well under the 140-char status-description ceiling.
func (a Authorized) description() string {
	return fmt.Sprintf("gate authorized · #%d · run=%s hash=%s", a.Number, a.Run, a.Hash)
}

// targetURL points at the PR gate evaluated — a real, clickable page that names
// the authorized PR directly, so the status is unambiguous even though it rides
// a commit-scoped rail. The verifiable identifiers live in the description.
func (a Authorized) targetURL() string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", a.Repo, a.Number)
}
