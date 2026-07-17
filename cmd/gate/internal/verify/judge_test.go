package verify

import (
	"strings"
	"testing"
)

func TestJudgeEnvForcesEffortOverAmbient(t *testing.T) {
	t.Setenv("CLAUDE_CODE_EFFORT_LEVEL", "low") // an operator/CI default in the ambient env
	env := judgeEnv("high")
	// The CLI honors CLAUDE_CODE_EFFORT_LEVEL over --effort, and Go's exec resolves
	// a duplicated key to its LAST value — so our entry must be the last one.
	last := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDE_CODE_EFFORT_LEVEL=") {
			last = kv
		}
	}
	if last != "CLAUDE_CODE_EFFORT_LEVEL=high" {
		t.Fatalf("ambient effort not overridden; effective entry = %q", last)
	}
}

func TestJudgeModelDefaultAndOverride(t *testing.T) {
	t.Run("default is the pinned premium model", func(t *testing.T) {
		t.Setenv("GATE_JUDGE_MODEL", "") // empty is treated as unset
		if got := judgeModel(); got != defaultJudgeModel {
			t.Fatalf("judgeModel() = %q, want default %q", got, defaultJudgeModel)
		}
	})
	t.Run("env overrides the default", func(t *testing.T) {
		t.Setenv("GATE_JUDGE_MODEL", "claude-sonnet-5")
		if got := judgeModel(); got != "claude-sonnet-5" {
			t.Fatalf("judgeModel() = %q, want the override", got)
		}
	})
}

func TestJudgeEffortDefaultAndOverride(t *testing.T) {
	t.Run("default is high", func(t *testing.T) {
		t.Setenv("GATE_JUDGE_EFFORT", "")
		if got := judgeEffort(); got != defaultJudgeEffort {
			t.Fatalf("judgeEffort() = %q, want default %q", got, defaultJudgeEffort)
		}
	})
	t.Run("env overrides the default", func(t *testing.T) {
		t.Setenv("GATE_JUDGE_EFFORT", "medium")
		if got := judgeEffort(); got != "medium" {
			t.Fatalf("judgeEffort() = %q, want the override", got)
		}
	})
}

// A judgment prompt (which carries diff evidence) must never ride on argv — on
// Windows that overflows the ~32 KiB command-line limit. judgeCommand keeps the
// prompt off argv; AutoJudge pipes it via stdin.
func TestJudgeCommandKeepsPromptOffArgv(t *testing.T) {
	cmd := judgeCommand("claude-fable-5", "high")
	for _, a := range cmd.Args {
		if len(a) > 64 {
			t.Fatalf("argv carries an oversized value (prompt leaked to argv?): %.64q…", a)
		}
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != "high" {
		t.Fatalf("trailing argv = %q; want the effort flag value, not a prompt", got)
	}
}
