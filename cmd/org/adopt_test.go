package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/org"
)

// adoptFixture is one tenant with an owner lane, so each test states only what
// it is actually about.
type adoptFixture struct {
	t     *testing.T
	state string
	role  string
	work  string
}

func newAdoptFixture(t *testing.T) *adoptFixture {
	t.Helper()
	f := &adoptFixture{t: t, state: t.TempDir(), role: "steward:api", work: "github:acme/api#4612"}
	f.must("charter", "-scope", "github:acme/api", "-supervisor", "human:op")
	return f
}

func (f *adoptFixture) run(verb ...string) (int, string, string) {
	f.t.Helper()
	return exec(f.t, f.state, append(verb, "-tenant", "acme", "-role", f.role)...)
}

func (f *adoptFixture) must(verb ...string) (string, string) {
	f.t.Helper()
	code, out, errOut := f.run(verb...)
	if code != 0 {
		f.t.Fatalf("%v: exit %d: %s", verb, code, errOut)
	}
	return out, errOut
}

// adopt performs the canonical adoption of f.work onto f.role.
func (f *adoptFixture) adopt(extra ...string) (string, string) {
	f.t.Helper()
	return f.must(append([]string{
		"adopt", "-work", f.work,
		"-pin", "head 9f2c1ab · branch camauto-1102 · wt .claude/worktrees/camauto",
		"-by", "supervisor:api",
	}, extra...)...)
}

// records reads a role's chain without folding it.
func (f *adoptFixture) records(role string) []org.Record {
	f.t.Helper()
	path := filepath.Join(f.state, "acme", strings.ReplaceAll(role, ":", "--"), "chain.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []org.Record
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r org.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			f.t.Fatalf("chain line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// blob reads a body straight off disk. Deliberately not `org blob <digest>`:
// that verb takes its digest positionally, and Go's flag package stops parsing
// at the first non-flag argument, so a trailing -state is silently ignored and
// the read lands in the default state root.
func (f *adoptFixture) blob(digest string) string {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.state, "blobs", strings.ReplaceAll(digest, ":", "-")))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(raw)
}

// TestAdoptPutsWorkOnAPlateWithoutStartingIt is the whole verb in one pass:
// four records, the lane released at the end, the work HELD and not active,
// an attributed note, and a successor able to pick it up with begin.
//
// The last assertion is the one that matters most. Adoption is only worth
// anything if the work it records is work somebody can resume without knowing
// an adoption happened — so the test proves the handoff, not just the writes.
func TestAdoptPutsWorkOnAPlateWithoutStartingIt(t *testing.T) {
	f := newAdoptFixture(t)

	out, _ := f.adopt("-evidence", "gh pr view 4612 --json headRefOid")
	for _, kind := range []string{"attach", "note", "assign", "release"} {
		if !strings.Contains(out, kind) {
			t.Fatalf("adopt lacks %s:\n%s", kind, out)
		}
	}
	if !strings.Contains(out, "phase chartered") {
		t.Fatalf("adopt did not leave the lane released:\n%s", out)
	}

	// Held, and deliberately not active: putting work on a plate is not
	// starting it, and a lane acts on exactly one item.
	boot, _ := f.must("boot")
	if !strings.Contains(boot, "held (1): "+f.work) {
		t.Fatalf("adopted work is not held:\n%s", boot)
	}
	if strings.Contains(boot, "phase: active") {
		t.Fatalf("adoption started the work:\n%s", boot)
	}

	// The note is the attribution the assign cannot carry: without it an
	// adoption is indistinguishable from the lane's own coverage sweep.
	rs := f.records(f.role)
	if len(rs) != 5 { // charter + the four
		t.Fatalf("chain has %d records, want 5", len(rs))
	}
	if rs[2].Kind != org.KindNote || rs[2].BodyDigest == "" {
		t.Fatalf("record 3 is not a note with a body: %+v", rs[2])
	}
	body := f.blob(rs[2].BodyDigest)
	for _, want := range []string{"adopted by supervisor:api", f.work, "9f2c1ab", "gh pr view 4612", "held, not claimed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("note body missing %q:\n%s", want, body)
		}
	}
	// Every adopted record carries the incarnation the adopt's own attach
	// minted, so the four are one tenure rather than four anonymous writes.
	inc := rs[2].Incarnation
	if inc == "" || rs[3].Incarnation != inc || rs[4].Incarnation != inc {
		t.Fatalf("adopted records do not share one incarnation: %q %q %q",
			rs[2].Incarnation, rs[3].Incarnation, rs[4].Incarnation)
	}

	// The handoff: a successor resumes adopted work with no pin, because the
	// pin is already on the chain.
	out, _ = f.must("begin", "-work", f.work)
	if !strings.Contains(out, "claim") || !strings.Contains(out, "phase active") {
		t.Fatalf("begin did not pick up adopted work:\n%s", out)
	}
}

// TestAdoptRefusesRatherThanDisplaceOrDoubleHold pins the two refusals that
// make adoption safe to hand to a watcher, plus the no-op that makes it safe
// to re-run. Both refusals must land on the refusal exit code: they are the
// substrate declining, not the command failing.
func TestAdoptRefusesRatherThanDisplaceOrDoubleHold(t *testing.T) {
	f := newAdoptFixture(t)

	// A live session holds the lane. Adoption never displaces one — that is a
	// takeover, which the kernel gates on the charter's supervisors.
	f.must("attach")
	code, _, errOut := f.run("adopt", "-work", f.work, "-pin", "x", "-by", "supervisor:api")
	if code != codeRefused || !strings.Contains(errOut, org.ReasonAlreadyHeld) {
		t.Fatalf("adopt onto a held lane: exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "takeover") {
		t.Fatalf("refusal did not name what it is not: %s", errOut)
	}
	f.must("release")

	// Idempotent by state: re-adopting work the lane already holds says so and
	// writes nothing.
	f.adopt()
	before := len(f.records(f.role))
	out, _ := f.adopt()
	if !strings.Contains(out, "already adopted") {
		t.Fatalf("re-adopt was not a no-op:\n%s", out)
	}
	if after := len(f.records(f.role)); after != before {
		t.Fatalf("re-adopt wrote %d records", after-before)
	}

	// A second lane already holds it. Adopting here would manufacture the
	// assign_conflict sweep reports, and which lane should own it is a routing
	// question no mechanical verb gets to answer.
	if code, _, errOut := exec(t, f.state, "charter", "-tenant", "acme", "-role", "steward:other",
		"-scope", "github:acme/api", "-supervisor", "human:op"); code != 0 {
		t.Fatal(errOut)
	}
	code, _, errOut = exec(t, f.state, "adopt", "-tenant", "acme", "-role", "steward:other",
		"-work", f.work, "-pin", "x", "-by", "supervisor:api")
	if code != codeRefused || !strings.Contains(errOut, org.ReasonWorkAlreadyHeld) {
		t.Fatalf("double-hold adopt: exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, f.role) {
		t.Fatalf("refusal did not name the existing holder: %s", errOut)
	}
}

// TestAdoptDemandsAnAdopterAndAPin pins the two flags without which an
// adoption record is worse than no record: unsigned, or undetectable as drift.
func TestAdoptDemandsAnAdopterAndAPin(t *testing.T) {
	f := newAdoptFixture(t)

	code, _, errOut := f.run("adopt", "-work", f.work, "-pin", "x")
	if code != codeError || !strings.Contains(errOut, "-work and -by are required") {
		t.Fatalf("unsigned adopt: exit %d: %s", code, errOut)
	}
	code, _, errOut = f.run("adopt", "-work", f.work, "-by", "supervisor:api")
	if code != codeError || !strings.Contains(errOut, "-digest or -pin is required") {
		t.Fatalf("unpinned adopt: exit %d: %s", code, errOut)
	}
	// A lane with no chain at all: adoption puts work on an EXISTING plate.
	code, _, errOut = exec(t, f.state, "adopt", "-tenant", "acme", "-role", "steward:ghost",
		"-work", f.work, "-pin", "x", "-by", "supervisor:api")
	if code != codeError || !strings.Contains(errOut, "has no chain") {
		t.Fatalf("adopt onto an unchartered lane: exit %d: %s", code, errOut)
	}
	if n := len(f.records(f.role)); n != 1 {
		t.Fatalf("a refused adopt wrote %d records to the target lane", n-1)
	}
}

// TestAdoptWarnsOnScopeDrift mirrors transfer: adoption reports the drift it
// is about to create and proceeds, because the operator may be adopting work
// deliberately ahead of a charter — but sweep will say so, so the command
// says so first.
func TestAdoptWarnsOnScopeDrift(t *testing.T) {
	f := newAdoptFixture(t)
	_, errOut := f.must("adopt", "-work", "jira:CAMAUTO-1102", "-pin", "ticket", "-by", "supervisor:api")
	if !strings.Contains(errOut, "scope_drift") {
		t.Fatalf("out-of-scope adopt did not warn: %s", errOut)
	}
	code, out, _ := exec(t, f.state, "sweep", "-tenant", "acme")
	if code != 0 || !strings.Contains(out, "scope_drift") {
		t.Fatalf("sweep did not report the drift the warning promised:\n%s", out)
	}
}
