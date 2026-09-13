package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

var discoveryHead = strings.Repeat("a", 40)

func assessedDiscovery(e env, floor string) grantDiscovery {
	return grantDiscovery{Subject: verify.Subject{Repo: "o/r", Number: 7, HeadSHA: discoveryHead},
		Action: "merge", StateDir: e.stateDir, KeyDir: filepath.Dir(e.keyPath), FloorBin: e.floorBin, MinimumTier: floor}
}

func fixtureGrant(t *testing.T, e env, ceiling string, cycles int, ttl time.Duration) state.Artifact {
	t.Helper()
	a, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", ceiling, cycles, "fixture operator", ttl, e.now)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDiscoveryReusesOlderCoveringGrant(t *testing.T) {
	e := testEnv(t)
	fixtureGrant(t, e, "T3", 3, -time.Hour)
	covering := fixtureGrant(t, e, "T2", 3, time.Hour)
	fixtureGrant(t, e, "T1", 3, 2*time.Hour)
	for _, minimum := range []string{"T0", "T2"} {
		d := selectGrant(e, assessedDiscovery(e, minimum))
		if d.Status != "available" || d.GrantID != covering.ID || d.MintRequest != "" {
			t.Fatalf("minimum %s: failed to reuse older covering grant: %+v", minimum, d)
		}
	}
}

func TestDiscoveryChecksAllCycleCeilings(t *testing.T) {
	e := testEnv(t)
	exhausted := fixtureGrant(t, e, "T2", 2, time.Hour)
	spendTwoCycles(t, e, exhausted.ID, assessedDiscovery(e, "T1").Subject)
	covering := fixtureGrant(t, e, "T1", 3, time.Hour)
	d := selectGrant(e, assessedDiscovery(e, "T1"))
	if d.Status != "available" || d.GrantID != covering.ID || d.NextCycle != 3 {
		t.Fatalf("exhausted widest grant hid a usable grant: %+v", d)
	}
	d = selectGrant(e, assessedDiscovery(e, "T2"))
	if d.Status != "uncovered" || d.GrantID != "" || len(d.Candidates) != 2 {
		t.Fatalf("combined tier and cycle gap was not explained: %+v", d)
	}
	if !strings.Contains(d.Candidates[0].Gaps[0], "grant_cycle_exceeded") || !strings.Contains(d.Candidates[1].Gaps[0], "grant_tier_exceeded") {
		t.Fatalf("wrong gap dimensions: %+v", d.Candidates)
	}
}

func TestDiscoveryRefreshesAfterOperatorGrant(t *testing.T) {
	e := testEnv(t)
	fixtureGrant(t, e, "T1", 3, time.Hour)
	d := selectGrant(e, assessedDiscovery(e, "T2"))
	if d.Status != "uncovered" || !strings.Contains(d.MintRequest, "T2") {
		t.Fatalf("missing tier must be concrete: %+v", d)
	}
	covering := fixtureGrant(t, e, "T2", 3, time.Hour)
	d = selectGrant(e, assessedDiscovery(e, "T2"))
	if d.Status != "available" || d.GrantID != covering.ID || d.MintRequest != "" {
		t.Fatalf("previous failure hid new authority: %+v", d)
	}
}

func TestDiscoveryEnforcesSubjectAndAuthentication(t *testing.T) {
	tests := []struct {
		name, repo, action, head string
		pr                       int
		corrupt                  bool
		want                     string
	}{
		{name: "wrong repo", repo: "o/other", action: "merge", want: "uncovered"},
		{name: "wrong action", repo: "o/r", action: "deploy", want: "uncovered"},
		{name: "wrong head", repo: "o/r", action: "merge", head: strings.Repeat("b", 40), pr: 7, want: "uncovered"},
		{name: "wrong PR", repo: "o/r", action: "merge", head: discoveryHead, pr: 8, want: "uncovered"},
		{name: "bound match", repo: "o/r", action: "merge", head: discoveryHead, pr: 7, want: "available"},
		{name: "bad signature", repo: "o/r", action: "merge", corrupt: true, want: "assessment_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := testEnv(t)
			var a state.Artifact
			var err error
			if tt.head != "" {
				a, err = capability.MintBound(e.st, e.keyPath, tt.repo, tt.action, "T2", 3, "fixture operator", time.Hour, tt.head, tt.pr, "gau_"+strings.Repeat("b", 64), e.now)
			}
			if tt.head == "" {
				a, err = capability.Mint(e.st, e.keyPath, tt.repo, tt.action, "T2", 3, "fixture operator", time.Hour, e.now)
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.corrupt {
				// A different existing key makes signatures unverifiable without
				// modifying the ledger or its independently anchored chain.
				if err := os.WriteFile(e.keyPath, bytes.Repeat([]byte{42}, 32), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			d := selectGrant(e, assessedDiscovery(e, "T1"))
			if d.Status != tt.want || d.Status == "available" && d.GrantID != a.ID {
				t.Fatalf("subject/authentication bypass: %+v", d)
			}
			if d.Status == "assessment_required" && d.MintRequest != "" {
				t.Fatal("authentication failure requested new authority")
			}
		})
	}
}

func TestDiscoveryFailureDoesNotInventAuthorityGap(t *testing.T) {
	for _, failure := range []string{"unknown floor", "missing key", "invalid key", "tampered log"} {
		t.Run(failure, func(t *testing.T) {
			e := testEnv(t)
			fixtureGrant(t, e, "T3", 0, time.Hour)
			d := assessedDiscovery(e, "T0")
			var err error
			switch failure {
			case "unknown floor":
				d.MinimumTier = ""
			case "missing key":
				err = os.Remove(e.keyPath)
			case "invalid key":
				err = os.WriteFile(e.keyPath, []byte("bad"), 0o600)
			case "tampered log":
				err = os.WriteFile(filepath.Join(e.stateDir, "log.jsonl"), nil, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			d = selectGrant(e, d)
			if d.Status != "assessment_required" || d.GrantID != "" || d.MintRequest != "" || d.exitCode() != codeError {
				t.Fatalf("unread assessment became a grant request: %+v", d)
			}
		})
	}
}

func TestDiscoveryIsReadOnly(t *testing.T) {
	e := testEnv(t)
	fixtureGrant(t, e, "T2", 3, time.Hour)
	paths := []string{filepath.Join(e.stateDir, "log.jsonl"), e.anchor, e.keyPath, filepath.Join(filepath.Dir(e.keyPath), "anchor.key")}
	before := make(map[string][]byte)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = data
	}
	d := selectGrant(e, assessedDiscovery(e, "T1"))
	if d.Status != "available" {
		t.Fatal(d.Why)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, before[path]) {
			t.Fatalf("discovery changed %s: %v", path, err)
		}
	}
	missing := filepath.Join(t.TempDir(), "absent")
	if requireDiscoveryState(missing) == nil {
		t.Fatal("absent state silently initialized")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("state was created: %v", err)
	}
}

func TestGateOmittedGrantUsesDiscovery(t *testing.T) {
	e := discoveryFixtureTools(t)
	covering := fixtureGrant(t, e, "T2", 3, time.Hour)
	fixtureGrant(t, e, "T1", 3, time.Hour)
	res, code, err := runGateSelected(e, "o/r", 7, "", false, "local", false)
	if err != nil || code != codeRefused || res.Outcome != outcomeAlreadyMerged || res.Discovery == nil || res.Discovery.GrantID != covering.ID {
		t.Fatalf("omitted grant failed to reach normal view/refusal logic: %+v code=%d err=%v", res, code, err)
	}
	// Explicit IDs remain binding even when discovery could select a good one.
	res, code, err = runGateSelected(e, "o/r", 7, "grt_absent", false, "local", false)
	if err != nil || code != codeRefused || res.Discovery != nil || res.Outcome != "capability_refused" {
		t.Fatalf("explicit grant silently substituted: %+v code=%d err=%v", res, code, err)
	}
}

func TestDiscoveryCommandsPreserveConfiguration(t *testing.T) {
	e := testEnv(t)
	fixtureGrant(t, e, "T0", 3, time.Hour)
	e.floorBin = "/fixture custom/floor"
	d := selectGrant(e, assessedDiscovery(e, "T2"))
	if d.Status != "uncovered" || !strings.Contains(d.MintRequest, shellJoin([]string{"-key", filepath.Dir(e.keyPath)})) {
		t.Fatalf("mint switched custody: %+v", d)
	}
	d.Status = "assessment_required"
	command := discoveryRoute(d).Next
	if !strings.Contains(command, shellJoin([]string{"-key", filepath.Dir(e.keyPath), "-floor", e.floorBin})) {
		t.Fatalf("retry lost configuration: %s", command)
	}
}

func TestDiscoveryCommandsResolveRelativeConfiguration(t *testing.T) {
	e := discoveryFixtureTools(t)
	fixtureGrant(t, e, "T0", 3, time.Hour)
	keyDir, floor := filepath.Dir(e.keyPath), e.floorBin
	dir := t.TempDir()
	t.Chdir(dir)
	var err error
	e.keyPath, err = filepath.Rel(dir, e.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	e.floorBin, err = filepath.Rel(dir, e.floorBin)
	if err != nil {
		t.Fatal(err)
	}
	d := discoverGrant(e, "o/r", 7)
	if d.Status != "uncovered" || d.KeyDir != keyDir || d.FloorBin != floor {
		t.Fatalf("relative configuration changed meaning: %+v", d)
	}
	if !strings.Contains(d.MintRequest, shellJoin([]string{"-key", keyDir})) {
		t.Fatalf("mint lost original custody: %s", d.MintRequest)
	}
	d.Status = "assessment_required"
	if !strings.Contains(discoveryRoute(d).Next, shellJoin([]string{"-floor", floor})) {
		t.Fatalf("retry lost original floor: %s", discoveryRoute(d).Next)
	}
}

func TestDiscoveryReportsRejectedCandidatesAlongsideValid(t *testing.T) {
	for _, body := range []any{
		capability.Grant{Repo: "o/r", Action: "merge", MaxTier: "T3", Sig: "invalid"},
		map[string]any{"repo": "o/r", "max_cycles": "not an integer"},
	} {
		e := testEnv(t)
		valid := fixtureGrant(t, e, "T2", 3, time.Hour)
		bad, err := e.st.Append(state.KindGrant, "run_fixture", nil, body)
		if err != nil {
			t.Fatal(err)
		}
		d := selectGrant(e, assessedDiscovery(e, "T1"))
		if d.Status != "available" || d.GrantID != valid.ID || len(d.Candidates) != 2 || d.Candidates[1].ID != bad.ID || len(d.Candidates[1].Gaps) == 0 {
			t.Fatalf("selection hid rejected grant diagnostics: %+v", d)
		}
	}
}

func TestDiscoveryUsesOversizedDiffFallback(t *testing.T) {
	e := discoveryFixtureTools(t)
	grant := fixtureGrant(t, e, "T2", 3, time.Hour)
	t.Setenv("GO_DISCOVERY_FAILURE", "oversized")
	d := discoverGrant(e, "o/r", 7)
	if d.Status != "available" || d.GrantID != grant.ID {
		t.Fatalf("default discovery lost the existing pinned fallback: %+v", d)
	}
}

func TestGateStopsWhenDiscoveredHeadMovesBeforeView(t *testing.T) {
	e := discoveryFixtureTools(t)
	fixtureGrant(t, e, "T2", 3, time.Hour)
	t.Setenv("GO_DISCOVERY_FAILURE", "moved before view")
	res, code, err := runGateSelected(e, "o/r", 7, "", false, "invalid backend proves early stop", false)
	if err == nil || code != codeError || !strings.Contains(err.Error(), "PR head changed after selection") || res.Discovery == nil {
		t.Fatalf("head change reached normal assessment: %+v code=%d err=%v", res, code, err)
	}
}

func TestDiscoveryClosedPRNeedsNoGrant(t *testing.T) {
	e := discoveryFixtureTools(t) // Deliberately no key or grant.
	t.Setenv("GO_DISCOVERY_FAILURE", "closed")
	d := discoverGrant(e, "o/r", 7)
	if d.Status != "not_applicable" || d.MintRequest != "" || d.GrantID != "" || d.MinimumTier != "" {
		t.Fatalf("closed subject requested authority: %+v", d)
	}
}

func TestDiscoveryRejectsUnreadOrMovedHead(t *testing.T) {
	for _, mode := range []string{"missing floor", "invalid floor", "unread diff", "moved head", "oversized moved"} {
		t.Run(mode, func(t *testing.T) {
			e := discoveryFixtureTools(t)
			fixtureGrant(t, e, "T3", 0, time.Hour)
			t.Setenv("GO_DISCOVERY_FAILURE", mode)
			if mode == "missing floor" {
				e.floorBin = filepath.Join(t.TempDir(), "missing")
			}
			d := discoverGrant(e, "o/r", 7)
			if d.Status != "assessment_required" || d.GrantID != "" || d.MintRequest != "" {
				t.Fatalf("unread/stale assessment selected authority: %+v", d)
			}
		})
	}
}

func discoveryFixtureTools(t *testing.T) env {
	t.Helper()
	e := testEnv(t)
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	install(t, executable, filepath.Join(dir, "gh"+suffix))
	install(t, executable, filepath.Join(dir, "git"+suffix))
	e.floorBin = filepath.Join(dir, "floor"+suffix)
	install(t, executable, e.floorBin)
	t.Setenv("PATH", dir) // No fallback to a real authenticated CLI or provider.
	t.Setenv("GO_WANT_DISCOVERY_FIXTURE", "1")
	t.Setenv("GO_DISCOVERY_READ_COUNT", filepath.Join(dir, "reads"))
	return e
}

func runDiscoveryFixture() int {
	mode := os.Getenv("GO_DISCOVERY_FAILURE")
	args := strings.Join(os.Args[1:], " ")
	if strings.HasPrefix(filepath.Base(os.Args[0]), "git") {
		if strings.Contains(args, "diff --no-ext-diff --no-textconv --no-color "+strings.Repeat("b", 40)+" "+discoveryHead) {
			fmt.Print("diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-old\n+new\n")
			return 0
		}
		if strings.HasPrefix(args, "init") || strings.Contains(args, " fetch ") {
			return 0
		}
		fmt.Fprintln(os.Stderr, "unexpected fixture git:", args)
		return 99
	}
	if strings.HasPrefix(filepath.Base(os.Args[0]), "floor") {
		if mode == "invalid floor" {
			fmt.Println(`{"floor":""}`)
			return 0
		}
		fmt.Println(`{"floor":"T1","files":1}`)
		return 0
	}
	if strings.HasPrefix(args, "pr view") {
		head := discoveryHead
		status := "MERGED"
		if mode == "moved before view" {
			head = strings.Repeat("c", 40)
		}
		if mode == "model failure" {
			status = "OPEN"
		}
		fmt.Printf(`{"state":%q,"headRefOid":%q}`, status, head)
		return 0
	}
	if strings.Contains(args, "/compare/") {
		if strings.Contains(args, "?per_page=1") {
			fmt.Printf(`{"merge_base_commit":{"sha":%q}}`, strings.Repeat("b", 40))
			return 0
		}
		if strings.HasPrefix(mode, "oversized") {
			fmt.Fprintln(os.Stderr, "HTTP 406: diff exceeded the maximum number of lines")
			return 1
		}
		if mode == "unread diff" {
			fmt.Fprintln(os.Stderr, "permission denied")
			return 1
		}
		fmt.Print("diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-old\n+new\n")
		return 0
	}
	if strings.Contains(args, "/pulls/7") {
		path := os.Getenv("GO_DISCOVERY_READ_COUNT")
		reads, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append(reads, 'x'), 0o600); err != nil {
			return 98
		}
		head := discoveryHead
		if (mode == "moved head" || mode == "oversized moved") && len(reads) >= 2 {
			head = strings.Repeat("c", 40)
		}
		status := "open"
		if mode == "closed" {
			status = "closed"
		}
		fmt.Printf(`{"state":%q,"head":{"sha":%q},"base":{"sha":%q}}`, status, head, strings.Repeat("b", 40))
		return 0
	}
	fmt.Fprintln(os.Stderr, "unexpected fixture command:", args)
	return 99
}

func TestDiscoverCommandJSON(t *testing.T) {
	e := discoveryFixtureTools(t)
	grant := fixtureGrant(t, e, "T2", 3, time.Hour)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "discover-grant", "-repo", "o/r", "-pr", "7", "-json", "-state", e.stateDir, "-key", filepath.Dir(e.keyPath), "-floor", e.floorBin)
	cmd.Env = append(os.Environ(), "GO_WANT_DISCOVERY_COMMAND=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("discover CLI: %v: %s", err, out)
	}
	var d grantDiscovery
	if err := json.Unmarshal(out, &d); err != nil || d.GrantID != grant.ID || d.Status != "available" {
		t.Fatalf("wrong discovery JSON: %s err=%v", out, err)
	}
	if bytes.Contains(out, []byte(`"sig"`)) || bytes.Contains(out, []byte("fixture operator")) {
		t.Fatal("discovery leaked unnecessary signing metadata")
	}
}

func TestGateDiscoveryAssessmentFailureIsHardError(t *testing.T) {
	for _, failure := range []string{"invalid floor", "unread diff", "bad signature", "moved before view"} {
		t.Run(failure, func(t *testing.T) {
			checkDiscoveryTerminalFailure(t, failure)
		})
	}
}

func checkDiscoveryTerminalFailure(t *testing.T, failure string) {
	t.Helper()
	e := discoveryFixtureTools(t)
	grant := fixtureGrant(t, e, "T2", 3, time.Hour)
	t.Setenv("GO_DISCOVERY_FAILURE", failure)
	if failure == "bad signature" {
		if err := os.WriteFile(e.keyPath, bytes.Repeat([]byte{42}, 32), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	terminal := runDiscoveryFailureCLI(t, e)
	d := terminal.Discovery
	if d == nil || d.Status != "assessment_required" || d.Subject.Repo != "o/r" || d.Subject.Number != 7 || d.Subject.HeadSHA != discoveryHead {
		t.Fatalf("terminal error lost the failed assessment: %+v", terminal)
	}
	want := shellJoin([]string{"gate", "discover-grant", "-repo", "o/r", "-pr", "7", "-json", "-state", e.stateDir, "-key", filepath.Dir(e.keyPath), "-floor", e.floorBin})
	if terminal.Escape.Next != want || d.StateDir != e.stateDir || d.KeyDir != filepath.Dir(e.keyPath) || d.FloorBin != e.floorBin {
		t.Fatalf("terminal recovery switched discovery configuration: %+v", terminal)
	}
	if d.MintRequest != "" || d.GrantID != "" || terminal.RetryHelps {
		t.Fatalf("assessment failure invented authority or a blind retry: %+v", terminal)
	}
	if failure == "bad signature" && (len(d.Candidates) != 1 || d.Candidates[0].ID != grant.ID || len(d.Candidates[0].Gaps) == 0) {
		t.Fatalf("terminal error lost candidate authentication diagnostics: %+v", terminal)
	}
}

func TestDiscoveryDoesNotRelabelNormalGateFailure(t *testing.T) {
	e := discoveryFixtureTools(t)
	fixtureGrant(t, e, "T2", 3, time.Hour)
	t.Setenv("GO_DISCOVERY_FAILURE", "model failure")
	terminal := runDiscoveryFailureCLI(t, e, "-model-backend", "fixture-invalid")
	if terminal.Discovery != nil || strings.Contains(terminal.Error, "grant_assessment_required") || strings.Contains(terminal.Escape.Next, "discover-grant") {
		t.Fatalf("normal evaluation error became a grant discovery error: %+v", terminal)
	}
	if !strings.Contains(terminal.Error, "fixture-invalid") {
		t.Fatalf("normal backend failure was lost: %+v", terminal)
	}
}

func runDiscoveryFailureCLI(t *testing.T, e env, extra ...string) terminalError {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"gate", "-repo", "o/r", "-pr", "7", "-state", e.stateDir, "-key", filepath.Dir(e.keyPath), "-floor", e.floorBin}
	cmd := exec.Command(executable, append(args, extra...)...)
	cmd.Env = append(os.Environ(), "GO_WANT_DISCOVERY_COMMAND=1")
	out, err := cmd.Output()
	if err == nil || cmd.ProcessState.ExitCode() != codeError {
		t.Fatalf("discovery assessment must hard-error: %v output=%s", err, out)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid terminal JSON: %v: %s", err, out)
	}
	if result["outcome"] != nil || result["error"] == nil {
		t.Fatalf("infrastructure failure became an authority refusal: %s", out)
	}
	var terminal terminalError
	if err := json.Unmarshal(out, &terminal); err != nil {
		t.Fatal(err)
	}
	return terminal
}
