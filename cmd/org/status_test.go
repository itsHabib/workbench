package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/org/internal/render"
)

func TestStatusSelectsConfiguredTenant(t *testing.T) {
	state := t.TempDir()
	for _, tenant := range []string{"acme", "beta", "mh"} {
		code, _, errOut := exec(t, state, "charter", "-tenant", tenant,
			"-role", "lead:platform", "-scope", "github:acme/api")
		if code != 0 {
			t.Fatalf("charter %s: %s", tenant, errOut)
		}
	}
	// A corrupt chain in an unrelated tenant must not break a scoped read.
	statusFile(t, state, "broken", "lead:platform", "chain.jsonl", "not JSON\n")
	t.Setenv("ORG_TENANT", "beta")
	rows := statusRowsForTest(t, state, "-tenant", "acme")
	if len(rows) != 1 || rows[0].Tenant != "acme" || rows[0].Role != "lead:platform" {
		t.Fatalf("explicit tenant: %+v", rows)
	}
	rows = statusRowsForTest(t, state)
	if len(rows) != 1 || rows[0].Tenant != "beta" {
		t.Fatalf("environment tenant: %+v", rows)
	}
	t.Setenv("ORG_TENANT", "")
	rows = statusRowsForTest(t, state)
	if len(rows) != 1 || rows[0].Tenant != "mh" {
		t.Fatalf("default tenant: %+v", rows)
	}
}

func TestStatusSkipsDirectoriesWithoutRecords(t *testing.T) {
	state := t.TempDir()
	// Exercise the real refused-first-write path, which leaves a lock behind.
	code, _, errOut := exec(t, state, "attach", "-tenant", "acme", "-role", "lead:ghost")
	if code != codeRefused {
		t.Fatalf("attach without charter: exit %d: %s", code, errOut)
	}
	lock := filepath.Join(state, "acme", "lead--ghost", "lock")
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("refused attach did not leave expected lock: %v", err)
	}
	statusFile(t, state, "acme", "lead:empty", "chain.jsonl", "")
	code, _, errOut = exec(t, state, "charter", "-tenant", "acme",
		"-role", "lead:real", "-scope", "github:acme/api")
	if code != 0 {
		t.Fatalf("charter: %s", errOut)
	}
	rows := statusRowsForTest(t, state, "-tenant", "acme")
	if len(rows) != 1 || rows[0].Role != "lead:real" || rows[0].Seq != 1 {
		t.Fatalf("directories became phantom roles: %+v", rows)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("status removed the existing lock: %v", err)
	}
}

func TestStatusEmptyTenantReturnsJSONArray(t *testing.T) {
	for _, contents := range []string{"absent", "lock-only", "empty-chain"} {
		t.Run(contents, func(t *testing.T) {
			state := t.TempDir()
			if contents == "lock-only" {
				statusFile(t, state, "acme", "lead:ghost", "lock", "")
			}
			if contents == "empty-chain" {
				statusFile(t, state, "acme", "lead:ghost", "chain.jsonl", "")
			}
			rows := statusRowsForTest(t, state, "-tenant", "acme")
			if rows == nil || len(rows) != 0 {
				t.Fatalf("expected non-null empty array, got %+v", rows)
			}
			code, out, errOut := exec(t, state, "status", "-tenant", "acme")
			if code != 0 || out != "no roles chartered\n" || errOut != "" {
				t.Fatalf("empty text status: exit %d, stdout %q, stderr %q", code, out, errOut)
			}
		})
	}
}

func TestStatusStillRejectsBrokenSelectedChain(t *testing.T) {
	for _, tt := range []struct {
		name, contents string
		code           int
	}{
		{"invalid JSON", "not JSON\n", codeError},
		{"invalid record", "{}\n", codeRefused},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := t.TempDir()
			statusFile(t, state, "acme", "lead:broken", "chain.jsonl", tt.contents)
			code, out, errOut := exec(t, state, "status", "-tenant", "acme", "-json")
			if code != tt.code || out != "" || errOut == "" {
				t.Fatalf("broken chain hidden: exit %d, stdout %q, stderr %q", code, out, errOut)
			}
		})
	}
}

func statusRowsForTest(t *testing.T, state string, args ...string) []render.Row {
	t.Helper()
	code, out, errOut := exec(t, state, append([]string{"status", "-json"}, args...)...)
	if code != 0 || errOut != "" {
		t.Fatalf("status: exit %d: %s", code, errOut)
	}
	var rows []render.Row
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("status did not emit a JSON array: %v: %s", err, out)
	}
	return rows
}

func statusFile(t *testing.T, state, tenant, role, name, contents string) {
	t.Helper()
	dir := filepath.Join(state, tenant, strings.ReplaceAll(role, ":", "--"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
