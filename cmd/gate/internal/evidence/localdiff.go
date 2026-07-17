package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// reSHA matches a full git object name (SHA-1 or SHA-256). SHAs parsed from
// API JSON flow into git argv; requiring hex here means a value like
// "--upload-pack=..." can never reach git as an option.
var reSHA = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// tooLarge reports whether err is the GitHub diff endpoint rejecting the PR
// as over its line cap (HTTP 406, "diff exceeded the maximum number of
// lines"). Only this rejection may route to the local fallback; every other
// failure aborts the run. Matching both the status and the body keeps an
// unrelated 406 from steering into the fallback.
func tooLarge(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 406") && strings.Contains(msg, "exceeded the maximum number of lines")
}

// fallbackDiff routes an oversized PR to localDiff, pinned to the head the
// run's view evidence recorded.
func fallbackDiff(pr PRRef, view json.RawMessage) (diffResult, error) {
	var v struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(view, &v); err != nil || v.HeadRefOid == "" {
		return diffResult{}, fmt.Errorf("evidence: view evidence carries no headRefOid to pin the local diff")
	}
	return localDiff(pr, v.HeadRefOid)
}

// diffResult is a computed diff plus the two commits it spans, so the caller
// can record which merge base and head the evidence was taken against.
type diffResult struct {
	Diff      string
	MergeBase string
	Head      string
}

// localDiff computes the PR's merge-base diff from a depth-1 fetch of exactly
// two commits — the fallback when the diff endpoint rejects the PR as too
// large. viewHead is the headRefOid the run's view evidence recorded; the diff
// is refused unless the PR still points at it. Every git step runs with ambient
// config neutralized so the recorded diff can't diverge from GitHub's rendering
// via a host textconv/format setting, and fails closed on any error, an
// empty result, or a merge base equal to head.
func localDiff(pr PRRef, viewHead string) (diffResult, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d", pr.Repo, pr.Number))
	if err != nil {
		return diffResult{}, err
	}
	base, head, err := parsePullHeads(raw)
	if err != nil {
		return diffResult{}, err
	}
	// Both flow into a URL or git argv; require hex before use. base and head
	// come from the same pulls payload, so validate them together here.
	if !reSHA.MatchString(base) || !reSHA.MatchString(head) {
		return diffResult{}, fmt.Errorf("evidence: non-hex commit id from api (base=%q head=%q)", base, head)
	}
	if head != viewHead {
		return diffResult{}, fmt.Errorf("evidence: pr head moved during gather: view %s, pulls %s", viewHead, head)
	}

	raw, err = gh("api", fmt.Sprintf("repos/%s/compare/%s...%s?per_page=1", pr.Repo, base, head))
	if err != nil {
		return diffResult{}, err
	}
	mb, err := parseMergeBase(raw)
	if err != nil {
		return diffResult{}, err
	}
	if !reSHA.MatchString(mb) {
		return diffResult{}, fmt.Errorf("evidence: non-hex merge base from api: %q", mb)
	}
	if mb == head {
		// The 406 guaranteed a >20k-line merge-base diff at pin time; an
		// equal base means the base advanced to contain head (e.g. a
		// concurrent merge). Refuse rather than record an empty diff.
		return diffResult{}, fmt.Errorf("evidence: merge base equals head (%s) — base moved during gather", head)
	}

	dir, err := os.MkdirTemp("", "gate-evidence-")
	if err != nil {
		return diffResult{}, fmt.Errorf("evidence: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if _, err := git(dir, "init", "-q"); err != nil {
		return diffResult{}, err
	}
	url := fmt.Sprintf("https://github.com/%s.git", pr.Repo)
	// Fetching by raw commit id needs the remote to advertise
	// uploadpack.allowReachableSHA1InWant; github.com does. A self-hosted
	// remote without it would fail here with an opaque git error.
	// Clear any inherited credential.helper list, then use gh's — the same
	// principal as every other evidence read. --end-of-options fixes url/mb/head
	// as positionals regardless of the (hex-validated) SHA content.
	cred := "credential.helper="
	ghcred := "credential.helper=!gh auth git-credential"
	if _, err := git(dir, "-c", cred, "-c", ghcred, "fetch", "-q", "--depth=1", "--end-of-options", url, mb, head); err != nil {
		return diffResult{}, err
	}
	// Match GitHub, which renders diffs from committed .gitattributes alone:
	// attr.tree=head honors the PR's committed attributes (e.g. a text file
	// force-marked -diff/binary) that the empty scratch worktree would ignore,
	// while core.attributesFile=null and GIT_ATTR_NOSYSTEM=1 (set in git's env)
	// suppress the host's global/XDG and system attributes files. head is
	// hex-validated.
	out, err := git(dir, "-c", "attr.tree="+head, "-c", "core.attributesFile="+os.DevNull,
		"diff", "--no-ext-diff", "--no-textconv", "--no-color", mb, head)
	if err != nil {
		return diffResult{}, err
	}
	if len(out) == 0 {
		return diffResult{}, fmt.Errorf("evidence: local diff of %s..%s is empty for an over-cap PR", mb, head)
	}
	return diffResult{Diff: string(out), MergeBase: mb, Head: head}, nil
}

// parsePullHeads extracts base.sha and head.sha from a pulls/<n> payload.
func parsePullHeads(raw []byte) (base, head string, err error) {
	var p struct {
		Base struct {
			SHA string `json:"sha"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", fmt.Errorf("evidence: parse pull: %w", err)
	}
	if p.Base.SHA == "" || p.Head.SHA == "" {
		return "", "", fmt.Errorf("evidence: pull payload missing base/head sha")
	}
	return p.Base.SHA, p.Head.SHA, nil
}

// parseMergeBase extracts merge_base_commit.sha from a compare payload.
func parseMergeBase(raw []byte) (string, error) {
	var c struct {
		MergeBaseCommit struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("evidence: parse compare: %w", err)
	}
	if c.MergeBaseCommit.SHA == "" {
		return "", fmt.Errorf("evidence: compare payload missing merge base")
	}
	return c.MergeBaseCommit.SHA, nil
}

// git runs one git command in dir with ambient configuration neutralized: no
// system/global config or attributes file (killing host textconv/format keys
// like astextplain, diff.noprefix, color, diff.external), no inherited GIT_*
// env (GIT_DIR, GIT_EXTERNAL_DIFF, …), and no credential prompt. Only the
// explicit -c flags the caller passes apply.
func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(scrubGit(os.Environ()),
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.Output()
	if err != nil {
		sub := subcommand(args)
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("evidence: git %s: %s", sub, ee.Stderr)
		}
		return nil, fmt.Errorf("evidence: git %s: %w", sub, err)
	}
	return out, nil
}

// subcommand finds the git subcommand for error labelling, skipping global
// flags and the value that follows a -c pair (which is not itself a flag).
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++ // skip the config value
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return "git"
}

// scrubGit drops every GIT_* variable from an environment so no inherited git
// setting leaks into the scratch repo.
func scrubGit(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
