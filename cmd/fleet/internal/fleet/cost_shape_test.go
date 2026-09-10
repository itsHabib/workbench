package fleet

import (
	"os"
	"strings"
	"testing"
)

// costFixture makes one rule expensive and returns a state root.
func costFixture(t *testing.T) {
	t.Helper()
	old := State
	State = t.TempDir()
	t.Cleanup(func() { State = old })
	// The rule matches the command's execution position, which stops at the first path
	// token: `bash` is the head of `bash scripts/bench.sh`.
	rules := `[{"name":"the suite","pattern":"^bash$","seconds":40,"instead":"one case"}]`
	if err := os.WriteFile(Path("expensive.json"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheCostRefusalNamesTheOneAllowedForm(t *testing.T) {
	costFixture(t)
	reason := CheckCost("bash scripts/bench.sh", "sess1")
	if reason == "" {
		t.Fatal("an expensive command must be refused")
	}
	want := []string{`FLEET_ALLOW_SLOW=the-suite bash scripts/bench.sh`, AllowSlowPattern("the suite")}
	for _, w := range want {
		if !strings.Contains(reason, w) {
			t.Fatalf("refusal does not carry %q:\n%s", w, reason)
		}
	}
}

func TestTheGateAcceptsExactlyTheFormItAsksFor(t *testing.T) {
	costFixture(t)
	if reason := CheckCost(`FLEET_ALLOW_SLOW=the-suite bash scripts/bench.sh`, "sess1"); reason != "" {
		t.Fatalf("the named form must be accepted: %s", reason)
	}
	if rows, err := os.ReadFile(Path("overrides.jsonl")); err != nil || !strings.Contains(string(rows), "bench.sh") {
		t.Fatalf("the override must be recorded: %v", err)
	}
	// A mention that sets nothing is not the override; a harness prefix rule could not
	// name it either, which is the whole point of pinning one shape.
	if reason := CheckCost("bash scripts/bench.sh FLEET_ALLOW_SLOW=x", "sess1"); reason == "" {
		t.Fatal("a trailing mention is not the override")
	}
}

func TestAllowSlowFormIsWhatThePatternMatches(t *testing.T) {
	form := AllowSlowForm("full unit suite", "npx vitest run")
	if !AllowSlowPrefixed(form) {
		t.Fatalf("the form the refusal prints is not one the gate accepts: %s", form)
	}
	// `Bash(<prefix>:*)` — the colon is the harness's separator, not part of the command.
	prefix := strings.TrimSuffix(strings.TrimPrefix(AllowSlowPattern("full unit suite"), "Bash("), ":*)")
	if !strings.HasPrefix(form, prefix) {
		t.Fatalf("%s does not start with the allow rule's prefix %s", form, prefix)
	}
}

// The remedy is meant to be RUN. A truncated command either fails to parse or, worse,
// runs a broader command than the one that was refused once its trailing filters are
// gone.
func TestTheOverrideRemedyKeepsTheCommandWhole(t *testing.T) {
	costFixture(t)
	long := "bash scripts/bench.sh --filter " + strings.Repeat("x", 200) + " --only one/case"
	reason := CheckCost(long, "sess1")
	if !strings.Contains(reason, long) {
		t.Fatalf("the remedy dropped part of a %d-byte command:\n%s", len(long), reason)
	}
}

// The projected allow is per measured command, so a command wearing another rule's
// token — or a token for nothing at all — must not slip past the gate on the prefix.
func TestTheOverrideIsScopedToTheMeasuredCommand(t *testing.T) {
	costFixture(t)
	if reason := CheckCost(`FLEET_ALLOW_SLOW=x gh pr merge 12 --squash`, "sess1"); reason == "" {
		t.Fatal("the prefix on an unmeasured command must be refused, not silently allowed")
	}
	if reason := CheckCost(`FLEET_ALLOW_SLOW="wire contract" bash scripts/bench.sh`, "sess1"); reason == "" {
		t.Fatal("free text is no longer the override token and must be refused")
	} else if !strings.Contains(reason, "FLEET_ALLOW_SLOW=the-suite bash scripts/bench.sh") {
		t.Fatalf("the refusal must name the token that would work:\n%s", reason)
	}
}

func TestAllowSlowPatternsAreOnePerNamedRule(t *testing.T) {
	costFixture(t)
	got := AllowSlowPatterns()
	if len(got) != 1 || got[0] != "Bash(FLEET_ALLOW_SLOW=the-suite:*)" {
		t.Fatalf("patterns=%v", got)
	}
	for _, p := range got {
		if strings.Contains(p, "FLEET_ALLOW_SLOW=*") {
			t.Fatalf("a universal override allow was projected: %s", p)
		}
	}
}
