package evidence

import (
	"errors"
	"strings"
	"testing"
)

func TestTooLarge(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"the 406 rejection", errors.New(`evidence: gh [pr diff]: could not find pull request diff: HTTP 406: Sorry, the diff exceeded the maximum number of lines (20000) (https://api.github.com/repos/o/r/pulls/51)`), true},
		{"unrelated 406 without the body", errors.New("evidence: gh [pr diff]: HTTP 406: Not Acceptable"), false},
		{"auth failure", errors.New("evidence: gh [pr diff]: HTTP 401: Bad credentials"), false},
		{"not found", errors.New("evidence: gh [pr diff]: HTTP 404: Not Found"), false},
		{"outage", errors.New("evidence: gh [pr diff]: HTTP 503: 503 Service Unavailable"), false},
		{"plain exec failure", errors.New(`evidence: gh [pr diff]: exec: "gh": executable file not found`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tooLarge(c.err); got != c.want {
				t.Fatalf("tooLarge(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestParsePullHeads(t *testing.T) {
	base, head, err := parsePullHeads([]byte(`{"base":{"sha":"aaa"},"head":{"sha":"bbb"}}`))
	if err != nil {
		t.Fatalf("parsePullHeads: %v", err)
	}
	if base != "aaa" || head != "bbb" {
		t.Fatalf("parsePullHeads = %q, %q; want aaa, bbb", base, head)
	}

	for name, raw := range map[string]string{
		"not json":     `<html>`,
		"missing head": `{"base":{"sha":"aaa"},"head":{}}`,
		"missing base": `{"base":{},"head":{"sha":"bbb"}}`,
		"empty":        `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parsePullHeads([]byte(raw)); err == nil {
				t.Fatalf("parsePullHeads(%s): want error, got nil", raw)
			}
		})
	}
}

func TestParseMergeBase(t *testing.T) {
	mb, err := parseMergeBase([]byte(`{"status":"ahead","merge_base_commit":{"sha":"ccc"}}`))
	if err != nil {
		t.Fatalf("parseMergeBase: %v", err)
	}
	if mb != "ccc" {
		t.Fatalf("parseMergeBase = %q, want ccc", mb)
	}

	for name, raw := range map[string]string{
		"not json":   `<html>`,
		"missing mb": `{"status":"ahead"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMergeBase([]byte(raw)); err == nil {
				t.Fatalf("parseMergeBase(%s): want error, got nil", raw)
			}
		})
	}
}

func TestFallbackDiffRequiresPinnedHead(t *testing.T) {
	// A view payload with no headRefOid must refuse before any network read.
	for name, view := range map[string]string{
		"empty object": `{}`,
		"empty oid":    `{"headRefOid":""}`,
		"not json":     `<html>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fallbackDiff(PRRef{Repo: "o/r", Number: 1}, []byte(view))
			if err == nil {
				t.Fatal("fallbackDiff: want error, got nil")
			}
			if !strings.Contains(err.Error(), "headRefOid") {
				t.Fatalf("fallbackDiff error = %v; want the missing-pin refusal", err)
			}
		})
	}
}

func TestReSHA(t *testing.T) {
	good := []string{
		"7e8c6474129ea043172df55796b182fbf54f6e9c",                         // sha-1
		"1111111111111111111111111111111111111111111111111111111111111111", // sha-256
	}
	bad := []string{
		"", "HEAD", "--upload-pack=touch pwned", "-x",
		"7e8c6474129ea043172df55796b182fbf54f6e9C",  // uppercase
		"7e8c6474129ea043172df55796b182fbf54f6e9",   // 39
		"g e8c6474129ea043172df55796b182fbf54f6e9c", // non-hex
	}
	for _, s := range good {
		if !reSHA.MatchString(s) {
			t.Errorf("reSHA rejected valid sha %q", s)
		}
	}
	for _, s := range bad {
		if reSHA.MatchString(s) {
			t.Errorf("reSHA accepted invalid sha %q", s)
		}
	}
}

func TestSubcommand(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"init", "-q"}, "init"},
		{[]string{"-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential", "fetch", "-q", "--depth=1", "--end-of-options", "url", "mb", "head"}, "fetch"},
		{[]string{"-c", "attr.tree=abc", "diff", "--no-color", "mb", "head"}, "diff"},
		{[]string{"-c", "x=y"}, "git"},
	}
	for _, c := range cases {
		if got := subcommand(c.args); got != c.want {
			t.Errorf("subcommand(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestScrubGit(t *testing.T) {
	in := []string{"PATH=/bin", "GIT_DIR=/evil", "HOME=/h", "GIT_EXTERNAL_DIFF=x", "git_config=y"}
	got := scrubGit(in)
	for _, kv := range got {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			t.Errorf("scrubGit left a GIT_ var: %q", kv)
		}
	}
	// non-GIT vars survive; the case-insensitive "git_config" is dropped
	want := map[string]bool{"PATH=/bin": true, "HOME=/h": true}
	if len(got) != len(want) {
		t.Fatalf("scrubGit = %v; want exactly %v", got, want)
	}
	for _, kv := range got {
		if !want[kv] {
			t.Errorf("scrubGit kept unexpected %q", kv)
		}
	}
}
