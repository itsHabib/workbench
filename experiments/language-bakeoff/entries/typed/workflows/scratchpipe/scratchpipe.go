// Package scratchpipe extends textpipe with the third adapter: a
// disposable directory holding a report built from upper's output. It
// composes textpipe with an ordinary function call; nothing in textpipe,
// the planner or the CLI changes.
package scratchpipe

import (
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/scratchdir"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/transform"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/workflows/textpipe"
)

// Define is the workflow definition.
func Define(p *wb.Params) *wb.Graph {
	g := wb.NewGraph("scratchpipe")
	pipe := textpipe.Declare(g, p)
	work := scratchdir.New(g, "work", scratchdir.Spec{
		Path:       "work",
		Generation: p.String("generation", "1"),
	})
	transform.New(g, "report", transform.Spec{
		Command: []string{"sort"},
		Stdin:   pipe.Upper,              // reads another task's output
		Output:  work.Join("report.txt"), // lives in, and depends on, the scratch dir
	})
	return g
}
