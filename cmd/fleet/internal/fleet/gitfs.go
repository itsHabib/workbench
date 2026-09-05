package fleet

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GitDirs is (gitdir, commondir) for the checkout containing start, or ("", "").
//
// No spawn — `.git` is read directly. For a linked worktree `.git` is a file
// pointing at `<common>/worktrees/<name>`, and that directory holds a `commondir`
// file pointing back at the shared `.git`. The commondir is the repo's identity:
// every worktree of one repo shares it, and two different repos never do.
func GitDirs(start string) (string, string) {
	d, err := filepath.Abs(start)
	if err != nil {
		return "", ""
	}
	if isFile(d) {
		d = filepath.Dir(d)
	}
	var gitdir string
	for gitdir == "" {
		g := filepath.Join(d, ".git")
		if isDir(g) {
			gitdir = g
			break
		}
		if isFile(g) {
			line, _ := readText(g)
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "gitdir:") {
				return "", ""
			}
			gitdir = strings.TrimSpace(line[len("gitdir:"):])
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Clean(filepath.Join(d, gitdir))
			}
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", ""
		}
		d = parent
	}
	common := gitdir
	if rel, ok := readText(filepath.Join(gitdir, "commondir")); ok {
		rel = strings.TrimSpace(rel)
		if filepath.IsAbs(rel) {
			common = rel
		} else {
			common = filepath.Clean(filepath.Join(gitdir, rel))
		}
	}
	return gitdir, common
}

// LongPath is absolute, long-form, forward-slashed. On Windows an 8.3 component
// (`MICHAE~1`, what %TMP% hands out) and a long one name the same directory but are
// different strings, so anything used as an identity is resolved first or one repo
// becomes two.
func LongPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = platformLongPath(abs)
	return strings.ReplaceAll(abs, "\\", "/")
}

// NormCase is Python's os.path.normcase: identity on POSIX; lower-cased with
// backslashes on Windows. It is hashed into every repo id, so it must match the
// Python byte for byte or a store the Python wrote keys nothing this binary computes.
func NormCase(p string) string { return platformNormCase(p) }

// RepoID is the stable identity of the repo containing start, or "" outside one:
// a readable name plus a hash of the commondir. The name alone is not unique; the
// hash alone is unreadable in `fleet leases`. Hashing the commondir means every
// worktree of a repo agrees and no two repos ever collide.
func RepoID(start string) string {
	_, common := GitDirs(start)
	if common == "" {
		return ""
	}
	real, err := filepath.EvalSymlinks(common)
	if err != nil {
		real = common
	}
	lp := LongPath(real)
	root := lp
	if strings.HasSuffix(lp, "/.git") {
		root = lp[:len(lp)-len("/.git")]
	}
	name := filepath.Base(strings.TrimRight(root, "/"))
	if name == "" || name == "." || name == "/" {
		name = "repo"
	}
	return Safe(name) + "-" + sha1hex(NormCase(lp))[:8]
}

// BranchOf is the branch of the checkout containing start, read from .git without
// spawning git. "" for a detached head or outside any repo.
func BranchOf(start string) string {
	gitdir, _ := GitDirs(start)
	if gitdir == "" {
		return ""
	}
	head, ok := readText(filepath.Join(gitdir, "HEAD"))
	if !ok {
		return ""
	}
	head = strings.TrimSpace(head)
	const prefix = "ref: refs/heads/"
	if !strings.HasPrefix(head, prefix) {
		return ""
	}
	return head[len(prefix):]
}

// LocalBranches is every local branch name: loose refs under refs/heads plus
// packed-refs. Read only when a case-insensitive match is needed, so the common path
// stays two stats.
func LocalBranches(common string) map[string]bool {
	names := map[string]bool{}
	root := filepath.Join(common, "refs", "heads")
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		names[strings.ReplaceAll(rel, string(os.PathSeparator), "/")] = true
		return nil
	})
	if packed, ok := readText(filepath.Join(common, "packed-refs")); ok {
		for _, line := range strings.Split(packed, "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 && strings.HasPrefix(parts[1], "refs/heads/") {
				names[parts[1][len("refs/heads/"):]] = true
			}
		}
	}
	return names
}

// BranchSpelling is cand as a local branch name in its on-disk spelling, or "".
// Exact first (loose, then packed). On a case-insensitive filesystem git accepts any
// casing and lands on the one spelling it has; leasing the typed spelling would lease
// a key nobody else holds while the incumbent holds the real one.
func BranchSpelling(common, cand string) string {
	loose := filepath.Join(append([]string{common, "refs", "heads"}, strings.Split(cand, "/")...)...)
	if isFile(loose) {
		if !isWindows {
			return cand
		}
	} else {
		packed, _ := readText(filepath.Join(common, "packed-refs"))
		if strings.Contains(packed+"\n", " refs/heads/"+cand+"\n") {
			return cand
		}
		if !isWindows {
			return ""
		}
	}
	want := NormCase(cand)
	for name := range LocalBranches(common) {
		if NormCase(name) == want {
			return name
		}
	}
	return ""
}

// RemoteBranchExists is `origin/x`-style DWIM: `git checkout x` with no local `x` but
// exactly one `refs/remotes/*/x` creates and switches to a local `x`. That IS a
// branch switch and must be leased as one.
func RemoteBranchExists(common, cand string) bool {
	remotes := filepath.Join(common, "refs", "remotes")
	hits := 0
	for _, r := range listDir(remotes) {
		if isFile(filepath.Join(append([]string{remotes, r}, strings.Split(cand, "/")...)...)) {
			hits++
		}
	}
	if hits > 0 {
		return hits == 1
	}
	packed, ok := readText(filepath.Join(common, "packed-refs"))
	if !ok {
		return false
	}
	re := regexp.MustCompile(` refs/remotes/[^/\s]+/` + regexp.QuoteMeta(cand) + `\n`)
	return len(re.FindAllString(packed+"\n", -1)) == 1
}

// RemoteNames is the set of remote names under refs/remotes.
func RemoteNames(common string) map[string]bool {
	out := map[string]bool{}
	for _, r := range listDir(filepath.Join(common, "refs", "remotes")) {
		out[r] = true
	}
	return out
}
