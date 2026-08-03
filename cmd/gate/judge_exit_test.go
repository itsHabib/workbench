package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// parkedFixture builds a real parked run — verifier verdict, reduced escalate
// verdict, escalation, and a live merge grant — in a throwaway state dir, and
// returns the dirs and ids a `gate judge` invocation needs to reach it.
type parkedFixture struct {
	stateDir string
	keyDir   string
	run      string
	grant    string
}

func newParkedFixture(t *testing.T) parkedFixture {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	keyDir := filepath.Join(root, "keys")
	e, err := newEnv(stateDir, "triage-floor", keyDir)
	if err != nil {
		t.Fatal(err)
	}
	grantArt, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T2", 0, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "head"}
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	v := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, grantArt.ID, v, id, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("fixture must park: code %d err %v", code, err)
	}
	return parkedFixture{stateDir: stateDir, keyDir: keyDir, run: run, grant: grantArt.ID}
}

// TestJudgeUnknownRunIsNotReportedAsUnparked pins the diagnostic split: a run
// that is absent from the searched state dir and a run that is present but
// never parked are different operator mistakes. Both used to surface as "has
// no escalation to resolve", which sent the operator looking for a missing
// escalation artifact when the real cause was a -state pointing at the wrong
// custody dir — the far more common slip, since GATE_STATE is not reliably
// exported into an agent's shell.
func TestJudgeUnknownRunIsNotReportedAsUnparked(t *testing.T) {
	f := newParkedFixture(t)
	err := cmdJudge([]string{
		"-run", "run_absent",
		"-grant", f.grant,
		"-decision", "pass",
		"-why", "because",
		"-state", f.stateDir,
		"-key", f.keyDir,
	})
	if err == nil {
		t.Fatal("judging an absent run must fail")
	}
	if strings.Contains(err.Error(), "no escalation to resolve") {
		t.Fatalf("an absent run must not be reported as unparked: %v", err)
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), f.stateDir) {
		t.Fatalf("the refusal must name the state dir actually searched, got: %v", err)
	}
}

// TestJudgeUnknownRunNamesAnAbsoluteStateDir keeps the refusal diagnostic under
// the condition it exists for. -state routinely carries a relative path, and a
// refusal whose whole job is naming the dir that was searched leaves the
// operator no better off if it echoes "state" back at them.
func TestJudgeUnknownRunNamesAnAbsoluteStateDir(t *testing.T) {
	f := newParkedFixture(t)
	parent, dir := filepath.Split(strings.TrimSuffix(f.stateDir, string(filepath.Separator)))
	t.Chdir(parent)

	err := cmdJudge([]string{
		"-run", "run_absent",
		"-grant", f.grant,
		"-decision", "pass",
		"-why", "because",
		"-state", dir,
		"-key", f.keyDir,
	})
	if err == nil {
		t.Fatal("judging an absent run must fail")
	}
	named := strings.TrimSpace(strings.TrimPrefix(err.Error(), "judge: run run_absent not found in state dir "))
	if !filepath.IsAbs(named) {
		t.Fatalf("the refusal must resolve a relative -state to an absolute path, got: %v", err)
	}
}

// TestJudgeKnownRunWithoutEscalationStillReportsUnparked keeps the other half
// of the split honest: a run that really is present and really has no
// escalation must still say so.
func TestJudgeKnownRunWithoutEscalationStillReportsUnparked(t *testing.T) {
	f := newParkedFixture(t)
	e, err := newEnv(f.stateDir, "triage-floor", f.keyDir)
	if err != nil {
		t.Fatal(err)
	}
	unparked := state.NewRunID()
	recordVerifier(t, e, unparked, verify.Subject{Repo: "o/r", Number: 8, HeadSHA: "head"}, verify.DecisionPass)

	err = cmdJudge([]string{
		"-run", unparked,
		"-grant", f.grant,
		"-decision", "pass",
		"-why", "because",
		"-state", f.stateDir,
		"-key", f.keyDir,
	})
	if err == nil || !strings.Contains(err.Error(), "no escalation to resolve") {
		t.Fatalf("a present, unparked run must report no escalation, got: %v", err)
	}
}

// judgeExitCases are the three ways `gate judge` fails that an operator hits in
// practice: the run cannot be found, a submitted artifact does not decode, and
// the auto-judge provider cannot be run.
var judgeExitCases = []string{"unknown-run", "malformed-judgment", "provider-failed"}

// TestJudgeErrorPathsExitError pins the driver contract at the judge verb: every
// judge failure exits codeError (4). Exit 0 on a failed judgment is the
// dangerous reading — a caller that checks only the status would take a
// deserialization failure for an authorized merge — and exit 2 would read as a
// fresh park. The subprocess re-exec observes the real os.Exit path, not the
// error value the mapping is derived from.
func TestJudgeErrorPathsExitError(t *testing.T) {
	if c := os.Getenv("GATE_JUDGE_EXIT_HELPER"); c != "" {
		judgeExitHelper(c)
		return
	}
	f := newParkedFixture(t)
	badJudgment := filepath.Join(t.TempDir(), "judgment.json")
	if err := os.WriteFile(badJudgment, []byte(`{"version":"gate-judgment-v1",`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range judgeExitCases {
		t.Run(c, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestJudgeErrorPathsExitError")
			cmd.Env = append(os.Environ(),
				"GATE_JUDGE_EXIT_HELPER="+c,
				"GATE_TEST_STATE="+f.stateDir,
				"GATE_TEST_KEY="+f.keyDir,
				"GATE_TEST_RUN="+f.run,
				"GATE_TEST_GRANT="+f.grant,
				"GATE_TEST_JUDGMENT="+badJudgment,
				// An empty PATH is what makes the provider case fail without
				// running — or paying for — a real judge provider.
				"PATH=",
			)
			out, err := cmd.CombinedOutput()
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("want a non-zero exit from %s, got err %v out %s", c, err, out)
			}
			if ee.ExitCode() != codeError {
				t.Fatalf("judge %s exited %d, want %d — out: %s", c, ee.ExitCode(), codeError, out)
			}
		})
	}
}

func judgeExitHelper(c string) {
	common := []string{
		"-grant", os.Getenv("GATE_TEST_GRANT"),
		"-state", os.Getenv("GATE_TEST_STATE"),
		"-key", os.Getenv("GATE_TEST_KEY"),
		"-stamp=false",
	}
	run := os.Getenv("GATE_TEST_RUN")
	switch c {
	case "unknown-run":
		os.Args = append([]string{"gate", "judge", "-run", "run_absent", "-decision", "pass", "-why", "because"}, common...)
	case "malformed-judgment":
		os.Args = append([]string{"gate", "judge", "-run", run, "-judgment", os.Getenv("GATE_TEST_JUDGMENT")}, common...)
	case "provider-failed":
		os.Args = append([]string{"gate", "judge", "-run", run, "-auto", "-provider", "codex"}, common...)
	}
	main()
}
