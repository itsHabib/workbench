package match

import (
	"net/url"
	"testing"

	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
)

// trackerKey is the tight, high-stakes key from the spec §5 example: PROJ-scoped
// read/comment rules plus a scalar query rule.
func trackerKey() manifest.Key {
	released := "released"
	return manifest.Key{
		Secret:   "wincred:tracker-pat",
		Upstream: "https://issues.example.com",
		Inject:   []manifest.Injection{{Kind: "header", Name: "Authorization", Template: "Bearer {secret}"}},
		Actions: map[string]manifest.Action{
			"read": {Rules: []manifest.Rule{
				{Methods: []string{"GET"}, Path: "/rest/api/2/issue/PROJ-*"},
				{Methods: []string{"GET"}, Path: "/rest/api/2/project/PROJ/versions",
					Query: map[string]manifest.Predicate{"state": {Equals: &released, Occurs: "once"}}},
			}},
			"comment": {Rules: []manifest.Rule{
				{Methods: []string{"POST"}, Path: "/rest/api/2/issue/PROJ-*/comment"},
			}},
		},
	}
}

func TestCanonicalizeRefusesAdversarialTargets(t *testing.T) {
	// Every one of these must REFUSE (spec §7 C): custody never best-effort
	// matches an ambiguous encoding, it fails closed.
	cases := []struct {
		name   string
		target string
	}{
		{"encoded-slash", "/tracker/rest/api/2/issue/PROJ-1%2F..%2FOTHER-9"},
		{"double-encoded-slash", "/tracker/rest/api/2/issue/PROJ-1%252F..%252FOTHER-9"},
		{"double-encoded-escape", "/tracker/rest/%25/PROJ-1"},
		{"encoded-backslash", "/tracker/rest%5Capi"},
		{"literal-backslash", "/tracker/rest\\api"},
		{"encoded-nul", "/tracker/rest/%00/PROJ-1"},
		{"encoded-control", "/tracker/rest/%0a/PROJ-1"},
		{"full-width", "/tracker/ｒｅｓｔ/PROJ-1"},
		{"non-ascii-literal", "/tracker/rest/PROJ-é"},
		{"truncated-escape", "/tracker/rest/%2"},
		{"stray-percent", "/tracker/rest/%zz/PROJ-1"},
		{"absolute-form", "http://evil.example/tracker/rest"},
		{"authority-form", "issues.example.com:443"},
		{"asterisk-form", "*"},
		{"empty", ""},
		{"fragment", "/tracker/rest#frag"},
		{"bad-query", "/tracker/rest?a=%2"},
		{"semicolon-query", "/tracker/rest?jql=a;jql=b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Canonicalize(tc.target); err == nil {
				t.Fatalf("Canonicalize(%q) = nil error, want refusal", tc.target)
			}
		})
	}
}

func TestCanonicalizeResolvesDotSegments(t *testing.T) {
	// A literal ".." is RESOLVED (not refused), and the resolved path must not
	// match PROJ-* — the load-bearing adversarial case of spec §7 C.
	target, err := Canonicalize("/tracker/rest/api/2/issue/PROJ-1/../OTHER-9")
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if target.Key != "tracker" {
		t.Fatalf("key = %q, want tracker", target.Key)
	}
	if target.Path != "/rest/api/2/issue/OTHER-9" {
		t.Fatalf("path = %q, want /rest/api/2/issue/OTHER-9", target.Path)
	}
	if _, ok := Match(trackerKey(), []string{"read"}, "GET", target.Path, target.Query); ok {
		t.Fatal("PROJ-* must not match a dot-segment-resolved OTHER-9")
	}
}

func TestCanonicalizeDotSegmentMovesKeyBoundary(t *testing.T) {
	// Canonicalizing the WHOLE target before splitting the key means a dot-segment
	// can move the effective key — but the grant is then checked against the
	// resolved key, so this cannot escalate: key becomes "evil", not "tracker".
	target, err := Canonicalize("/tracker/../evil/rest")
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if target.Key != "evil" {
		t.Fatalf("key = %q, want evil (boundary resolved before split)", target.Key)
	}
}

func TestMatchHappyAndDenied(t *testing.T) {
	k := trackerKey()
	t.Run("read matches issue", func(t *testing.T) {
		f, ok := Match(k, []string{"read"}, "GET", "/rest/api/2/issue/PROJ-123", url.Values{})
		if !ok {
			t.Fatal("expected match")
		}
		if f.Label() != "read[0]" {
			t.Fatalf("label = %q, want read[0]", f.Label())
		}
	})
	t.Run("post comment denied under read grant", func(t *testing.T) {
		if _, ok := Match(k, []string{"read"}, "POST", "/rest/api/2/issue/PROJ-123/comment", url.Values{}); ok {
			t.Fatal("read grant must not cover POST comment")
		}
	})
	t.Run("post comment matches under comment grant", func(t *testing.T) {
		f, ok := Match(k, []string{"comment"}, "POST", "/rest/api/2/issue/PROJ-123/comment", url.Values{})
		if !ok || f.Label() != "comment[0]" {
			t.Fatalf("comment match: ok=%v label=%q", ok, f.Label())
		}
	})
	t.Run("wrong project denied", func(t *testing.T) {
		if _, ok := Match(k, []string{"read"}, "GET", "/rest/api/2/issue/OTHER-9", url.Values{}); ok {
			t.Fatal("OTHER-9 must not match PROJ-*")
		}
	})
	t.Run("extra path segment denied", func(t *testing.T) {
		if _, ok := Match(k, []string{"read"}, "GET", "/rest/api/2/issue/PROJ-1/subresource", url.Values{}); ok {
			t.Fatal("PROJ-* is a single segment; a trailing segment must not match")
		}
	})
	t.Run("reserved char denied", func(t *testing.T) {
		if _, ok := Match(k, []string{"read"}, "GET", "/rest/api/2/issue/PROJ-1;x", url.Values{}); ok {
			t.Fatal("* must not admit reserved routing punctuation")
		}
	})
}

func TestMatchQueryPredicate(t *testing.T) {
	k := trackerKey()
	path := "/rest/api/2/project/PROJ/versions"
	t.Run("exact value matches", func(t *testing.T) {
		f, ok := Match(k, []string{"read"}, "GET", path, url.Values{"state": {"released"}})
		if !ok {
			t.Fatal("expected match")
		}
		if got := f.Matched["state"]; len(got) != 1 || got[0] != "released" {
			t.Fatalf("matched_query state = %v, want [released]", got)
		}
	})
	t.Run("wrong value denied", func(t *testing.T) {
		if _, ok := Match(k, []string{"read"}, "GET", path, url.Values{"state": {"archived"}}); ok {
			t.Fatal("non-anchored value must be denied")
		}
	})
	t.Run("repeated param denied", func(t *testing.T) {
		if _, ok := Match(k, []string{"read"}, "GET", path, url.Values{"state": {"released", "released"}}); ok {
			t.Fatal("occurs-once must deny a repeated param")
		}
	})
	t.Run("unlisted param denied by default", func(t *testing.T) {
		v := url.Values{"state": {"released"}, "expand": {"changelog"}}
		if _, ok := Match(k, []string{"read"}, "GET", path, v); ok {
			t.Fatal("an unlisted param must be denied by default")
		}
	})
}

func TestMatchAllowExtraParams(t *testing.T) {
	all := "all"
	_ = all
	k := manifest.Key{
		Actions: map[string]manifest.Action{
			"read": {Rules: []manifest.Rule{
				{Methods: []string{"GET"}, Path: "/search", AllowExtraParams: true,
					Query: map[string]manifest.Predicate{"q": {Equals: strptr("x"), Occurs: "once"}}},
			}},
		},
	}
	v := url.Values{"q": {"x"}, "page": {"2"}}
	if _, ok := Match(k, []string{"read"}, "GET", "/search", v); !ok {
		t.Fatal("allowExtraParams must permit an unlisted scalar param")
	}
}

func TestSuggestNamesCoveringAction(t *testing.T) {
	k := trackerKey()
	action, ok := Suggest(k, "POST", "/rest/api/2/issue/PROJ-123/comment", url.Values{})
	if !ok || action != "comment" {
		t.Fatalf("Suggest = %q, %v; want comment, true", action, ok)
	}
	if _, ok := Suggest(k, "DELETE", "/rest/api/2/issue/PROJ-123", url.Values{}); ok {
		t.Fatal("no action covers DELETE; Suggest should report none")
	}
}

func TestDoubleStarMatchesAnyPath(t *testing.T) {
	loose := manifest.Key{
		Actions: map[string]manifest.Action{
			"all": {Rules: []manifest.Rule{{Methods: []string{"*"}, Path: "/**"}}},
		},
	}
	for _, p := range []string{"/", "/a", "/a/b/c", "/v1/resource/42"} {
		if _, ok := Match(loose, []string{"all"}, "GET", p, url.Values{}); !ok {
			t.Fatalf("/** must match %q", p)
		}
	}
}

func strptr(s string) *string { return &s }
