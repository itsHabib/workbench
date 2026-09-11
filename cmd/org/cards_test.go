package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func cardCommand(t *testing.T, state string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := run(append(args, "-state", state), strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}

func TestEditableRoleHasNoLifecycle(t *testing.T) {
	state := t.TempDir()
	file := filepath.Join(t.TempDir(), "lead.md")
	if err := os.WriteFile(file, []byte("Lead repo A. Ask peers directly."), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"charter", "-tenant", "acme", "-role", "lead:anything", "-file", file, "-parent", "human:op"}
	if code, _, err := cardCommand(t, state, args...); code != 0 {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("Lead repos A and B. Add children as needed."), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, err := cardCommand(t, state, "boot", "-tenant", "acme", "-role", "lead:anything")
	if code != 0 || !strings.Contains(out, "repos A and B") || !strings.Contains(out, "human:op") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	// Registering the same name again updates config, retaining an omitted parent.
	if code, _, err := cardCommand(t, state, args[:len(args)-2]...); code != 0 {
		t.Fatal(err)
	}
	cards, errLoad := loadCards(state)
	if errLoad != nil || len(cards) != 1 || cards[0].Parent != "human:op" {
		t.Fatalf("%+v %v", cards, errLoad)
	}
	if _, err := os.Stat(filepath.Join(state, "acme")); !os.IsNotExist(err) {
		t.Fatalf("card created a chain directory: %v", err)
	}
	// A runtime verb cannot silently enroll new work in a chain.
	if code, _, _ := cardCommand(t, state, "attach", "-role", "lead:anything"); code != codeUsage {
		t.Fatal(code)
	}
	code, out, err = cardCommand(t, state, "status", "-tenant", "another", "-json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("tenant leak: %d %s %s", code, out, err)
	}
}

func TestCardRegistryRetainsLegacyAndConcurrentRoles(t *testing.T) {
	state := t.TempDir()
	if code, _, err := exec(t, state, "charter", "-role", "lead:old", "-tenant", "mh", "-scope", "github:acme/api"); code != 0 {
		t.Fatal(err)
	}
	chain := filepath.Join(state, "mh", "lead--old", "chain.jsonl")
	before, err := os.ReadFile(chain)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "card.md")
	if err := os.WriteFile(file, []byte("A role."), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, role := range []string{"lead:old", "lead:new"} {
		wg.Go(func() {
			c := roleCard{Tenant: "mh", Role: role, Card: file}
			if err := saveCard(state, &c, true); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	cards, err := loadCards(state)
	if err != nil || len(cards) != 2 {
		t.Fatalf("lost registry write: %+v %v", cards, err)
	}
	after, err := os.ReadFile(chain)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("legacy history changed: %v", err)
	}
	code, out, errText := cardCommand(t, state, "legacy", "boot", "-role", "lead:old")
	if code != 0 || !strings.Contains(out, "baton boot") {
		t.Fatalf("legacy reader unavailable: %d %s %s", code, out, errText)
	}
}

func TestBrokenRegistryIsNeverOverwritten(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "roles.json")
	before := []byte(`[{"tenant":"mh",`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	c := roleCard{Tenant: "mh", Role: "x", Card: "/card.md"}
	if err := saveCard(state, &c, true); err == nil {
		t.Fatal("accepted corrupt registry")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("registry overwritten: %v", err)
	}
}

func TestCardBudgetAndMissingFileAreVisible(t *testing.T) {
	file := filepath.Join(t.TempDir(), "card.md")
	if err := os.WriteFile(file, []byte("ééé"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e := &env{stdout: &out}
	if err := showCard(e, roleCard{Role: "x", Card: file}, 3, true); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["instructions"] != "é" || row["truncated"] != true || row["card"] != file {
		t.Fatal(row)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := showCard(e, roleCard{Role: "x", Card: file}, 0, false); err == nil {
		t.Fatal("missing prose passed")
	}
}
