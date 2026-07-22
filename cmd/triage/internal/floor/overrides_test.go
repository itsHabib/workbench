package floor

import (
	"reflect"
	"strings"
	"testing"
)

// classifyRepoDiff is a test helper: parse raw diff text and classify it under a
// repo identity, so the compiled-in path overrides for that repo apply.
func classifyRepoDiff(t *testing.T, raw, repo string) Result {
	t.Helper()
	d, err := ParseUnifiedDiff(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff: %v", err)
	}
	return ClassifyRepo(d, repo)
}

// goFileDiff builds a minimal one-file Go diff with a benign added line, so the
// only thing that can move the floor is a path rule or an override — never a
// content signal.
func goFileDiff(path string) string {
	return "diff --git a/" + path + " b/" + path + "\n+++ b/" + path + "\n@@\n+func helper() {}\n"
}

const workbench = "itsHabib/workbench"

// TestOverrideBandSplit is the acceptance table: with -repo itsHabib/workbench,
// the T3 band (merge-authorization state, the verifier ladder, the exit-code
// seam) floors T3, while the broader gate/driver/triage machinery floors T2.
func TestOverrideBandSplit(t *testing.T) {
	cases := []struct {
		name string
		path string
		want Tier
	}{
		// T3 band — merge-authorization + the load-bearing exit-code contract.
		{"gate state", "cmd/gate/internal/state/anchor.go", T3},
		{"gate verify ladder", "cmd/gate/internal/verify/floor.go", T3},
		{"gate capability grants", "cmd/gate/internal/capability/capability.go", T3},
		{"gate main exit-code seam", "cmd/gate/main.go", T3},
		{"override table self-classification", "cmd/triage/internal/floor/overrides.go", T3},
		// T2 band — the rest of gate, plus triage's own classifier machinery.
		{"gate evidence", "cmd/gate/internal/evidence/evidence.go", T2},
		{"gate observe", "cmd/gate/internal/observe/explain.go", T2},
		{"triage machinery", "cmd/triage/internal/floor/parse.go", T2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyRepoDiff(t, goFileDiff(c.path), workbench).Floor
			if got != c.want {
				t.Fatalf("floor(%s) = %s, want %s", c.path, got, c.want)
			}
		})
	}
}

// TestOverrideNoRepoPassthrough: with no -repo, the override paths classify at
// their pre-override base tier (plain internal Go → T1). This is the no-flag
// byte-identical guarantee: nothing raised without a repo.
func TestOverrideNoRepoPassthrough(t *testing.T) {
	paths := []string{
		"cmd/gate/internal/state/anchor.go",
		"cmd/gate/internal/verify/floor.go",
		"cmd/gate/main.go",
		"cmd/gate/internal/evidence/evidence.go",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			got := classifyRepoDiff(t, goFileDiff(p), "").Floor
			if got != T1 {
				t.Fatalf("no-repo floor(%s) = %s, want T1 (base, unraised)", p, got)
			}
		})
	}
}

// TestOverrideEmptyRepoByteIdentical: ClassifyRepo(d, "") must equal Classify(d)
// field-for-field, including the signal list — the whole-Result guarantee that
// the no-repo path is byte-identical to the pre-override floor.
func TestOverrideEmptyRepoByteIdentical(t *testing.T) {
	diffs := []string{
		goFileDiff("cmd/gate/internal/state/anchor.go"),
		goFileDiff("cmd/gate/main.go"),
		"diff --git a/internal/auth/session.go b/internal/auth/session.go\n+++ b/internal/auth/session.go\n@@\n+func check(){}\n",
		"diff --git a/README.md b/README.md\n+++ b/README.md\n@@\n+a line\n",
	}
	for _, raw := range diffs {
		d, err := ParseUnifiedDiff(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("ParseUnifiedDiff: %v", err)
		}
		if want, got := Classify(d), ClassifyRepo(d, ""); !reflect.DeepEqual(want, got) {
			t.Fatalf("ClassifyRepo(d, \"\") != Classify(d)\n want %+v\n  got %+v", want, got)
		}
	}
}

// TestOverrideUnknownRepoInert: a repo with no override table contributes no
// globs — same result as no repo at all. Nothing global ever applies.
func TestOverrideUnknownRepoInert(t *testing.T) {
	raw := goFileDiff("cmd/gate/internal/state/anchor.go")
	d, err := ParseUnifiedDiff(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff: %v", err)
	}
	if want, got := Classify(d), ClassifyRepo(d, "some/unknown-repo"); !reflect.DeepEqual(want, got) {
		t.Fatalf("unknown repo raised the floor\n want %+v\n  got %+v", want, got)
	}
}

// TestOverrideRaiseOnly: an override never lowers. A file whose base floor is
// already T3 (a removed authz call — a content signal) sits under only a T2
// override; max(T3, T2) stays T3.
func TestOverrideRaiseOnly(t *testing.T) {
	// cmd/gate/internal/evidence/** carries only the T2 "gate machinery" override,
	// but this diff removes an authorize() call → content signal T3.
	raw := "diff --git a/cmd/gate/internal/evidence/evidence.go b/cmd/gate/internal/evidence/evidence.go\n" +
		"+++ b/cmd/gate/internal/evidence/evidence.go\n@@\n" +
		"-\tif err := authorize(ctx, user); err != nil { return err }\n+\t// fast path\n"
	if got := classifyRepoDiff(t, raw, workbench).Floor; got != T3 {
		t.Fatalf("raise-only violated: floor = %s, want T3 (override T2 must not lower a T3 base)", got)
	}
}

// TestOverrideT3Preserved: a diff that already floors T3 from a base rule (an
// auth path) stays T3 with overrides present but not matching — overrides only
// ever add, never remove, a signal.
func TestOverrideT3Preserved(t *testing.T) {
	raw := "diff --git a/internal/auth/session.go b/internal/auth/session.go\n+++ b/internal/auth/session.go\n@@\n+func check(){}\n"
	if got := classifyRepoDiff(t, raw, workbench).Floor; got != T3 {
		t.Fatalf("T3 base not preserved under overrides: floor = %s, want T3", got)
	}
}

// TestOverridePerRepoGlobs: a repo only ever gets its own globs. ship's
// packages/driver/** floors T2 under -repo itsHabib/ship, but the same path is
// inert under -repo itsHabib/workbench (which has no driver glob), and
// workbench's gate globs are inert under -repo itsHabib/ship.
func TestOverridePerRepoGlobs(t *testing.T) {
	driver := goFileDiff("packages/driver/state.go")
	if got := classifyRepoDiff(t, driver, "itsHabib/ship").Floor; got != T2 {
		t.Fatalf("ship driver floor = %s, want T2", got)
	}
	if got := classifyRepoDiff(t, driver, workbench).Floor; got != T1 {
		t.Fatalf("ship's driver glob leaked into workbench: floor = %s, want T1", got)
	}

	gateState := goFileDiff("cmd/gate/internal/state/anchor.go")
	if got := classifyRepoDiff(t, gateState, "itsHabib/ship").Floor; got != T1 {
		t.Fatalf("workbench's gate glob leaked into ship: floor = %s, want T1", got)
	}
}

// TestOverrideFindingLine: an override hit records its own explainable signal —
// name "path-override", the band label, and the file path — so a -v run names
// each override hit and the verdict stays explainable.
func TestOverrideFindingLine(t *testing.T) {
	res := classifyRepoDiff(t, goFileDiff("cmd/gate/internal/state/anchor.go"), workbench)
	var sig *Signal
	for i := range res.Signals {
		if res.Signals[i].Name == "path-override" {
			sig = &res.Signals[i]
			break
		}
	}
	if sig == nil {
		t.Fatalf("no path-override signal in %+v", res.Signals)
	}
	if sig.Tier != T3 || sig.TierS != "T3" {
		t.Fatalf("override signal tier = %s, want T3", sig.TierS)
	}
	if !strings.Contains(sig.Why, "cmd/gate/internal/state/anchor.go") {
		t.Fatalf("override finding does not name the file: %q", sig.Why)
	}
	if !strings.Contains(sig.Why, "merge-authorization") {
		t.Fatalf("override finding lacks its band label: %q", sig.Why)
	}
}

// TestOverrideMaxOfBands: a file matching BOTH a T3 rule and the broad T2 rule
// (cmd/gate/main.go matches the exit-code rule and cmd/gate/**) resolves to the
// higher tier — the two bands need no mutual exclusion.
func TestOverrideMaxOfBands(t *testing.T) {
	res := classifyRepoDiff(t, goFileDiff("cmd/gate/main.go"), workbench)
	if res.Floor != T3 {
		t.Fatalf("cmd/gate/main.go floor = %s, want T3 (max of T3 exit-code + T2 gate rules)", res.Floor)
	}
	// both override rules fire — two path-override findings, higher wins the floor.
	var overrides int
	for _, s := range res.Signals {
		if s.Name == "path-override" {
			overrides++
		}
	}
	if overrides < 2 {
		t.Fatalf("expected both override rules to fire on cmd/gate/main.go, got %d", overrides)
	}
}

// TestGlobToRe pins the restricted glob language: `**` crosses `/`, `*` stays
// within one path segment, everything else is literal and anchored.
func TestGlobToRe(t *testing.T) {
	cases := []struct {
		glob  string
		path  string
		match bool
	}{
		// dir/** — prefix match, crossing slashes.
		{"cmd/gate/internal/state/**", "cmd/gate/internal/state/anchor.go", true},
		{"cmd/gate/internal/state/**", "cmd/gate/internal/state/deep/nested.go", true},
		{"cmd/gate/internal/state/**", "cmd/gate/internal/statex/x.go", false},
		{"cmd/gate/internal/state/**", "other/cmd/gate/internal/state/x.go", false},
		// exact file — anchored, no partial.
		{"cmd/gate/main.go", "cmd/gate/main.go", true},
		{"cmd/gate/main.go", "cmd/gate/main.go.bak", false},
		{"cmd/gate/main.go", "x/cmd/gate/main.go", false},
		// single-segment star — does not cross `/`.
		{"a/*.go", "a/x.go", true},
		{"a/*.go", "a/b/x.go", false},
	}
	for _, c := range cases {
		re := globToRe(c.glob)
		if got := re.MatchString(c.path); got != c.match {
			t.Fatalf("globToRe(%q).Match(%q) = %v, want %v (re=%s)", c.glob, c.path, got, c.match, re)
		}
	}
}
