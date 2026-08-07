package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/observe"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func TestLookupOpenPRsContextCancelsStalledGH(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := lookupOpenPRsContext(ctx, "o/r", func(ctx context.Context, _ string) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled gh lookup was not cancelled promptly: %v", elapsed)
	}
	if !strings.Contains(err.Error(), "list PRs o/r") {
		t.Fatalf("timeout must identify the failed repo: %v", err)
	}
}

// TestCommonFlagsEnvDefaults pins step 1: -state and -key default to
// $GATE_STATE / $GATE_KEY when set, and an explicit flag still wins.
func TestCommonFlagsEnvDefaults(t *testing.T) {
	t.Setenv("GATE_STATE", "/canon/state")
	t.Setenv("GATE_KEY", "/canon/keys")

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	sd, _, kd := commonFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *sd != "/canon/state" || *kd != "/canon/keys" {
		t.Fatalf("env defaults not applied: state=%q key=%q", *sd, *kd)
	}

	// Explicit beats ambient: a passed -state overrides the env default.
	fs2 := flag.NewFlagSet("t2", flag.ContinueOnError)
	sd2, _, _ := commonFlags(fs2)
	if err := fs2.Parse([]string{"-state", "/explicit"}); err != nil {
		t.Fatal(err)
	}
	if *sd2 != "/explicit" {
		t.Fatalf("explicit -state must override $GATE_STATE, got %q", *sd2)
	}
}

// TestCommonFlagsNoEnvFallback pins the fallbacks when the env is unset: -state
// stays the relative "state", -key stays empty (later resolved to the user
// config dir).
func TestCommonFlagsNoEnvFallback(t *testing.T) {
	t.Setenv("GATE_STATE", "")
	t.Setenv("GATE_KEY", "")
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	sd, _, kd := commonFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *sd != "state" || *kd != "" {
		t.Fatalf("fallbacks wrong: state=%q key=%q", *sd, *kd)
	}
}

func TestStateArgFor(t *testing.T) {
	t.Setenv("GATE_STATE", "")
	if got := stateArgFor("state"); got != "" {
		t.Errorf("ambient default should omit -state, got %q", got)
	}
	if got := stateArgFor("/custom"); got != " -state /custom" {
		t.Errorf("explicit dir should splice -state, got %q", got)
	}
	if got := stateArgFor("/has space"); got != ` -state "/has space"` {
		t.Errorf("a spaced path must be quoted, got %q", got)
	}

	t.Setenv("GATE_STATE", "/canon")
	if got := stateArgFor("/canon"); got != "" {
		t.Errorf("dir matching $GATE_STATE should omit -state, got %q", got)
	}
	if got := stateArgFor("/other"); got != " -state /other" {
		t.Errorf("dir differing from $GATE_STATE should splice -state, got %q", got)
	}
}

// TestNextCommandEndToEnd drives `gate next -json` through the real verb: env
// defaults resolve the state dir, the store is read, and the projection reaches
// stdout with clean (no -state) suggested commands because the dir is ambient.
func TestNextCommandEndToEnd(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	keyDir := filepath.Join(root, "keys")

	// Seed a parked run and its grant directly through the state API — no gh or
	// evidence gathering needed to exercise the inbox read.
	st, err := state.Open(stateDir, func() time.Time { return time.Unix(1_000_000, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	// cmdNext resolves expiry against the real wall clock (time.Now), so the
	// grant's ExpiresAt must be live against it, not against the store's stub now.
	if _, err := st.Append(state.KindGrant, "run_mint", nil, capability.Grant{
		Repo: "o/r", Action: "merge", MaxTier: "T1", MaxCycles: 3,
		ExpiresAt: time.Now().UTC().Add(3 * time.Hour), MintedBy: "test", Sig: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindEscalation, "run_park", []string{"vrd_x", "grt_x"}, map[string]any{
		"outcome": "parked_for_judgment", "verdict": "vrd_x", "grant": "grt_x",
		"question": "tier T2 exceeds ceiling T1", "code": "grant_tier_exceeded", "repo": "o/r", "number": 42,
	}); err != nil {
		t.Fatal(err)
	}

	// Point the ambient env at the fixture so `gate next` needs no -state flag,
	// and the suggested commands stay clean.
	t.Setenv("GATE_STATE", stateDir)
	t.Setenv("GATE_KEY", keyDir)

	out := captureStdout(t, func() error { return cmdNext([]string{"-json"}) })

	var in observe.Inbox
	if err := json.Unmarshal([]byte(out), &in); err != nil {
		t.Fatalf("next -json is not valid JSON: %v\n%s", err, out)
	}
	if len(in.Parked) != 1 {
		t.Fatalf("want 1 parked run, got %d", len(in.Parked))
	}
	p := in.Parked[0]
	if p.Run != "run_park" || p.Repo != "o/r" || p.Number != 42 || p.Grant != "grt_x" {
		t.Fatalf("parked projection wrong: %+v", p)
	}
	// The fixture park is a ceiling park: no judge command (judging under the
	// same grant re-applies the ceiling), and the derived escape stays clean of
	// -state under the ambient env.
	if p.JudgeCommand != "" {
		t.Fatalf("ceiling park must not advertise a judge command, got %q", p.JudgeCommand)
	}
	if p.Escape == nil || p.Escape.Next != "gate explain -run run_park" {
		t.Fatalf("ambient ceiling escape should omit -state, got %+v", p.Escape)
	}
	if len(in.Grants) != 1 || in.Grants[0].Repo != "o/r" || in.Grants[0].Expired {
		t.Fatalf("grant ledger wrong: %+v", in.Grants)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// wrote, mirroring the explain-flag tests.
func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if runErr != nil {
		t.Fatalf("command returned error: %v", runErr)
	}
	return strings.TrimSpace(string(out))
}

// TestStartNextProfilingWritesCPUProfile pins the debug profiling seam: passing
// a cpuprofile path produces a non-empty profile after the stop closure flushes.
func TestStartNextProfilingWritesCPUProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")
	stop, err := startNextProfiling(path, "", "")
	if err != nil {
		t.Fatalf("startNextProfiling: %v", err)
	}
	// A little work so the CPU profile has at least one sample to flush.
	sum := 0
	for i := 0; i < 1_000_000; i++ {
		sum += i
	}
	_ = sum
	stop()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cpu profile: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("cpu profile is empty")
	}
}
