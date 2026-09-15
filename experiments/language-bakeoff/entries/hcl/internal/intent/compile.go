// Package intent compiles HCL source into normalized intent: typed nodes,
// evaluated attribute values and a dependency order. Apart from reading
// source it does no I/O, and it knows nothing about any adapter's semantics,
// only the schema each adapter declares.
package intent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
)

// Node is one block after compilation.
type Node struct {
	Address string         `json:"address"`
	Kind    adapter.Kind   `json:"kind"`
	Type    string         `json:"type"`
	Attrs   adapter.Values `json:"attrs"`
	Deps    []string       `json:"deps"`
	Owns    []string       `json:"owns,omitempty"`
	Reads   []string       `json:"reads,omitempty"`
	Source  string         `json:"source"`
}

// Intent is compiled source: nodes in dependency order, ties kept in source
// order. It is planner input, never a fact about the world.
type Intent struct {
	SourceDigest string  `json:"source_digest"`
	Nodes        []*Node `json:"nodes"`
}

// Node returns the node at address, or nil.
func (in *Intent) Node(address string) *Node {
	for _, n := range in.Nodes {
		if n.Address == address {
			return n
		}
	}
	return nil
}

// Source is one authored file. Name is workspace-relative.
type Source struct {
	Name  string
	Bytes []byte
}

// block is a declared block before evaluation.
type block struct {
	addr  string
	ad    adapter.Adapter
	attrs hclsyntax.Attributes
	def   hcl.Range
	order int
	deps  map[string]hcl.Range // dependency address -> first reference
}

type compiler struct {
	reg    *adapter.Registry
	blocks []*block
	byAddr map[string]*block
	diags  hcl.Diagnostics
}

// Compile parses and evaluates srcs against the registered adapters. The
// returned files let callers render diagnostics with source snippets.
func Compile(srcs []Source, reg *adapter.Registry) (*Intent, map[string]*hcl.File, hcl.Diagnostics) {
	files, bodies, diags := parse(srcs)
	if diags.HasErrors() {
		return nil, files, diags
	}
	c := &compiler{reg: reg, byAddr: map[string]*block{}}
	for _, body := range bodies {
		c.declare(body)
	}
	if c.diags.HasErrors() {
		return nil, files, c.diags
	}
	c.link()
	if c.diags.HasErrors() {
		return nil, files, c.diags
	}
	nodes := c.evaluate(c.sort())
	if c.diags.HasErrors() {
		return nil, files, c.diags
	}
	c.checkPaths(nodes, sourceNames(srcs))
	if c.diags.HasErrors() {
		return nil, files, c.diags
	}
	return &Intent{SourceDigest: DigestSources(srcs), Nodes: nodes}, files, nil
}

func parse(srcs []Source) (map[string]*hcl.File, []*hclsyntax.Body, hcl.Diagnostics) {
	p := hclparse.NewParser()
	var bodies []*hclsyntax.Body
	var diags hcl.Diagnostics
	for _, s := range srcs {
		f, d := p.ParseHCL(s.Bytes, s.Name)
		diags = append(diags, d...)
		if f == nil {
			continue
		}
		bodies = append(bodies, f.Body.(*hclsyntax.Body))
	}
	return p.Files(), bodies, diags
}

// blockKinds are the only top-level block types in the subset.
var blockKinds = map[string]adapter.Kind{"resource": adapter.Resource, "task": adapter.Task}

// notSupported explains configuration-language features left out on purpose.
var notSupported = map[string]string{
	"variable":  "there are no input variables; write values inline",
	"locals":    "there are no locals; reference another block's attribute instead",
	"module":    "modules are out of scope; put *.wb.hcl files side by side in one directory",
	"provider":  "adapters are compiled into wb; there is no provider configuration",
	"data":      "every plan observes resources directly; there are no data sources",
	"output":    "read results with `wb compile`, `wb plan -out` or `wb log`",
	"terraform": "there is no settings block",
}

// metaArgs are Terraform-style meta-arguments the subset rejects.
var metaArgs = map[string]string{
	"count":      "repetition is out of scope; declare each block explicitly",
	"for_each":   "repetition is out of scope; declare each block explicitly",
	"depends_on": "dependencies come from references such as file.input.path",
	"lifecycle":  "wb never deletes or replaces, so there are no lifecycle rules",
	"provider":   "adapters are compiled into wb; there is no provider selection",
}

func (c *compiler) declare(body *hclsyntax.Body) {
	for _, name := range sortedNames(body.Attributes) {
		c.errorf(body.Attributes[name].NameRange, "Unsupported top-level argument",
			"%q cannot be set at the top level; declare resource or task blocks.", name)
	}
	for _, hb := range body.Blocks {
		c.declareBlock(hb)
	}
}

func (c *compiler) declareBlock(hb *hclsyntax.Block) {
	kind, ok := blockKinds[hb.Type]
	if !ok {
		c.unsupportedBlock(hb.Type, hb.TypeRange, "at the top level")
		return
	}
	if len(hb.Labels) != 2 {
		c.errorf(hb.DefRange(), "Invalid "+hb.Type+" block",
			"A %s block needs two labels: %s \"<type>\" \"<name>\".", hb.Type, hb.Type)
		return
	}
	ad, ok := c.adapterFor(hb, kind)
	if !ok {
		return
	}
	b := &block{addr: hb.Labels[0] + "." + hb.Labels[1], ad: ad, attrs: hb.Body.Attributes, def: hb.DefRange(), order: len(c.blocks)}
	if prev, dup := c.byAddr[b.addr]; dup {
		c.errorf(b.def, "Duplicate block", "%s is already declared at %s.", b.addr, prev.def)
		return
	}
	c.checkBody(hb.Body, b)
	c.byAddr[b.addr] = b
	c.blocks = append(c.blocks, b)
}

func (c *compiler) adapterFor(hb *hclsyntax.Block, kind adapter.Kind) (adapter.Adapter, bool) {
	typ, name := hb.Labels[0], hb.Labels[1]
	ad, ok := c.reg.Lookup(typ)
	if !ok {
		c.errorf(hb.LabelRanges[0], "Unknown "+hb.Type+" type",
			"No adapter is registered for %q. Registered types: %s.", typ, strings.Join(c.reg.Types(), ", "))
		return nil, false
	}
	if ad.Kind() != kind {
		c.errorf(hb.TypeRange, "Wrong block kind",
			"%q is a %s adapter; declare it as %s %q %q.", typ, ad.Kind(), ad.Kind(), typ, name)
		return nil, false
	}
	if !hclsyntax.ValidIdentifier(name) {
		c.errorf(hb.LabelRanges[1], "Invalid block name",
			"%q is not a valid identifier; it is referenced as %s.<name>.<attribute>.", name, typ)
		return nil, false
	}
	return ad, true
}

func (c *compiler) checkBody(body *hclsyntax.Body, b *block) {
	fields := map[string]adapter.Field{}
	for _, f := range b.ad.Fields() {
		fields[f.Name] = f
	}
	for _, nb := range body.Blocks {
		c.unsupportedBlock(nb.Type, nb.TypeRange, "inside "+b.addr)
	}
	for _, name := range sortedNames(body.Attributes) {
		c.checkAttr(body.Attributes[name], fields, b)
	}
	for _, f := range b.ad.Fields() {
		if f.Required && body.Attributes[f.Name] == nil {
			c.errorf(b.def, "Missing required argument", "%s needs %q (%s).", b.addr, f.Name, f.Type)
		}
	}
}

func (c *compiler) checkAttr(attr *hclsyntax.Attribute, fields map[string]adapter.Field, b *block) {
	if hint, ok := metaArgs[attr.Name]; ok {
		c.errorf(attr.NameRange, "Unsupported meta-argument", "%q is not part of this subset: %s.", attr.Name, hint)
		return
	}
	if _, ok := fields[attr.Name]; !ok {
		c.errorf(attr.NameRange, "Unsupported argument",
			"%s has no argument %q. Valid arguments: %s.", b.addr, attr.Name, fieldNames(b.ad))
		return
	}
	c.diags = append(c.diags, rejectCalls(attr.Expr)...)
}

func (c *compiler) unsupportedBlock(typ string, rng hcl.Range, where string) {
	if hint, ok := notSupported[typ]; ok {
		c.errorf(rng, "Unsupported block type", "%q blocks are not part of this subset: %s.", typ, hint)
		return
	}
	if hint, ok := metaArgs[typ]; ok {
		c.errorf(rng, "Unsupported meta-argument", "%q is not part of this subset: %s.", typ, hint)
		return
	}
	c.errorf(rng, "Unsupported block type",
		"Blocks of type %q are not expected %s. The subset is resource and task blocks with plain arguments.", typ, where)
}

// rejectCalls keeps functions out of the subset: file(), timestamp() and
// friends would be inputs the planner cannot see.
func rejectCalls(expr hclsyntax.Expression) hcl.Diagnostics {
	return hclsyntax.VisitAll(expr, func(n hclsyntax.Node) hcl.Diagnostics {
		call, ok := n.(*hclsyntax.FunctionCallExpr)
		if !ok {
			return nil
		}
		return hcl.Diagnostics{{
			Severity: hcl.DiagError,
			Summary:  "Function calls are not supported",
			Detail: fmt.Sprintf("%s() could read the clock, files or the environment, which the planner cannot see. "+
				"Functions are excluded from this subset; interpolation, operators and conditionals are allowed.", call.Name),
			Subject: call.NameRange.Ptr(),
		}}
	})
}

func (c *compiler) errorf(rng hcl.Range, summary, format string, args ...any) {
	c.diags = append(c.diags, &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
		Subject:  rng.Ptr(),
	})
}

func sortedNames(attrs hclsyntax.Attributes) []string {
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func fieldNames(ad adapter.Adapter) string {
	var names []string
	for _, f := range ad.Fields() {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}

func sourceNames(srcs []Source) []string {
	names := make([]string, 0, len(srcs))
	for _, s := range srcs {
		names = append(names, s.Name)
	}
	return names
}
