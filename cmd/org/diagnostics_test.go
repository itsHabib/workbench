package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/org"
)

// TestPreflightRefusalsKeepTheExitCodeSeam pins the seam the package comment
// promises: 1 is the kernel refusing a record, 4 is the command failing. A
// composite that checks early must be indistinguishable from one that let the
// kernel refuse — and the refusals a composite catches early are the common
// ones, so a caller branching on 1-vs-4 was wrong most of the time.
func TestPreflightRefusalsKeepTheExitCodeSeam(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:api"}
	must := func(verb ...string) {
		t.Helper()
		if code, _, errOut := exec(t, state, append(verb, role...)...); code != 0 {
			t.Fatalf("%v: exit %d: %s", verb, code, errOut)
		}
	}
	must("charter", "-scope", "github:acme/api", "-supervisor", "human:op")
	must("begin", "-work", "github:acme/api#1", "-pin", "first")

	// begin while another item is active. The kernel's own answer to the claim
	// this composite would have written is claim_active; the pre-flight must
	// give the same reason at the same exit code.
	code, _, errOut := exec(t, state, append([]string{"begin", "-work", "github:acme/api#2", "-pin", "second"}, role...)...)
	if code != codeRefused {
		t.Fatalf("begin over an active claim: exit %d, want %d (%s)", code, codeRefused, errOut)
	}
	if !strings.Contains(errOut, org.ReasonClaimActive) {
		t.Fatalf("pre-flight did not name the kernel's reason: %s", errOut)
	}
	// The same law reached the long way round, for comparison: the bare verb.
	must("assign", "-work", "github:acme/api#2", "-pin", "second")
	code2, _, errOut2 := exec(t, state, append([]string{"claim", "-work", "github:acme/api#2"}, role...)...)
	if code2 != code || org.ReasonClaimActive != reasonOf(errOut2) {
		t.Fatalf("early and late refusals differ: %d/%q vs %d/%q", code, errOut, code2, errOut2)
	}

	// done naming a HELD item while another is active: the record done would
	// write is `complete #2`, and the kernel's answer to that is
	// claim_subject_mismatch — not claim_active, which belongs to a claim done
	// never writes. The bare verb is the comparison.
	code, _, errOut = exec(t, state, append([]string{"done", "-work", "github:acme/api#2"}, role...)...)
	if code != codeRefused || org.ReasonClaimSubjectMismatch != reasonOf(errOut) {
		t.Fatalf("done over an active claim: exit %d: %s", code, errOut)
	}
	code2, _, errOut2 = exec(t, state, append([]string{"complete", "-work", "github:acme/api#2"}, role...)...)
	if code2 != code || org.ReasonClaimSubjectMismatch != reasonOf(errOut2) {
		t.Fatalf("early and late refusals differ: %d/%q vs %d/%q", code, errOut, code2, errOut2)
	}

	must("yield", "-work", "github:acme/api#1")

	// done naming work the lane does not hold is work_not_held, not a crash.
	code, _, errOut = exec(t, state, append([]string{"done", "-work", "github:acme/api#99"}, role...)...)
	if code != codeRefused || !strings.Contains(errOut, org.ReasonWorkNotHeld) {
		t.Fatalf("done on unheld work: exit %d: %s", code, errOut)
	}

	// A genuine caller mistake stays an error: two items held, none active, so
	// no law was broken — the question simply has no answer.
	if code, _, errOut = exec(t, state, append([]string{"done"}, role...)...); code != codeError {
		t.Fatalf("ambiguous done: exit %d, want %d (%s)", code, codeError, errOut)
	}
}

// reasonOf extracts the reason id from a refusal line: "org: <reason> at seq…".
func reasonOf(errOut string) string {
	_, rest, ok := strings.Cut(strings.TrimSpace(errOut), "org: ")
	if !ok {
		return ""
	}
	reason, _, _ := strings.Cut(rest, " ")
	return strings.TrimSuffix(reason, ":")
}

// TestNeverAttachedNamesTheAttach pins the diagnosis, not the law. The refusal
// is correct; the cause it named was not, and an agent that reads "must name
// the incarnation" on a charter-only lane presents a digest from an earlier
// session — the exact impersonation strict identity exists to stop.
func TestNeverAttachedNamesTheAttach(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:fresh"}
	if code, _, errOut := exec(t, state, append([]string{"charter", "-scope", "github:acme/api", "-supervisor", "human:op"}, role...)...); code != 0 {
		t.Fatal(errOut)
	}

	code, _, errOut := exec(t, state, append([]string{"assign", "-work", "github:acme/api#1", "-pin", "x"}, role...)...)
	if code != codeRefused {
		t.Fatalf("assign on a charter-only lane: exit %d, want %d", code, codeRefused)
	}
	// The frozen reason is unchanged: it is what callers branch on, and a
	// record-level law really did fire. Only the prose is repaired.
	if !strings.Contains(errOut, org.ReasonIncarnationMissing) {
		t.Fatalf("reason changed: %s", errOut)
	}
	for _, want := range []string{"has never been attached", "org attach", "org begin"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("refusal missing %q: %s", want, errOut)
		}
	}

	// A RELEASED lane is a different fact and must not borrow the message: an
	// incarnation existed there, it just ended.
	if code, _, errOut := exec(t, state, append([]string{"attach"}, role...)...); code != 0 {
		t.Fatal(errOut)
	}
	if code, _, errOut := exec(t, state, append([]string{"release"}, role...)...); code != 0 {
		t.Fatal(errOut)
	}
	_, _, errOut = exec(t, state, append([]string{"assign", "-work", "github:acme/api#1", "-pin", "x"}, role...)...)
	if strings.Contains(errOut, "has never been attached") {
		t.Fatalf("a released lane was reported as never attached: %s", errOut)
	}
}

// TestIntakeDoesNotNameAVerbThatDoesNotExist. The uncovered branch is the only
// routing guidance an agent gets at the moment it decides where work belongs,
// and it named `recharter`, which has no CLI writer.
func TestIntakeDoesNotNameAVerbThatDoesNotExist(t *testing.T) {
	state := t.TempDir()
	if code, _, errOut := exec(t, state, "charter", "-tenant", "acme", "-role", "steward:api",
		"-scope", "github:acme/api", "-supervisor", "human:op"); code != 0 {
		t.Fatal(errOut)
	}
	code, out, errOut := exec(t, state, "intake", "-work", "jira:PROJ-1", "-tenant", "acme")
	if code != 0 {
		t.Fatalf("intake: exit %d: %s", code, errOut)
	}
	if strings.Contains(out, "recharter") {
		t.Fatalf("intake still points at a verb org does not have:\n%s", out)
	}
	if !strings.Contains(out, "org retire") || !strings.Contains(out, "terms are set once") {
		t.Fatalf("intake does not name the route that exists:\n%s", out)
	}
	// Retire is terminal for the chain, so the fresh charter must be told to
	// use a new role id — the retired one is refused `retired`.
	if !strings.Contains(out, "NEW role id") {
		t.Fatalf("intake implies the retired role can be re-chartered:\n%s", out)
	}
	// Every verb the guidance names must actually be a verb.
	for _, verb := range []string{"retire", "charter"} {
		if _, ok := verbs[verb]; !ok {
			t.Fatalf("intake names %q, which is not a verb", verb)
		}
	}
}

// TestBlobRefusesSilentlyDroppedFlags. blob is the only verb with a positional
// argument, so it is the only one where a trailing flag is parsed as nothing
// and the read lands in the default state root — a wrong answer rather than an
// error.
func TestBlobRefusesSilentlyDroppedFlags(t *testing.T) {
	state := t.TempDir()
	role := []string{"-tenant", "acme", "-role", "steward:api"}
	if code, _, errOut := exec(t, state, append([]string{"charter", "-scope", "github:acme/api", "-supervisor", "human:op"}, role...)...); code != 0 {
		t.Fatal(errOut)
	}
	if code, _, errOut := exec(t, state, append([]string{"attach"}, role...)...); code != 0 {
		t.Fatal(errOut)
	}
	if code, _, errOut := exec(t, state, append([]string{"note", "-body", "the body"}, role...)...); code != 0 {
		t.Fatal(errOut)
	}
	digest := org.DigestBytes([]byte("the body"))

	// The order-independent form works.
	code, out, errOut := exec(t, state, "blob", "-digest", digest)
	if code != 0 || out != "the body" {
		t.Fatalf("blob -digest: exit %d, out %q, err %q", code, out, errOut)
	}
	// Flags before the positional still work — run directly, since the exec
	// helper appends -state and would itself trip the check below.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"blob", "-state", state, digest}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("blob [flags] <digest>: exit %d: %s", code, stderr.String())
	}
	if stdout.String() != "the body" {
		t.Fatalf("blob [flags] <digest> printed %q", stdout.String())
	}
	// Both forms at once would read the flag and ignore the positional.
	code, _, errOut = exec(t, state, "blob", "-digest", digest, "sha256:other")
	if code != codeError || !strings.Contains(errOut, "alongside -digest") {
		t.Fatalf("-digest plus positional: exit %d: %s", code, errOut)
	}
	// A flag AFTER the positional is refused rather than silently dropped —
	// which is exactly the shape the exec helper produces.
	code, _, errOut = exec(t, state, "blob", digest)
	if code != codeError || !strings.Contains(errOut, "follows a positional argument") {
		t.Fatalf("trailing flag: exit %d: %s", code, errOut)
	}
}
