package github

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

type fakeRunner struct {
	pages [][]byte
	calls [][]string
}

func (f *fakeRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{executable}, args...))
	joined := strings.Join(args, " ")
	switch {
	case joined == "--version":
		return []byte("gh version 2.90.0 (test)"), nil
	case joined == "api user --jq .login":
		return []byte("operator\n"), nil
	case strings.HasPrefix(joined, "api graphql"):
		if len(f.pages) == 0 {
			return nil, fmt.Errorf("no page")
		}
		page := f.pages[0]
		f.pages = f.pages[1:]
		return page, nil
	default:
		return nil, fmt.Errorf("unexpected argv: %s", joined)
	}
}

func TestNewRejectsAmbiguousScopes(t *testing.T) {
	for _, scopes := range [][]string{nil, {"all"}, {"repo:owner"}, {"user:a", "user:a"}} {
		if _, err := New("gh", scopes); err == nil {
			t.Fatalf("New(%#v) unexpectedly succeeded", scopes)
		}
	}
}

func TestCollectNormalizesAndMarksNestedTruncation(t *testing.T) {
	page := []byte(`{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"PR_1","repository":{"nameWithOwner":"o/r"},"number":1,"title":"title","url":"https://github.com/o/r/pull/1","author":{"login":"a"},"baseRefName":"main","headRefName":"feat","state":"OPEN","createdAt":"2026-07-13T10:00:00Z","updatedAt":"2026-07-13T11:00:00Z","mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED","reviewDecision":"REVIEW_REQUIRED","reviewRequests":{"totalCount":1},"statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":true},"nodes":[{"__typename":"CheckRun","name":"ci","status":"COMPLETED","conclusion":"FAILURE"}]}},"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[{"isResolved":false}]}}]}}}`)
	a, err := New("gh", []string{"repo:o/r"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{pages: [][]byte{page}}
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceOK || len(got.PullRequests) != 1 {
		t.Fatalf("unexpected result: %#v", got)
	}
	pr := got.PullRequests[0]
	if pr.DetailState != "truncated" || pr.UnresolvedThreads != 1 || pr.Checks[0].Conclusion != "FAILURE" {
		t.Fatalf("unexpected PR: %#v", pr)
	}
	graphql := strings.Join(f.calls[2], " ")
	if strings.Contains(graphql, "pr view") || !strings.Contains(graphql, QueryVersion()) || !strings.Contains(graphql, "repo:o/r") {
		t.Fatalf("unexpected graphql argv: %s", graphql)
	}
}

func TestCollectPagesScopesRoundRobinAndCapsAtFour(t *testing.T) {
	page := func(cursor string) []byte {
		return []byte(fmt.Sprintf(`{"data":{"search":{"pageInfo":{"hasNextPage":true,"endCursor":%q},"nodes":[]}}}`, cursor))
	}
	a, err := New("gh", []string{"user:z", "org:a"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{pages: [][]byte{page("a1"), page("z1"), page("a2"), page("z2")}}
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceDegraded || got.Receipt.ErrorCode != "inventory_truncated" || len(f.calls) != 6 {
		t.Fatalf("unexpected result: %#v calls=%d", got, len(f.calls))
	}
	queries := []string{strings.Join(f.calls[2], " "), strings.Join(f.calls[3], " "), strings.Join(f.calls[4], " "), strings.Join(f.calls[5], " ")}
	if !strings.Contains(queries[0], "org:a") || !strings.Contains(queries[1], "user:z") || !strings.Contains(queries[2], "after=a1") || !strings.Contains(queries[3], "after=z1") {
		t.Fatalf("not round robin: %#v", queries)
	}
}

func TestCollectRequiresMinimumVersion(t *testing.T) {
	a, _ := New("gh", []string{"user:a"})
	a.runner = runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("gh version 2.89.9"), nil
		}
		return nil, fmt.Errorf("unexpected call")
	})
	got := a.Collect(context.Background())
	if got.Receipt.ErrorCode != "unsupported_version" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return f(ctx, executable, args...)
}
