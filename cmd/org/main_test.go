package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/org/internal/render"
	"github.com/itsHabib/workbench/contracts/org"
)

// exec runs one verb against a state dir and returns exit code and streams.
func exec(t *testing.T, state string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	full := append(args, "-state", state)
	code := run(full, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestVerbLoopExitCodes drives the CLI end to end through a temp state dir and
// pins the exit-code seam: 0 ok, 1 kernel refusal, 2 usage.
func TestVerbLoopExitCodes(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "lead:platform"}
	step := func(wantCode int, args ...string) (string, string) {
		t.Helper()
		code, out, errOut := exec(t, state, append(args, role...)...)
		if code != wantCode {
			t.Fatalf("%v: exit %d, want %d (stderr: %s)", args, code, wantCode, errOut)
		}
		return out, errOut
	}

	step(0, "charter", "-scope", "github:acme/api", "-tier", "T2", "-supervisor", "human:op")
	step(0, "attach")
	step(0, "assign", "-work", "github:acme/api#88", "-pin", "the ticket body")
	step(0, "claim", "-work", "github:acme/api#88")
	step(0, "checkpoint", "-body", "half way through")
	step(0, "yield", "-work", "github:acme/api#88")

	// A refusal is exit 1 and names the kernel's reason on stderr.
	_, errOut := step(1, "claim", "-work", "jira:NOPE-1")
	if !strings.Contains(errOut, "work_not_held") {
		t.Fatalf("refusal stderr lacks the reason id: %s", errOut)
	}

	out, _ := step(0, "boot")
	for _, want := range []string{"# baton boot — lead:platform @ acme", "held (1)", "half way through"} {
		if !strings.Contains(out, want) {
			t.Fatalf("boot lacks %q:\n%s", want, out)
		}
	}

	out, _ = step(0, "status")
	if !strings.Contains(out, "lead:platform") {
		t.Fatalf("status lacks the role:\n%s", out)
	}

	step(0, "verify")

	if code, _, _ := exec(t, state, "nonsense"); code != codeUsage {
		t.Fatalf("unknown verb: exit %d, want %d", code, codeUsage)
	}
}

// TestBootRefusesVoidChain pins the empty-chain read: booting a role that was
// never chartered is an error, not an empty index.
func TestBootRefusesVoidChain(t *testing.T) {
	code, _, errOut := exec(t, t.TempDir(), "boot", "-tenant", "acme", "-role", "lead:ghost")
	if code != codeError {
		t.Fatalf("exit %d, want %d", code, codeError)
	}
	if !strings.Contains(errOut, "no chain") {
		t.Fatalf("stderr: %s", errOut)
	}
}

// TestBootInjectsOperatorContext proves the context.d sources ride the boot
// output, sorted, and truncate with a pointer to the directory.
func TestBootInjectsOperatorContext(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "lead:platform"}
	if code, _, errOut := exec(t, state, append([]string{"charter", "-scope", "github:acme/api"}, role...)...); code != 0 {
		t.Fatalf("charter: %s", errOut)
	}
	ctxDir := filepath.Join(state, "acme", "lead--platform", "context.d")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ctxDir, "10-mission.md"), []byte("ship the org loop"), 0o644)
	os.WriteFile(filepath.Join(ctxDir, "20-rules.md"), []byte("two fix-rounds, then the judge"), 0o644)

	code, out, errOut := exec(t, state, append([]string{"boot"}, role...)...)
	if code != 0 {
		t.Fatalf("boot: %s", errOut)
	}
	mission := strings.Index(out, "ship the org loop")
	rules := strings.Index(out, "two fix-rounds")
	if mission < 0 || rules < 0 || mission > rules {
		t.Fatalf("context missing or unordered (mission %d, rules %d):\n%s", mission, rules, out)
	}

	_, out, _ = exec(t, state, append([]string{"boot", "-context-bytes", "40"}, role...)...)
	if !strings.Contains(out, "context truncated at 40 bytes") {
		t.Fatalf("no truncation note:\n%s", out)
	}
}

// TestStrictIdentityPolicy pins the -strict seam: a write without a presented
// incarnation is refused before the append, a presented-but-stale incarnation
// is the kernel's stale_incarnation refusal, and the minting kinds stay exempt.
func TestStrictIdentityPolicy(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "lead:platform"}
	if code, _, e := exec(t, state, append([]string{"charter", "-scope", "github:acme/api", "-strict"}, role...)...); code != 0 {
		t.Fatalf("strict charter must stay exempt (minting kind): %s", e)
	}
	if code, _, e := exec(t, state, append([]string{"attach", "-strict"}, role...)...); code != 0 {
		t.Fatalf("strict attach must stay exempt (minting kind): %s", e)
	}

	code, _, errOut := exec(t, state, append([]string{"assign", "-strict", "-work", "github:acme/api#88", "-pin", "x"}, role...)...)
	if code != codeError || !strings.Contains(errOut, "strict mode") {
		t.Fatalf("strict write without incarnation: exit %d, stderr %s", code, errOut)
	}

	code, _, errOut = exec(t, state, append([]string{"assign", "-incarnation",
		"sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"-work", "github:acme/api#88", "-pin", "x"}, role...)...)
	if code != codeRefused || !strings.Contains(errOut, "stale_incarnation") {
		t.Fatalf("stale presented incarnation: exit %d, stderr %s", code, errOut)
	}
}

// TestSweepReportsBrokenChainThroughTheCLI is the fix for the review's second
// P2: cmdSweep used to read through Load, which folds internally and is
// all-or-nothing, so a broken chain arrived empty and the sweep reported zero
// of the work recorded before the break. It now reads the records and lets the
// replay decide, which is the only path that can keep those counts.
func TestSweepReportsBrokenChainThroughTheCLI(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "lead:platform"}
	for _, args := range [][]string{
		{"charter", "-scope", "github:acme/api"},
		{"attach"},
		{"checkpoint", "-body", "real work happened"},
	} {
		if code, _, e := exec(t, state, append(args, role...)...); code != 0 {
			t.Fatalf("%v: %s", args, e)
		}
	}

	// Append a line the kernel must refuse: a claim on work nobody holds.
	chain := filepath.Join(state, "acme", "lead--platform", "chain.jsonl")
	raw, err := os.ReadFile(chain)
	if err != nil {
		t.Fatal(err)
	}
	forged := `{"v":1,"scheme":"canon/v1","seq":4,"tenant":"acme","role":"lead:platform",` +
		`"kind":"claim","kind_class":"structural","subject":{"work":"jira:NOPE-1"}}` + "\n"
	if err := os.WriteFile(chain, append(raw, forged...), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := exec(t, state, append([]string{"sweep"}, role...)...)
	if code != 0 {
		t.Fatalf("a broken chain must not fail the sweep: exit %d, %s", code, errOut)
	}
	if !strings.Contains(out, "BROKEN") {
		t.Fatalf("sweep did not flag the broken chain:\n%s", out)
	}
	if !strings.Contains(out, "1 checkpoint(s) of 1 end(s)") {
		t.Fatalf("counts recorded before the break were lost:\n%s", out)
	}
	if strings.Contains(out, "assign_conflict") {
		t.Fatalf("unrelated BROKEN attention gained a zero-conflict suffix:\n%s", out)
	}
}

type sweepConflict struct {
	Tenant string   `json:"tenant"`
	Work   string   `json:"work"`
	Roles  []string `json:"roles"`
}

type sweepReport struct {
	AssignConflicts []sweepConflict `json:"assign_conflicts"`
	Roles           []struct {
		Tenant string `json:"tenant"`
	} `json:"roles"`
	Totals struct {
		Roles int `json:"roles"`
	} `json:"totals"`
}

type conflictFixture struct {
	t     *testing.T
	state string
	work  string
}

func newConflictFixture(t *testing.T) conflictFixture {
	t.Helper()
	f := conflictFixture{t: t, state: t.TempDir(), work: "github:acme/api#88"}
	for _, role := range []string{"lead:zeta", "lead:alpha"} {
		f.write(role, "charter", "-scope", "github:acme/api")
		f.write(role, "attach")
	}
	f.write("lead:zeta", "assign", "-work", f.work, "-pin", "zeta version", "-party", "ic:zeta")
	f.write("lead:alpha", "assign", "-work", f.work, "-pin", "alpha version", "-party", "ic:alpha")
	return f
}

func (f conflictFixture) write(role string, args ...string) {
	f.t.Helper()
	scope := []string{"-tenant", "acme", "-role", role}
	if code, _, errOut := exec(f.t, f.state, append(args, scope...)...); code != 0 {
		f.t.Fatalf("%s %v: exit %d: %s", role, args, code, errOut)
	}
}

func (f conflictFixture) report() sweepReport {
	f.t.Helper()
	code, out, errOut := exec(f.t, f.state, "sweep", "-json", "-tenant", "acme")
	if code != 0 {
		f.t.Fatalf("sweep -json: exit %d: %s", code, errOut)
	}
	var got sweepReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		f.t.Fatalf("decode sweep: %v\n%s", err, out)
	}
	return got
}

func (f conflictFixture) text() string {
	f.t.Helper()
	code, out, errOut := exec(f.t, f.state, "sweep", "-tenant", "acme")
	if code != 0 {
		f.t.Fatalf("sweep: exit %d: %s", code, errOut)
	}
	return out
}

// TestSweepDetectsCrossRoleAssignmentConflicts is the executable A4 control:
// two real chains may each admit the same URI, so their tenant sweep must say so
// deterministically. Unassign is the explicit reconciliation.
func TestSweepDetectsCrossRoleAssignmentConflicts(t *testing.T) {
	f := newConflictFixture(t)
	got := f.report()
	if len(got.AssignConflicts) != 1 {
		t.Fatalf("assign_conflicts = %#v, want one", got.AssignConflicts)
	}
	wantRoles := []string{"lead:alpha", "lead:zeta"}
	if c := got.AssignConflicts[0]; c.Tenant != "acme" || c.Work != f.work || !slices.Equal(c.Roles, wantRoles) {
		t.Fatalf("assign_conflict = %#v, want acme/%s owned by %v", c, f.work, wantRoles)
	}
	code, other, errOut := exec(t, f.state, "sweep", "-json", "-tenant", "beta")
	if code != 0 {
		t.Fatalf("beta sweep: exit %d: %s", code, errOut)
	}
	var beta sweepReport
	if err := json.Unmarshal([]byte(other), &beta); err != nil {
		t.Fatalf("decode beta sweep: %v", err)
	}
	if beta.AssignConflicts == nil || len(beta.AssignConflicts) != 0 || len(beta.Roles) != 0 || beta.Totals.Roles != 0 {
		t.Fatalf("acme data leaked into beta sweep: %#v", beta)
	}
	out := f.text()
	for _, want := range append([]string{"assign_conflicts", "1 assign_conflict(s)", f.work}, wantRoles...) {
		if !strings.Contains(out, want) {
			t.Fatalf("text sweep lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "attention: 1 assign_conflict(s)") || strings.Contains(out, "attention: 0 dangling") {
		t.Fatalf("conflict-only attention is noisy or incomplete:\n%s", out)
	}

	f.write("lead:alpha", "unassign", "-work", f.work)
	got = f.report()
	if got.AssignConflicts == nil || len(got.AssignConflicts) != 0 {
		t.Fatalf("after unassign: assign_conflicts = %#v, want []", got.AssignConflicts)
	}
	out = f.text()
	if strings.Contains(out, "assign_conflict") {
		t.Fatalf("clean text sweep retained a conflict warning:\n%s", out)
	}
}

// TestSweepDoesNotTraverseOtherTenants pins the tenant read boundary: a sweep
// for acme must not depend on being able to enumerate an unrelated tenant.
func TestSweepDoesNotTraverseOtherTenants(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "lead:alpha"}
	if code, _, errOut := exec(t, state, append([]string{"charter", "-scope", "github:acme/api"}, role...)...); code != 0 {
		t.Fatalf("charter: exit %d: %s", code, errOut)
	}

	unrelated := filepath.Join(state, "beta")
	if err := os.Mkdir(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unrelated, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unrelated, 0o755) })
	if _, err := os.ReadDir(unrelated); err == nil {
		t.Skip("filesystem does not enforce an unreadable directory for this process")
	}

	code, out, errOut := exec(t, state, "sweep", "-json", "-tenant", "acme")
	if code != 0 {
		t.Fatalf("acme sweep traversed unreadable beta tenant: exit %d: %s", code, errOut)
	}
	var got sweepReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode sweep: %v\n%s", err, out)
	}
	if len(got.Roles) != 1 || got.Roles[0].Tenant != "acme" {
		t.Fatalf("roles = %#v, want only acme", got.Roles)
	}
}

// TestSweepMalformedTailKeepsAssignmentConflict proves a corrupt tail cannot
// erase ownership already established by the valid prefix. The row remains
// BROKEN, while the normal Load/write path still refuses the chain.
func TestSweepMalformedTailKeepsAssignmentConflict(t *testing.T) {
	f := newConflictFixture(t)
	chain := filepath.Join(f.state, "acme", "lead--alpha", "chain.jsonl")
	chainFile, err := os.OpenFile(chain, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chainFile.WriteString("{not-json}\n"); err != nil {
		chainFile.Close()
		t.Fatal(err)
	}
	if err := chainFile.Close(); err != nil {
		t.Fatal(err)
	}

	got := f.report()
	if len(got.AssignConflicts) != 1 {
		t.Fatalf("malformed tail suppressed valid-prefix conflict: %#v", got.AssignConflicts)
	}
	out := f.text()
	if !strings.Contains(out, "BROKEN") || !strings.Contains(out, "assign_conflicts") {
		t.Fatalf("malformed chain must be both BROKEN and conflicted:\n%s", out)
	}
	role := []string{"-tenant", "acme", "-role", "lead:alpha"}
	if code, _, _ := exec(t, f.state, append([]string{"note", "-body", "must not append"}, role...)...); code == 0 {
		t.Fatal("normal write path accepted a malformed chain")
	}
}

// TestIntakeRoutesWork pins the intake report: in-scope lanes surface, an
// out-of-scope hold is named as drift, an uncovered URI states the fix, and
// the verb never writes.
func TestIntakeRoutesWork(t *testing.T) {
	state := t.TempDir()
	steward := []string{"-tenant", "acme", "-role", "steward:api"}
	rogue := []string{"-tenant", "acme", "-role", "lead:misc"}
	must := func(args []string, verb ...string) {
		t.Helper()
		if code, _, errOut := exec(t, state, append(verb, args...)...); code != 0 {
			t.Fatalf("%v %v: %s", verb, args, errOut)
		}
	}
	must(steward, "charter", "-scope", "github:acme/api", "-tier", "T1", "-supervisor", "human:op")
	must(rogue, "charter", "-scope", "jira:MISC-", "-tier", "T1", "-supervisor", "human:op")
	must(rogue, "attach")
	// assign enforces no scope (field report §4.4), so the rogue hold lands.
	must(rogue, "assign", "-work", "github:acme/api#7", "-pin", "drift")

	code, out, errOut := exec(t, state, "intake", "-work", "github:acme/api#7", "-tenant", "acme", "-json")
	if code != 0 {
		t.Fatalf("intake: exit %d: %s", code, errOut)
	}
	var got render.Intake
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode intake: %v", err)
	}
	if !got.Covered || len(got.Lanes) != 2 {
		t.Fatalf("intake = %#v, want covered with two lanes", got)
	}
	byRole := map[string]render.IntakeLane{}
	for _, l := range got.Lanes {
		byRole[l.Role] = l
	}
	if l := byRole["steward:api"]; l.ScopeMatch != "github:acme/api" || l.Holds {
		t.Fatalf("steward lane = %#v", l)
	}
	if l := byRole["lead:misc"]; l.ScopeMatch != "" || !l.Holds {
		t.Fatalf("rogue lane = %#v", l)
	}

	_, text, _ := exec(t, state, "intake", "-work", "github:acme/api#7", "-tenant", "acme")
	for _, want := range []string{"in scope (github:acme/api)", "OUT OF SCOPE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("intake text lacks %q:\n%s", want, text)
		}
	}

	_, text, _ = exec(t, state, "intake", "-work", "banana:whatever", "-tenant", "acme")
	for _, want := range []string{"no chartered scope covers banana:whatever", "fix: charter"} {
		if !strings.Contains(text, want) {
			t.Fatalf("uncovered intake lacks %q:\n%s", want, text)
		}
	}

	if code, _, _ := exec(t, state, "intake", "-tenant", "acme"); code != codeError {
		t.Fatal("intake without -work must error")
	}
	// A URI the kernel could never record must not be routed to a lane.
	if code, _, errOut := exec(t, state, "intake", "-work", "jira: bad", "-tenant", "acme"); code != codeError || !strings.Contains(errOut, "not a valid work URI") {
		t.Fatalf("malformed -work: exit %d: %s", code, errOut)
	}
}

// TestBeginDoneBracketsSmallWork pins the §4.2 composites: two commands
// bracket a small task, writing the same records the seven-verb ceremony
// would, and §4.3's uncompletable trap — finished work on a released lane —
// closes with one done.
func TestBeginDoneBracketsSmallWork(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:api"}
	must := func(verb ...string) (string, string) {
		t.Helper()
		code, out, errOut := exec(t, state, append(verb, role...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", verb, code, errOut)
		}
		return out, errOut
	}
	must("charter", "-scope", "github:acme/api", "-tier", "T1", "-supervisor", "human:op")

	out, _ := must("begin", "-work", "github:acme/api#7", "-pin", "delete the dead file")
	for _, kind := range []string{"attach", "assign", "claim"} {
		if !strings.Contains(out, kind) {
			t.Fatalf("begin lacks %s:\n%s", kind, out)
		}
	}
	if !strings.Contains(out, "phase active") {
		t.Fatalf("begin did not end active:\n%s", out)
	}

	out, _ = must("done", "-body", "deleted; CI green")
	for _, kind := range []string{"complete", "release"} {
		if !strings.Contains(out, kind) {
			t.Fatalf("done lacks %s:\n%s", kind, out)
		}
	}
	if strings.Contains(out, "attach") || strings.Contains(out, "claim seq") {
		t.Fatalf("done on an active lane re-wrote entry records:\n%s", out)
	}

	// The §4.3 trap: assign + yield + release leaves finished work held on a
	// released lane. done must reconstruct and tear down in one command.
	must("attach")
	must("assign", "-work", "github:acme/api#8", "-pin", "second task")
	must("claim", "-work", "github:acme/api#8")
	must("yield", "-work", "github:acme/api#8")
	_, errOut := must("release")
	if !strings.Contains(errOut, "warning: releasing while still holding 1 item(s)") {
		t.Fatalf("release did not warn about the held item: %s", errOut)
	}
	out, _ = must("done", "-work", "github:acme/api#8", "-body", "was already finished")
	for _, kind := range []string{"attach", "claim", "complete", "release"} {
		if !strings.Contains(out, kind) {
			t.Fatalf("done from released lacks %s:\n%s", kind, out)
		}
	}
	if !strings.Contains(out, "phase chartered") {
		t.Fatalf("done did not return the role to chartered:\n%s", out)
	}

	// Nothing to finish: no held work, no active claim.
	if code, _, _ := exec(t, state, append([]string{"done"}, role...)...); code != codeError {
		t.Fatal("done with nothing held must error")
	}
	// begin of unassigned work without a pin must refuse to write.
	if code, _, errOut := exec(t, state, append([]string{"begin", "-work", "github:acme/api#9"}, role...)...); code != codeError || !strings.Contains(errOut, "-digest or -pin is required") {
		t.Fatalf("unpinned begin: exit %d: %s", code, errOut)
	}
}

// TestBeginStrictMintsIdentity pins strict-mode composites: begin's fresh
// attach mints the identity its later steps write under, so ORG_STRICT does
// not force a human to copy digests mid-command.
func TestBeginStrictMintsIdentity(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:api", "-strict"}
	if code, _, errOut := exec(t, state, append([]string{"charter", "-scope", "github:acme/api", "-tier", "T1", "-supervisor", "human:op"}, role...)...); code != 0 {
		t.Fatalf("charter: %s", errOut)
	}
	code, _, errOut := exec(t, state, append([]string{"begin", "-work", "github:acme/api#7", "-pin", "task"}, role...)...)
	if code != 0 {
		t.Fatalf("strict begin: exit %d: %s", code, errOut)
	}
	// done on the now-held lane without presenting the incarnation must be
	// refused by the strict policy before anything is written.
	code, _, errOut = exec(t, state, append([]string{"done"}, role...)...)
	if code != codeError || !strings.Contains(errOut, "strict mode") {
		t.Fatalf("strict done without identity: exit %d: %s", code, errOut)
	}
}

// TestIntakeSchemeWideScopeAndUnreadableLanes pins two review findings: a
// bare-scheme scope entry (`jira:`) is charterable and covers the scheme, and
// an unreadable chain makes an uncovered report hedge instead of declaring
// definitively that nothing covers the work.
func TestIntakeSchemeWideScopeAndUnreadableLanes(t *testing.T) {
	state := t.TempDir()
	wide := []string{"-tenant", "acme", "-role", "supervisor:tickets"}
	if code, _, errOut := exec(t, state, append([]string{"charter", "-scope", "jira:", "-tier", "T1", "-supervisor", "human:op"}, wide...)...); code != 0 {
		t.Fatalf("bare-scheme charter refused: %s", errOut)
	}
	_, out, _ := exec(t, state, "intake", "-work", "jira:ANY-1", "-tenant", "acme")
	if !strings.Contains(out, "in scope (jira:)") {
		t.Fatalf("scheme-wide scope did not cover a ticket:\n%s", out)
	}

	// Corrupt the chain: an uncovered URI must now hedge, not conclude.
	chain := filepath.Join(state, "acme", "supervisor--tickets", "chain.jsonl")
	if err := os.WriteFile(chain, []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := exec(t, state, "intake", "-work", "github:acme/api#1", "-tenant", "acme")
	if code != 0 {
		t.Fatalf("intake over a broken chain must still report: %s", errOut)
	}
	if !strings.Contains(out, "no READABLE chartered scope") || !strings.Contains(out, "1 lane(s) unreadable") {
		t.Fatalf("uncovered report did not hedge on the unreadable lane:\n%s", out)
	}
}

// TestDoneTargetRefusesToGuess pins the resolver's case matrix directly: it
// never picks between several held items, and an explicit target must be
// held (or active) — a typo is refused before anything is written.
func TestDoneTargetRefusesToGuess(t *testing.T) {
	held2 := org.RoleState{Held: []org.Assignment{{Work: "a"}, {Work: "b"}}}
	if _, err := doneTarget(held2, ""); err == nil {
		t.Fatal("must refuse to guess between 2 held items")
	}
	if _, err := doneTarget(held2, "c"); err == nil {
		t.Fatal("must refuse an explicit target the role does not hold")
	}
	if got, err := doneTarget(held2, "b"); err != nil || got != "b" {
		t.Fatalf("explicit held target = %q, %v", got, err)
	}
	active := org.RoleState{Active: "a", Held: []org.Assignment{{Work: "a"}}}
	if got, err := doneTarget(active, ""); err != nil || got != "a" {
		t.Fatalf("active target = %q, %v", got, err)
	}
	if _, err := doneTarget(active, "b"); err == nil {
		t.Fatal("must refuse a different target while another is active")
	}
}

// TestNextDueDiesWithTheTenure pins the liveness law's kernel half: a deadline
// belongs to the writer that declared it, so releasing drops it. Without this a
// released lane reads "late" forever against a deadline nobody owns, and a
// watcher pages about a lane that correctly went home.
func TestNextDueDiesWithTheTenure(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:api"}
	must := func(verb ...string) {
		t.Helper()
		if code, _, errOut := exec(t, state, append(verb, role...)...); code != 0 {
			t.Fatalf("%v: %s", verb, errOut)
		}
	}
	must("charter", "-scope", "github:acme/api", "-tier", "T1", "-supervisor", "human:op")
	must("attach", "-next-due", "1s")

	_, out, _ := exec(t, state, append([]string{"boot"}, role...)...)
	if !strings.Contains(out, "next-due:") {
		t.Fatalf("attach did not declare a deadline:\n%s", out)
	}
	must("release")
	_, out, _ = exec(t, state, append([]string{"boot"}, role...)...)
	if strings.Contains(out, "next-due:") {
		t.Fatalf("release kept the predecessor's deadline:\n%s", out)
	}
	_, board, _ := exec(t, state, "status", "-tenant", "acme")
	if strings.Contains(board, "LATE") {
		t.Fatalf("a released lane is not late:\n%s", board)
	}
}

// TestSweepReportsScopeDrift pins the finding the kernel cannot enforce: work
// held outside its own charter's scope. Admission is replayed over history, so
// a scope law added now would break the fold of every chain that ever assigned
// outside its scope — reporting is the honest alternative.
func TestSweepReportsScopeDrift(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "lead:misc"}
	must := func(verb ...string) {
		t.Helper()
		if code, _, errOut := exec(t, state, append(verb, role...)...); code != 0 {
			t.Fatalf("%v: %s", verb, errOut)
		}
	}
	must("charter", "-scope", "jira:MISC-", "-tier", "T1", "-supervisor", "human:op")
	must("attach")
	must("assign", "-work", "jira:MISC-1", "-pin", "in scope")
	must("assign", "-work", "github:acme/api#7", "-pin", "out of scope")

	code, out, errOut := exec(t, state, "sweep", "-json", "-tenant", "acme")
	if code != 0 {
		t.Fatalf("sweep: %s", errOut)
	}
	var report struct {
		ScopeDrift []struct {
			Role, Work string
			Scope      []string
		} `json:"scope_drift"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode sweep: %v", err)
	}
	if len(report.ScopeDrift) != 1 {
		t.Fatalf("scope_drift = %#v, want exactly the out-of-scope hold", report.ScopeDrift)
	}
	if d := report.ScopeDrift[0]; d.Role != "lead:misc" || d.Work != "github:acme/api#7" {
		t.Fatalf("scope_drift = %#v", d)
	}
	_, text, _ := exec(t, state, "sweep", "-tenant", "acme")
	for _, want := range []string{"scope_drift (held outside", "github:acme/api#7", "attention: 1 scope_drift(s)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text sweep lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "0 dangling") {
		t.Fatalf("drift-only attention gained a zero-count prefix:\n%s", text)
	}
	must("unassign", "-work", "github:acme/api#7")
	_, text, _ = exec(t, state, "sweep", "-tenant", "acme")
	if strings.Contains(text, "scope_drift") {
		t.Fatalf("clean sweep retained a drift warning:\n%s", text)
	}
}

// TestAnnulRepudiatesWithoutReverting pins what annul actually is: the fold
// records the digest and leaves the record's effect standing, so the verb must
// say so rather than let a caller read "annul" as "undo".
func TestAnnulRepudiatesWithoutReverting(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:api"}
	must := func(verb ...string) (string, string) {
		t.Helper()
		code, out, errOut := exec(t, state, append(verb, role...)...)
		if code != 0 {
			t.Fatalf("%v: %s", verb, errOut)
		}
		return out, errOut
	}
	must("charter", "-scope", "github:acme/api", "-tier", "T1", "-supervisor", "human:op")
	must("attach")
	must("assign", "-work", "github:acme/api#7", "-pin", "assigned in error")

	_, warn := must("annul", "-body", "wrong lane")
	if !strings.Contains(warn, "does not revert") || !strings.Contains(warn, "held 1") {
		t.Fatalf("annul did not report the standing effect: %s", warn)
	}
	// The assignment it repudiated is still held — that is the kernel's law,
	// and the reason the warning exists.
	_, boot, _ := exec(t, state, append([]string{"boot"}, role...)...)
	if !strings.Contains(boot, "github:acme/api#7") {
		t.Fatalf("annul silently reverted state; the fold does not do that:\n%s", boot)
	}
	_, log, _ := exec(t, state, append([]string{"log"}, role...)...)
	if !strings.Contains(log, "annul") {
		t.Fatalf("annul absent from the chain:\n%s", log)
	}
	// An annul naming anything but the tip is the kernel's refusal.
	if code, _, errOut := exec(t, state, append([]string{"annul", "-target",
		"sha256:0000000000000000000000000000000000000000000000000000000000000000"}, role...)...); code != codeRefused || !strings.Contains(errOut, "annul_unknown") {
		t.Fatalf("non-tip annul: exit %d: %s", code, errOut)
	}
}

// TestCompositesPreflightAndDischarge pins the two composite deferrals: begin
// refuses a blocked claim before writing anything, and done discharges a
// dangling claim by completing it rather than trying to re-claim it.
func TestCompositesPreflightAndDischarge(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:api"}
	sup := []string{"-tenant", "acme", "-role", "human:op"}
	must := func(args []string, verb ...string) {
		t.Helper()
		if code, _, errOut := exec(t, state, append(verb, args...)...); code != 0 {
			t.Fatalf("%v: %s", verb, errOut)
		}
	}
	must(role, "charter", "-scope", "github:acme/api", "-tier", "T1", "-supervisor", "human:op")
	must(role, "begin", "-work", "github:acme/api#7", "-pin", "first task")

	// A second begin while #7 is active is refused before any record lands.
	before, _, _ := exec(t, state, append([]string{"log"}, role...)...)
	code, _, errOut := exec(t, state, append([]string{"begin", "-work", "github:acme/api#8", "-pin", "second"}, role...)...)
	if code != codeError || !strings.Contains(errOut, "already active") {
		t.Fatalf("blocked begin: exit %d: %s", code, errOut)
	}
	after, _, _ := exec(t, state, append([]string{"log"}, role...)...)
	if before != after {
		t.Fatal("a refused begin still wrote to the chain")
	}

	// A takeover strands #7 as a dangling claim; done must close it.
	must(sup, "charter", "-scope", "org:acme", "-tier", "T2", "-supervisor", "human:op")
	must(role, "takeover", "-party", "human:op")
	_, boot, _ := exec(t, state, append([]string{"boot"}, role...)...)
	if !strings.Contains(boot, "github:acme/api#7") {
		t.Fatalf("takeover did not strand the claim:\n%s", boot)
	}
	_, out, _ := exec(t, state, append([]string{"done", "-body", "successor discharge"}, role...)...)
	if !strings.Contains(out, "complete") {
		t.Fatalf("done did not complete the dangling claim:\n%s", out)
	}
	if strings.Contains(out, "claim seq") {
		t.Fatalf("done re-claimed a dangling obligation instead of discharging it:\n%s", out)
	}
}

// transferFixture stands up two attached lanes in one tenant, the source
// holding one work item.
type transferFixture struct {
	t                *testing.T
	state            string
	src, dst, srcInc string
	dstInc, work     string
}

func newTransferFixture(t *testing.T, srcScope, dstScope, work string) *transferFixture {
	t.Helper()
	f := &transferFixture{t: t, state: t.TempDir(), src: "steward:a", dst: "steward:b", work: work}
	f.srcInc = f.standUp(f.src, srcScope)
	f.dstInc = f.standUp(f.dst, dstScope)
	f.run(0, f.src, "assign", "-work", work, "-pin", "the item")
	return f
}

func (f *transferFixture) standUp(role, scope string) string {
	f.t.Helper()
	f.run(0, role, "charter", "-scope", scope, "-tier", "T1", "-supervisor", "human:op")
	out := f.run(0, role, "attach", "-json")
	var r struct {
		Holder string `json:"holder"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		f.t.Fatalf("decode attach: %v", err)
	}
	return r.Holder
}

func (f *transferFixture) run(want int, role string, args ...string) string {
	f.t.Helper()
	code, out, errOut := exec(f.t, f.state, append(args, "-tenant", "acme", "-role", role)...)
	if code != want {
		f.t.Fatalf("%v on %s: exit %d, want %d (%s)", args, role, code, want, errOut)
	}
	return out
}

// TestTransferMovesWorkAndResumes pins the verb's whole contract: the normal
// move, the crash-window resume, and the completed no-op — all decided from
// state, never from a flag.
func TestTransferMovesWorkAndResumes(t *testing.T) {
	f := newTransferFixture(t, "github:acme/api", "github:acme/api", "github:acme/api#7")

	out := f.run(0, f.src, "transfer", "-work", f.work, "-to", f.dst,
		"-to-incarnation", f.dstInc, "-incarnation", f.srcInc)
	for _, want := range []string{"assign", "unassign"} {
		if !strings.Contains(out, want) {
			t.Fatalf("transfer lacks %s:\n%s", want, out)
		}
	}
	srcBoot := f.run(0, f.src, "boot")
	if strings.Contains(srcBoot, f.work) {
		t.Fatalf("source still holds the work:\n%s", srcBoot)
	}
	dstBoot := f.run(0, f.dst, "boot")
	if !strings.Contains(dstBoot, f.work) {
		t.Fatalf("destination did not receive the work:\n%s", dstBoot)
	}

	// Re-running a completed transfer is a no-op that says so.
	again := f.run(0, f.src, "transfer", "-work", f.work, "-to", f.dst,
		"-to-incarnation", f.dstInc, "-incarnation", f.srcInc)
	if !strings.Contains(again, "already transferred") {
		t.Fatalf("completed transfer was not idempotent:\n%s", again)
	}

	// The crash window: assigned to the destination, not yet unassigned from
	// the source. Re-running must finish it, not duplicate the assign.
	f.run(0, f.src, "assign", "-work", f.work, "-pin", "the item", "-incarnation", f.srcInc)
	resumed := f.run(0, f.src, "transfer", "-work", f.work, "-to", f.dst,
		"-to-incarnation", f.dstInc, "-incarnation", f.srcInc)
	// Exactly one record: the unassign that finishes the move. ("unassign seq"
	// contains "assign seq", so this counts lines rather than substrings.)
	lines := strings.Split(strings.TrimSpace(resumed), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "unassign seq") {
		t.Fatalf("resume did not finish with exactly one unassign:\n%s", resumed)
	}
	if code, _, _ := exec(t, f.state, "sweep", "-json", "-tenant", "acme"); code != 0 {
		t.Fatal("sweep failed after resume")
	}
}

// TestTransferRefusesRatherThanGuess pins what it will not do: mint authority
// for an unattached lane, act on a chain that moved, or move an active claim.
func TestTransferRefusesRatherThanGuess(t *testing.T) {
	f := newTransferFixture(t, "github:acme/api", "github:acme/api", "github:acme/api#7")

	// Destination unattached: refuse and name the command that fixes it.
	f.run(0, f.dst, "release", "-incarnation", f.dstInc)
	code, _, errOut := exec(t, f.state, "transfer", "-work", f.work, "-to", f.dst,
		"-tenant", "acme", "-role", f.src, "-incarnation", f.srcInc)
	if code != codeError || !strings.Contains(errOut, "is not held") {
		t.Fatalf("unattached destination: exit %d: %s", code, errOut)
	}
	f.dstInc = f.standUpAgain(f.dst)

	// A stale destination tip is a lost race, refused before anything moves.
	code, _, errOut = exec(t, f.state, "transfer", "-work", f.work, "-to", f.dst,
		"-to-incarnation", "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"-tenant", "acme", "-role", f.src, "-incarnation", f.srcInc)
	if code == 0 {
		t.Fatal("transfer accepted a bogus destination incarnation")
	}
	srcBoot := f.run(0, f.src, "boot")
	if !strings.Contains(srcBoot, f.work) {
		t.Fatalf("a refused transfer moved the work anyway:\n%s", srcBoot)
	}

	// An active claim is not transferable.
	f.run(0, f.src, "claim", "-work", f.work, "-incarnation", f.srcInc)
	code, _, errOut = exec(t, f.state, "transfer", "-work", f.work, "-to", f.dst,
		"-to-incarnation", f.dstInc, "-tenant", "acme", "-role", f.src, "-incarnation", f.srcInc)
	if code != codeError || !strings.Contains(errOut, "active claim") {
		t.Fatalf("active claim: exit %d: %s", code, errOut)
	}
}

func (f *transferFixture) standUpAgain(role string) string {
	f.t.Helper()
	out := f.run(0, role, "attach", "-json")
	var r struct {
		Holder string `json:"holder"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		f.t.Fatalf("decode attach: %v", err)
	}
	return r.Holder
}

// TestTransferWarnsOnDestinationScopeDrift pins the posture this substrate
// keeps everywhere the kernel cannot enforce: moving work outside the
// destination's charter scope is allowed, named, and left for sweep to report.
func TestTransferWarnsOnDestinationScopeDrift(t *testing.T) {
	f := newTransferFixture(t, "github:acme/api", "jira:OTHER-", "github:acme/api#7")
	code, _, errOut := exec(t, f.state, "transfer", "-work", f.work, "-to", f.dst,
		"-to-incarnation", f.dstInc, "-tenant", "acme", "-role", f.src, "-incarnation", f.srcInc)
	if code != 0 {
		t.Fatalf("transfer: exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "outside") || !strings.Contains(errOut, "scope_drift") {
		t.Fatalf("no drift warning: %s", errOut)
	}
	_, sweep, _ := exec(t, f.state, "sweep", "-tenant", "acme")
	if !strings.Contains(sweep, "scope_drift") {
		t.Fatalf("sweep does not report the drift the transfer warned about:\n%s", sweep)
	}
}
