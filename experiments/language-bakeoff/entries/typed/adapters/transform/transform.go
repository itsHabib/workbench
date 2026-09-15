// Package transform is the one-shot task: run a command with a declared
// node's file on stdin and commit its stdout to an output file the task
// owns. It runs again only when the engine's replay rule says its result
// is stale; the command must tolerate running more than once.
package transform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// Kind is the node kind this package declares and adapts.
const Kind = "transform"

// Spec is one transformation. Stdin is a Ref, not a path, so a transform
// can only read a declared node, and that node's digest is part of the
// task's fingerprint.
type Spec struct {
	Command []string `json:"command"`
	Stdin   wb.Ref   `json:"stdin"`
	Output  wb.Path  `json:"output"`
}

// New declares a transform task. The returned Ref points at its output.
func New(g *wb.Graph, name string, s Spec) wb.Ref {
	return g.Declare(wb.Decl{Kind: Kind, Name: name, Class: wb.Task, Owns: s.Output, Spec: s})
}

// Adapter observes and runs transform nodes.
type Adapter struct{}

// Kind implements engine.Adapter.
func (Adapter) Kind() string { return Kind }

// Observe reports the output file.
func (Adapter) Observe(ws *foundation.Workspace, n wb.Node) (engine.State, error) {
	s, err := decode(n)
	if err != nil {
		return engine.State{}, err
	}
	out, err := ws.ReadFile(s.Output.String())
	if err != nil || !out.Exists {
		return engine.State{}, err
	}
	return engine.State{Exists: true, Digest: out.Digest}, nil
}

// Run executes the command once through the foundation.
func (Adapter) Run(ctx context.Context, ws *foundation.Workspace, n wb.Node) (engine.State, error) {
	s, err := decode(n)
	if err != nil {
		return engine.State{}, err
	}
	out, err := ws.Exec(ctx, foundation.ExecSpec{
		Label:  string(n.Address),
		Argv:   s.Command,
		Stdin:  s.Stdin.String(),
		Output: s.Output.String(),
	})
	if err != nil {
		return engine.State{}, err
	}
	return engine.State{Exists: true, Digest: out.Digest}, nil
}

func decode(n wb.Node) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(n.Spec, &s); err != nil {
		return Spec{}, fmt.Errorf("decode transform spec: %w", err)
	}
	if len(s.Command) == 0 {
		return Spec{}, errors.New("command is empty")
	}
	if s.Stdin.Address() == "" {
		return Spec{}, errors.New("stdin must reference a declared node")
	}
	if s.Output.String() != n.Owns {
		return Spec{}, fmt.Errorf("output %q is not the owned path %q", s.Output.String(), n.Owns)
	}
	return s, nil
}
