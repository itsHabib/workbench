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

	// The default path reconciles, so inject a seam reporting the fixture PR
	// open — otherwise the live pass would correctly drop a row for a PR that
	// does not exist on GitHub, and this test asserts the projection, not the
	// reconcile (which has its own cases below).
	out := captureStdout(t, func() error { return runNext([]string{"-json"}, openFixture(42)) })

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
	// Nor a resolve line: a ceiling park needs a wider grant from the operator,
	// not a human decision — `escalate resolve` would re-apply the same ceiling.
	if p.ResolveCommand != "" {
		t.Fatalf("ceiling park must not advertise a resolve command, got %q", p.ResolveCommand)
	}
	if len(in.Grants) != 1 || in.Grants[0].Repo != "o/r" || in.Grants[0].Expired {
		t.Fatalf("grant ledger wrong: %+v", in.Grants)
	}
}

// TestNextCommandRendersResolveLine pins the paste-ready human route: an open
// (non-ceiling) park prints the `escalate resolve` line carrying its real
// escalation id and grant, alongside the unchanged `gate judge` line. A
// grantless park in the same inbox prints neither — escalate's ingest refuses
// an empty grant.
func TestNextCommandRendersResolveLine(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")

	st, err := state.Open(stateDir, func() time.Time { return time.Unix(1_000_000, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindGrant, "run_mint", nil, capability.Grant{
		Repo: "o/r", Action: "merge", MaxTier: "T2", MaxCycles: 3,
		ExpiresAt: time.Now().UTC().Add(3 * time.Hour), MintedBy: "test", Sig: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	park, err := st.Append(state.KindEscalation, "run_open", []string{"vrd_x", "grt_x"}, map[string]any{
		"outcome": "parked_for_judgment", "verdict": "vrd_x", "grant": "grt_x",
		"question": "reviewer coverage incomplete", "code": "panel_incomplete",
		"repo": "o/r", "number": 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindEscalation, "run_grantless", []string{"vrd_y"}, map[string]any{
		"outcome": "parked_for_judgment", "verdict": "vrd_y",
		"question": "no grant on record", "code": "panel_incomplete",
		"repo": "o/r", "number": 43,
	}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GATE_STATE", stateDir)
	t.Setenv("GATE_KEY", filepath.Join(root, "keys"))

	out := captureStdout(t, func() error { return runNext(nil, openFixture(42, 43)) })

	want := "escalate resolve -escalation " + park.ID +
		" -grant grt_x -decision <pass|block> -who <you> -why \"...\""
	if !strings.Contains(out, want) {
		t.Fatalf("gate next must print %q:\n%s", want, out)
	}
	if !strings.Contains(out, "gate judge -run run_open -grant grt_x") {
		t.Fatalf("the judge line must be unchanged:\n%s", out)
	}
	if strings.Count(out, "escalate resolve") != 1 {
		t.Fatalf("a grantless park must print no resolve line:\n%s", out)
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

// openFixture is a live seam reporting exactly the given PR numbers as open, for
// the repo the inbox fixtures use. Tests asserting the projection inject it so
// the default (reconciling) path does not reach the network.
func openFixture(numbers ...int) observe.OpenPRs {
	return func(repo string) (map[int]observe.LivePR, error) {
		out := make(map[int]observe.LivePR, len(numbers))
		for _, n := range numbers {
			out[n] = observe.LivePR{State: "OPEN", Title: "fixture", URL: "https://github.com/" + repo + "/pull/1"}
		}
		return out, nil
	}
}

// TestNextReconcilesByDefault pins the default: `gate next` with no flags
// reconciles, so a parked row whose PR has since merged (absent from the open
// set) is DROPPED rather than shown. This is the whole point of the default —
// an un-reconciled inbox accumulates finished work and reports it as pending.
func TestNextReconcilesByDefault(t *testing.T) {
	stateDir := seedParkedInbox(t)
	t.Setenv("GATE_STATE", stateDir)
	t.Setenv("GATE_KEY", t.TempDir())

	called := false
	// The PR is absent from the open set: it merged.
	merged := func(string) (map[int]observe.LivePR, error) {
		called = true
		return map[int]observe.LivePR{}, nil
	}
	out := captureStdout(t, func() error { return runNext(nil, merged) })

	if !called {
		t.Fatal("the default path must reconcile against the live seam")
	}
	if strings.Contains(out, "run_park") {
		t.Fatalf("a merged subject must not appear in the default view:\n%s", out)
	}
}

// TestNextCachedSkipsTheReconcile pins the opt-out: -cached must not touch the
// live seam, and keeps the row the log alone still believes is open.
func TestNextCachedSkipsTheReconcile(t *testing.T) {
	stateDir := seedParkedInbox(t)
	t.Setenv("GATE_STATE", stateDir)
	t.Setenv("GATE_KEY", t.TempDir())

	fetch := func(string) (map[int]observe.LivePR, error) {
		t.Fatal("-cached must not reach the live seam")
		return nil, nil
	}
	out := captureStdout(t, func() error { return runNext([]string{"-cached"}, fetch) })

	if !strings.Contains(out, "run_park") {
		t.Fatalf("-cached must project the log alone:\n%s", out)
	}
}

// TestNextLiveFlagStillAccepted pins the compatibility promise: -live was the
// flag that turned reconciling ON, so every pasted command and older doc still
// carries it. It must parse and behave exactly like the default, never error.
func TestNextLiveFlagStillAccepted(t *testing.T) {
	stateDir := seedParkedInbox(t)
	t.Setenv("GATE_STATE", stateDir)
	t.Setenv("GATE_KEY", t.TempDir())

	called := false
	merged := func(string) (map[int]observe.LivePR, error) {
		called = true
		return map[int]observe.LivePR{}, nil
	}
	// captureStdout fails the test if the verb returns an error, which is the
	// assertion: -live must parse, not be rejected as an unknown flag.
	captureStdout(t, func() error { return runNext([]string{"-live"}, merged) })
	if !called {
		t.Fatal("-live must reconcile, exactly like the default")
	}
}

// seedParkedInbox writes a state dir holding one live grant and one parked run
// on o/r#42, the shape the inbox projects.
func seedParkedInbox(t *testing.T) string {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	st, err := state.Open(stateDir, func() time.Time { return time.Unix(1_000_000, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindGrant, "run_mint", nil, capability.Grant{
		Repo: "o/r", Action: "merge", MaxTier: "T1", MaxCycles: 3,
		ExpiresAt: time.Now().UTC().Add(3 * time.Hour), MintedBy: "test", Sig: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindEscalation, "run_park", []string{"vrd_x", "grt_x"}, map[string]any{
		"outcome": "parked_for_judgment", "verdict": "vrd_x", "grant": "grt_x",
		"question": "needs judgment", "code": "panel_incomplete", "repo": "o/r", "number": 42,
	}); err != nil {
		t.Fatal(err)
	}
	return stateDir
}
