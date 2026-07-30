package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStagedRulesetPlanIsPinned(t *testing.T) {
	path := filepath.Join(
		"..", "..", "docs", "features",
		"trusted-gate-judgment-bridge", "ruleset-plan-v1.json",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("staged ruleset plan is not valid JSON")
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	const want = "fcdb17126657008498c86a23c48478432ef53be9d4981a0d70978727adfc6fd3"
	if got != want {
		t.Fatalf("staged ruleset plan changed: hash = %s, want %s", got, want)
	}
}
