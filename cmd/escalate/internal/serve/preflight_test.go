package serve

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lookOnly resolves exactly the named binaries and fails everything else, so a
// test can describe a machine's PATH without touching the process's.
func lookOnly(names ...string) LookPath {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/opt/homebrew/bin/" + name, nil
		}
		return "", errors.New(`exec: "` + name + `": executable file not found in $PATH`)
	}
}

// TestPreflightFiresOnAnUnresolvableDependency is the regression this check
// exists for: launchd's PATH is /usr/bin:/bin:/usr/sbin:/sbin, gh lives under
// /opt/homebrew/bin, and every phone approval failed to post its status while
// the Slack card reported success. The ingress must refuse to start instead.
func TestPreflightFiresOnAnUnresolvableDependency(t *testing.T) {
	err := Preflight("gate", lookOnly("gate"))
	if err == nil {
		t.Fatal("an ingress whose child cannot resolve gh must refuse to start")
	}
	for _, want := range []string{"gh", "gate/authorized", "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal must name %q so the operator can act on it, got: %v", want, err)
		}
	}
}

func TestPreflightPassesWhenEveryDependencyResolves(t *testing.T) {
	names := []string{"gate"}
	for _, d := range Dependencies {
		names = append(names, d.Name)
	}
	if err := Preflight("gate", lookOnly(names...)); err != nil {
		t.Fatalf("a complete environment must start: %v", err)
	}
}

// TestPreflightTakesAnAbsoluteGatePathLiterally pins the plist's shape: it
// renders -gate as an absolute path, so a stale one must fail as a missing file
// rather than fall back to a PATH search that could resolve a different build.
func TestPreflightTakesAnAbsoluteGatePathLiterally(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "gate")
	err := Preflight(stale, lookOnly("gate", "gh"))
	if err == nil || !strings.Contains(err.Error(), stale) {
		t.Fatalf("a stale absolute -gate must fail naming the path, got: %v", err)
	}

	usable := filepath.Join(dir, "realgate")
	if err := os.WriteFile(usable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Preflight(usable, lookOnly("gh")); err != nil {
		t.Fatalf("an executable absolute -gate must pass: %v", err)
	}

	notExec := filepath.Join(dir, "notexec")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Preflight(notExec, lookOnly("gh")); err == nil {
		t.Fatal("a non-executable -gate must be refused")
	}
	if err := Preflight("", lookOnly("gh")); err == nil {
		t.Fatal("an empty -gate must be refused")
	}
}
