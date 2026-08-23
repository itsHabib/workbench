package verify

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func fixedClock(t *testing.T, stamp string) func() time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	return func() time.Time { return at }
}

// TestValidateDeciderClosedVocabulary pins what gate will and will not record as
// a decider. The rejections are the point: an empty who names nobody, and a
// method gate has no vocabulary for is a channel claim it cannot render.
func TestValidateDeciderClosedVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		decider Decider
		wantErr bool
	}{
		{"operator at a shell", Decider{Who: "mh", Method: MethodCLIOperator, At: "2026-08-21T22:15:00Z"}, false},
		{"slack tap", Decider{Who: "@mhdevstuff (U123)", Method: MethodSlackInteractive, At: "2026-08-21T22:15:00Z"}, false},
		{"delegated provider", Decider{Who: "claude-cli[...]:claude-opus-5", Method: MethodAuto + "claude", At: "2026-08-21T22:15:00Z"}, false},
		{"empty who", Decider{Who: "", Method: MethodCLIOperator, At: "2026-08-21T22:15:00Z"}, true},
		{"blank who", Decider{Who: "   ", Method: MethodCLIOperator, At: "2026-08-21T22:15:00Z"}, true},
		{"empty method", Decider{Who: "mh", Method: "", At: "2026-08-21T22:15:00Z"}, true},
		{"unknown method", Decider{Who: "mh", Method: "carrier-pigeon", At: "2026-08-21T22:15:00Z"}, true},
		{"bare auto prefix names no provider", Decider{Who: "mh", Method: MethodAuto, At: "2026-08-21T22:15:00Z"}, true},
		{"missing at", Decider{Who: "mh", Method: MethodCLIOperator, At: ""}, true},
		{"at is not rfc3339", Decider{Who: "mh", Method: MethodCLIOperator, At: "yesterday"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateDecider(c.decider)
			if c.wantErr && err == nil {
				t.Fatalf("ValidateDecider(%+v) = nil, want a refusal", c.decider)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("ValidateDecider(%+v) = %v, want nil", c.decider, err)
			}
			if c.wantErr && !errors.Is(err, ErrDeciderMissing) {
				t.Fatalf("refusal must carry ErrDeciderMissing so callers can classify it, got %v", err)
			}
		})
	}
}

// TestNewDeciderStampsTheDecidersOwnClock pins the second timestamp the record
// needs: when the decision was MADE, which a phone tap and a later append make
// genuinely different facts.
func TestNewDeciderStampsTheDecidersOwnClock(t *testing.T) {
	d, err := NewDecider("mh", MethodCLIOperator, fixedClock(t, "2026-08-21T22:15:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if d.At != "2026-08-21T22:15:00Z" {
		t.Fatalf("At = %q, want the injected clock in RFC 3339 UTC", d.At)
	}
	if _, err := NewDecider("", MethodCLIOperator, time.Now); err == nil {
		t.Fatal("NewDecider must refuse an anonymous decider")
	}
}

// TestRequireDeciderBoundsTheWritePathOnly is the whole shape of the binding: a
// JUDGMENT must name its decider, and nothing else has one to name. A code or
// local-model verdict passing untouched is what keeps the rule from becoming
// noise on every deterministic rung.
func TestRequireDeciderBoundsTheWritePathOnly(t *testing.T) {
	judgment := Verdict{Producer: Producer{Class: ClassJudgment, Impl: "operator"}, Decision: DecisionPass}
	if err := RequireDecider(judgment); err == nil {
		t.Fatal("an unattributed judgment must be refused at the write path")
	}
	if !errors.Is(RequireDecider(judgment), ErrDeciderMissing) {
		t.Fatal("the refusal must be classifiable as ErrDeciderMissing")
	}
	judgment.Decider = &Decider{Who: "mh", Method: MethodCLIOperator, At: "2026-08-21T22:15:00Z"}
	if err := RequireDecider(judgment); err != nil {
		t.Fatalf("an attributed judgment must pass: %v", err)
	}
	for _, class := range []string{ClassCode, ClassLocal} {
		v := Verdict{Producer: Producer{Class: class}, Decision: DecisionPass}
		if err := RequireDecider(v); err != nil {
			t.Fatalf("a %s verdict has no decider to name, got %v", class, err)
		}
	}
}

// TestReduceStaysTolerantOfUnattributedJudgments is the other half of the
// contract, and the reason the refusal lives on the write path: the ~230
// judgments recorded before this field existed must still compose and explain.
// A reducer that rejected them would trade one unanswerable question for a log
// that cannot be read at all.
func TestReduceStaysTolerantOfUnattributedJudgments(t *testing.T) {
	subject := Subject{Repo: "itsHabib/workbench", Number: 1, HeadSHA: "abc"}
	verdicts := []Verdict{
		{Subject: subject, Source: "floor", Producer: Producer{Class: ClassCode}, Decision: DecisionEscalate, Tier: "T2", Confidence: 1},
		{Subject: subject, Source: "operator-judgment", Producer: Producer{Class: ClassJudgment, Impl: "operator"}, Decision: DecisionPass, Tier: "T0", Confidence: 1},
	}
	reduced, err := Reduce(subject, verdicts)
	if err != nil {
		t.Fatalf("Reduce must not reject a judgment that predates the decider: %v", err)
	}
	if reduced.Decision != DecisionPass {
		t.Fatalf("reduced decision = %s, want the judgment to still resolve the escalation", reduced.Decision)
	}
}

// TestAttributionNamesTheAbsence pins that "nobody is named" renders as a word a
// reader can see, never as a blank. A missing line and an unanswered question
// look identical, and that is how 128 anonymous approvals went unnoticed.
func TestAttributionNamesTheAbsence(t *testing.T) {
	unattributed := Verdict{Producer: Producer{Class: ClassJudgment, Impl: "operator"}}
	if got := Attribution(unattributed); got != "unattributed" {
		t.Fatalf("Attribution(unattributed judgment) = %q, want %q", got, "unattributed")
	}
	attributed := unattributed
	attributed.Decider = &Decider{Who: "mh", Method: MethodSlackInteractive, At: "2026-08-21T22:15:00Z"}
	got := Attribution(attributed)
	for _, want := range []string{"mh", MethodSlackInteractive, "2026-08-21T22:15:00Z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Attribution = %q, missing %q", got, want)
		}
	}
	if Attribution(Verdict{Producer: Producer{Class: ClassCode}}) != "" {
		t.Fatal("a code verdict has no attribution line to render")
	}
}
