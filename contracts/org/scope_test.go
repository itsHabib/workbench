package org

import "testing"

func TestInScope(t *testing.T) {
	cases := []struct {
		entry, work string
		want        bool
	}{
		// Exact.
		{"github:owner/repo", "github:owner/repo", true},
		// Repo grain covers its PRs and paths.
		{"github:owner/repo", "github:owner/repo#88", true},
		{"github:owner/repo", "github:owner/repo/docs", true},
		// A prefix that is not a segment is not membership.
		{"github:owner/repo", "github:owner/repository", false},
		{"github:owner/my", "github:owner/my-repo", false},
		// Cross-scheme never matches — the field report's rule.
		{"github:owner/repo", "jira:PROJ-123", false},
		{"jira:PROJ-123", "github:owner/repo#88", false},
		// Explicit open prefixes state their grain.
		{"jira:PROJ-", "jira:PROJ-123", true},
		{"jira:PROJ-", "jira:OTHER-1", false},
		{"github:owner/", "github:owner/any", true},
		{"jira:", "jira:PROJ-123", true},
		{"jira:", "github:owner/repo", false},
		// Without the trailing separator, project grain is not implied.
		{"jira:PROJ", "jira:PROJ-123", false},
		// Nonsense schemes match nothing but themselves.
		{"github:owner/repo", "banana:whatever", false},
		// Degenerate inputs.
		{"", "github:owner/repo", false},
		{"github:owner/repo", "", false},
	}
	for _, c := range cases {
		if got := InScope(c.entry, c.work); got != c.want {
			t.Errorf("InScope(%q, %q) = %v, want %v", c.entry, c.work, got, c.want)
		}
	}
}

func TestMatchScope(t *testing.T) {
	scope := []string{"jira:PROJ-", "github:owner/repo"}
	if entry, ok := MatchScope(scope, "github:owner/repo#7"); !ok || entry != "github:owner/repo" {
		t.Errorf("MatchScope repo = %q, %v", entry, ok)
	}
	if entry, ok := MatchScope(scope, "jira:PROJ-9"); !ok || entry != "jira:PROJ-" {
		t.Errorf("MatchScope ticket = %q, %v", entry, ok)
	}
	if _, ok := MatchScope(scope, "banana:whatever"); ok {
		t.Error("MatchScope accepted a foreign scheme")
	}
	if _, ok := MatchScope(nil, "jira:PROJ-9"); ok {
		t.Error("MatchScope matched an empty scope")
	}
}
