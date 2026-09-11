package verbs

import (
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Select the work's repository without moving the caller or changing its role.
// A named seat already identifies a checkout, so the common path needs no flag.
func dispatchCheckout(repo, slot string) (string, error) {
	if slot != "" {
		if r := slotRow(slot); r != nil && repo == "" {
			return fleet.S(r, "path"), nil
		}
	}
	if repo == "" {
		return cwd(), nil
	}
	if fleet.RepoID(repo) != "" {
		return repo, nil
	}
	_, rows := fleet.MapRows(fleet.RolesMap())
	paths := []string{cwd()}
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	byRepo := map[string]string{}
	for _, path := range paths {
		if rc, remote := gitTry(path, gitTimeout, "remote", "get-url", "origin"); rc == 0 {
			if m := originRe.FindStringSubmatch(remote); m != nil && strings.EqualFold(m[1]+"/"+m[2], repo) {
				byRepo[fleet.RepoID(path)] = path
			}
		}
	}
	if len(byRepo) != 1 {
		return "", refuse("fleet dispatch: %s resolves to %d local repositories; pass --repo <checkout-path>", repo, len(byRepo))
	}
	for _, path := range byRepo {
		return path, nil
	}
	return "", refuse("fleet dispatch: no checkout for %s", repo)
}

func dispatchTarget(change, repo, slot string) (string, string, string, error) {
	if repo == "" && slot == "" {
		return resolveDispatchTarget("dispatch", change)
	}
	co, err := dispatchCheckout(repo, slot)
	if err != nil {
		return "", "", "", err
	}
	rid := fleet.RepoID(co)
	if rid == "" {
		return "", "", "", refuse("fleet dispatch: %s is not a checkout; select --repo or --slot", co)
	}
	branch := strings.TrimSpace(change)
	if isChangeNumber(change) {
		data, why := ghJSONAt(co, "gh", "pr", "view", strings.TrimPrefix(branch, "#"), "--json", "headRefName")
		row, _ := data.(map[string]any)
		branch = fleet.S(row, "headRefName")
		if branch == "" {
			return "", "", "", refuse("fleet dispatch: cannot resolve %s in %s: %s", change, co, why)
		}
	}
	sha, branch, _, err := resolveBranchHeadAt(co, rid, branch, "selected repository")
	return rid, branch, sha, err
}
