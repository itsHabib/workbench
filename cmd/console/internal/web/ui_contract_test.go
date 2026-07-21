package web

import (
	"regexp"
	"strings"
	"testing"
)

// The embedded page is inline HTML+JS with no build step, so the one class of
// regression a Go test can't otherwise see is drift between the markup and the
// script: an element the JS binds to gets renamed, or an /api path the JS
// fetches no longer routes. These two tests pin exactly that seam — cheaply, in
// stdlib `go test`, with no browser. What they deliberately do NOT cover is JS
// runtime behavior (that a fetch resolves and renders without throwing); that
// needs a headless browser and stays a manual/agent check for now.

// apiRefRe finds every /api/... path literal the page references.
var apiRefRe = regexp.MustCompile(`/api/[a-z/]+`)

// TestPageFetchPathsAreLiveRoutes asserts every /api path the embedded JS
// fetches resolves to a real server route — so renaming a route in Go without
// updating the page (or vice versa) fails the build instead of 404ing at
// runtime in the operator's browser.
func TestPageFetchPathsAreLiveRoutes(t *testing.T) {
	page := string(appPage)
	refs := map[string]bool{}
	for _, m := range apiRefRe.FindAllString(page, -1) {
		refs[m] = true
	}
	if len(refs) == 0 {
		t.Fatal("no /api paths found in the page — did the fetch calls change shape?")
	}

	// Stub every gate subcommand so a real route answers 200 and only a missing
	// route 404s.
	s := New(clientReturning(map[string]string{
		"next": `{"parked":[],"grants":[]}`, "explain": `{"run":"run_abc123","artifacts":[]}`, "audit": "chain intact\n",
	}), testHost)

	for ref := range refs {
		target := ref
		if strings.HasSuffix(ref, "/") { // a prefix like /api/run/ needs a segment
			target += "run_abc123"
		}
		rec := do(t, s, "GET", target, testHost)
		if rec.Code == 404 {
			t.Errorf("page fetches %q but the server has no route for %q", ref, target)
		}
	}
}

// TestPageMountPointsExist asserts the static elements the script binds to on
// load are present in the markup — catching the orphaned-element regression
// (rename the container, the JS silently no-ops).
func TestPageMountPointsExist(t *testing.T) {
	page := string(appPage)
	// The four ids the IIFE resolves up front (getElementById at the top of the
	// script). Dynamically-created ids (the graph, rail markers) are built by the
	// JS and correctly absent from the static markup.
	for _, id := range []string{"view", "statusline", "banner", "refresh"} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("script binds #%s but the markup has no element with that id", id)
		}
	}
	if !strings.Contains(page, "<script>") {
		t.Error("page has no inline script block")
	}
}

func TestDocketKeepsPRIdentityActionable(t *testing.T) {
	page := string(appPage)
	for _, want := range []string{
		`class="pr-link"`,
		`target="_blank" rel="noopener noreferrer"`,
		`data.unattributed || []`,
		`diagnostic history, not counted above`,
		`PR state unknown`,
		`p.pr_state_reason`,
		`run ' + esc(p.run)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("docket page missing actionable-identity contract %q", want)
		}
	}
}
