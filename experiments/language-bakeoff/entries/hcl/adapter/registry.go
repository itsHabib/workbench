package adapter

import (
	"fmt"
	"regexp"
	"sort"
)

// Registry maps block type names to adapters. It is built once at startup
// from compiled-in values; there is no dynamic plugin loading.
type Registry struct{ byType map[string]Adapter }

// typeName keeps type names usable as HCL reference roots without this
// package importing HCL: adapters never see the configuration language.
var typeName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// NewRegistry validates and indexes adapters by type.
func NewRegistry(ads ...Adapter) (*Registry, error) {
	r := &Registry{byType: map[string]Adapter{}}
	for _, a := range ads {
		if err := r.add(a); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) add(a Adapter) error {
	t := a.Type()
	if !typeName.MatchString(t) {
		return fmt.Errorf("adapter type %q must match %s", t, typeName)
	}
	if _, dup := r.byType[t]; dup {
		return fmt.Errorf("adapter type %q registered twice", t)
	}
	if err := checkKind(a); err != nil {
		return err
	}
	r.byType[t] = a
	return nil
}

func checkKind(a Adapter) error {
	switch a.Kind() {
	case Resource:
		if _, ok := a.(ResourceAdapter); !ok {
			return fmt.Errorf("adapter %q declares kind resource but does not implement ResourceAdapter", a.Type())
		}
		return nil
	case Task:
		if _, ok := a.(TaskAdapter); !ok {
			return fmt.Errorf("adapter %q declares kind task but does not implement TaskAdapter", a.Type())
		}
		return nil
	}
	return fmt.Errorf("adapter %q has unknown kind %q", a.Type(), a.Kind())
}

// Lookup returns the adapter registered for type t.
func (r *Registry) Lookup(t string) (Adapter, bool) {
	a, ok := r.byType[t]
	return a, ok
}

// Types lists registered type names in sorted order.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.byType))
	for t := range r.byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
