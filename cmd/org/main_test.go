package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
