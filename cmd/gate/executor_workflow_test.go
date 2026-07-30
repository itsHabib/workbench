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
	for _, forbidden := range []string{"gate executor run", "gh pr merge", "operation == 'execute'"} {
		if strings.Contains(preparation, forbidden) {
			t.Fatalf("preparation contains merge-path token %q", forbidden)
		}
	}
	if !strings.Contains(workflow[end:], "uses: ./.github/actions/gate-executor") {
		t.Fatal("exact-action execution step is missing")
	}
}
