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
	want := []string{`FLEET_ALLOW_SLOW="<why>" bash scripts/bench.sh`, AllowSlowPattern}
	for _, w := range want {
		if !strings.Contains(reason, w) {
			t.Fatalf("refusal does not carry %q:\n%s", w, reason)
		}
	}
}

func TestTheGateAcceptsExactlyTheFormItAsksFor(t *testing.T) {
	costFixture(t)
	if reason := CheckCost(`FLEET_ALLOW_SLOW="won the resource" bash scripts/bench.sh`, "sess1"); reason != "" {
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
	form := AllowSlowForm("npx vitest run")
	if !AllowSlowPrefixed(form) {
		t.Fatalf("the form the refusal prints is not one the gate accepts: %s", form)
	}
	prefix := strings.TrimSuffix(strings.TrimPrefix(AllowSlowPattern, "Bash("), "*)")
	if !strings.HasPrefix(form, prefix) {
		t.Fatalf("%s does not start with the allow rule's prefix %s", form, prefix)
	}
}
