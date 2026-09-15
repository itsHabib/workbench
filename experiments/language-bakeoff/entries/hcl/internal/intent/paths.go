package intent

import (
	"path"
	"sort"
	"strings"
)

// checkPaths enforces file ownership from what adapters declare, without
// knowing any adapter:
//   - every path has at most one owner;
//   - source files, and any top-level *.wb.hcl a block could create, have none;
//   - a block never reads a path it writes itself;
//   - a block that touches a path owned by another block (the path itself,
//     or a directory containing it) must reference that block, so it is
//     ordered after it.
//
// Paths compare case-insensitively: on macOS's default filesystem NOTES.txt
// and notes.txt are one file. Unicode normalization is not folded.
func (c *compiler) checkPaths(nodes []*Node, sources []string) {
	owner := c.owners(nodes, sources)
	if c.diags.HasErrors() {
		return
	}
	anc := ancestors(nodes)
	for _, n := range nodes {
		c.checkSelfRead(n)
		for _, u := range uses(n) {
			c.checkUse(n, u, owner, anc[n.Address])
		}
	}
}

func fold(p string) string { return strings.ToLower(p) }

func (c *compiler) owners(nodes []*Node, sources []string) map[string]*Node {
	isSource := map[string]bool{}
	for _, s := range sources {
		isSource[fold(s)] = true
	}
	owner := map[string]*Node{}
	for _, n := range nodes {
		for _, p := range n.Owns {
			c.claim(owner, isSource, n, p)
		}
	}
	return owner
}

func (c *compiler) claim(owner map[string]*Node, isSource map[string]bool, n *Node, p string) {
	def := c.byAddr[n.Address].def
	key := fold(p)
	if isSource[key] || (!strings.Contains(p, "/") && strings.HasSuffix(key, ".wb.hcl")) {
		c.errorf(def, "Path is a source file", "%s would own %s, which is or would become wb source.", n.Address, p)
		return
	}
	if prev := owner[key]; prev != nil {
		c.errorf(def, "Path owned twice", "%s and %s both own %s; each path has exactly one owner.", prev.Address, n.Address, p)
		return
	}
	owner[key] = n
}

// checkSelfRead rejects a block that reads what it writes: every retry
// would feed it its own previous result.
func (c *compiler) checkSelfRead(n *Node) {
	owned := map[string]bool{}
	for _, p := range n.Owns {
		owned[fold(p)] = true
	}
	for _, r := range n.Reads {
		if anyOwned(owned, covering(r, true)) {
			c.errorf(c.byAddr[n.Address].def, "Block reads its own output",
				"%s reads %s, which it also writes; every retry would consume its own previous result.", n.Address, r)
		}
	}
}

func anyOwned(owned map[string]bool, paths []string) bool {
	for _, p := range paths {
		if owned[fold(p)] {
			return true
		}
	}
	return false
}

// use is one path a node touches. Reads count the path itself; writes only
// count containing directories, since the node owns the path.
type use struct {
	path  string
	verb  string
	exact bool
}

func uses(n *Node) []use {
	var out []use
	for _, p := range n.Owns {
		out = append(out, use{path: p, verb: "writes"})
	}
	for _, p := range n.Reads {
		out = append(out, use{path: p, verb: "reads", exact: true})
	}
	return out
}

func (c *compiler) checkUse(n *Node, u use, owner map[string]*Node, anc map[string]bool) {
	for _, p := range covering(u.path, u.exact) {
		o := owner[fold(p)]
		if o == nil || o == n || anc[o.Address] {
			continue
		}
		c.errorf(c.byAddr[n.Address].def, "Missing reference",
			"%s %s %s, which %s owns, but does not reference %s. Use %s so the dependency is explicit and ordered.",
			n.Address, u.verb, u.path, o.Address, o.Address, suggestRef(o, p))
	}
}

// covering lists the paths that can own p: p itself when exact, then each
// containing directory.
func covering(p string, exact bool) []string {
	var out []string
	if exact {
		out = append(out, p)
	}
	for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
		out = append(out, dir)
	}
	return out
}

// suggestRef names the owner's attribute whose value is the owned path.
func suggestRef(o *Node, p string) string {
	keys := make([]string, 0, len(o.Attrs))
	for k := range o.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := o.Attrs[k].(string); ok && fold(s) == fold(p) {
			return o.Address + "." + k
		}
	}
	return "one of " + o.Address + "'s attributes"
}

// ancestors maps each node to everything it transitively depends on.
func ancestors(nodes []*Node) map[string]map[string]bool {
	anc := map[string]map[string]bool{}
	for _, n := range nodes {
		set := map[string]bool{}
		for _, d := range n.Deps {
			set[d] = true
			merge(set, anc[d])
		}
		anc[n.Address] = set
	}
	return anc
}

func merge(dst, src map[string]bool) {
	for k := range src {
		dst[k] = true
	}
}
