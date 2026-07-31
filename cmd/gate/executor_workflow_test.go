package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutorWorkflowKeepsPreparationAndMergeSeparate(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "gate-executor.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	start := strings.Index(workflow, "- name: Evaluate and publish hosted Gate action without merging")
	end := strings.Index(workflow, "- name: Claim, refetch, merge, and record")
	if start < 0 || end <= start {
		t.Fatal("preparation and execution steps are missing or reordered")
	}
	preparation := workflow[start:end]
	if !strings.Contains(preparation, "gate executor prepare") {
		t.Fatal("preparation does not invoke the publish-only command")
	}
	if !strings.Contains(preparation, "GATE_ANCHOR_RECORD: ${{ github.workspace }}/hosted-state/anchor.json") {
		t.Fatal("host preparation does not use the runner workspace anchor path")
	}
	if strings.Contains(preparation, "GATE_ANCHOR_RECORD: /github/workspace/") {
		t.Fatal("host preparation uses the container workspace anchor path")
	}
	for _, forbidden := range []string{"gate executor run", "gh pr merge", "operation == 'execute'"} {
		if strings.Contains(preparation, forbidden) {
			t.Fatalf("preparation contains merge-path token %q", forbidden)
		}
	}
	execution := workflow[end:]
	if !strings.Contains(execution, "uses: ./.github/actions/gate-executor") {
		t.Fatal("exact-action execution step is missing")
	}
	if !strings.Contains(execution, "GATE_ANCHOR_RECORD: /github/workspace/hosted-state/anchor.json") {
		t.Fatal("container execution does not use the container workspace anchor path")
	}
	if count := strings.Count(execution, "INPUT_APP_PRIVATE_KEY: ${{ secrets.GATE_APP_PRIVATE_KEY }}"); count != 2 {
		t.Fatalf("executor and reconciler expose the App key to their process environment %d times, want 2", count)
	}
	if strings.Contains(execution, "app-private-key:") {
		t.Fatal("container action receives the App key through a hyphenated input")
	}
	for _, action := range []string{"gate-executor", "gate-reconciler"} {
		path := filepath.Join("..", "..", ".github", "actions", action, "action.yml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "app-private-key:") {
			t.Fatalf("%s declares a hyphenated App-key input", action)
		}
	}
}
