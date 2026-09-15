package lang

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

// Schemas is what the checker needs from the adapter registry. It is
// declared here, by its consumer, so the language never imports an adapter.
type Schemas interface {
	Lookup(kind string) (kind.Schema, kind.Lifecycle, bool)
	Kinds() []string
}

// Compile parses and checks src and returns its normalized intent, or every
// problem found as Diagnostics.
func Compile(name string, src []byte, schemas Schemas) (*intent.Intent, error) {
	f, err := Parse(src)
	if err != nil {
		return nil, err
	}
	c := &checker{schemas: schemas, byName: map[string]*decl{}}
	c.collect(f)
	c.link()
	c.ownership()
	order := c.order()
	if len(c.diags) > 0 {
		return nil, c.diags.sorted()
	}
	c.resolve(order)
	return c.intent(name, order), nil
}

type decl struct {
	block  *Block
	index  int
	life   kind.Lifecycle
	schema kind.Schema
	known  bool // the kind is registered
	attrs  []*Attr
	byAttr map[string]*Attr
	values map[string][]string
	refs   map[string]string
	needs  []string
}

func (d *decl) name() string { return d.block.Name.Text }

// pathUse is a literal workspace path a declaration owns or reads.
type pathUse struct {
	path string
	d    *decl
	pos  Pos
}

type checker struct {
	schemas Schemas
	decls   []*decl
	byName  map[string]*decl
	claims  []pathUse // owned paths
	reads   []pathUse // literal paths read or used
	diags   Diagnostics
}

func (c *checker) errorf(p Pos, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{Pos: p, Msg: fmt.Sprintf(format, args...)})
}

func (c *checker) collect(f *File) {
	for i, b := range f.Decls {
		if prev, dup := c.byName[b.Name.Text]; dup {
			c.errorf(b.Name.Pos, "%s is already declared on line %d", b.Name.Text, prev.block.Name.Pos.Line)
			continue
		}
		d := &decl{block: b, index: i, byAttr: map[string]*Attr{}, values: map[string][]string{}, refs: map[string]string{}}
		c.byName[d.name()] = d
		c.decls = append(c.decls, d)
		c.lifecycle(d)
		if d.known {
			c.attributes(d)
		}
	}
}

// lifecycle checks that the kind exists and that the declaration's leading
// keyword matches how that kind behaves over time.
func (c *checker) lifecycle(d *decl) {
	b := d.block
	schema, want, ok := c.schemas.Lookup(b.Kind.Text)
	if ok {
		d.schema, d.life, d.known = schema, want, true
	}
	life := kind.Lifecycle(b.Lifecycle.Text)
	switch {
	case life != kind.Keep && life != kind.Run:
		c.errorf(b.Lifecycle.Pos, "%q is not a lifecycle: start with keep (a resource wb converges) or run (work wb replays when its inputs change)", b.Lifecycle.Text)
	case !ok:
		c.errorf(b.Kind.Pos, "unknown kind %q (registered kinds: %s)", b.Kind.Text, strings.Join(c.schemas.Kinds(), ", "))
	case life != want:
		c.errorf(b.Lifecycle.Pos, "%s is %s: write \"%s %s %s\"", b.Kind.Text, describe(want), want, b.Kind.Text, b.Name.Text)
	}
}

func describe(l kind.Lifecycle) string {
	if l == kind.Keep {
		return "a kept resource that wb converges"
	}
	return "one-shot work that wb replays when stale"
}

func (c *checker) attributes(d *decl) {
	for _, a := range d.block.Attrs {
		f, ok := d.schema.Field(a.Name.Text)
		if !ok {
			c.errorf(a.Name.Pos, "%s has no attribute %q (attributes: %s)", d.schema.Kind, a.Name.Text, fieldNames(d.schema))
			continue
		}
		if prev, dup := d.byAttr[f.Name]; dup {
			c.errorf(a.Name.Pos, "%s is already set on line %d", f.Name, prev.Name.Pos.Line)
			continue
		}
		d.byAttr[f.Name] = a
		d.attrs = append(d.attrs, a)
		c.values(d, f, a)
	}
	for _, f := range d.schema.Fields {
		if f.Required && d.byAttr[f.Name] == nil {
			c.errorf(d.block.Name.Pos, "%s %s is missing required attribute %s", d.schema.Kind, d.name(), f.Name)
		}
	}
}

func fieldNames(s kind.Schema) string {
	var names []string
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}

func (c *checker) values(d *decl, f kind.Field, a *Attr) {
	if f.Type != kind.List && len(a.Values) != 1 {
		c.errorf(a.Values[1].Pos, "%s takes one value, found %d", f.Name, len(a.Values))
		return
	}
	for _, v := range a.Values {
		c.value(d, f, v)
	}
}

func (c *checker) value(d *decl, f kind.Field, v Value) {
	if v.Ref != nil {
		c.refAllowed(d, f, v)
		return
	}
	if f.Type != kind.Path && f.Type != kind.OwnedPath {
		return
	}
	if !c.path(f, v) {
		return
	}
	use := pathUse{path: v.Lit, d: d, pos: v.Pos}
	if f.Type == kind.OwnedPath {
		c.claims = append(c.claims, use)
		return
	}
	c.reads = append(c.reads, use)
}

func (c *checker) refAllowed(d *decl, f kind.Field, v Value) {
	switch f.Type {
	case kind.List:
		c.errorf(v.Pos, "%s takes literal strings, not references", f.Name)
	case kind.OwnedPath:
		c.errorf(v.Pos, "%s names a path %s owns, so it must be a literal path, not a reference", f.Name, d.name())
	}
}

// path checks that a literal path stays inside the workspace and avoids the
// names wb reserves. Paths are printable ASCII and compared without regard
// to case, so two spellings can never name one file on a case-insensitive
// or normalizing filesystem.
func (c *checker) path(f kind.Field, v Value) bool {
	p := v.Lit
	switch {
	case p == "":
		c.errorf(v.Pos, "%s is an empty path", f.Name)
	case !portable(p):
		c.errorf(v.Pos, "path %q may use only printable ASCII, without backslashes", p)
	case !filepath.IsLocal(p):
		c.errorf(v.Pos, "path %q must stay inside the workspace: relative, without ..", p)
	case filepath.Clean(p) != p:
		c.errorf(v.Pos, "write path %q as %q", p, filepath.Clean(p))
	case strings.EqualFold(strings.Split(p, "/")[0], foundation.StateDir):
		c.errorf(v.Pos, "path %q is inside %s, which wb reserves for its journal", p, foundation.StateDir)
	case tempComponent(p):
		c.errorf(v.Pos, "path %q uses %s, which wb reserves for temporary files", p, foundation.TempSuffix)
	case f.Type == kind.OwnedPath && p == ".":
		c.errorf(v.Pos, "%s cannot own the whole workspace", f.Name)
	default:
		return true
	}
	return false
}

func portable(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] > 0x7e || p[i] == '\\' {
			return false
		}
	}
	return true
}

// tempComponent reports whether any element of p could be, or sit inside,
// one of wb's temporary files.
func tempComponent(p string) bool {
	for _, elem := range strings.Split(fold(p), "/") {
		if strings.HasSuffix(elem, foundation.TempSuffix) {
			return true
		}
	}
	return false
}

// fold is the form paths are compared in.
func fold(p string) string { return strings.ToLower(p) }

// link resolves each reference to its target declaration and records the
// dependency edge it creates.
func (c *checker) link() {
	for _, d := range c.decls {
		for _, a := range d.attrs {
			c.linkAttr(d, a)
		}
	}
}

func (c *checker) linkAttr(d *decl, a *Attr) {
	f, _ := d.schema.Field(a.Name.Text)
	v := a.Values[0]
	if v.Ref == nil || len(a.Values) != 1 || f.Type == kind.List || f.Type == kind.OwnedPath {
		return
	}
	t, ok := c.target(d, v)
	if !ok {
		return
	}
	d.refs[f.Name] = v.Ref.String()
	if !contains(d.needs, t.name()) {
		d.needs = append(d.needs, t.name())
	}
}

func (c *checker) target(d *decl, v Value) (*decl, bool) {
	r := v.Ref
	t, ok := c.byName[r.Decl]
	switch {
	case !ok:
		c.errorf(v.Pos, "%s refers to %s, but nothing is named %s", d.name(), r, r.Decl)
	case t == d:
		c.errorf(v.Pos, "%s refers to itself", d.name())
	case !t.known:
		// The unknown kind is already reported.
	case !t.schema.Exported(r.Attr):
		c.errorf(v.Pos, "%s does not export %s (exports: %s)", r.Decl, r.Attr, strings.Join(t.schema.Exports, ", "))
	case t.byAttr[r.Attr] == nil:
		c.errorf(v.Pos, "%s refers to %s, which is not set", d.name(), r)
	default:
		return t, true
	}
	return nil, false
}

// ownership checks that owned paths are disjoint and that no declaration
// reads another's path by literal, which would hide the dependency.
func (c *checker) ownership() {
	for i, u := range c.claims {
		c.overlapping(u, c.claims[:i])
	}
	for _, r := range c.reads {
		c.readOfOwned(r)
	}
}

func (c *checker) overlapping(u pathUse, earlier []pathUse) {
	for _, e := range earlier {
		if same(u.path, e.path) || inside(u.path, e.path) || inside(e.path, u.path) {
			c.errorf(u.pos, "%s claims %q, which overlaps %q owned by %s (line %d)", u.d.name(), u.path, e.path, e.d.name(), e.pos.Line)
			return
		}
	}
}

func (c *checker) readOfOwned(r pathUse) {
	for _, owned := range c.claims {
		if !same(r.path, owned.path) && !inside(r.path, owned.path) {
			continue
		}
		if owned.d == r.d {
			c.errorf(r.pos, "%s uses %q, which it owns itself", r.d.name(), r.path)
			return
		}
		c.errorf(r.pos, "%q belongs to %s; %s so wb settles %s first and tracks it as an input", r.path, owned.d.name(), suggest(owned.d, r.path), owned.d.name())
		return
	}
}

func suggest(owner *decl, path string) string {
	for _, e := range owner.schema.Exports {
		a := owner.byAttr[e]
		if a != nil && a.Values[0].Ref == nil && same(a.Values[0].Lit, path) {
			return "reference " + owner.name() + "." + e + " instead"
		}
	}
	return "declare what you read as its own resource"
}

// same reports whether two paths name one file.
func same(a, b string) bool { return fold(a) == fold(b) }

// inside reports whether path p is strictly inside directory dir.
func inside(p, dir string) bool {
	return strings.HasPrefix(fold(p), fold(dir)+"/")
}

// order sorts declarations so each follows everything it needs; ties keep
// source order. Declarations left over sit on a cycle, which is an error.
func (c *checker) order() []*decl {
	s := &sorter{waiting: map[*decl]int{}, dependents: map[*decl][]*decl{}}
	for _, d := range c.decls {
		for _, n := range d.needs {
			s.waiting[d]++
			s.dependents[c.byName[n]] = append(s.dependents[c.byName[n]], d)
		}
	}
	for _, d := range c.decls {
		s.release(d)
	}
	for len(s.ready) > 0 {
		sort.Slice(s.ready, func(i, j int) bool { return s.ready[i].index < s.ready[j].index })
		d := s.ready[0]
		s.ready = s.ready[1:]
		s.out = append(s.out, d)
		for _, n := range s.dependents[d] {
			s.waiting[n]--
			s.release(n)
		}
	}
	if len(s.out) < len(c.decls) {
		c.cycle(s.waiting)
	}
	return s.out
}

type sorter struct {
	waiting    map[*decl]int
	dependents map[*decl][]*decl
	ready      []*decl
	out        []*decl
}

func (s *sorter) release(d *decl) {
	if s.waiting[d] == 0 {
		s.ready = append(s.ready, d)
	}
}

// cycle reports one dependency cycle. Every declaration still waiting has
// an upstream that is also waiting, so following those edges must loop.
func (c *checker) cycle(waiting map[*decl]int) {
	var d *decl
	for _, x := range c.decls {
		if waiting[x] > 0 {
			d = x
			break
		}
	}
	at := map[*decl]int{}
	var path []*decl
	for {
		if i, seen := at[d]; seen {
			path = path[i:]
			break
		}
		at[d] = len(path)
		path = append(path, d)
		d = c.waitingUpstream(d, waiting)
	}
	var names []string
	for _, x := range path {
		names = append(names, x.name())
	}
	names = append(names, path[0].name())
	c.errorf(path[0].block.Name.Pos, "dependency cycle: %s", strings.Join(names, " -> "))
}

func (c *checker) waitingUpstream(d *decl, waiting map[*decl]int) *decl {
	for _, n := range d.needs {
		if waiting[c.byName[n]] > 0 {
			return c.byName[n]
		}
	}
	panic("lang: waiting declaration without a waiting upstream")
}

// resolve replaces each reference with the value it names. Declarations are
// visited in dependency order, so every target is already resolved.
func (c *checker) resolve(order []*decl) {
	for _, d := range order {
		for _, a := range d.attrs {
			d.values[a.Name.Text] = c.resolved(a)
		}
	}
}

func (c *checker) resolved(a *Attr) []string {
	var out []string
	for _, v := range a.Values {
		if v.Ref == nil {
			out = append(out, v.Lit)
			continue
		}
		out = append(out, c.byName[v.Ref.Decl].values[v.Ref.Attr]...)
	}
	return out
}

func (c *checker) intent(name string, order []*decl) *intent.Intent {
	in := &intent.Intent{Source: filepath.Base(name)}
	for _, d := range order {
		in.Decls = append(in.Decls, intent.Decl{
			ID:        d.name(),
			Lifecycle: string(d.life),
			Kind:      d.schema.Kind,
			Line:      d.block.Lifecycle.Pos.Line,
			Attrs:     d.values,
			Refs:      nilIfEmpty(d.refs),
			Owns:      d.owned(),
			Needs:     d.needs,
		})
	}
	return in
}

func (d *decl) owned() []string {
	var out []string
	for _, f := range d.schema.Fields {
		if f.Type == kind.OwnedPath && d.byAttr[f.Name] != nil {
			out = append(out, d.values[f.Name]...)
		}
	}
	return out
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
