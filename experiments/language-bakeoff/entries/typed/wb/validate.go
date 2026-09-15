package wb

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	nameRE      = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	componentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// nodeSet is a dependency-ordered list of nodes, each validated against
// the nodes before it.
type nodeSet struct {
	nodes []Node
	index map[Address]int
}

func newNodeSet() nodeSet { return nodeSet{index: map[Address]int{}} }

func (s *nodeSet) list() []Node { return slices.Clone(s.nodes) }

// add validates n against the nodes before it, then appends it.
func (s *nodeSet) add(n Node) error {
	if _, dup := s.index[n.Address]; dup {
		return errors.New("declared twice")
	}
	if err := checkPath(n.Owns); err != nil {
		return err
	}
	if err := s.checkRefs(n); err != nil {
		return err
	}
	if err := s.checkOwnership(n); err != nil {
		return err
	}
	s.index[n.Address] = len(s.nodes)
	s.nodes = append(s.nodes, n)
	return nil
}

// checkRefs requires every dependency to be an earlier node, and every
// reference in the spec to be a dependency whose path is the path that
// node owns or lies inside it. That catches a Ref decoded from JSON or
// taken from another graph that happens to reuse an address.
func (s *nodeSet) checkRefs(n Node) error {
	for _, d := range n.Deps {
		if _, ok := s.index[d]; !ok {
			return fmt.Errorf("references %s, which is not declared before it in this graph", d)
		}
	}
	uses, err := refsIn(n.Spec)
	if err != nil {
		return err
	}
	for _, u := range uses {
		if err := s.checkUse(n, u); err != nil {
			return err
		}
	}
	return nil
}

// checkUse holds a "$ref" to exactly the owned path and an "$in" to the
// owned path or a path inside it.
func (s *nodeSet) checkUse(n Node, u refUse) error {
	i, ok := s.index[u.addr]
	if !ok || !slices.Contains(n.Deps, u.addr) {
		return fmt.Errorf("references %s, which is not a declared dependency", u.addr)
	}
	owner := s.nodes[i].Owns
	if u.path == owner || (u.within && strings.HasPrefix(u.path, owner+"/")) {
		return nil
	}
	return fmt.Errorf("a reference to %s names path %q, but %s owns %q", u.addr, u.path, u.addr, owner)
}

// checkOwnership gives every path one owner. Paths are compared
// case-insensitively so that one file on a case-insensitive filesystem
// cannot have two owners. A path may sit inside another node's path only
// when it belongs to a task that depends on that resource: a resource
// nested in another could be deleted by the outer one's replace without
// the planner knowing, while a dependent task is re-planned whenever its
// container changes.
func (s *nodeSet) checkOwnership(n Node) error {
	mine := strings.ToLower(n.Owns)
	for _, o := range s.nodes {
		theirs := strings.ToLower(o.Owns)
		if theirs == mine {
			return fmt.Errorf("path %s is already owned by %s (as %s)", n.Owns, o.Address, o.Owns)
		}
		if inside(theirs, mine) {
			return fmt.Errorf("path %s would contain %s, owned by %s", n.Owns, o.Owns, o.Address)
		}
		if inside(mine, theirs) && !mayNest(n, o) {
			return fmt.Errorf("path %s is inside %s (owned by %s); only a task that references %s may write there",
				n.Owns, o.Owns, o.Address, o.Address)
		}
	}
	return nil
}

func inside(inner, outer string) bool { return strings.HasPrefix(inner, outer+"/") }

func mayNest(inner, outer Node) bool {
	return inner.Class == Task && outer.Class == Resource && slices.Contains(inner.Deps, outer.Address)
}

// checkIdentity validates a node's address, kind and class.
func checkIdentity(n Node) error {
	kind, name, ok := strings.Cut(string(n.Address), ".")
	if !ok || kind != n.Kind || !nameRE.MatchString(kind) || !nameRE.MatchString(name) {
		return fmt.Errorf("address must be <kind>.<name>, each matching %s", nameRE)
	}
	if n.Class != Resource && n.Class != Task {
		return fmt.Errorf("unknown class %q", n.Class)
	}
	return nil
}

// checkPath accepts clean, relative, slash-separated paths inside the
// workspace whose names use a portable ASCII character set. With ASCII
// names and case-insensitive ownership checks, one file has one owner
// whether or not the filesystem folds case or normalizes Unicode.
// ".wb" (evidence) and names containing ".wb-" (the engine's temporary,
// marker and swap names) are reserved.
func checkPath(p string) error {
	if p == "" || p == "." || path.Clean(p) != p || !filepath.IsLocal(filepath.FromSlash(p)) {
		return fmt.Errorf("path %q must be a clean relative path inside the workspace", p)
	}
	for i, name := range strings.Split(p, "/") {
		if err := checkName(p, name, i == 0); err != nil {
			return err
		}
	}
	return nil
}

func checkName(p, name string, top bool) error {
	if !componentRE.MatchString(name) {
		return fmt.Errorf("path %q: names may use only A-Z a-z 0-9 . _ -", p)
	}
	lower := strings.ToLower(name)
	if (top && lower == ".wb") || strings.Contains(lower, ".wb-") {
		return fmt.Errorf("path %q uses a reserved name (.wb, or a name containing .wb-)", p)
	}
	return nil
}

// refUse is one {"$ref"|"$in": ..., "path": ...} object found in a spec.
type refUse struct {
	addr   Address
	path   string
	within bool // "$in": the path may lie inside the node's path
}

// refsIn returns every reference in a marshaled spec, in a stable order.
func refsIn(spec []byte) ([]refUse, error) {
	var v any
	if err := json.Unmarshal(spec, &v); err != nil {
		return nil, fmt.Errorf("spec is not valid JSON: %w", err)
	}
	var uses []refUse
	collectRefs(v, &uses)
	slices.SortFunc(uses, func(a, b refUse) int {
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
	})
	return uses, nil
}

func collectRefs(v any, uses *[]refUse) {
	switch t := v.(type) {
	case map[string]any:
		p, _ := t["path"].(string)
		if a, ok := t["$ref"].(string); ok {
			*uses = append(*uses, refUse{addr: Address(a), path: p})
		}
		if a, ok := t["$in"].(string); ok {
			*uses = append(*uses, refUse{addr: Address(a), path: p, within: true})
		}
		for _, c := range t {
			collectRefs(c, uses)
		}
	case []any:
		for _, c := range t {
			collectRefs(c, uses)
		}
	}
}
