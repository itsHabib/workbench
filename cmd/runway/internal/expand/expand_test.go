package expand_test

import (
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/expand"
	"github.com/itsHabib/workbench/contracts/execution"
)

func TestExpandWindowsAndLinuxFixtures(t *testing.T) {
	roots := expand.Roots{
		Workspace: `C:\runs\r1\workspace`,
		Inputs:    `C:\runs\r1\inputs`,
		Out:       `C:\runs\r1\artifacts`,
	}
	linuxRoots := expand.Roots{
		Workspace: "/runs/r1/workspace",
		Inputs:    "/runs/r1/inputs",
		Out:       "/runs/r1/artifacts",
	}
	ref := execution.PathRef{Root: execution.RootInputs, Value: `scripts/run.go`}
	winJoin := func(elem ...string) string { return expand.JoinWithSep(`\`, elem...) }
	linJoin := func(elem ...string) string { return expand.JoinWithSep(`/`, elem...) }

	gotWin, err := expand.PathWith(winJoin, roots, ref)
	if err != nil {
		t.Fatal(err)
	}
	wantWin := `C:\runs\r1\inputs\scripts\run.go`
	if gotWin != wantWin {
		t.Fatalf("windows expand: got %q want %q", gotWin, wantWin)
	}

	// Same work.json bytes (same PathRef value with /) expand on Linux.
	gotLin, err := expand.PathWith(linJoin, linuxRoots, ref)
	if err != nil {
		t.Fatal(err)
	}
	wantLin := "/runs/r1/inputs/scripts/run.go"
	if gotLin != wantLin {
		t.Fatalf("linux expand: got %q want %q", gotLin, wantLin)
	}

	// Backslash-form values in the contract still expand without mutating the
	// original PathRef — callers pass by value.
	bs := execution.PathRef{Root: execution.RootWorkspace, Value: `pkg\main`}
	original := bs.Value
	got, err := expand.PathWith(winJoin, roots, bs)
	if err != nil {
		t.Fatal(err)
	}
	if bs.Value != original {
		t.Fatalf("expansion must not change PathRef bytes: %q", bs.Value)
	}
	if got != `C:\runs\r1\workspace\pkg\main` {
		t.Fatalf("backslash value expand: got %q", got)
	}
}

func TestEnvRootsMatchExpansion(t *testing.T) {
	roots := expand.Roots{
		Workspace: "/tmp/run/workspace",
		Inputs:    "/tmp/run/inputs",
		Out:       "/tmp/run/artifacts",
	}
	env := expand.Env(roots)
	if env[expand.EnvWorkspace] != roots.Workspace {
		t.Fatalf("RUNWAY_WORKSPACE %q != root %q", env[expand.EnvWorkspace], roots.Workspace)
	}
	if env[expand.EnvInputs] != roots.Inputs {
		t.Fatalf("RUNWAY_INPUTS %q != root %q", env[expand.EnvInputs], roots.Inputs)
	}
	if env[expand.EnvOut] != roots.Out {
		t.Fatalf("RUNWAY_OUT %q != root %q", env[expand.EnvOut], roots.Out)
	}

	name := "go"
	lit := "run"
	work := execution.WorkSpec{
		SchemaVersion: execution.SchemaVersion,
		Command: execution.Command{
			Executable: execution.Executable{Name: &name},
			Args:       []execution.Arg{{Literal: &lit}, {Path: &execution.PathRef{Root: execution.RootInputs, Value: "main.go"}}},
		},
		Cwd: execution.PathRef{Root: execution.RootWorkspace, Value: "."},
	}
	prep, err := expand.Command(roots, work)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Env[expand.EnvWorkspace] != roots.Workspace || prep.Cwd != roots.Workspace {
		t.Fatalf("cwd/env parity failed: cwd=%q env=%q", prep.Cwd, prep.Env[expand.EnvWorkspace])
	}
	if prep.Argv[0] != "go" || prep.Argv[1] != "run" {
		t.Fatalf("argv: %+v", prep.Argv)
	}
	if prep.Argv[2] != roots.Inputs+"/main.go" && prep.Argv[2] != roots.Inputs+`\main.go` {
		// filepath.Join on this host
		want, _ := expand.Path(roots, execution.PathRef{Root: execution.RootInputs, Value: "main.go"})
		if prep.Argv[2] != want {
			t.Fatalf("path arg: got %q want %q", prep.Argv[2], want)
		}
	}
}
