package wb

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Define is a workflow definition: an ordinary Go function that declares
// nodes and returns the graph. It should only declare. Nothing enforces
// that: it is arbitrary Go code running with the caller's privileges.
type Define func(p *Params) *Graph

// Params are a workflow's declared inputs, the sanctioned way to vary a
// definition without editing its source. Every value read is recorded in
// the intent; a value supplied but never read is an error, so a typo
// cannot silently plan nothing.
type Params struct {
	given map[string]string
	read  map[string]string
}

// String returns the value supplied for key, or def.
func (p *Params) String(key, def string) string {
	v, ok := p.given[key]
	if !ok {
		v = def
	}
	p.read[key] = v
	return v
}

// Evaluate runs define and returns its normalized intent. It runs define
// twice and compares the results: a cheap guard against definitions that
// read the clock, randomness or other changing state. It cannot prove a
// definition pure and it does not sandbox one.
func Evaluate(define Define, given map[string]string) (Intent, error) {
	first, err := evaluateOnce(define, given)
	if err != nil {
		return Intent{}, err
	}
	second, err := evaluateOnce(define, given)
	if err != nil {
		return Intent{}, err
	}
	if first.Digest() != second.Digest() {
		return Intent{}, errors.New("workflow is not deterministic: two evaluations with the same params produced different intents")
	}
	return first, nil
}

func evaluateOnce(define Define, given map[string]string) (Intent, error) {
	p := &Params{given: given, read: map[string]string{}}
	g := define(p)
	if g == nil {
		return Intent{}, errors.New("workflow returned no graph")
	}
	in, err := g.Intent()
	if err != nil {
		return Intent{}, err
	}
	var unread []string
	for k := range given {
		if _, ok := p.read[k]; !ok {
			unread = append(unread, k)
		}
	}
	if len(unread) > 0 {
		slices.Sort(unread)
		known := slices.Sorted(maps.Keys(p.read))
		return Intent{}, fmt.Errorf("unknown param(s) %s; this workflow reads: %s",
			strings.Join(unread, ", "), strings.Join(known, ", "))
	}
	if len(p.read) > 0 {
		in.Params = p.read
	}
	return in, nil
}
