package adapters

import "github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"

// Builtin returns the adapters compiled into wb. Registering a new block
// type is its own file plus one line here.
func Builtin() []adapter.Adapter {
	return []adapter.Adapter{
		File{},
		Exec{},
		Dir{},
	}
}
