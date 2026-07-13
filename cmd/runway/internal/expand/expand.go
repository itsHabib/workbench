// Package expand maps structured {root, value} path refs onto a run's three
// native roots and computes the RUNWAY_* env values from the same roots —
// one function, two consumers, so Gate A parity ("env vars equal the
// expansion roots") holds by construction (FR3).
package expand

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/contracts/execution"
)

// Roots are the three absolute native roots for one run.
type Roots struct {
	Workspace string
	Inputs    string
	Out       string
}

// EnvNames are the root-discovery environment variable names every backend sets.
const (
	EnvWorkspace = "RUNWAY_WORKSPACE"
	EnvInputs    = "RUNWAY_INPUTS"
	EnvOut       = "RUNWAY_OUT"
)

// Joiner joins path elements with an OS-native separator. Production uses
// filepath.Join; fixtures inject a separator-specific joiner to prove
// Windows and Linux expansions without changing work.json bytes.
type Joiner func(elem ...string) string

// Env returns RUNWAY_WORKSPACE / RUNWAY_INPUTS / RUNWAY_OUT equal to the
// expansion roots.
func Env(r Roots) map[string]string {
	return map[string]string{
		EnvWorkspace: r.Workspace,
		EnvInputs:    r.Inputs,
		EnvOut:       r.Out,
	}
}

// Path expands one structured ref to a native absolute path under roots.
func Path(r Roots, ref execution.PathRef) (string, error) {
	return PathWith(filepath.Join, r, ref)
}

// PathWith is Path with an injectable joiner for cross-OS fixtures.
func PathWith(join Joiner, r Roots, ref execution.PathRef) (string, error) {
	root, err := rootPath(r, ref.Root)
	if err != nil {
		return "", err
	}
	parts := append([]string{root}, nativeSegments(ref.Value)...)
	return join(parts...), nil
}

// Prepared is the expanded command surface a backend can Start without
// further path interpretation.
type Prepared struct {
	Cwd  string
	Argv []string
	Env  map[string]string
}

// Command expands cwd, executable.path, and every path-typed arg, and
// returns the RUNWAY_* env map from the same roots.
func Command(r Roots, w execution.WorkSpec) (Prepared, error) {
	return CommandWith(filepath.Join, r, w)
}

// CommandWith is Command with an injectable joiner for fixtures.
func CommandWith(join Joiner, r Roots, w execution.WorkSpec) (Prepared, error) {
	cwd, err := PathWith(join, r, w.Cwd)
	if err != nil {
		return Prepared{}, fmt.Errorf("expand: cwd: %w", err)
	}
	exe, err := expandExecutable(join, r, w.Command.Executable)
	if err != nil {
		return Prepared{}, err
	}
	argv := []string{exe}
	for i, a := range w.Command.Args {
		s, err := expandArg(join, r, i, a)
		if err != nil {
			return Prepared{}, err
		}
		argv = append(argv, s)
	}
	return Prepared{Cwd: cwd, Argv: argv, Env: Env(r)}, nil
}

func rootPath(r Roots, name string) (string, error) {
	switch name {
	case execution.RootWorkspace:
		return r.Workspace, nil
	case execution.RootInputs:
		return r.Inputs, nil
	case execution.RootOut:
		return r.Out, nil
	}
	return "", fmt.Errorf("expand: unknown root %q", name)
}

func expandExecutable(join Joiner, r Roots, e execution.Executable) (string, error) {
	if e.Name != nil {
		return *e.Name, nil
	}
	if e.Path == nil {
		return "", fmt.Errorf("expand: executable sets neither name nor path")
	}
	p, err := PathWith(join, r, *e.Path)
	if err != nil {
		return "", fmt.Errorf("expand: executable.path: %w", err)
	}
	return p, nil
}

func expandArg(join Joiner, r Roots, i int, a execution.Arg) (string, error) {
	if a.Literal != nil {
		return *a.Literal, nil
	}
	if a.Path == nil {
		return "", fmt.Errorf("expand: args[%d] sets neither literal nor path", i)
	}
	p, err := PathWith(join, r, *a.Path)
	if err != nil {
		return "", fmt.Errorf("expand: args[%d].path: %w", i, err)
	}
	return p, nil
}

// nativeSegments splits a contract path value that may use / or \ into
// elements safe for an OS-native joiner. "." alone yields no extra elements
// so join(root) == root.
func nativeSegments(value string) []string {
	normalized := strings.ReplaceAll(value, `\`, "/")
	if normalized == "." || normalized == "" {
		return nil
	}
	parts := strings.Split(normalized, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		out = append(out, p)
	}
	return out
}

// JoinWithSep joins elements with an explicit separator — fixture helper
// for proving Windows (\) and Linux (/) expansions on any host.
func JoinWithSep(sep string, elem ...string) string {
	if len(elem) == 0 {
		return ""
	}
	cleaned := make([]string, 0, len(elem))
	for _, e := range elem {
		if e == "" {
			continue
		}
		cleaned = append(cleaned, strings.TrimRight(e, `/\`))
	}
	if len(cleaned) == 0 {
		return ""
	}
	return strings.Join(cleaned, sep)
}
