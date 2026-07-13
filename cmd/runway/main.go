// runway — local execution-runtime controller. This PR wires Flow A steps
// 1–8 below the policy line: admit, mint run ID, persist request, journal
// through workload_exited. Emitting run_terminal and writing result.json
// are PR 2 — the run is left open.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/backend/local"
	"github.com/itsHabib/workbench/cmd/runway/internal/bundle"
	"github.com/itsHabib/workbench/cmd/runway/internal/expand"
	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: runway run --spec <request.json> --bundle <dir> [--state <dir>]")
		os.Exit(2)
	}
	os.Exit(dispatch(os.Args[1:]))
}

func dispatch(args []string) int {
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	}
	fmt.Fprintf(os.Stderr, "runway: unknown verb %q\n", args[0])
	return 2
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	spec := fs.String("spec", "", "path to placed request.json")
	bundleDir := fs.String("bundle", "", "work bundle directory")
	stateDir := fs.String("state", state.DefaultRoot(), "runway state root (default $"+state.EnvState+" or ~/.runway)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *spec == "" || *bundleDir == "" {
		fmt.Fprintln(os.Stderr, "usage: runway run --spec <request.json> --bundle <dir> [--state <dir>]")
		return 2
	}
	// A relative state root would leak into RUNWAY_* env and expanded path
	// arguments while the child runs with cwd=workspace; canonicalize once.
	stateAbs, err := filepath.Abs(*stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runway: resolve state root: %v\n", err)
		return 2
	}
	runID, code, err := runOnce(*spec, *bundleDir, stateAbs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if runID != "" {
			fmt.Fprintf(os.Stderr, "runway: run_id=%s (open; no result.json in this PR)\n", runID)
		}
		return 1
	}
	if code != 0 {
		// PR 2 replaces this with the §6 exit-code table once result.json
		// and run_terminal exist; until then a failed workload must not
		// masquerade as success.
		fmt.Fprintf(os.Stderr, "runway: run_id=%s workload exited with code %d (open; no result.json in this PR)\n", runID, code)
		return 1
	}
	fmt.Fprintf(os.Stderr, "runway: run_id=%s workload finished (open; no result.json in this PR)\n", runID)
	return 0
}

// runOnce executes Flow A steps 1–8 and returns the run ID plus the
// workload's exit code. The journal is left open (Terminal=false under
// execution.Reduce).
func runOnce(specPath, bundleDir, stateRoot string) (string, int, error) {
	adm, err := bundle.Admit(specPath, bundleDir)
	if err != nil {
		return "", 0, err
	}
	if adm.Request.Placement.Backend != "local" {
		return "", 0, fmt.Errorf("runway: placement.backend %q is not installed in this PR (local only)", adm.Request.Placement.Backend)
	}
	if adm.Request.Placement.Profile != "default" {
		return "", 0, fmt.Errorf("runway: placement.profile %q is not installed in this PR (default only)", adm.Request.Placement.Profile)
	}

	runID, err := mintRunID()
	if err != nil {
		return "", 0, err
	}
	run, err := state.Create(stateRoot, runID)
	if err != nil {
		return "", 0, err
	}
	if err := os.WriteFile(run.RequestPath(), adm.RequestBytes, 0o600); err != nil {
		return runID, 0, fmt.Errorf("runway: write request.json: %w", err)
	}

	j, err := journal.Create(run.EventsPath(), runID)
	if err != nil {
		return runID, 0, err
	}
	defer j.Close()

	emit := func(phase, kind string, details map[string]any) error {
		_, err := j.Append(phase, kind, details)
		return err
	}
	if err := emit(execution.PhaseAdmission, execution.KindRunAccepted, map[string]any{
		"request_id": adm.Request.RequestID,
	}); err != nil {
		return runID, 0, err
	}
	if err := bundle.Materialize(adm, run); err != nil {
		return runID, 0, err
	}

	roots := expand.NewRoots(run.WorkspaceDir(), run.InputsDir(), run.ArtifactsDir())
	prep, err := expand.Command(roots, adm.Work)
	if err != nil {
		return runID, 0, err
	}
	secrets, secretBytes, err := resolveSecrets(adm.Work.Secrets)
	if err != nil {
		return runID, 0, err
	}
	childEnv := mergeEnv(os.Environ(), prep.Env, secrets)

	be := local.New()
	ctx := context.Background()
	h, err := be.Start(ctx, backend.PreparedRun{
		RunID:      runID,
		Cwd:        prep.Cwd,
		Argv:       prep.Argv,
		Env:        childEnv,
		StdoutPath: run.StdoutLog(),
		StderrPath: run.StderrLog(),
		Secrets:    secretBytes,
	}, emit)
	if err != nil {
		return runID, 0, err
	}
	exit, err := be.Wait(ctx, h, emit)
	if err != nil {
		_ = be.Cleanup(ctx, h)
		return runID, 0, err
	}
	if err := be.Cleanup(ctx, h); err != nil {
		return runID, 0, err
	}
	return runID, exit.Code, nil
}

func mintRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("runway: mint run id: %w", err)
	}
	return "run_" + hex.EncodeToString(b[:]), nil
}

func resolveSecrets(secrets []execution.Secret) (map[string]string, [][]byte, error) {
	out := make(map[string]string, len(secrets))
	vals := make([][]byte, 0, len(secrets))
	for i, s := range secrets {
		name, ok := strings.CutPrefix(s.Ref, "env:")
		if !ok {
			return nil, nil, fmt.Errorf("runway: secrets[%d].ref %q is not env:NAME", i, s.Ref)
		}
		v, ok := os.LookupEnv(name)
		if !ok {
			return nil, nil, fmt.Errorf("runway: secret env %q is unset", name)
		}
		out[s.Name] = v
		vals = append(vals, []byte(v))
	}
	return out, vals, nil
}

func mergeEnv(base []string, roots map[string]string, secrets map[string]string) []string {
	index := map[string]int{}
	out := make([]string, 0, len(base)+len(roots)+len(secrets))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		index[k] = len(out)
		out = append(out, kv)
	}
	set := func(k, v string) {
		entry := k + "=" + v
		if i, ok := index[k]; ok {
			out[i] = entry
			return
		}
		index[k] = len(out)
		out = append(out, entry)
	}
	for k, v := range roots {
		set(k, v)
	}
	for k, v := range secrets {
		set(k, v)
	}
	return out
}
