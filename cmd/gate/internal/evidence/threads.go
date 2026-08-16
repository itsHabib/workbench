package evidence

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Unresolved review threads outlive the findings they report: a later commit
// fixes the code, the reviewer never comes back, and the thread sits there
// looking actionable until a human writes "fixed in <sha>, covered by <test>"
// and clicks resolve. That hand-disposition is the friction this file removes.
//
// It prepares a disposition; it never decides one. A thread is only ever
// reported as fixed when BOTH facts are on the record — a commit after the
// thread's anchor that touches the thread's own file, and a test change riding
// in that same commit. Anything short of both stays actionable and reaches a
// human, because a false "this was fixed" buries a live finding while a false
// "still actionable" only costs a look.
//
// Nothing here resolves a thread. The output is a comment body and the exact
// resolve call, for a caller (or an operator) to run — the same read-only
// posture as the rest of the evidence sweep.

// Thread is one GitHub review thread as the disposition logic consumes it.
type Thread struct {
	// ID is the GraphQL node id — the handle the resolve mutation takes.
	ID     string `json:"id"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Author string `json:"author,omitempty"`
	Body   string `json:"body,omitempty"`
	// AnchorSHA is the commit the thread's first comment was posted against.
	// It orders the thread against the PR's commits: only what landed AFTER it
	// can have fixed it.
	AnchorSHA string `json:"anchor_sha,omitempty"`
	Resolved  bool   `json:"resolved,omitempty"`
}

// Commit is one commit of a pull request with the files it touched.
type Commit struct {
	SHA     string   `json:"sha"`
	Subject string   `json:"subject,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// Disposition is what the sweep prepared for one unresolved thread: either a
// commit/test-backed resolve comment, or a statement of which fact is missing.
type Disposition struct {
	Thread Thread `json:"thread"`
	// Actionable is the conservative default: true unless both the fixing
	// commit and its covering test were identified.
	Actionable bool     `json:"actionable"`
	FixCommit  string   `json:"fix_commit,omitempty"`
	FixSubject string   `json:"fix_subject,omitempty"`
	Tests      []string `json:"tests,omitempty"`
	// Why states the fact that decided it, in both directions.
	Why string `json:"why"`
	// Comment is the prepared resolve comment; empty when actionable.
	Comment string `json:"comment,omitempty"`
	// ResolveCommand is the exact call that resolves the thread once a human
	// accepts the comment. Never run here.
	ResolveCommand string `json:"resolve_command,omitempty"`
}

// Dispositions prepares a disposition for every unresolved thread, oldest
// first. commits must be the pull request's commits in history order with
// their touched files populated; headSHA is the head the sweep read.
func Dispositions(threads []Thread, commits []Commit, headSHA string) []Disposition {
	index := make(map[string]int, len(commits))
	for i, c := range commits {
		index[c.SHA] = i
	}
	var out []Disposition
	for _, th := range threads {
		if th.Resolved {
			continue
		}
		out = append(out, disposition(th, commits, index, headSHA))
	}
	return out
}

func disposition(th Thread, commits []Commit, index map[string]int, headSHA string) Disposition {
	d := Disposition{Thread: th, Actionable: true}
	if th.Path == "" {
		d.Why = "thread has no file anchor — nothing to match a fixing commit against"
		return d
	}
	anchor, ok := index[th.AnchorSHA]
	if !ok {
		d.Why = fmt.Sprintf("thread's anchor commit %s is not in this pull request's history — it cannot be ordered against later commits", shortSHA(th.AnchorSHA))
		return d
	}
	fix, tests, touched := fixingCommit(commits[anchor+1:], th.Path)
	if touched == "" {
		d.Why = fmt.Sprintf("no commit after %s touches %s", shortSHA(th.AnchorSHA), th.Path)
		return d
	}
	if fix.SHA == "" {
		d.Why = fmt.Sprintf("commit %s touches %s after the thread, but carries no test change — a human must confirm the finding is addressed", shortSHA(touched), th.Path)
		return d
	}
	d.Actionable = false
	d.FixCommit, d.FixSubject, d.Tests = fix.SHA, fix.Subject, tests
	d.Why = fmt.Sprintf("%s changes %s and its regression test in one commit", shortSHA(fix.SHA), th.Path)
	d.Comment = resolveComment(th, fix, tests, headSHA)
	d.ResolveCommand = resolveCommand(th.ID)
	return d
}

// fixingCommit picks the earliest commit that both touches the thread's file
// and carries a test change — the pairing that makes a disposition evidence-
// backed rather than a guess. It also reports the earliest commit touching the
// file at all, so a caller can say which of the two facts is missing.
func fixingCommit(after []Commit, file string) (Commit, []string, string) {
	touched := ""
	for _, c := range after {
		if !touches(c.Files, file) {
			continue
		}
		if touched == "" {
			touched = c.SHA
		}
		tests := testFiles(c.Files, file)
		if len(tests) == 0 {
			continue
		}
		return c, tests, touched
	}
	return Commit{}, nil, touched
}

func touches(files []string, file string) bool {
	for _, f := range files {
		if f == file {
			return true
		}
	}
	return false
}

// testFiles returns the commit's test files, excluding the reviewed file
// itself: a change to a test file the thread was already about is the fix, not
// independent coverage of it.
func testFiles(files []string, reviewed string) []string {
	var out []string
	for _, f := range files {
		if f == reviewed || !isTestFile(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// isTestFile recognises the test-file conventions of the stacks in this
// portfolio and the common ones next door. It is deliberately name-based: the
// sweep reads a commit's file list, never its contents.
func isTestFile(file string) bool {
	base := path.Base(file)
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.rs"),
		strings.HasSuffix(base, "_test.exs"), strings.HasSuffix(base, "_test.py"),
		strings.HasPrefix(base, "test_"),
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."):
		return true
	}
	for _, seg := range strings.Split(path.Dir(file), "/") {
		if seg == "test" || seg == "tests" || seg == "__tests__" {
			return true
		}
	}
	return false
}

// resolveComment writes the disposition in the exact-commit + evidence shape
// that reads well on the thread: the commit that fixed it, the test that keeps
// it fixed, and the head those facts were read at, so a reader can re-derive
// every one of them.
func resolveComment(th Thread, fix Commit, tests []string, headSHA string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fixed in %s", "`"+fix.SHA+"`")
	if fix.Subject != "" {
		fmt.Fprintf(&b, " — %s", fix.Subject)
	}
	b.WriteString(".\n\nEvidence:\n")
	fmt.Fprintf(&b, "- fix: `%s` changes `%s`\n", fix.SHA, th.Path)
	fmt.Fprintf(&b, "- regression test: %s\n", codeList(tests))
	if headSHA != "" {
		fmt.Fprintf(&b, "- read at head `%s`\n", headSHA)
	}
	b.WriteString("\nResolving as addressed. Re-open if the finding still stands at this head.")
	return b.String()
}

// resolveCommand emits the exact resolve call rather than running it: a
// disposition is prepared here, accepted elsewhere.
func resolveCommand(threadID string) string {
	if threadID == "" {
		return ""
	}
	q := fmt.Sprintf("mutation{resolveReviewThread(input:{threadId:%s}){thread{isResolved}}}", jsonString(threadID))
	return "gh api graphql -f query=" + shellSingleQuote(q)
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func codeList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, "`"+item+"`")
	}
	return strings.Join(quoted, ", ")
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

// reviewThreadsQuery pages a PR's review threads with the anchor and the first
// comment each disposition needs. Distinct from resolvedThreadsQuery, which
// joins resolution onto REST comment ids and needs none of this.
const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!,$cursor:String){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    reviewThreads(first:100,after:$cursor){
      pageInfo{hasNextPage endCursor}
      nodes{id isResolved path line
        comments(first:1){nodes{body author{login} originalCommit{oid}}}}}}}}`

// FetchThreads reads every review thread on a pull request. Threads are the
// GraphQL-only surface — resolution, the node id the resolve mutation takes,
// and the anchor commit all live there and nowhere in REST.
func FetchThreads(pr PRRef) ([]Thread, error) {
	owner, name, ok := strings.Cut(pr.Repo, "/")
	if !ok {
		return nil, fmt.Errorf("evidence: bad repo %q", pr.Repo)
	}
	var out []Thread
	cursor := ""
	for {
		args := []string{"api", "graphql",
			"-f", "query=" + reviewThreadsQuery,
			"-f", "owner=" + owner,
			"-f", "name=" + name,
			"-F", fmt.Sprintf("number=%d", pr.Number),
		}
		if cursor != "" {
			args = append(args, "-f", "cursor="+cursor)
		}
		raw, err := gh(args...)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							PageInfo struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
							Nodes []struct {
								ID         string `json:"id"`
								IsResolved bool   `json:"isResolved"`
								Path       string `json:"path"`
								Line       int    `json:"line"`
								Comments   struct {
									Nodes []struct {
										Body   string `json:"body"`
										Author struct {
											Login string `json:"login"`
										} `json:"author"`
										OriginalCommit struct {
											OID string `json:"oid"`
										} `json:"originalCommit"`
									} `json:"nodes"`
								} `json:"comments"`
							} `json:"nodes"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("evidence: parse review threads: %w", err)
		}
		threads := resp.Data.Repository.PullRequest.ReviewThreads
		for _, th := range threads.Nodes {
			t := Thread{ID: th.ID, Path: th.Path, Line: th.Line, Resolved: th.IsResolved}
			if len(th.Comments.Nodes) > 0 {
				first := th.Comments.Nodes[0]
				t.Author, t.Body, t.AnchorSHA = first.Author.Login, first.Body, first.OriginalCommit.OID
			}
			out = append(out, t)
		}
		if !threads.PageInfo.HasNextPage {
			return out, nil
		}
		cursor = threads.PageInfo.EndCursor
	}
}

// FetchCommits reads the pull request's commits in history order, then the
// files each one touched. Files are a per-commit read, so the file fetch is
// skipped for any commit no unresolved thread could be dispositioned by —
// commits at or before the earliest anchor.
func FetchCommits(pr PRRef, threads []Thread) ([]Commit, error) {
	raw, err := pagedCommits(pr)
	if err != nil {
		return nil, err
	}
	from := firstRelevant(raw, threads)
	for i := from; i < len(raw); i++ {
		files, err := fetchCommitFiles(pr, raw[i].SHA)
		if err != nil {
			return nil, err
		}
		raw[i].Files = files
	}
	return raw, nil
}

// firstRelevant returns the index of the earliest commit whose files can matter
// — the one just after the earliest unresolved thread's anchor. Commits before
// it can only precede every thread, so their file lists buy nothing.
func firstRelevant(commits []Commit, threads []Thread) int {
	index := make(map[string]int, len(commits))
	for i, c := range commits {
		index[c.SHA] = i
	}
	earliest := len(commits)
	for _, th := range threads {
		if th.Resolved {
			continue
		}
		i, ok := index[th.AnchorSHA]
		if ok && i < earliest {
			earliest = i
		}
	}
	if earliest == len(commits) {
		return len(commits)
	}
	return earliest + 1
}

const commitsPerPage = 100

func pagedCommits(pr PRRef) ([]Commit, error) {
	var out []Commit
	for page := 1; ; page++ {
		raw, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d/commits?per_page=%d&page=%d",
			pr.Repo, pr.Number, commitsPerPage, page))
		if err != nil {
			return nil, err
		}
		var body []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("evidence: parse pull commits: %w", err)
		}
		for _, c := range body {
			out = append(out, Commit{SHA: c.SHA, Subject: subjectOf(c.Commit.Message)})
		}
		if len(body) < commitsPerPage {
			return out, nil
		}
	}
}

func fetchCommitFiles(pr PRRef, sha string) ([]string, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/commits/%s", pr.Repo, sha))
	if err != nil {
		return nil, err
	}
	var body struct {
		Files []struct {
			Filename string `json:"filename"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("evidence: parse commit files: %w", err)
	}
	out := make([]string, 0, len(body.Files))
	for _, f := range body.Files {
		out = append(out, f.Filename)
	}
	return out, nil
}

func subjectOf(message string) string {
	line, _, _ := strings.Cut(message, "\n")
	return strings.TrimSpace(line)
}

// HeadSHA reads the pull request's current head — the head every prepared
// disposition is stamped with, so a reader knows which state the facts were
// read against.
func HeadSHA(pr PRRef) (string, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d", pr.Repo, pr.Number))
	if err != nil {
		return "", err
	}
	_, head, err := parsePullHeads(raw)
	return head, err
}

// SweepThreads reads a pull request's threads and commits and prepares a
// disposition for each unresolved one. The whole path is read-only.
func SweepThreads(pr PRRef, headSHA string) ([]Disposition, error) {
	threads, err := FetchThreads(pr)
	if err != nil {
		return nil, err
	}
	commits, err := FetchCommits(pr, threads)
	if err != nil {
		return nil, err
	}
	return Dispositions(threads, commits, headSHA), nil
}
