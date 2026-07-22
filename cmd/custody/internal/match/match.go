// Package match is custody's request-identity policy: it turns a raw origin-form
// request target into one canonical form and matches that form against a key's
// action rules. It is the load-bearing security layer of spec §7 C/D — the exact
// semantic target that is matched is the exact target the proxy forwards.
//
// Canonicalize refuses ambiguous input (encoded separators, revealed escapes,
// dot-segments that escape, controls, or non-ASCII) rather than best-effort
// matching it. Match then tests method, an anchored whole-path glob over a
// constrained segment alphabet, and anchored-scalar query predicates. The
// package holds no I/O and no proxy plumbing; it decides reach and nothing else.
package match

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
)

// ErrBadTarget is returned by Canonicalize for any request target that is not
// unambiguously origin-form and safe to decode once. Callers map it to a 400
// refusal (spec §3 step 0): custody refuses rather than guessing.
var ErrBadTarget = errors.New("refused_bad_target")

// Target is the canonical, decision-ready form of a request target. Key is the
// first path segment (the manifest prefix); Path is the rooted vendor path after
// the key, already decoded and dot-segment-resolved; Query is the parsed,
// re-encodable query; Raw is the original request target, retained only so the
// caller can hash it for the log (never re-parsed for a decision).
type Target struct {
	Key       string
	Path      string
	Query     url.Values
	QueryKeys []string
	Raw       string
}

// Fired names the rule that authorized a request: the action name, the rule's
// index within that action, and the query values the rule actually tested
// (matched_query, spec §5). It is what makes a value-branching verdict replayable.
type Fired struct {
	Action  string
	Rule    int
	Matched map[string][]string
}

// Label renders the rule_fired log field, e.g. "read[0]".
func (f Fired) Label() string { return fmt.Sprintf("%s[%d]", f.Action, f.Rule) }

// Canonicalize parses and canonicalizes an origin-form request target (the value
// of r.RequestURI). It accepts origin-form only and refuses anything ambiguous:
// absolute/authority-form, fragments, a stray or double percent-escape, an
// encoded separator, a decoded control/NUL, or non-ASCII. The whole path is
// canonicalized BEFORE the key prefix is split off, so an encoded dot-segment
// cannot move the effective path across the key boundary (spec §3 step 0, §7 C).
func Canonicalize(target string) (Target, error) {
	if target == "" || target[0] != '/' {
		return Target{}, fmt.Errorf("%w: target must be origin-form", ErrBadTarget)
	}
	rawPath, rawQuery := target, ""
	if i := strings.IndexByte(target, '?'); i >= 0 {
		rawPath, rawQuery = target[:i], target[i+1:]
	}
	if strings.IndexByte(rawPath, '#') >= 0 || strings.IndexByte(rawQuery, '#') >= 0 {
		return Target{}, fmt.Errorf("%w: fragment not allowed in a request target", ErrBadTarget)
	}
	decoded, err := decodeOnce(rawPath)
	if err != nil {
		return Target{}, err
	}
	clean := removeDotSegments(decoded)
	key, vendorPath := splitKey(clean)
	if key == "" {
		return Target{}, fmt.Errorf("%w: no key prefix", ErrBadTarget)
	}
	query, keys, err := parseQuery(rawQuery)
	if err != nil {
		return Target{}, err
	}
	return Target{Key: key, Path: vendorPath, Query: query, QueryKeys: keys, Raw: target}, nil
}

// Match returns the first rule, among the named granted actions of key k, that
// matches the request. Iteration order follows the granted-action slice then
// rule index, so the fired label is deterministic; because any match authorizes,
// order affects only the label, never the decision.
func Match(k manifest.Key, granted []string, method, path string, query url.Values) (Fired, bool) {
	return firstMatch(k, granted, method, path, query)
}

// Suggest returns the action name that WOULD match this request if it were
// granted, scanning every action in the key (sorted for determinism). It powers
// the exact remedy on a 403 denial — "grant this action" — and is advisory only.
func Suggest(k manifest.Key, method, path string, query url.Values) (string, bool) {
	f, ok := firstMatch(k, sortedActions(k), method, path, query)
	if !ok {
		return "", false
	}
	return f.Action, true
}

func firstMatch(k manifest.Key, actions []string, method, path string, query url.Values) (Fired, bool) {
	for _, name := range actions {
		action, ok := k.Actions[name]
		if !ok {
			continue
		}
		for i, rule := range action.Rules {
			matched, ok := ruleMatches(rule, method, path, query)
			if ok {
				return Fired{Action: name, Rule: i, Matched: matched}, true
			}
		}
	}
	return Fired{}, false
}

func ruleMatches(r manifest.Rule, method, path string, query url.Values) (map[string][]string, bool) {
	if !methodMatches(r.Methods, method) {
		return nil, false
	}
	if !pathMatches(r.Path, path) {
		return nil, false
	}
	return queryMatches(r, query)
}

func methodMatches(methods []string, method string) bool {
	for _, m := range methods {
		if m == "*" || m == method {
			return true
		}
	}
	return false
}

// queryMatches enforces the anchored-scalar contract (spec §7 D): each param a
// rule names must occur exactly once and equal the predicate value; any param no
// rule mentions is denied unless the rule opts in with allowExtraParams. It
// returns the tested values as matched_query.
func queryMatches(r manifest.Rule, query url.Values) (map[string][]string, bool) {
	matched := map[string][]string{}
	for name, pred := range r.Query {
		vals := query[name]
		if len(vals) != 1 {
			return nil, false
		}
		if pred.Equals == nil || vals[0] != *pred.Equals {
			return nil, false
		}
		matched[name] = vals
	}
	if r.AllowExtraParams {
		return matched, true
	}
	for name := range query {
		if _, ok := r.Query[name]; !ok {
			return nil, false
		}
	}
	return matched, true
}

func sortedActions(k manifest.Key) []string {
	names := make([]string, 0, len(k.Actions))
	for name := range k.Actions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
