package evidence

import (
	"errors"
	"fmt"
	"testing"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

const (
	oldHead = "1111111111111111111111111111111111111111"
	newHead = "2222222222222222222222222222222222222222"
	olderHd = "3333333333333333333333333333333333333333"
)

var errNoDigest = errors.New("no digest")

func refreshBase(expected []string) reviewpanel.Evidence {
	return reviewpanel.Evidence{
		SchemaVersion: reviewpanel.SchemaVersion,
		Subject:       reviewpanel.Subject{Repo: "o/r", Number: 7, HeadSHA: newHead},
		Declaration:   reviewpanel.Declaration{Path: ".ship.json", Revision: "blob", Expected: expected},
	}
}

func botReview(login, state, commit string, id int64) rawComment {
	var rc rawComment
	rc.ID = id
	rc.User.Login = login
	rc.User.Type = "Bot"
	rc.State = state
	rc.CommitID = commit
	return rc
}

// digests answers from a fixed table; an unlisted head is an unknown digest,
// which must never read as equivalence.
func digests(table map[string]string) (digestFn, *int) {
	calls := 0
	return func(head string) (string, error) {
		calls++
		digest, ok := table[head]
		if !ok {
			return "", errNoDigest
		}
		return digest, nil
	}, &calls
}

func TestCarryEquivalentRefreshCreditsIdenticalDiff(t *testing.T) {
	panel := refreshBase([]string{"codex"})
	panel.Missing = []string{"codex"}
	reviews := []rawComment{botReview("chatgpt-codex-connector[bot]", "COMMENTED", oldHead, 11)}
	digest, _ := digests(map[string]string{newHead: "sha256:same", oldHead: "sha256:same"})

	got := carryEquivalentRefresh(panel, reviews, nil, digest)

	if len(got.Completed) != 1 || len(got.Missing) != 0 {
		t.Fatalf("panel not carried: %+v", got)
	}
	if got.Completed[0].HeadSHA != oldHead {
		t.Fatalf("carried reviewer anchored to %s, want the reviewed head", got.Completed[0].HeadSHA)
	}
	if got.Equivalence == nil || got.Equivalence.ReviewedHeadSHA != oldHead ||
		got.Equivalence.DiffDigest != "sha256:same" {
		t.Fatalf("equivalence not recorded: %+v", got.Equivalence)
	}
	if err := reviewpanel.Validate(got); err != nil {
		t.Fatalf("carried panel violates the contract: %v", err)
	}
}

func TestCarryEquivalentRefreshLeavesChangedDiffAlone(t *testing.T) {
	panel := refreshBase([]string{"codex"})
	panel.Missing = []string{"codex"}
	reviews := []rawComment{botReview("chatgpt-codex-connector[bot]", "COMMENTED", oldHead, 11)}
	digest, _ := digests(map[string]string{newHead: "sha256:new", oldHead: "sha256:old"})

	got := carryEquivalentRefresh(panel, reviews, nil, digest)

	if len(got.Missing) != 1 || got.Equivalence != nil {
		t.Fatalf("a real content change was carried forward: %+v", got)
	}
}

// An unreadable diff must park, never pass: absence of a digest is not
// evidence that nothing changed.
func TestCarryEquivalentRefreshFailsClosedOnDigestError(t *testing.T) {
	panel := refreshBase([]string{"codex"})
	panel.Missing = []string{"codex"}
	reviews := []rawComment{botReview("chatgpt-codex-connector[bot]", "COMMENTED", oldHead, 11)}
	digest, _ := digests(map[string]string{newHead: "sha256:same"}) // old head unreadable

	got := carryEquivalentRefresh(panel, reviews, nil, digest)

	if len(got.Missing) != 1 || got.Equivalence != nil {
		t.Fatalf("unreadable diff read as equivalence: %+v", got)
	}
}

// The common path — a complete panel at the judged head — costs no diff reads.
func TestCarryEquivalentRefreshSkipsCompletePanel(t *testing.T) {
	panel := refreshBase([]string{"codex"})
	panel.Completed = []reviewpanel.Reviewer{{
		Name: "codex", Actor: "codex[bot]", State: "COMMENTED", HeadSHA: newHead, ReviewID: 11,
	}}
	digest, calls := digests(map[string]string{newHead: "sha256:same"})

	got := carryEquivalentRefresh(panel, nil, nil, digest)

	if got.Equivalence != nil || *calls != 0 {
		t.Fatalf("complete panel paid for %d diff reads: %+v", *calls, got.Equivalence)
	}
}

// Unknown state is unknown for a reason — never resolve it with a refresh.
func TestCarryEquivalentRefreshSkipsUnknownPanel(t *testing.T) {
	panel := refreshBase([]string{"codex"})
	panel.Unknown = []string{"codex"}
	digest, calls := digests(map[string]string{newHead: "sha256:same", oldHead: "sha256:same"})

	got := carryEquivalentRefresh(panel, nil, nil, digest)

	if got.Equivalence != nil || *calls != 0 {
		t.Fatalf("unknown panel was carried: %+v", got)
	}
}

// A reviewer with no review at either head stays missing, and a pending one
// stays pending: the refresh carries reviews, it does not invent them.
func TestCarryEquivalentRefreshKeepsUncarriedDispositions(t *testing.T) {
	panel := refreshBase([]string{"codex", "cursor", "claude"})
	panel.Pending = []string{"cursor"}
	panel.Missing = []string{"codex", "claude"}
	reviews := []rawComment{botReview("chatgpt-codex-connector[bot]", "COMMENTED", oldHead, 11)}
	digest, _ := digests(map[string]string{newHead: "sha256:same", oldHead: "sha256:same"})

	got := carryEquivalentRefresh(panel, reviews, nil, digest)

	if len(got.Completed) != 1 || got.Completed[0].Name != "codex" {
		t.Fatalf("wrong reviewer carried: %+v", got.Completed)
	}
	if len(got.Pending) != 1 || got.Pending[0] != "cursor" {
		t.Fatalf("pending disposition lost: %+v", got.Pending)
	}
	if len(got.Missing) != 1 || got.Missing[0] != "claude" {
		t.Fatalf("missing disposition lost: %+v", got.Missing)
	}
	if err := reviewpanel.Validate(got); err != nil {
		t.Fatalf("carried panel violates the contract: %v", err)
	}
}

// A workflow attestation is head-bound evidence too, so it names a candidate
// head like a formal review does.
func TestCarryEquivalentRefreshUsesAttestationHead(t *testing.T) {
	panel := refreshBase([]string{"claude"})
	panel.Missing = []string{"claude"}
	comments := []Comment{{
		ID: 42, Author: attestationAuthor, IsBot: true,
		Body: fmt.Sprintf("%s\n**Reviewer:** claude\n**Reviewed commit:** `%s`", attestationMarker, oldHead),
	}}
	digest, _ := digests(map[string]string{newHead: "sha256:same", oldHead: "sha256:same"})

	got := carryEquivalentRefresh(panel, nil, comments, digest)

	if len(got.Completed) != 1 || got.Equivalence == nil {
		t.Fatalf("attestation head not tried: %+v", got)
	}
}

// Candidate heads are tried newest first, and only heads a reviewer actually
// reviewed are tried at all.
func TestCandidateHeadsOrderAndFilter(t *testing.T) {
	reviews := []rawComment{
		botReview("chatgpt-codex-connector[bot]", "COMMENTED", olderHd, 9),
		botReview("some-other[bot]", "COMMENTED", "4444444444444444444444444444444444444444", 10),
		botReview("chatgpt-codex-connector[bot]", "COMMENTED", oldHead, 11),
		botReview("chatgpt-codex-connector[bot]", "DISMISSED", "5555555555555555555555555555555555555555", 12),
	}
	got := candidateHeads(newHead, []string{"codex"}, reviews, nil)
	want := []string{oldHead, olderHd}
	if len(got) != len(want) {
		t.Fatalf("candidates=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates=%v want=%v", got, want)
		}
	}
}
