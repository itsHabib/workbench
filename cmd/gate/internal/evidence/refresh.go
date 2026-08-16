package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

// A conflict-free base refresh moves the pull request's head without changing
// a byte of what the panel reviewed. Head-bound panel evidence reads that as
// every reviewer having vanished, so the run parks and the driver re-triggers a
// review cycle it did not need. This file records the one fact that makes the
// difference decidable downstream: the judged head and an earlier reviewed head
// produce a byte-identical diff.
//
// It records the fact; it never decides what the fact buys. The verifier ladder
// still runs in full against the judged head — CI is re-read there and a park
// still reaches a judge. Only the panel rung stops treating a pure refresh as
// missing evidence.

// diffDigestAlgo prefixes every digest so the value is self-describing and a
// future algorithm change cannot silently compare equal to an old one.
const diffDigestAlgo = "sha256"

// digestFn returns the digest of the effective diff at one head of a pull
// request. An error means "unknown", which callers must treat as no
// equivalence — never as equivalence.
type digestFn func(headSHA string) (string, error)

// carryEquivalentRefresh credits reviewers whose review is anchored to an
// earlier head that yields the exact same diff as the judged head, and records
// the equivalence that licenses the credit. Reviewers still unsatisfied at both
// heads are left exactly where classifyPanel put them.
//
// It is a no-op unless the panel is actually incomplete: a complete panel has
// nothing to carry, and paying for two diff fetches to learn that would be
// waste on the common path.
func carryEquivalentRefresh(panel reviewpanel.Evidence, reviews []rawComment, comments []Comment, digest digestFn) reviewpanel.Evidence {
	if len(panel.Unknown) > 0 || (len(panel.Pending) == 0 && len(panel.Missing) == 0) {
		return panel
	}
	unsatisfied := append(append([]string(nil), panel.Pending...), panel.Missing...)
	candidates := candidateHeads(panel.Subject.HeadSHA, unsatisfied, reviews, comments)
	if len(candidates) == 0 {
		return panel
	}
	judged, err := digest(panel.Subject.HeadSHA)
	if err != nil {
		return panel
	}
	for _, candidate := range candidates {
		prior, err := digest(candidate)
		if err != nil || prior != judged {
			continue
		}
		carried := creditAt(panel, candidate, unsatisfied, reviews, comments)
		if len(carried.Completed) == len(panel.Completed) {
			continue
		}
		carried.Equivalence = &reviewpanel.Equivalence{
			ReviewedHeadSHA: candidate, DiffDigest: judged,
		}
		return carried
	}
	return panel
}

// creditAt moves every unsatisfied reviewer that completed a review of head
// into Completed, preserving the expected order and the pending/missing split
// for the rest.
func creditAt(panel reviewpanel.Evidence, head string, unsatisfied []string, reviews []rawComment, comments []Comment) reviewpanel.Evidence {
	pending := make(map[string]struct{}, len(panel.Pending))
	for _, name := range panel.Pending {
		pending[name] = struct{}{}
	}
	out := panel
	out.Pending, out.Missing = nil, nil
	for _, name := range unsatisfied {
		reviewer, ok := panelCompletion(name, head, reviews, comments)
		if ok {
			out.Completed = append(out.Completed, reviewer)
			continue
		}
		if _, ok := pending[name]; ok {
			out.Pending = append(out.Pending, name)
			continue
		}
		out.Missing = append(out.Missing, name)
	}
	return out
}

// candidateHeads lists earlier heads worth testing for equivalence: the exact
// commits an unsatisfied reviewer already reviewed, most recent first. Only
// full object names are offered — an abbreviated sentinel commit would name a
// head this code cannot pin, and the equivalence must be anchored to a head a
// reader can re-derive.
func candidateHeads(judgedHead string, unsatisfied []string, reviews []rawComment, comments []Comment) []string {
	var heads []string
	seen := map[string]struct{}{judgedHead: {}}
	add := func(sha string) {
		if _, ok := seen[sha]; ok || !reSHA.MatchString(sha) {
			return
		}
		seen[sha] = struct{}{}
		heads = append(heads, sha)
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		review := reviews[i]
		if review.User.Type != "Bot" || !completedReviewState(review.State) ||
			!actorPresentIn(unsatisfied, review.User.Login) {
			continue
		}
		add(review.CommitID)
	}
	for i := len(comments) - 1; i >= 0; i-- {
		match := attestationBody.FindStringSubmatch(comments[i].Body)
		if len(match) == 3 && contains(unsatisfied, match[1]) {
			add(match[2])
		}
	}
	return heads
}

// actorPresentIn reports whether a GitHub login is one of the named expected
// reviewers — the inverse direction of actorPresent, which matches one expected
// reviewer against many logins.
func actorPresentIn(expected []string, actor string) bool {
	for _, name := range expected {
		if actorMatches(name, actor) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// prDiffDigester digests the pull request's effective diff at an arbitrary head
// by asking GitHub to compare that head against the base tip: GitHub compares
// from the merge base, so each head is digested over exactly the change the PR
// proposes at that head — which is what a base refresh leaves untouched. The
// base tip is fetched once and reused, so testing N candidates costs N+1 reads.
func prDiffDigester(pr PRRef) digestFn {
	var base string
	return func(headSHA string) (string, error) {
		if !reSHA.MatchString(headSHA) {
			return "", fmt.Errorf("evidence: non-hex head for diff digest: %q", headSHA)
		}
		if base == "" {
			raw, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d", pr.Repo, pr.Number))
			if err != nil {
				return "", err
			}
			baseSHA, _, err := parsePullHeads(raw)
			if err != nil {
				return "", err
			}
			if !reSHA.MatchString(baseSHA) {
				return "", fmt.Errorf("evidence: non-hex base sha from api: %q", baseSHA)
			}
			base = baseSHA
		}
		raw, err := gh("api", "-H", "Accept: application/vnd.github.v3.diff",
			fmt.Sprintf("repos/%s/compare/%s...%s", pr.Repo, base, headSHA))
		if err != nil {
			return "", err
		}
		if len(raw) == 0 {
			return "", fmt.Errorf("evidence: empty diff at head %s", headSHA)
		}
		sum := sha256.Sum256(raw)
		return diffDigestAlgo + ":" + hex.EncodeToString(sum[:]), nil
	}
}
