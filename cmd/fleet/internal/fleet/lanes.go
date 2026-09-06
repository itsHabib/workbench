package fleet

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// exeDir is the directory of this binary, for the installed-layout lanes lookup.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// LanesDir is where lane manifests live: $FLEET_LANES, else `lanes/` beside the
// state root (the installed layout, ~/.fleet/lanes), else beside the binary.
//
// The Python looked beside its own file. A binary is installed anywhere, so the
// state root is the installed home here; $FLEET_LANES still wins, and the suite
// sets it.
func LanesDir() string {
	if env := os.Getenv("FLEET_LANES"); env != "" {
		return env
	}
	for _, cand := range []string{Path("lanes"), filepath.Join(exeDir(), "lanes"), filepath.Join(filepath.Dir(exeDir()), "lanes")} {
		if isDir(cand) {
			return cand
		}
	}
	return Path("lanes")
}

var durationRe = regexp.MustCompile(`\A\s*(\d+(?:\.\d+)?)\s*([smh]?)\s*\z`)

// ParseDuration is seconds from a manifest duration: a number, or `<n>s|m|h`. 0 for
// anything else, so a typo in a manifest disables the check it feeds rather than
// inventing a deadline.
func ParseDuration(v any) float64 {
	switch x := v.(type) {
	case bool:
		return 0
	case float64:
		if x > 0 {
			return x
		}
		return 0
	case string:
		m := durationRe.FindStringSubmatch(x)
		if m == nil {
			return 0
		}
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0
		}
		switch m[2] {
		case "m":
			n *= 60
		case "h":
			n *= 3600
		}
		if n > 0 {
			return n
		}
	}
	return 0
}

// LaneOf is the manifest keys the hook reads for a role — kind, requires, produces,
// cadence — or nil when the kind has no readable manifest. Never a denial: a missing
// manifest is logged and the checks that need it are skipped. `cadence` is the lane's
// own statement of how long it may go without observable progress; it is data the
// board reads lazily, never something the hook acts on.
func LaneOf(role string) Rec {
	if role == "" {
		return nil
	}
	kind := strings.SplitN(role, ":", 2)[0]
	m := ReadJSON(filepath.Join(LanesDir(), kind, "manifest.json"))
	if m == nil || S(m, "kind") != kind {
		return nil
	}
	req := []any{}
	for _, r := range Strs(m, "requires") {
		req = append(req, r)
	}
	var prod any
	if p, ok := m["produces"].(string); ok {
		prod = p
	}
	var cadence any
	if c := ParseDuration(m["cadence"]); c > 0 {
		cadence = c
	}
	// `watch`: this lane reads the board at every prompt — the attention rows and what
	// changed since its last prompt, injected so a hub never has to ask. Per-kind data
	// admitted under the same rule as cadence; the substrate does not know which kind.
	watch, _ := m["watch"].(bool)
	return Rec{"kind": kind, "requires": req, "produces": prod, "cadence": cadence, "watch": watch}
}

// MapRow is one real line of roles.map.
type MapRow struct {
	Index  int
	Path   string
	Tenant string
	Role   string
	Slot   string
}

// MapRows is roles.map as its raw lines plus the real rows; comments keep their place.
func MapRows(mapfile string) ([]string, []MapRow) {
	text, ok := readText(mapfile)
	if !ok {
		return nil, nil
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	var rows []MapRow
	for i, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 3 && !strings.HasPrefix(parts[0], "#") {
			r := MapRow{Index: i, Path: parts[0], Tenant: parts[1], Role: parts[2]}
			if len(parts) > 3 {
				r.Slot = parts[3]
			}
			rows = append(rows, r)
		}
	}
	return lines, rows
}

// RolesMap is the path of roles.map.
func RolesMap() string { return filepath.Join(OrgState, "roles.map") }

// MapRowsFor is (exact role, longest-prefix tenant, slot) for cwd.
//
// A tenant inherits down a tree, so longest-prefix is right. A role does not: a
// nested worktree is a different node from the checkout above it, and under prefix
// matching every unlisted nested worktree wore the checkout's card. So a role requires
// an exact match, and an unlisted directory has no role rather than a borrowed one.
func MapRowsFor(cwd string) (role, tenant, slot string) {
	_, rows := MapRows(RolesMap())
	// Canonical on both sides: a map entry written as /var/x and a cwd reported as
	// /private/var/x are one directory.
	want := canonPath(cwd)
	best := 0
	for _, r := range rows {
		entry := canonPath(r.Path)
		if entry == want {
			role, slot = r.Role, r.Slot
		}
		// An ancestor is a path-component prefix, never a string prefix.
		above := entry == want || Within(cwd, r.Path)
		if above && len(entry) > best {
			best, tenant = len(entry), r.Tenant
		}
	}
	return role, tenant, slot
}

// RoleOf is the role bound to exactly this checkout, or "".
func RoleOf(cwd string) string { r, _, _ := MapRowsFor(cwd); return r }

// RoledRoot is the roled checkout that cwd is exactly, or "". A session's receipts
// must come from inside its own roled worktree.
func RoledRoot(cwd string) string {
	if RoleOf(cwd) == "" {
		return ""
	}
	return LongPath(cwd)
}

// TenantOf is the tenant owning this path — longest prefix.
func TenantOf(cwd string) string { _, t, _ := MapRowsFor(cwd); return t }

// PooledSlotNames is every fourth-column slot name in roles.map.
func PooledSlotNames() map[string]bool {
	_, rows := MapRows(RolesMap())
	out := map[string]bool{}
	for _, r := range rows {
		if r.Slot != "" {
			out[r.Slot] = true
		}
	}
	return out
}
