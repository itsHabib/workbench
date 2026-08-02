package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/codexguard/internal/projection"
)

func TestReviewedRulesWithInstalledExecpolicy(t *testing.T) {
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex CLI is not installed")
	}
	home := filepath.Join(projectionTempDir(t), "codex")
	assets, err := reviewedProjectionAssets()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := projection.Inspect(home, assets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.Apply(home, plan.Digest, assets); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(home, "rules", "workbench-codexguard.rules")
	tests := []struct {
		name     string
		command  []string
		decision string
	}{
		{"merge", []string{"gh", "pr", "merge", "175"}, "prompt"},
		{"grant", []string{"gate", "grant", "-repo", "itsHabib/workbench"}, "forbidden"},
		{"force exact prefix", []string{"git", "push", "--force", "origin", "main"}, "forbidden"},
		{"normal push stays unclassified", []string{"git", "push", "origin", "main"}, ""},
		{"force after ref stays hook-owned", []string{"git", "push", "origin", "main", "--force"}, ""},
		{"safe git global option stays unclassified", []string{"git", "--no-pager", "diff"}, ""},
		{"safe powershell stays unclassified", []string{"powershell", "-NoProfile", "-Command", "Get-Content README.md"}, ""},
		{"safe bash stays unclassified", []string{"bash", "-lc", "git status"}, ""},
		{"safe cmd stays unclassified", []string{"cmd", "/c", "dir"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			args := []string{
				"execpolicy", "check",
				"--rules", rules,
				"--",
			}
			args = append(args, test.command...)
			command := exec.CommandContext(ctx, codex, args...)
			command.Env = append(os.Environ(), "CODEX_HOME="+home)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("codex execpolicy check: %v\n%s", err, output)
			}
			var result struct {
				Decision string `json:"decision"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("decode output: %v\n%s", err, output)
			}
			if result.Decision != test.decision {
				t.Fatalf("decision = %q, want %q\n%s", result.Decision, test.decision, output)
			}
		})
	}
}
