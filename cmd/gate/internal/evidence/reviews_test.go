package evidence

import "testing"

func rawReview(login, typ, body, commit, state string) rawComment {
	var rc rawComment
	rc.User.Login = login
	rc.User.Type = typ
	rc.Body = body
	rc.CommitID = commit
	rc.State = state
	return rc
}

func TestReviewBodies(t *testing.T) {
	// Oldest-first, as the reviews endpoint returns them.
	in := []rawComment{
		rawReview("codex[bot]", "Bot", "requests changes: bug X", "sha1", "CHANGES_REQUESTED"),
		rawReview("codex[bot]", "Bot", "looks good now", "sha1", "APPROVED"),      // supersedes the above (same author)
		rawReview("cursor[bot]", "Bot", "withdrawn finding", "sha1", "DISMISSED"), // dropped: dismissed
		rawReview("copilot", "Bot", "", "sha1", "APPROVED"),                       // dropped: empty body
		rawReview("claude[bot]", "Bot", "one concern", "sha1", "COMMENTED"),
	}
	out := reviewBodies(in)

	// Expect exactly: codex's LATEST ("looks good now") + claude's ("one concern").
	if len(out) != 2 {
		t.Fatalf("expected 2 review bodies (latest codex + claude), got %d: %+v", len(out), out)
	}
	byAuthor := map[string]Comment{}
	for _, c := range out {
		byAuthor[c.Author] = c
	}
	if c, ok := byAuthor["codex[bot]"]; !ok || c.Body != "looks good now" {
		t.Fatalf("codex must be its LATEST non-dismissed review, got %+v", c)
	}
	if _, ok := byAuthor["cursor[bot]"]; ok {
		t.Fatal("a DISMISSED review must be dropped")
	}
	if _, ok := byAuthor["copilot"]; ok {
		t.Fatal("a body-less review must be dropped")
	}
	// Bodies stay anchored to the commit they reviewed, so staleComment (in the
	// reviews rung) drops one from an earlier head.
	if byAuthor["claude[bot]"].CommitID != "sha1" {
		t.Fatalf("review body must carry the review's commit_id, got %q", byAuthor["claude[bot]"].CommitID)
	}
}

func TestReviewBodiesAllDismissedOrEmpty(t *testing.T) {
	in := []rawComment{
		rawReview("codex[bot]", "Bot", "old finding", "sha1", "DISMISSED"),
		rawReview("codex[bot]", "Bot", "", "sha1", "APPROVED"),
	}
	if out := reviewBodies(in); len(out) != 0 {
		t.Fatalf("an author with only dismissed/empty reviews contributes nothing, got %+v", out)
	}
}
