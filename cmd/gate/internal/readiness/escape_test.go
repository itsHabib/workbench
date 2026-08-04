package readiness

import "testing"

func TestEscapeIsTotal(t *testing.T) {
	for _, code := range []string{"", "novel_unseen_code"} {
		route := Escape(code, true)
		if route.Why == "" || route.Next == "" {
			t.Fatalf("Escape(%q) = %+v, want a usable fallback route", code, route)
		}
	}
}

func TestEscapeDoesNotRouteThroughBrokenSubstrate(t *testing.T) {
	route := Escape("anchor_missing", false)
	if route.Next != SubstrateRoute {
		t.Fatalf("broken-substrate route = %+v, want an external filesystem check", route)
	}
}

func TestEscapeNamesSelfGatedFailure(t *testing.T) {
	selfGated := Escape("judge_provider_failed", true)
	fallback := Escape("novel_unseen_code", true)
	if selfGated.Why == fallback.Why {
		t.Fatalf("self-gated route did not explain the independent path: %+v", selfGated)
	}
}

func TestSelfGatedRegistry(t *testing.T) {
	for _, code := range selfGatedCodes {
		if !SelfGated(code) {
			t.Fatalf("SelfGated(%q) = false", code)
		}
	}
	if SelfGated("novel_unseen_code") {
		t.Fatal("an unseen code was guessed to be self-gating")
	}
}

func TestCodeFindsWrappedStableCode(t *testing.T) {
	got := Code(`gate: judgment_malformed: json: unknown field "findings"`)
	if got != "judgment_malformed" {
		t.Fatalf("Code() = %q, want judgment_malformed", got)
	}
}

func TestCodeMatchesCompleteStableCode(t *testing.T) {
	if got := Code("judgment_malformed_escalation: question is empty"); got != "judgment_malformed_escalation" {
		t.Fatalf("Code() = %q, want judgment_malformed_escalation", got)
	}
	if got := Code(""); got != "" {
		t.Fatalf("Code(empty) = %q, want empty", got)
	}
}
