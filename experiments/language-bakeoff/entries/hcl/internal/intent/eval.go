package intent

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
)

// evaluate resolves blocks in dependency order. Each block sees the
// resolved attributes of the blocks before it as <type>.<name>.<attribute>;
// HCL does the expression evaluation.
func (c *compiler) evaluate(order []*block) []*Node {
	scope := map[string]map[string]cty.Value{}
	var nodes []*Node
	for _, b := range order {
		n, ok := c.evalBlock(b, &hcl.EvalContext{Variables: objects(scope)})
		if !ok {
			return nil
		}
		nodes = append(nodes, n)
		typ, name, _ := strings.Cut(b.addr, ".")
		if scope[typ] == nil {
			scope[typ] = map[string]cty.Value{}
		}
		scope[typ][name] = toCty(n.Attrs)
	}
	return nodes
}

func (c *compiler) evalBlock(b *block, ctx *hcl.EvalContext) (*Node, bool) {
	cfg := adapter.Values{}
	for _, f := range b.ad.Fields() {
		attr, ok := b.attrs[f.Name]
		if !ok {
			continue
		}
		if v, ok := c.evalField(attr, f, ctx); ok {
			cfg[f.Name] = v
		}
	}
	if c.diags.HasErrors() {
		return nil, false
	}
	vals, err := b.ad.Resolve(cfg)
	if err != nil {
		c.resolveError(b, err)
		return nil, false
	}
	paths := b.ad.Paths(vals)
	return &Node{
		Address: b.addr,
		Kind:    b.ad.Kind(),
		Type:    b.ad.Type(),
		Attrs:   vals,
		Deps:    sortedDeps(b.deps),
		Owns:    paths.Owns,
		Reads:   paths.Reads,
		Source:  fmt.Sprintf("%s:%d", b.def.Filename, b.def.Start.Line),
	}, true
}

func (c *compiler) evalField(attr *hclsyntax.Attribute, f adapter.Field, ctx *hcl.EvalContext) (any, bool) {
	val, diags := attr.Expr.Value(ctx)
	c.diags = append(c.diags, diags...)
	if diags.HasErrors() {
		return nil, false
	}
	want := cty.String
	if f.Type == adapter.StringList {
		want = cty.List(cty.String)
	}
	conv, err := convert.Convert(val, want)
	if err != nil {
		c.errorf(attr.Expr.Range(), "Incorrect attribute value type", "%s must be %s: %s.", f.Name, f.Type, err)
		return nil, false
	}
	if conv.IsNull() && !f.Required {
		return nil, false // null on an optional field means "unset"
	}
	if conv.IsNull() || !conv.IsWhollyKnown() {
		c.errorf(attr.Expr.Range(), "Invalid attribute value", "%s must be a non-null %s.", f.Name, f.Type)
		return nil, false
	}
	if f.Type == adapter.String {
		return conv.AsString(), true
	}
	return c.stringList(attr, conv)
}

func (c *compiler) stringList(attr *hclsyntax.Attribute, list cty.Value) ([]string, bool) {
	out := []string{}
	for _, el := range list.AsValueSlice() {
		if el.IsNull() {
			c.errorf(attr.Expr.Range(), "Invalid attribute value", "%s must not contain null elements.", attr.Name)
			return nil, false
		}
		out = append(out, el.AsString())
	}
	return out, true
}

func (c *compiler) resolveError(b *block, err error) {
	rng := b.def
	var fe *adapter.FieldError
	if errors.As(err, &fe) && b.attrs[fe.Field] != nil {
		rng = b.attrs[fe.Field].Expr.Range()
	}
	c.errorf(rng, "Invalid block configuration", "%s: %s.", b.addr, err)
}

func objects(scope map[string]map[string]cty.Value) map[string]cty.Value {
	vars := map[string]cty.Value{}
	for typ, byName := range scope {
		vars[typ] = cty.ObjectVal(byName)
	}
	return vars
}

// toCty exposes resolved values to references. Adapters return strings and
// string lists only.
func toCty(v adapter.Values) cty.Value {
	attrs := map[string]cty.Value{}
	for k, x := range v {
		attrs[k] = ctyOf(x)
	}
	return cty.ObjectVal(attrs)
}

func ctyOf(x any) cty.Value {
	switch t := x.(type) {
	case string:
		return cty.StringVal(t)
	case []string:
		if len(t) == 0 {
			return cty.ListValEmpty(cty.String)
		}
		vals := make([]cty.Value, len(t))
		for i, s := range t {
			vals[i] = cty.StringVal(s)
		}
		return cty.ListVal(vals)
	}
	return cty.NullVal(cty.DynamicPseudoType)
}

func sortedDeps(deps map[string]hcl.Range) []string {
	out := make([]string, 0, len(deps))
	for d := range deps {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
