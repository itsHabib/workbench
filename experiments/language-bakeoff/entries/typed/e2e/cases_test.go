package e2e

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/cli"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

const tp = "textpipe"

var textpipeOutputs = []string{"ws/input.txt", "ws/upper.txt", "ws/wordcount.txt"}

// applied is a sandbox where the default textpipe intent has been applied.
func applied(t *testing.T) sandbox {
	t.Helper()
	s := newSandbox(t)
	s.must(cli.ExitOK, nil, tp, "apply", "-dir", "ws")
	return s
}

func TestIntentIsTheNormalizedSource(t *testing.T) {
	s := newSandbox(t)
	r := s.must(cli.ExitOK, nil, tp, "intent")
	golden(t, "textpipe.intent.json", r.stdout)
	if !strings.Contains(r.stdout, `"$ref": "file.input"`) {
		t.Error("intent does not show the input reference")
	}
}

// Case 1: initial plan and apply in a freshly created disposable directory.
func TestCase1InitialPlanAndApply(t *testing.T) {
	s := newSandbox(t)
	r := s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-out", "plans/1.json")
	golden(t, "1-plan.txt", r.stdout)
	golden(t, "1-plan.json", s.read("plans/1.json"))
	if entries, _ := os.ReadDir(s.path("ws")); len(entries) != 0 {
		t.Fatalf("plan changed the workspace: %v", entries)
	}
	assertOps(t, s.plan("plans/1.json"), map[wb.Address]engine.Op{
		"file.input": engine.Create, "transform.upper": engine.Run, "transform.wordcount": engine.Run,
	})

	r = s.must(cli.ExitOK, nil, tp, "apply", "-dir", "ws", "-plan", "plans/1.json")
	golden(t, "1-apply.txt", r.stdout)
	if got := s.read("ws/input.txt"); got != text0 {
		t.Errorf("input.txt = %q", got)
	}
	if got := s.read("ws/upper.txt"); got != strings.ToUpper(text0) {
		t.Errorf("upper.txt = %q", got)
	}
	if got := s.read("ws/wordcount.txt"); got != "9\n" {
		t.Errorf("wordcount.txt = %q", got)
	}
	want := []string{
		"apply file.input", "start transform.upper", "finish transform.upper ok",
		"start transform.wordcount", "finish transform.wordcount ok",
	}
	if got := s.events(); !slices.Equal(got, want) {
		t.Errorf("evidence = %q, want %q", got, want)
	}
}

// Case 2: an unchanged re-apply repeats no effect.
func TestCase2UnchangedReapply(t *testing.T) {
	s := applied(t)
	before := s.identities(textpipeOutputs...)
	evidence := s.read("ws/.wb/evidence.jsonl")

	s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-out", "plans/2.json")
	assertOps(t, s.plan("plans/2.json"), map[wb.Address]engine.Op{
		"file.input": engine.None, "transform.upper": engine.None, "transform.wordcount": engine.None,
	})
	r := s.must(cli.ExitOK, nil, tp, "apply", "-dir", "ws")
	golden(t, "2-reapply.txt", r.stdout)

	s.assertNotRewritten(before)
	if s.read("ws/.wb/evidence.jsonl") != evidence {
		t.Error("an unchanged re-apply wrote evidence")
	}
}

// Case 3: an input change and the downstream plan it produces.
func TestCase3InputChange(t *testing.T) {
	s := applied(t)
	r := s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-var", "text="+text3, "-out", "plans/3.json")
	golden(t, "3-plan-input-change.txt", r.stdout)

	p := s.plan("plans/3.json")
	assertOps(t, p, map[wb.Address]engine.Op{
		"file.input": engine.Update, "transform.upper": engine.Run, "transform.wordcount": engine.Run,
	})
	in := stepOf(p, "file.input")
	if in.Before.Text != text0 || in.After.Text != text3 {
		t.Errorf("file.input diff = %q -> %q", in.Before.Text, in.After.Text)
	}
	for _, a := range []wb.Address{"transform.upper", "transform.wordcount"} {
		if !mentions(stepOf(p, a).Reasons, "input file.input changed") {
			t.Errorf("%s reasons %q do not name the changed input", a, stepOf(p, a).Reasons)
		}
	}

	s.must(cli.ExitOK, nil, tp, "apply", "-dir", "ws", "-plan", "plans/3.json")
	if got := s.read("ws/upper.txt"); got != strings.ToUpper(text3) {
		t.Errorf("upper.txt = %q", got)
	}
	if got := s.read("ws/wordcount.txt"); got != "11\n" {
		t.Errorf("wordcount.txt = %q", got)
	}
}

// Case 4: an external modification between plan and apply makes the saved
// plan stale. Nothing is applied; a new plan shows the real change.
func TestCase4ExternalEditBetweenPlanAndApply(t *testing.T) {
	s := applied(t)
	s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-var", "text="+text4, "-out", "plans/4.json")
	s.write("ws/input.txt", "edited by hand\n")
	before := s.identities("ws/upper.txt", "ws/wordcount.txt")
	evidence := s.read("ws/.wb/evidence.jsonl")

	r := s.must(cli.ExitStale, nil, tp, "apply", "-dir", "ws", "-plan", "plans/4.json")
	golden(t, "4-apply-stale.txt", r.stderr)
	if !strings.Contains(r.stderr, "file.input: input.txt was") {
		t.Errorf("refusal does not name the drifted node:\n%s", r.stderr)
	}
	if got := s.read("ws/input.txt"); got != "edited by hand\n" {
		t.Errorf("stale apply touched input.txt: %q", got)
	}
	if s.read("ws/.wb/evidence.jsonl") != evidence {
		t.Error("stale apply wrote evidence")
	}
	s.assertNotRewritten(before)

	r = s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-var", "text="+text4, "-out", "plans/4b.json")
	golden(t, "4-replan.txt", r.stdout)
	if got := stepOf(s.plan("plans/4b.json"), "file.input").Before.Text; got != "edited by hand\n" {
		t.Errorf("re-plan does not show the hand edit it overwrites: %q", got)
	}
	s.must(cli.ExitOK, nil, tp, "apply", "-dir", "ws", "-plan", "plans/4b.json")
	if got := s.read("ws/input.txt"); got != text4 {
		t.Errorf("input.txt = %q", got)
	}
}

// Case 5: a partial failure, then a retry that repeats no completed effect.
func TestCase5PartialFailureAndRetry(t *testing.T) {
	s := applied(t)
	fault := []string{"WB_FAULT=fail:transform.wordcount"}
	r := s.must(cli.ExitFailed, fault, tp, "apply", "-dir", "ws", "-var", "text="+text5)
	golden(t, "5-apply-partial-failure.txt", r.stdout)
	if got := s.read("ws/upper.txt"); got != strings.ToUpper(text5) {
		t.Errorf("upper.txt = %q; the unrelated task should have completed", got)
	}
	if got := s.read("ws/wordcount.txt"); got != "9\n" {
		t.Errorf("wordcount.txt = %q; a failed run must leave the old output", got)
	}
	done := s.identities("ws/input.txt", "ws/upper.txt")

	r = s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-var", "text="+text5, "-out", "plans/5.json")
	golden(t, "5-plan-retry.txt", r.stdout)
	assertOps(t, s.plan("plans/5.json"), map[wb.Address]engine.Op{
		"file.input": engine.None, "transform.upper": engine.None, "transform.wordcount": engine.Run,
	})
	s.must(cli.ExitOK, nil, tp, "apply", "-dir", "ws", "-plan", "plans/5.json")

	if got := s.read("ws/wordcount.txt"); got != "4\n" {
		t.Errorf("wordcount.txt = %q", got)
	}
	s.assertNotRewritten(done)
	if n := s.starts("transform.upper"); n != 2 {
		t.Errorf("transform.upper started %d times, want 2 (initial apply + the failed apply, none on retry)", n)
	}
}

// Interrupted run: the process dies after a task's output is committed and
// before its finish record. A fresh caller needs only the workspace and
// its evidence to see what happened and recover.
func TestInterruptedRunRecovery(t *testing.T) {
	s := newSandbox(t)
	r := s.run([]string{"WB_FAULT=crash:transform.upper"}, tp, "apply", "-dir", "ws")
	if !r.killedBySig {
		t.Fatalf("exit %d, want the process killed by the crash fault", r.code)
	}
	if got := s.read("ws/upper.txt"); got != strings.ToUpper(text0) {
		t.Fatalf("upper.txt = %q; the effect should have committed before the crash", got)
	}
	if got := s.events(); !slices.Equal(got, []string{"apply file.input", "start transform.upper"}) {
		t.Fatalf("evidence = %q, want a start with no finish", got)
	}

	r = s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-out", "plans/after-crash.json")
	golden(t, "5b-plan-after-crash.txt", r.stdout)
	p := s.plan("plans/after-crash.json")
	if !mentions(stepOf(p, "transform.upper").Reasons, "interrupted") {
		t.Errorf("plan does not report the interrupted run: %q", stepOf(p, "transform.upper").Reasons)
	}
	s.must(cli.ExitOK, nil, tp, "apply", "-dir", "ws", "-plan", "plans/after-crash.json")
	if n := s.starts("transform.upper"); n != 2 {
		t.Errorf("transform.upper started %d times, want 2: replay is at-least-once", n)
	}
	s.must(cli.ExitOK, nil, tp, "plan", "-dir", "ws", "-out", "plans/final.json")
	for _, st := range s.plan("plans/final.json").Steps {
		if st.Op != engine.None {
			t.Errorf("%s still pending after recovery: %s", st.Address, st.Op)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	s := newSandbox(t)
	s.must(cli.ExitUsage, nil, tp, "plan")
	s.must(cli.ExitUsage, nil, tp, "apply", "-dir", "ws", "-plan", "p.json", "-var", "text=x")
	r := s.must(cli.ExitFailed, nil, tp, "plan", "-dir", "ws", "-var", "txt=typo")
	if !strings.Contains(r.stderr, "unknown param(s) txt") {
		t.Errorf("stderr = %q", r.stderr)
	}
}
