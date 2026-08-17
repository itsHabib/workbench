package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ghStub routes a fake `gh api` by path: the repo read answers with a default
// branch, and the protection read answers per the case under test. It records
// every path called, so "which branch did we actually ask about" is assertable.
type ghStub struct {
	defaultBranch string
	repoErr       error
	protection    string
	protStderr    string
	protErr       error
	paths         []string
}

func (g *ghStub) run(_ context.Context, path string) ([]byte, []byte, error) {
	g.paths = append(g.paths, path)
	if !strings.Contains(path, "/branches/") {
		if g.repoErr != nil {
			return nil, []byte("gh: Bad credentials (HTTP 401)"), g.repoErr
		}
		return []byte(`{"default_branch":"` + g.defaultBranch + `"}`), nil, nil
	}
	if g.protErr != nil {
		return nil, []byte(g.protStderr), g.protErr
	}
	return []byte(g.protection), nil, nil
}

// TestLookupProtectionResolvesTheDefaultBranch pins the fix for the assumed
// `main`: protection is read from the repository's ACTUAL default branch. On a
// `master` repo the assumed path 404s, and a 404 reads as unprotected — so the
// assumption would report a strict repo as having no protection at all, the one
// error direction a sweep must not make.
func TestLookupProtectionResolvesTheDefaultBranch(t *testing.T) {
	g := &ghStub{
		defaultBranch: "master",
		protection:    `{"required_status_checks":{"strict":true,"contexts":["Go"]}}`,
	}
	p, err := lookupProtectionContext(context.Background(), "o/r", g.run)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Protected || !p.Strict || p.Branch != "master" {
		t.Fatalf("protection must come from the real default branch: %+v", p)
	}
	want := []string{"repos/o/r", "repos/o/r/branches/master/protection"}
	if strings.Join(g.paths, "|") != strings.Join(want, "|") {
		t.Errorf("gh paths = %v, want %v", g.paths, want)
	}
}

// TestLookupProtectionEscapesTheBranchSegment pins that the resolved branch is
// escaped as ONE path segment. A valid default branch like `release/v1` would
// otherwise split into extra segments, 404, and be read as "unprotected" —
// recreating the exact false-negative resolving the branch was added to
// prevent. The repo stays raw: `owner/name` really is two segments.
func TestLookupProtectionEscapesTheBranchSegment(t *testing.T) {
	g := &ghStub{
		defaultBranch: "release/v1",
		protection:    `{"required_status_checks":{"strict":true}}`,
	}
	p, err := lookupProtectionContext(context.Background(), "o/r", g.run)
	if err != nil {
		t.Fatal(err)
	}
	want := "repos/o/r/branches/release%2Fv1/protection"
	if g.paths[1] != want {
		t.Fatalf("protection path = %q, want %q", g.paths[1], want)
	}
	if !p.Protected || p.Branch != "release/v1" {
		t.Errorf("the projection must carry the unescaped branch: %+v", p)
	}
}

// TestLookupOpenPRsCappedListingIsIncomplete pins that a full page is reported
// as a TRUNCATED read, not a short answer. Returning it as success is unsafe
// both ways: the inbox would reconcile an unseen-but-open PR as "not open" and
// drop its row, and preflight would fold the repo into an all-clear while a PR
// past the page still needed a mint.
func TestLookupOpenPRsCappedListingIsIncomplete(t *testing.T) {
	var page []string
	for i := 1; i <= prListLimit; i++ {
		page = append(page, fmt.Sprintf(`{"number":%d,"title":"t","headRefOid":"sha","url":"u"}`, i))
	}
	full := "[" + strings.Join(page, ",") + "]"
	run := func(context.Context, string) ([]byte, []byte, error) {
		return []byte(full), nil, nil
	}
	_, err := lookupOpenPRsContext(context.Background(), "o/r", run)
	if err == nil {
		t.Fatal("a capped listing must not be reported as a complete read")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("the error should name the truncation, got %v", err)
	}

	// One PR short of the page is a complete read and stays a success.
	short := "[" + strings.Join(page[:prListLimit-1], ",") + "]"
	runShort := func(context.Context, string) ([]byte, []byte, error) {
		return []byte(short), nil, nil
	}
	prs, err := lookupOpenPRsContext(context.Background(), "o/r", runShort)
	if err != nil {
		t.Fatalf("an unsaturated page is a complete read: %v", err)
	}
	if len(prs) != prListLimit-1 {
		t.Errorf("got %d PRs, want %d", len(prs), prListLimit-1)
	}
}

// TestLookupProtectionUnresolvedBranchIsAnError pins that a failure to resolve
// the default branch never falls back to a guessed name: guessing is what
// produces the false "unprotected" this path exists to prevent.
func TestLookupProtectionUnresolvedBranchIsAnError(t *testing.T) {
	g := &ghStub{repoErr: errors.New("exit status 1")}
	if _, err := lookupProtectionContext(context.Background(), "o/r", g.run); err == nil {
		t.Fatal("an unresolvable default branch must be an error, not a guess")
	}
	if len(g.paths) != 1 {
		t.Errorf("protection must not be read against a guessed branch: %v", g.paths)
	}
}

// TestLookupProtectionUnprotectedIsAnAnswer pins the one gh failure that is a
// FACT, not an error: an unprotected default branch answers the protection
// endpoint with 404, and "no protection — gate and the guard are the only
// boundary" is exactly what the sweep needs to know. It is trustworthy only
// because the branch was resolved first.
func TestLookupProtectionUnprotectedIsAnAnswer(t *testing.T) {
	g := &ghStub{
		defaultBranch: "main",
		protStderr:    "gh: Not Found (HTTP 404)",
		protErr:       errors.New("exit status 1"),
	}
	p, err := lookupProtectionContext(context.Background(), "o/r", g.run)
	if err != nil {
		t.Fatalf("404 must read as unprotected, not an error: %v", err)
	}
	if p.Protected || p.Branch != "main" {
		t.Errorf("404 must report Protected=false on the resolved branch, got %+v", p)
	}
}

// TestLookupProtectionErrorsAreNotUnprotected is the safety half: a repo gate
// could not READ must never be reported as unprotected, or a sweep would plan a
// refresh-free merge against a strict repo on the strength of an auth failure.
func TestLookupProtectionErrorsAreNotUnprotected(t *testing.T) {
	g := &ghStub{
		defaultBranch: "main",
		protStderr:    "gh: Bad credentials (HTTP 401)",
		protErr:       errors.New("exit status 1"),
	}
	if _, err := lookupProtectionContext(context.Background(), "o/r", g.run); err == nil {
		t.Fatal("an auth failure must stay an error")
	}
}

// TestLookupProtectionCancelledReportsContext pins that a timeout surfaces as
// the context error rather than gh's exit status.
func TestLookupProtectionCancelledReportsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := func(context.Context, string) ([]byte, []byte, error) {
		return nil, nil, errors.New("signal: killed")
	}
	_, err := lookupProtectionContext(ctx, "o/r", run)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("want the context error, got %v", err)
	}
}

// TestLookupProtectionDecodesStrictShape pins the read a sweep plans around:
// strict, the required contexts, and conversation resolution.
func TestLookupProtectionDecodesStrictShape(t *testing.T) {
	g := &ghStub{
		defaultBranch: "main",
		protection: `{"required_status_checks":{"strict":true,"contexts":["ubuntu-latest","windows-latest"]},
		              "required_conversation_resolution":{"enabled":true}}`,
	}
	p, err := lookupProtectionContext(context.Background(), "o/r", g.run)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Protected || !p.Strict || !p.ConversationResolution {
		t.Fatalf("protection shape wrong: %+v", p)
	}
	if strings.Join(p.Contexts, ",") != "ubuntu-latest,windows-latest" {
		t.Errorf("required contexts wrong: %+v", p.Contexts)
	}
}

// TestRepeatedFlagSplits pins that a sweep scope reads the same typed flag by
// flag or pasted as one comma-separated string.
func TestRepeatedFlagSplits(t *testing.T) {
	var r repeatedFlag
	if err := r.Set("a/b, c/d"); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("e/f"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r, "|") != "a/b|c/d|e/f" {
		t.Fatalf("repeated flag = %v", r)
	}
}
