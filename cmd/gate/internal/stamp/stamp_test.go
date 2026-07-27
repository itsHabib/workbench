package stamp

import (
	"strings"
	"testing"
)

func TestArgsCarryRunAndHashBoundToHead(t *testing.T) {
	a := Authorized{
		Repo:    "itsHabib/workbench",
		HeadSHA: "479c96224a68eddebfd5cd231c609c6f0e8b39a2",
		Run:     "run_0123456789abcdef",
		Hash:    strings.Repeat("a", 64),
	}
	got := strings.Join(a.args(), " ")

	// The status must bind to the exact judged head, under the success-only
	// authorized context.
	for _, want := range []string{
		"repos/itsHabib/workbench/statuses/479c96224a68eddebfd5cd231c609c6f0e8b39a2",
		"state=success",
		"context=gate/authorized",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q\ngot: %s", want, got)
		}
	}

	// The verifiable payload — run selects the artifact group, hash is the
	// tamper-evident anchor — must ride the description.
	desc := a.description()
	if !strings.Contains(desc, a.Run) || !strings.Contains(desc, a.Hash) {
		t.Errorf("description drops run or hash: %q", desc)
	}
	if len(desc) > 140 {
		t.Errorf("description %d chars exceeds the 140-char status ceiling: %q", len(desc), desc)
	}
}

func TestPRsArgsResolveHeadAssociatedOpenPRs(t *testing.T) {
	a := Authorized{Repo: "itsHabib/workbench", HeadSHA: "deadbeef"}
	got := strings.Join(a.prsArgs(), " ")
	if !strings.Contains(got, "repos/itsHabib/workbench/commits/deadbeef/pulls") {
		t.Errorf("ambiguity guard must query the head's associated PRs\ngot: %s", got)
	}
	// The count must exclude closed/merged PRs — only an open PR can receive a
	// misleading live stamp.
	if !strings.Contains(got, `state=="open"`) {
		t.Errorf("ambiguity guard must count only open PRs\ngot: %s", got)
	}
}

func TestValidateRefusesIncompleteStamp(t *testing.T) {
	cases := map[string]Authorized{
		"no repo":     {HeadSHA: "sha", Run: "run_x", Hash: "h"},
		"no head sha": {Repo: "o/r", Run: "run_x", Hash: "h"},
		"no run":      {Repo: "o/r", HeadSHA: "sha", Hash: "h"},
		"no hash":     {Repo: "o/r", HeadSHA: "sha", Run: "run_x"},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			if err := a.validate(); err == nil {
				t.Errorf("expected validate to refuse %s, got nil", name)
			}
		})
	}

	full := Authorized{Repo: "o/r", HeadSHA: "sha", Run: "run_x", Hash: "h"}
	if err := full.validate(); err != nil {
		t.Errorf("complete stamp should validate, got %v", err)
	}
}
