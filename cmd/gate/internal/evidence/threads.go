package evidence

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Unresolved review threads outlive the findings they report: a later commit
// fixes the code, the reviewer never comes back, and the thread sits there
// looking actionable until a human reads it. Finding those threads and the
// commits that plausibly bear on them is mechanical, and that is what this
// does.
//
// Deciding whether a thread is FIXED is not mechanical, and this no longer
// tries. The first version did — it reported a thread as fixed and prepared a
// resolve comment plus the exact resolve call. Review found seven distinct
// ways for that claim to be wrong while looking right: a test deleted in the
// fixing commit, an unrelated test in it, a test added and later removed, a
// truncated commit history, a rename, a reviewer's rebuttal posted after the
// candidate fix, and a test whose path only loosely matched. Each was fixed
// and the next appeared, because all of them are invisible from the output —
// a wrong disposition reads exactly like a right one.
//
// So the claim is gone and the observation remains: which commits after the
// thread's anchor touch its file, and which of those also changed a test
// naming that file. No verdict, no prepared comment, no resolve call. A human
// reads the thread and decides, which is the only place that judgement was
// ever sound.

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

// File is one file a commit touched, with the one status distinction the
// disposition turns on: a deleted file changed the tree but covers nothing.
type File struct {
	Path    string `json:"path"`
	Removed bool   `json:"removed,omitempty"`
}

// Commit is one commit of a pull request with the files it touched.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject,omitempty"`
	Files   []File `json:"files,omitempty"`
	// Parents is how many parents the commit has. A merge commit has two or
	// more, and the commits API reports its file list as the diff against the
	// FIRST parent — so a merge of main into the branch lists main's files as if
	// the merge introduced them, mixed with any conflict resolution the merge
	// itself made, and the file list cannot tell those apart. See candidates.
	Parents int `json:"parents,omitempty"`
}

// Disposition is what the sweep OBSERVED about one unresolved thread. It is
// deliberately not a verdict.
//
// The first shape of this reported a thread as fixed and prepared a resolve
// comment plus the exact resolve call. Review found seven distinct ways for
// that claim to be wrong while looking right — a deleted test counted as
// coverage, an unrelated test counted as coverage, a test added then removed
// later, a truncated commit history, a rename, a reviewer's rebuttal posted
// after the candidate fix, and a test whose path only loosely matched. Each
// was fixed and the next one appeared, because every one of them is invisible
// from the disposition itself: the output looks equally confident either way.
//
// So the claim is gone. What survives is the mechanical part that was never in
// doubt — which commits after the thread's anchor touch its file, and which of
// those also changed a test naming that file. A human reads the thread and
// decides. Nothing here says "fixed", nothing prepares a comment, and nothing
// emits a resolve call, so there is no false-fixed disposition to produce.
type Disposition struct {
	Thread Thread `json:"thread"`
	// Candidates are the commits after the thread's anchor that touch the
	// reviewed file, oldest first — leads to read, not evidence of a fix.
	Candidates []Candidate `json:"candidates,omitempty"`
	// Note states what could and could not be established mechanically. It
	// never concludes that a finding was addressed.
	Note string `json:"note"`
}

// Candidate is one commit that touched the reviewed file after the thread,
// with any test change in it that names that file as its subject.
type Candidate struct {
	SHA     string   `json:"sha"`
	Subject string   `json:"subject,omitempty"`
	Tests   []string `json:"tests,omitempty"`
	// Deleted marks a commit whose relationship to the reviewed file is that it
	// REMOVED it. Still surfaced — deleting the file may well be how a thread was
	// addressed — but marked, so it is not mistaken for a change that carries a
	// fix and a covering test (it can carry neither).
	Deleted bool `json:"deleted,omitempty"`
	// Merge marks a merge commit. Its file list is the diff against its first
	// parent, which mixes what arrived on the merged branch with any conflict
	// resolution the merge itself made — and the list cannot tell those apart.
	// Still surfaced — a conflict resolution in the reviewed file is a real
	// change to it — but marked, and carrying no test or deletion claim, since
	// either could be base noise.
	Merge bool `json:"merge,omitempty"`
}

// Dispositions prepares a disposition for every unresolved thread, oldest
// first. commits must be the pull request's commits in history order with their
// touched files populated.
func Dispositions(threads []Thread, commits []Commit) []Disposition {
	index := make(map[string]int, len(commits))
	for i, c := range commits {
		index[c.SHA] = i
	}
	var out []Disposition
	for _, th := range threads {
		if th.Resolved {
			continue
		}
		out = append(out, disposition(th, commits, index))
	}
	return out
}

func disposition(th Thread, commits []Commit, index map[string]int) Disposition {
	d := Disposition{Thread: th}
	if th.Path == "" {
		d.Note = "no file anchor — nothing to match commits against"
		return d
	}
	if th.AnchorSHA == "" {
		d.Note = "no anchor commit — its first comment was not posted against a commit, so nothing can be ordered after it"
		return d
	}
	anchor, ok := index[th.AnchorSHA]
	if !ok {
		d.Note = fmt.Sprintf("anchor commit %s is not in this pull request's history — later commits cannot be ordered against it", shortSHA(th.AnchorSHA))
		return d
	}
	d.Candidates = candidates(commits[anchor+1:], th.Path)
	if len(d.Candidates) == 0 {
		d.Note = fmt.Sprintf("no commit after %s touches %s", shortSHA(th.AnchorSHA), th.Path)
		return d
	}
	withTests := 0
	for _, c := range d.Candidates {
		if len(c.Tests) > 0 {
			withTests++
		}
	}
	// Deliberately reports counts, not a conclusion. Whether any of these
	// actually addressed the finding is a question about the thread's content,
	// which this never reads.
	d.Note = fmt.Sprintf("%d commit(s) after %s touch %s, %d carrying a test that names it — read the thread to decide",
		len(d.Candidates), shortSHA(th.AnchorSHA), th.Path, withTests)
	return d
}

// candidates lists every commit after the anchor that touches the reviewed
// file, with any test change in it naming that file. Every one is reported:
// picking a single "fixing" commit is exactly the judgement this no longer
// makes, and a later commit can undo an earlier one.
func candidates(after []Commit, file string) []Candidate {
	var out []Candidate
	for _, c := range after {
		if !touches(c.Files, file) {
			continue
		}
		// A merge commit's file list is the diff against its first parent, which
		// mixes changes that arrived on the merged branch with any conflict
		// resolution the merge itself made in this file. Crediting it whole would
		// "fix" a thread with whatever arrived on main; dropping it whole would
		// let the note claim no later commit touches the file when a conflict
		// resolution did. So it surfaces marked, with no test or deletion claim —
		// either could be base noise.
		if c.Parents > 1 {
			out = append(out, Candidate{SHA: c.SHA, Subject: c.Subject, Merge: true})
			continue
		}
		out = append(out, Candidate{
			SHA: c.SHA, Subject: c.Subject,
			Tests:   coveringTests(c.Files, file),
			Deleted: deletes(c.Files, file),
		})
	}
	return out
}

func touches(files []File, file string) bool {
	for _, f := range files {
		if f.Path == file {
			return true
		}
	}
	return false
}

// deletes reports whether the commit removed the reviewed file. A pure deletion
// still touches the file, but it is not a change that could carry a fix, so the
// candidate is marked rather than read as one.
func deletes(files []File, file string) bool {
	for _, f := range files {
		if f.Path == file {
			return f.Removed
		}
	}
	return false
}

// coveringTests returns the commit's test files that are evidence about the
// REVIEWED file — its named counterparts, by the naming convention that ties a
// test to its subject. Any-test-in-the-commit would be no evidence at all: a
// commit changing a.go beside an unrelated other_test.go would produce a comment
// claiming other_test.go keeps the finding fixed, and invite a human to resolve
// a live thread on it.
//
// Three exclusions, each a way the pairing is not evidence: the reviewed file
// itself (a test cannot be its own independent coverage), a DELETED test
// (coverage removed, never added), and a test whose name ties it to some other
// subject. A same-package test with an unrelated name may well cover the fix,
// but nothing in the file list says so — and an unprovable connection leaves the
// thread actionable, which is always the safe answer.
func coveringTests(files []File, reviewed string) []string {
	var out []string
	for _, f := range files {
		if f.Removed || f.Path == reviewed || !isTestFile(f.Path) {
			continue
		}
		if !covers(f.Path, reviewed) {
			continue
		}
		out = append(out, f.Path)
	}
	return out
}

// covers reports whether a test file's name ties it to the reviewed file:
// panel.go/panel_test.go, foo.ts/foo.test.ts, src/foo.py/tests/test_foo.py. The
// subject stem is the link, not the directory — a mirrored test tree names its
// subject exactly as a sibling test does.
func covers(test, reviewed string) bool {
	if subjectStem(test) != subjectStem(reviewed) {
		return false
	}
	// Same stem is necessary but not sufficient. main_test.go names its subject
	// "main", and so does every other main.go in the tree — cmd/console/main_-
	// test.go would otherwise be credited as coverage for cmd/gate/main.go. A
	// naming convention ties a test to its subject in one of two shapes: the test
	// sits beside the file (Go's a_test.go, Rust's parse_test.rs), or it lives in
	// a mirror test directory (Python's tests/, JS's __tests__/). Require one of
	// them; a same-stem test in an unrelated package satisfies neither.
	return path.Dir(test) == path.Dir(reviewed) || inTestDir(test)
}

// inTestDir reports whether any path segment marks a test directory — the mirror
// convention's signal. A same-directory _test.go is NOT in one, which is what
// separates a legitimate mirror (tests/test_foo.py) from a cross-package stem
// collision (cmd/console/main_test.go).
func inTestDir(file string) bool {
	for _, seg := range strings.Split(path.Dir(file), "/") {
		switch seg {
		case "test", "tests", "__tests__":
			return true
		}
	}
	return false
}

// subjectStem reduces a file name to the subject it is about: the base name
// without its extension and without the affixes that mark a test.
func subjectStem(file string) string {
	base := path.Base(file)
	base = strings.TrimSuffix(base, path.Ext(base))
	base = strings.TrimPrefix(base, "test_")
	for _, affix := range []string{"_test", ".test", ".spec", "_spec", ".steps"} {
		base = strings.TrimSuffix(base, affix)
	}
	return base
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

type threadsPage struct {
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
}

// parseThreadsPage decodes one GraphQL page, refusing a response that carries
// errors. GraphQL answers a bad repo, an unknown PR, or a lost permission with
// HTTP 200 and a top-level errors array, which decodes cleanly into the
// zero-value struct — a truncated or empty thread set that would read as "no
// threads to disposition". A partial answer is not evidence.
func parseThreadsPage(raw []byte) (threadsPage, error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads threadsPage `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return threadsPage{}, fmt.Errorf("evidence: parse review threads: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return threadsPage{}, fmt.Errorf("evidence: review threads query: %s", strings.Join(msgs, "; "))
	}
	return resp.Data.Repository.PullRequest.ReviewThreads, nil
}

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
		threads, err := parseThreadsPage(raw)
		if err != nil {
			return nil, err
		}
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

const (
	commitsPerPage = 100
	filesPerPage   = 100
)

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
			Parents []struct {
				SHA string `json:"sha"`
			} `json:"parents"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("evidence: parse pull commits: %w", err)
		}
		for _, c := range body {
			out = append(out, Commit{
				SHA: c.SHA, Subject: subjectOf(c.Commit.Message), Parents: len(c.Parents),
			})
		}
		if len(body) < commitsPerPage {
			return out, nil
		}
	}
}

// fetchCommitFiles reads every file a commit touched. The commit endpoint pages
// its file list, so it is walked to exhaustion the same way the comment
// endpoints are: one page deep would silently hide the reviewed path or its
// test on a large commit, and a hidden test reads as missing coverage.
func fetchCommitFiles(pr PRRef, sha string) ([]File, error) {
	var out []File
	for page := 1; ; page++ {
		raw, err := gh("api", fmt.Sprintf("repos/%s/commits/%s?per_page=%d&page=%d",
			pr.Repo, sha, filesPerPage, page))
		if err != nil {
			return nil, err
		}
		files, n, err := parseCommitFiles(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, files...)
		if n < filesPerPage {
			return out, nil
		}
	}
}

// parseCommitFiles decodes one page of a commit's file list. It returns the
// decoded files and the raw page count — a rename decodes to two entries, so
// pagination must count what GitHub sent, not what came out.
//
// A rename reports only the NEW name as the file's path, with the old one in
// previous_filename. A thread anchored to the old path was still touched by
// this commit — the path it reviewed is gone from the tree — so the old name is
// recorded as removed: it shows up as a candidate, and a renamed-away test
// stops counting as standing coverage.
func parseCommitFiles(raw json.RawMessage) ([]File, int, error) {
	var body struct {
		Files []struct {
			Filename         string `json:"filename"`
			Status           string `json:"status"`
			PreviousFilename string `json:"previous_filename"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, 0, fmt.Errorf("evidence: parse commit files: %w", err)
	}
	var out []File
	for _, f := range body.Files {
		out = append(out, File{Path: f.Filename, Removed: f.Status == "removed"})
		if f.Status == "renamed" && f.PreviousFilename != "" {
			out = append(out, File{Path: f.PreviousFilename, Removed: true})
		}
	}
	return out, len(body.Files), nil
}

func subjectOf(message string) string {
	line, _, _ := strings.Cut(message, "\n")
	return strings.TrimSpace(line)
}

// truncated reports whether the paged history fails to reach the head the
// sweep was read against. The commits endpoint stops at GitHub's 250-commit
// ceiling, and the short final page is indistinguishable from normal
// exhaustion. Every note the sweep writes is a completeness claim ("no commit
// after X touches Y"), so a history that does not end at the head is refused,
// never trimmed to.
func truncated(commits []Commit, head string) bool {
	return len(commits) == 0 || commits[len(commits)-1].SHA != head
}

// lastSHA names where a commit list actually ends, for the truncation refusal.
func lastSHA(commits []Commit) string {
	if len(commits) == 0 {
		return "(none)"
	}
	return commits[len(commits)-1].SHA
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
// disposition for each unresolved one, stamped with the head it read. The whole
// path is read-only.
//
// The head is read on both sides of the sweep and the run is abandoned if it
// moved: a push mid-sweep would let a comment cite a fix from history the
// stamped head does not contain, which is a claim no reader could re-derive.
// The caller re-runs against the new head.
func SweepThreads(pr PRRef) ([]Disposition, string, error) {
	head, err := HeadSHA(pr)
	if err != nil {
		return nil, "", err
	}
	threads, err := FetchThreads(pr)
	if err != nil {
		return nil, "", err
	}
	commits, err := FetchCommits(pr, threads)
	if err != nil {
		return nil, "", err
	}
	if truncated(commits, head) {
		return nil, "", fmt.Errorf("evidence: commit history is truncated — %d commit(s) end at %s, not the head %s; the sweep cannot claim completeness",
			len(commits), shortSHA(lastSHA(commits)), shortSHA(head))
	}
	after, err := HeadSHA(pr)
	if err != nil {
		return nil, "", err
	}
	if after != head {
		return nil, "", fmt.Errorf("evidence: head moved during the sweep (%s → %s) — re-run against the new head",
			shortSHA(head), shortSHA(after))
	}
	return Dispositions(threads, commits), head, nil
}
