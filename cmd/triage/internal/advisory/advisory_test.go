package advisory

import (
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/triage/internal/floor"
)

const diffText = "diff --git a/src/gate.go b/src/gate.go\n" +
	"+++ b/src/gate.go\n@@\n" +
	"+	if verdict == nil {\n" +
	"+		return allowMerge() // absence of signal must not allow\n" +
	"+	}\n"

func classify(raw string) floor.Result {
	return floor.Classify(floor.ParseUnifiedDiff(strings.NewReader(raw)))
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name  string
		p     Proposal
		fails int
	}{
		{"none needs no verification", Proposal{Escalate: "none"}, 0},
		{"empty escalate treated as none", Proposal{}, 0},
		{
			"trusted escalation passes all three",
			Proposal{Escalate: "T3", Trigger: "gate-machinery", Evidence: "return allowMerge() // absence of signal must not allow"},
			0,
		},
		{
			// the §4.2 fail-open: none is schema-legal, so enum membership alone is not enough
			"escalation with trigger none is rejected",
			Proposal{Escalate: "T2", Trigger: "none", Evidence: "return allowMerge() // absence of signal must not allow"},
			1,
		},
		{
			"unknown trigger is rejected",
			Proposal{Escalate: "T2", Trigger: "vibes", Evidence: "return allowMerge() // absence of signal must not allow"},
			1,
		},
		{
			"short evidence is rejected",
			Proposal{Escalate: "T2", Trigger: "gate-machinery", Evidence: "allowMerge()"},
			1,
		},
		{
			"confabulated evidence is rejected",
			Proposal{Escalate: "T3", Trigger: "trust-boundary-widening", Evidence: "disables the sandbox before dispatching the run"},
			1,
		},
		{
			// whitespace-only / punctuation-only quotes normalize to empty and match everything
			"whitespace evidence is rejected on substring, not just length",
			Proposal{Escalate: "T2", Trigger: "production-default", Evidence: "\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t"},
			1,
		},
		{
			"reflowed quote still verifies (whitespace-normalized)",
			Proposal{Escalate: "T2", Trigger: "gate-machinery", Evidence: "if verdict == nil { return allowMerge()"},
			0,
		},
		{
			"invalid tier is rejected",
			Proposal{Escalate: "T9", Trigger: "gate-machinery", Evidence: "return allowMerge() // absence of signal must not allow"},
			1,
		},
		{
			// PR #4 review: whitespace-padded short evidence passes a raw-byte length gate
			// but normalizes to a common short phrase — must be rejected on normalized length.
			"whitespace-padded short common phrase is rejected (length after normalize)",
			Proposal{Escalate: "T2", Trigger: "gate-machinery", Evidence: "                    return nil"},
			1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// a proposal is either admitted (no failures) or rejected (one or more);
			// a rejected proposal may trip several checks at once — the contract is
			// rejected-vs-admitted, not the exact failure count.
			got := Check(diffText, c.p)
			if (len(got) > 0) != (c.fails > 0) {
				t.Fatalf("Check = %v, want rejected=%v", got, c.fails > 0)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	res := classify(diffText) // internal change -> T1

	t.Run("trusted escalation raises the floor", func(t *testing.T) {
		v := Merge(res, diffText, Proposal{Escalate: "T3", Trigger: "gate-machinery", Evidence: "return allowMerge() // absence of signal must not allow"})
		if v.Final != "T3" || v.Route != "owner+adversarial" {
			t.Fatalf("final = %s route = %s", v.Final, v.Route)
		}
	})
	t.Run("rejected escalation contributes nothing", func(t *testing.T) {
		v := Merge(res, diffText, Proposal{Escalate: "T3", Trigger: "none", Evidence: "return allowMerge() // absence of signal must not allow"})
		if v.Final != v.Floor || len(v.Rejected) == 0 {
			t.Fatalf("final = %s (floor %s), rejected = %v", v.Final, v.Floor, v.Rejected)
		}
	})
	t.Run("advisory can never lower the floor", func(t *testing.T) {
		auth := classify("diff --git a/internal/auth/s.go b/internal/auth/s.go\n+++ b/internal/auth/s.go\n@@\n+func check() {}\n")
		v := Merge(auth, diffText, Proposal{Escalate: "T2", Trigger: "gate-machinery", Evidence: "return allowMerge() // absence of signal must not allow"})
		if v.Final != "T3" {
			t.Fatalf("final = %s, want floor T3 to stand", v.Final)
		}
	})
	t.Run("none keeps the floor", func(t *testing.T) {
		v := Merge(res, diffText, Proposal{})
		if v.Final != v.Floor || v.Escalate != "none" || v.Rejected != nil {
			t.Fatalf("verdict = %+v", v)
		}
	})
}
