// Command textpipe is the program built from the textpipe workflow's
// source: it evaluates, plans and applies that workflow.
package main

import (
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/file"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/transform"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/cli"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/workflows/textpipe"
)

func main() {
	cli.Program{
		Define:   textpipe.Define,
		Adapters: []engine.Adapter{file.Adapter{}, transform.Adapter{}},
	}.Main()
}
