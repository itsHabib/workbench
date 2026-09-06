package verbs

// Remote: the change's own pull request as the ownership record every machine and
// a cloud agent can read. Two worktrees on two machines share nothing but origin,
// so whatever must be seen from both has to live on GitHub or be derivable from
// it. The declared part of each row (relationship, accountable, dispatcher, due,
// slot, machine) is written as ONE sticky comment per change, marked so both
// watchers can find and parse it; a rendered table sits above the JSON for a
// person. Observed state (hands, done) is never written there: it is derived
// where the evidence is.
//
// Best effort, fail-open, and said so: a dispatch whose remote write fails is
// still a dispatch, and prints `record: local only (<why>)`. `fleet sync` is the
// read side: the open changes of every repo bound here, with their rows, into a
// cache whose age every reader states. FLEET_GITHUB=off disables both.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

const ownershipMarker = "<!-- fleet:ownership v1 -->"

// receiptMarker marks a receipt posted to a change: one comment per receipt, never
// edited, so the change's page is the ledger another machine's `done` reads.
const receiptMarker = "<!-- fleet:receipt v1 -->"

var pullPathRe = regexp.MustCompile(`github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)`)

func githubOff() bool { return os.Getenv("FLEET_GITHUB") == "off" }

func hostName() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "unknown-host"
	}
	return strings.ToLower(strings.TrimSuffix(h, ".local"))
}

// ghRun is gh with stdin, 30s cap, returning stdout and a one-line reason on failure.
func ghRun(stdin string, args ...string) (string, string) {
	cmd := exec.Command("gh", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return "", "gh unavailable (OSError)"
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
			last := lines[len(lines)-1]
			if last == "" {
				last = "gh failed"
			}
			return "", last
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return "", "gh unavailable (TimeoutExpired)"
	}
	return stdout.String(), ""
}

// changeRemote is (owner/name, number) for a branch: the local pull cache first,
// then gh in the checkout.
func changeRemote(rid, branch string) (string, int, string) {
	for _, r := range pullRecords(0, false, rid) {
		if fleet.S(r, "branch") == branch && fleet.S(r, "github") != "" && fleet.F(r, "number") > 0 {
			return fleet.S(r, "github"), int(fleet.F(r, "number")), ""
		}
	}
	data, why := ghJSON("gh", "pr", "view", branch, "--json", "number,url")
	dm, _ := data.(map[string]any)
	if dm == nil {
		if why == "" {
			why = "gh returned nothing"
		}
		return "", 0, why
	}
	m := pullPathRe.FindStringSubmatch(fleet.S(dm, "url"))
	if m == nil || fleet.F(dm, "number") == 0 {
		return "", 0, "gh gave no pull request url"
	}
	return m[1] + "/" + m[2], int(fleet.F(dm, "number")), ""
}

// declaredRows is the declared part of a change's local rows, stamped with this
// machine, in the shape the comment carries.
func declaredRows(rid, branch string) []map[string]any {
	host := hostName()
	var rows []map[string]any
	for _, d := range dispatchRows() {
		if fleet.S(d, "repo") != rid || fleet.S(d, "change") != branch {
			continue
		}
		rows = append(rows, map[string]any{"relationship": d["relationship"], "for": d["for"], "by": d["by"], "at": d["at"],
			"due": d["due"], "slot": d["slot"], "brief": d["brief"], "reply_to": d["reply_to"], "machine": host})
	}
	return rows
}

// parseOwnership is the rows in a comment body, or nil when it is not ours.
func parseOwnership(body string) []map[string]any {
	if !strings.Contains(body, ownershipMarker) {
		return nil
	}
	i := strings.Index(body, "```json")
	if i < 0 {
		return []map[string]any{}
	}
	rest := body[i+len("```json"):]
	j := strings.Index(rest, "```")
	if j < 0 {
		return []map[string]any{}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:j])), &doc); err != nil {
		return []map[string]any{}
	}
	var rows []map[string]any
	for _, x := range fleet.L(doc, "rows") {
		if r, ok := x.(map[string]any); ok {
			rows = append(rows, r)
		}
	}
	return rows
}

func renderOwnership(branch string, rows []map[string]any) string {
	var b strings.Builder
	b.WriteString(ownershipMarker + "\n")
	fmt.Fprintf(&b, "**fleet ownership · `%s`** — declared rows; hands and done are observed where the evidence is\n\n", branch)
	if len(rows) == 0 {
		b.WriteString("_no rows: every relationship on this change has been retired_\n")
	} else {
		b.WriteString("| relationship | accountable | dispatched by | due | slot | machine |\n|---|---|---|---|---|---|\n")
		for _, r := range rows {
			due := "—"
			if d := fleet.F(r, "due"); d > 0 {
				due = time.Unix(int64(d), 0).UTC().Format("2006-01-02 15:04Z")
			}
			slot := fleet.S(r, "slot")
			if slot == "" {
				slot = "—"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", fleet.S(r, "relationship"), fleet.S(r, "for"), fleet.S(r, "by"), due, slot, fleet.S(r, "machine"))
		}
	}
	doc := map[string]any{"v": 1, "change": branch, "rows": rows, "written_at": fleet.Now(), "written_by": hostName()}
	fmt.Fprintf(&b, "\n```json\n%s\n```\n", string(fleet.DumpJSON(doc)))
	return b.String()
}

// upsertOwnership writes the change's rows to its pull request. This machine's
// rows replace this machine's earlier rows; another machine's rows are kept as
// they were. Returns the one-line note the verb prints.
func upsertOwnership(rid, branch string) string {
	if githubOff() {
		return "record: local only (FLEET_GITHUB=off)"
	}
	slug, n, why := changeRemote(rid, branch)
	if why != "" {
		return "record: local only (" + why + ")"
	}
	host := hostName()
	path := fmt.Sprintf("repos/%s/issues/%d/comments", slug, n)
	data, why := ghJSON("gh", "api", path+"?per_page=100")
	if why != "" {
		return fmt.Sprintf("record: local only (%s: %s)", slug, why)
	}
	var existingID float64
	var theirs []map[string]any
	if list, ok := data.([]any); ok {
		for _, x := range list {
			c, _ := x.(map[string]any)
			rows := parseOwnership(fleet.S(c, "body"))
			if rows == nil {
				continue
			}
			existingID = fleet.F(c, "id")
			for _, r := range rows {
				if fleet.S(r, "machine") != host {
					theirs = append(theirs, r)
				}
			}
			break
		}
	}
	rows := append(declaredRows(rid, branch), theirs...)
	sort.SliceStable(rows, func(i, j int) bool { return fleet.S(rows[i], "relationship") < fleet.S(rows[j], "relationship") })
	body, _ := json.Marshal(map[string]any{"body": renderOwnership(branch, rows)})
	if existingID > 0 {
		_, why = ghRun(string(body), "api", fmt.Sprintf("repos/%s/issues/comments/%d", slug, int64(existingID)), "--method", "PATCH", "--input", "-")
	} else {
		_, why = ghRun(string(body), "api", path, "--method", "POST", "--input", "-")
	}
	if why != "" {
		return fmt.Sprintf("record: local only (%s#%d: %s)", slug, n, why)
	}
	return fmt.Sprintf("record: %s#%d (%d row(s))", slug, n, len(rows))
}

// postReceipt copies a receipt to the change's pull request as one marked comment.
// Exact head, kind and verdict travel in the JSON; a person reads the line above it.
func postReceipt(rid, branch string, r fleet.Rec) string {
	if githubOff() {
		return "record: local only (FLEET_GITHUB=off)"
	}
	if rid == "" || branch == "" {
		return "record: local only (no branch to find the change by)"
	}
	slug, n, why := changeRemote(rid, branch)
	if why != "" {
		return "record: local only (" + why + ")"
	}
	r["machine"] = hostName()
	obs := fleet.S(r, "observable")
	body := fmt.Sprintf("%s\n**fleet receipt · %s %s** at `%s` by %s on %s — %s\n\n```json\n%s\n```\n",
		receiptMarker, fleet.S(r, "kind"), fleet.S(r, "verdict"), cut(fleet.S(r, "head"), 12), fleet.Short(fleet.S(r, "session")), fleet.S(r, "machine"), obs, string(fleet.DumpJSON(r)))
	payload, _ := json.Marshal(map[string]any{"body": body})
	if _, why := ghRun(string(payload), "api", fmt.Sprintf("repos/%s/issues/%d/comments", slug, n), "--method", "POST", "--input", "-"); why != "" {
		return fmt.Sprintf("record: local only (%s#%d: %s)", slug, n, why)
	}
	return fmt.Sprintf("record: %s#%d", slug, n)
}

// parseReceipt is the receipt in a comment body, or nil when it is not one.
func parseReceipt(body string) map[string]any {
	if !strings.Contains(body, receiptMarker) {
		return nil
	}
	i := strings.Index(body, "```json")
	if i < 0 {
		return nil
	}
	rest := body[i+len("```json"):]
	j := strings.Index(rest, "```")
	if j < 0 {
		return nil
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:j])), &r); err != nil {
		return nil
	}
	if fleet.S(r, "kind") == "" || fleet.S(r, "head") == "" || (fleet.S(r, "verdict") != "pass" && fleet.S(r, "verdict") != "fail") {
		return nil
	}
	return r
}

// ---------- sync: the read side ----------

func cacheFile(slug string) string { return fleet.Path("cache", "github", fleet.Safe(slug)+".json") }

// CmdSync refreshes the cache of open changes and their rows for every GitHub repo
// bound here (or one). One `gh pr list` plus one comment listing per open change,
// capped, so a tick never turns into a crawl.
func CmdSync(repo string) error {
	if githubOff() {
		return refuse("fleet sync: FLEET_GITHUB=off")
	}
	repos := githubReposHere()
	if len(repos) == 0 {
		return refuse("fleet sync: no GitHub repo is known here (run inside a checkout with an origin, or after a `gh pr` call)")
	}
	var slugs []string
	for s := range repos {
		if repo == "" || repo == s || strings.HasSuffix(s, "/"+repo) {
			slugs = append(slugs, s)
		}
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		return refuse("fleet sync: %s is not a repo bound here", repo)
	}
	failed := 0
	for _, slug := range slugs {
		n, why := syncRepo(slug)
		if why != "" {
			say("%s: not refreshed (%s)", slug, why)
			failed++
			continue
		}
		say("%s: %d open change(s) cached", slug, n)
	}
	if failed == len(slugs) {
		return exitCode(1, "")
	}
	return nil
}

// syncRepo caches one repo's open changes and their rows; the count, or why not.
func syncRepo(slug string) (int, string) {
	data, why := ghJSON("gh", "pr", "list", "-R", slug, "--state", "open", "--limit", "50", "--json", "number,headRefName,headRefOid,url,updatedAt")
	list, _ := data.([]any)
	if why != "" || list == nil {
		if why == "" {
			why = "gh returned no list"
		}
		return 0, why
	}
	// A change whose comments could not be read this time keeps what the last
	// sync saw, marked stale: a failed read is not "no rows".
	prior := map[int]map[string]any{}
	for _, px := range fleet.L(fleet.ReadJSON(cacheFile(slug)), "prs") {
		if p, ok := px.(map[string]any); ok {
			prior[int(fleet.F(p, "number"))] = p
		}
	}
	changes := make([]any, 0, len(list))
	for i, x := range list {
		if i >= 30 {
			break
		}
		ch, _ := x.(map[string]any)
		n := int(fleet.F(ch, "number"))
		changes = append(changes, changeEntry(ch, n, prior[n], slug))
	}
	if err := fleet.WriteJSON(cacheFile(slug), map[string]any{"at": fleet.Now(), "repo": slug, "prs": changes}); err != nil {
		return 0, err.Error()
	}
	return len(changes), ""
}

// changeEntry is one cached change: its rows and receipts read now, or the prior
// sync's copy marked stale when the read failed.
func changeEntry(ch map[string]any, n int, prior map[string]any, slug string) map[string]any {
	entry := map[string]any{"number": n, "branch": fleet.S(ch, "headRefName"), "head": fleet.S(ch, "headRefOid"), "url": fleet.S(ch, "url"),
		"updated_at": fleet.S(ch, "updatedAt"), "rows": []any{}, "receipts": []any{}}
	rows, receipts, ok := commentsOn(slug, n)
	if ok {
		entry["rows"], entry["receipts"], entry["comments_read_at"] = rows, receipts, fleet.Now()
		return entry
	}
	entry["comments_stale"] = true
	if prior != nil {
		entry["rows"], entry["receipts"], entry["comments_read_at"] = prior["rows"], prior["receipts"], prior["comments_read_at"]
	}
	return entry
}

// commentsOn is what the fleet wrote on a change: the ownership rows (one sticky
// comment) and every receipt (one comment each), in posting order.
func commentsOn(slug string, n int) ([]any, []any, bool) {
	rows, receipts := []any{}, []any{}
	cdata, why := ghJSON("gh", "api", fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", slug, n))
	comments, ok := cdata.([]any)
	if why != "" || !ok {
		return rows, receipts, false
	}
	seenRows := false
	for _, cx := range comments {
		c, _ := cx.(map[string]any)
		body := fleet.S(c, "body")
		if r := parseReceipt(body); r != nil {
			receipts = append(receipts, r)
			continue
		}
		if seenRows {
			continue
		}
		if rs := parseOwnership(body); rs != nil {
			for _, r := range rs {
				rows = append(rows, r)
			}
			seenRows = true
		}
	}
	return rows, receipts, true
}

// remoteRows is the rows another machine declared, from the cache: state `remote`
// (or `late` past due), with the cache's age on each. Rows this machine wrote are
// local truth and skipped; a row a local record already declares is skipped too.
func remoteRows(declared map[string]bool, now float64) []WorkRow {
	host := hostName()
	rids := githubReposHere()
	ents, _ := os.ReadDir(fleet.Path("cache", "github"))
	var out []WorkRow
	for _, e := range ents {
		c := fleet.ReadJSON(filepath.Join(fleet.Path("cache", "github"), e.Name()))
		if c == nil {
			continue
		}
		for _, px := range fleet.L(c, "prs") {
			ch, _ := px.(map[string]any)
			out = append(out, remoteRowsOf(ch, c, declared, rids[fleet.S(c, "repo")], host, now)...)
		}
	}
	return out
}

// remoteRowsOf is one cached change's rows from other machines, skipping this
// machine's own and any a local record already declares.
func remoteRowsOf(ch, cache fleet.Rec, declared map[string]bool, rids map[string]bool, host string, now float64) []WorkRow {
	branch := fleet.S(ch, "branch")
	var out []WorkRow
	for _, rx := range fleet.L(ch, "rows") {
		r, _ := rx.(map[string]any)
		if r == nil || fleet.S(r, "machine") == host || declaredLocally(declared, rids, branch, fleet.S(r, "relationship")) {
			continue
		}
		state, doneAt := remoteEvidence(ch, fleet.S(r, "relationship"))
		if due := fleet.F(r, "due"); state == "remote" && due > 0 && now > due {
			state = "late"
		}
		out = append(out, WorkRow{"change": branch, "repo": fleet.S(cache, "repo"), "number": ch["number"], "relationship": r["relationship"], "for": r["for"], "by": r["by"],
			"at": r["at"], "due": r["due"], "slot": r["slot"], "brief": r["brief"], "key": nil, "hands": nil, "state": state, "head": ch["head"],
			"done_at": doneAt, "machine": r["machine"], "cache_at": fleet.F(cache, "at")})
	}
	return out
}

// remoteEvidence is what the change's posted receipts say about a relationship at
// the cached head: the latest receipt of that kind AT THAT HEAD decides — done on
// pass, failed on fail; a receipt for an older head is not evidence about this one.
func remoteEvidence(ch fleet.Rec, rel string) (string, any) {
	head := fleet.S(ch, "head")
	state, doneAt := "remote", any(nil)
	var latest float64
	for _, rx := range fleet.L(ch, "receipts") {
		r, _ := rx.(map[string]any)
		if fleet.S(r, "kind") != rel || fleet.S(r, "head") != head || fleet.F(r, "at") < latest {
			continue
		}
		latest = fleet.F(r, "at")
		if fleet.S(r, "verdict") == "pass" {
			state, doneAt = "done", r["at"]
			continue
		}
		state, doneAt = "failed", nil
	}
	return state, doneAt
}

func declaredLocally(declared map[string]bool, rids map[string]bool, branch, rel string) bool {
	for rid := range rids {
		if declared[rid+"|"+branch+"|"+rel] {
			return true
		}
	}
	return false
}

// cacheAges is the age of every GitHub cache file, oldest first, for a scope line.
func cacheAges(now float64) string {
	ents, _ := os.ReadDir(fleet.Path("cache", "github"))
	var parts []string
	for _, e := range ents {
		c := fleet.ReadJSON(filepath.Join(fleet.Path("cache", "github"), e.Name()))
		if c == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s old", fleet.S(c, "repo"), fleet.FmtAge(now-fleet.F(c, "at"))))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
