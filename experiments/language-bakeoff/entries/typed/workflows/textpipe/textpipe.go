// Package textpipe is the bakeoff's common workload written against the
// typed API: a managed input text file and two transforms that read it.
package textpipe

import (
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/file"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/transform"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// DefaultText is the input artifact's content unless -var text=... is set.
const DefaultText = "the quick brown fox\njumps over the lazy dog\n"

// Nodes are the pipeline's handles, returned so other workflows can build
// on it with ordinary function calls.
type Nodes struct {
	Input, Upper, Words wb.Ref
}

// Define is the workflow definition.
func Define(p *wb.Params) *wb.Graph {
	g := wb.NewGraph("textpipe")
	Declare(g, p)
	return g
}

// Declare adds the pipeline's nodes to g.
func Declare(g *wb.Graph, p *wb.Params) Nodes {
	input := file.New(g, "input", file.Spec{
		Path:    "input.txt",
		Content: p.String("text", DefaultText),
	})
	upper := transform.New(g, "upper", transform.Spec{
		Command: []string{"tr", "[:lower:]", "[:upper:]"},
		Stdin:   input, // a Ref: the compiler rejects anything but a declared node
		Output:  wb.Rel("upper.txt"),
	})
	words := transform.New(g, "wordcount", transform.Spec{
		Command: []string{"awk", "{ n += NF } END { print n + 0 }"},
		Stdin:   input,
		Output:  wb.Rel("wordcount.txt"),
	})
	return Nodes{Input: input, Upper: upper, Words: words}
}
