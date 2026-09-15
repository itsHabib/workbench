package intent

import (
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// reservedRoots are Terraform-style reference roots the subset leaves out.
var reservedRoots = map[string]string{
	"var":       "there are no variables",
	"local":     "there are no locals",
	"self":      "there is no self reference",
	"path":      "paths are already relative to the workspace",
	"each":      "there is no for_each",
	"count":     "there is no count",
	"module":    "there are no modules",
	"data":      "there are no data sources",
	"terraform": "there is no settings block",
}

// link turns every reference into a dependency edge. HCL extracts the
// references (Expression.Variables); nothing here parses syntax.
func (c *compiler) link() {
	for _, b := range c.blocks {
		b.deps = map[string]hcl.Range{}
		for _, name := range sortedNames(b.attrs) {
			c.linkAttr(b, b.attrs[name])
		}
	}
}

func (c *compiler) linkAttr(b *block, attr *hclsyntax.Attribute) {
	for _, tr := range attr.Expr.Variables() {
		dep, ok := c.resolveRef(tr)
		if !ok {
			continue
		}
		if dep == b.addr {
			c.errorf(tr.SourceRange(), "Self reference", "%s cannot reference its own attributes.", b.addr)
			continue
		}
		if _, seen := b.deps[dep]; !seen {
			b.deps[dep] = tr.SourceRange()
		}
	}
}

func (c *compiler) resolveRef(tr hcl.Traversal) (string, bool) {
	root := tr.RootName()
	if hint, ok := reservedRoots[root]; ok {
		c.errorf(tr.SourceRange(), "Unsupported reference",
			"%s.* is not available in this subset: %s. References look like <type>.<name>.<attribute>.", root, hint)
		return "", false
	}
	name, ok := refName(tr)
	if !ok {
		c.errorf(tr.SourceRange(), "Unsupported reference",
			"References look like <type>.<name>.<attribute>, for example file.input.path.")
		return "", false
	}
	addr := root + "." + name
	if _, ok := c.byAddr[addr]; !ok {
		c.errorf(tr.SourceRange(), "Reference to undeclared block", "%s is not declared in this workspace.", addr)
		return "", false
	}
	return addr, true
}

func refName(tr hcl.Traversal) (string, bool) {
	if len(tr) < 3 {
		return "", false
	}
	name, ok := tr[1].(hcl.TraverseAttr)
	if !ok {
		return "", false
	}
	_, ok = tr[2].(hcl.TraverseAttr)
	return name.Name, ok
}

// sort orders blocks so every block follows its dependencies; among ready
// blocks, source order wins. A leftover means a cycle, which is reported.
func (c *compiler) sort() []*block {
	pending := map[string]int{}
	users := map[string][]*block{}
	var ready []*block
	for _, b := range c.blocks {
		pending[b.addr] = len(b.deps)
		for dep := range b.deps {
			users[dep] = append(users[dep], b)
		}
		if len(b.deps) == 0 {
			ready = append(ready, b)
		}
	}
	var out []*block
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return ready[i].order < ready[j].order })
		b := ready[0]
		ready = ready[1:]
		out = append(out, b)
		for _, u := range users[b.addr] {
			pending[u.addr]--
			if pending[u.addr] == 0 {
				ready = append(ready, u)
			}
		}
	}
	if len(out) < len(c.blocks) {
		c.reportCycle(pending)
	}
	return out
}

// reportCycle names one cycle. Every block still pending has a pending
// dependency, so walking pending dependencies must revisit a block.
func (c *compiler) reportCycle(pending map[string]int) {
	var b *block
	for _, cand := range c.blocks {
		if pending[cand.addr] > 0 {
			b = cand
			break
		}
	}
	seen := map[string]int{}
	var path []*block
	for {
		if i, ok := seen[b.addr]; ok {
			c.cycleError(path[i:])
			return
		}
		seen[b.addr] = len(path)
		path = append(path, b)
		b = c.byAddr[firstPendingDep(b, pending)]
	}
}

func (c *compiler) cycleError(cycle []*block) {
	names := make([]string, 0, len(cycle)+1)
	for _, b := range cycle {
		names = append(names, b.addr)
	}
	names = append(names, cycle[0].addr)
	c.errorf(cycle[0].deps[cycle[1].addr], "Dependency cycle",
		"%s. Blocks are ordered by their references, so a cycle has no valid order.", strings.Join(names, " -> "))
}

func firstPendingDep(b *block, pending map[string]int) string {
	deps := make([]string, 0, len(b.deps))
	for d := range b.deps {
		deps = append(deps, d)
	}
	sort.Strings(deps)
	for _, d := range deps {
		if pending[d] > 0 {
			return d
		}
	}
	return ""
}
