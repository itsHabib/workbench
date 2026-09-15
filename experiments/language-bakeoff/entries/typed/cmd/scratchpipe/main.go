// Command scratchpipe is the program built from the scratchpipe workflow:
// textpipe plus a disposable directory, served by the third adapter.
package main

import (
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/file"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/scratchdir"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/transform"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/cli"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/workflows/scratchpipe"
)

func main() {
	cli.Program{
		Define:   scratchpipe.Define,
		Adapters: []engine.Adapter{file.Adapter{}, transform.Adapter{}, scratchdir.Adapter{}},
	}.Main()
}
