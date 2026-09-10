package verbs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

var (
	shaRe  = regexp.MustCompile(`\A[0-9a-f]{7,40}\z`)
	kindRe = regexp.MustCompile(`\A[a-z][a-z0-9-]*\z`)
	urlRe  = regexp.MustCompile(`\Ahttps?://\S+\z`)
)

func cmdReady(sha, action, observable string) error {
	var missing []string
	for _, kv := range [][2]string{{"sha", sha}, {"action", action}, {"observable", observable}} {
		if strings.TrimSpace(kv[1]) == "" {
			missing = append(missing, kv[0])
		}
	}
	if len(missing) > 0 || !shaRe.MatchString(sha) {
		why := strings.Join(missing, ", ")
		if why == "" {
			why = "bad sha"
		}
		return refuse("fleet ready: a packet needs the exact commit, the action to take, and the observable that proves or refutes the claim — %s", why)
	}
	say("READY TO RUN  commit %s\n  do:      %s\n  observe: %s\n  then:    fleet receipt %s <kind> pass|fail \"<what you saw>\"", sha, action, observable, sha)
	return nil
}

// cmdReceipt records evidence bound to its provenance: the emitting session's lane
// must produce this kind; the cwd must be inside that session's roled worktree; the
// tree must be at <sha> and clean. `card` is an optional URL to the human-readable
// evidence; the receipt is the fact a reader polls, the card is what a person opens.
func cmdReceipt(sha, kind, verdict, observable, session, card string, hasCard bool) error {
	if err := receiptArgs(sha, kind, verdict, observable, card, hasCard); err != nil {
		return err
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	rec := fleet.SessionRecord(sid)
	if err := receiptLane(rec, sid, kind); err != nil {
		return err
	}
	root, here, err := receiptTree(rec)
	if err != nil {
		return err
	}
	head, err := receiptHead(sha)
	if err != nil {
		return err
	}
	var cardV any
	if hasCard {
		cardV = card
	}
	err = recordReceipt(sha, kind, fleet.Rec{
		"sha": sha, "head": head, "kind": kind, "verdict": verdict, "observable": observable,
		"session": sid, "role": nilIfEmpty(fleet.S(rec, "role")), "slot": nilIfEmpty(fleet.S(rec, "slot")), "repo": nilIfEmpty(fleet.RepoID(here)),
		"worktree": nilIfEmpty(root), "dirty": false, "card": cardV, "at": fleet.Now()})
	if err != nil {
		return err
	}
	tail := ""
	if s := fleet.S(rec, "slot"); s != "" {
		tail += " in slot " + s
	}
	if hasCard {
		tail += " — card " + card
	}
	// The receipt is local evidence; its copy on the change's pull request is what
	// another machine's `done` reads. Best effort, and the note says which happened.
	note := postReceipt(fleet.RepoID(here), fleet.S(rec, "branch"), fleet.Rec{"kind": kind, "verdict": verdict, "sha": sha, "head": head,
		"observable": observable, "session": sid, "role": nilIfEmpty(fleet.S(rec, "role")), "card": cardV, "at": fleet.Now()})
	say("receipt: %s %s @ %s by %s %s%s; %s", kind, verdict, sha, roleOr(rec, "session"), fleet.Short(sid), tail, note)
	return nil
}

// receiptPaths are the two files a verdict at one head lives in: the latest file every
// reader already knows, and the history beside it.
func receiptPaths(sha, kind string) (latest, history string) {
	return fleet.Path("receipts", sha+"."+kind+".json"), fleet.Path("receipts", sha+"."+kind+".jsonl")
}

// recordReceipt publishes the latest verdict and appends it to the per-head history.
// A second verdict of one kind at one head used to overwrite the first, so a failure
// that a later pass replaced survived only in whatever a human had copied elsewhere.
// The latest file keeps its exact shape and name — a reader that knows only it reads
// what it always did — and a store written before this existed is seeded with its own
// latest record first, so the history never starts by claiming the earlier verdict
// never happened.
func recordReceipt(sha, kind string, rec fleet.Rec) error {
	latest, history := receiptPaths(sha, kind)
	if _, err := os.Stat(history); err != nil {
		if prev := fleet.ReadJSON(latest); prev != nil {
			_ = fleet.AppendJSONL(history, prev)
		}
	}
	if err := fleet.WriteJSON(latest, rec); err != nil {
		return err
	}
	return fleet.AppendJSONL(history, rec)
}

// receiptHistory is every verdict recorded for a revision and kind, oldest first.
// Absent history is no history, never an error: the store may predate it.
func receiptHistory(sha, kind string) []fleet.Rec {
	_, history := receiptPaths(sha, kind)
	b, err := os.ReadFile(history)
	if err != nil {
		return nil
	}
	var rows []fleet.Rec
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r fleet.Rec
		if strings.TrimSpace(line) == "" || json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		rows = append(rows, r)
	}
	return rows
}

// supersededBy is the history of a latest record minus that record itself — the
// verdicts it replaced. Identity is the timestamp and the session that wrote it, not
// the line's position: an older binary can publish a latest file without appending.
func supersededBy(latest fleet.Rec) []fleet.Rec {
	sha, kind := fleet.S(latest, "sha"), fleet.S(latest, "kind")
	if sha == "" || kind == "" {
		return nil
	}
	var out []fleet.Rec
	for _, r := range receiptHistory(sha, kind) {
		if fleet.F(r, "at") == fleet.F(latest, "at") && fleet.S(r, "session") == fleet.S(latest, "session") {
			continue
		}
		out = append(out, r)
	}
	return out
}

// receiptArgs is the packet's shape: a verdict, a sha, a kind, a non-empty
// observable, and a card that is a URL when given.
func receiptArgs(sha, kind, verdict, observable, card string, hasCard bool) error {
	if (verdict != "pass" && verdict != "fail") || !shaRe.MatchString(sha) || !kindRe.MatchString(kind) {
		return refuse(`usage: fleet receipt <sha> <kind> pass|fail "<observable>" [--card <url>]`)
	}
	if strings.TrimSpace(observable) == "" {
		return refuse("fleet receipt: the observable is what would have read differently had the claim been false; it cannot be empty")
	}
	if hasCard && !urlRe.MatchString(card) {
		return refuse("fleet receipt: --card must be a URL, got %s", fleet.PyRepr(card))
	}
	return nil
}

// receiptLane: only a lane whose manifest produces this kind may record it.
func receiptLane(rec fleet.Rec, sid, kind string) error {
	lane := fleet.M(rec, "lane")
	if lane == nil {
		return refuse("fleet receipt: session %s has no lane (role %s, no manifest); a receipt needs a lane that produces %s", fleet.Short(sid), roleOr(rec, "none"), fleet.PyRepr(kind))
	}
	if fleet.S(lane, "produces") != kind {
		return refuse("fleet receipt: lane %s produces %s, not %s; only a lane whose manifest produces %s may record it", fleet.S(lane, "kind"), pyReprOrNone(lane["produces"]), fleet.PyRepr(kind), fleet.PyRepr(kind))
	}
	return nil
}

// receiptTree: the receipt is recorded from inside the session's roled worktree,
// because its HEAD and cleanliness are read from the tree it names.
func receiptTree(rec fleet.Rec) (root, here string, err error) {
	root = fleet.RoledRoot(fleet.S(rec, "cwd"))
	here = canon(cwd())
	if root != "" && fleet.Within(here, root) {
		return root, here, nil
	}
	where := root
	if where == "" {
		where = fleet.S(rec, "cwd")
	}
	return "", "", refuse("fleet receipt: this must run inside the session's roled worktree %s, not %s; the receipt's HEAD and cleanliness are read from the tree it names", where, here)
}

// receiptHead is the tree's HEAD, which must be the revision the packet names, from a
// clean tree.
func receiptHead(sha string) (string, error) {
	head, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	head = strings.TrimSpace(head)
	if !strings.HasPrefix(head, sha) {
		return "", refuse("fleet receipt: the packet names %s but this tree is at %s; a receipt names the revision that ran, nothing else", sha, cut(head, 12))
	}
	dirty, err := gitOut("status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dirty) != "" {
		return "", refuse("fleet receipt: the tree is not clean (%d path(s) per git status --porcelain); a dirty tree is not revision %s", len(strings.Split(strings.TrimSpace(dirty), "\n")), sha)
	}
	return head, nil
}

func pyReprOrNone(v any) string {
	if s, ok := v.(string); ok {
		return fleet.PyRepr(s)
	}
	return "None"
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// receiptRows is every receipt, newest first, filtered by revision prefix, kind and
// age. Pure read.
func receiptRows(sha, kind string, since float64, hasSince bool) []fleet.Rec {
	d := fleet.Path("receipts")
	ents, _ := os.ReadDir(d)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var rows []fleet.Rec
	for _, n := range names {
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		p := filepath.Join(d, n)
		r := fleet.ReadJSON(p)
		v := fleet.S(r, "verdict")
		if r == nil || fleet.S(r, "head") == "" || fleet.S(r, "kind") == "" || (v != "pass" && v != "fail") {
			rows = append(rows, fleet.Rec{"malformed": p})
			continue
		}
		// The requested revision is a prefix of the receipt's FULL head, or it is not
		// this receipt. The stored short sha never widens the match: two heads sharing
		// seven characters are two heads.
		if sha != "" && !strings.HasPrefix(fleet.S(r, "head"), sha) {
			continue
		}
		if kind != "" && fleet.S(r, "kind") != kind {
			continue
		}
		if hasSince && fleet.Now()-fleet.F(r, "at") > since {
			continue
		}
		rows = append(rows, r)
	}
	sortBy(rows, func(a, b fleet.Rec) bool { return fleet.F(a, "at") > fleet.F(b, "at") })
	return rows
}

func cmdReceipts(sha, kind string, since float64, hasSince, asJSON, all bool) error {
	rows := receiptRows(sha, kind, since, hasSince)
	if asJSON {
		if all {
			say("%s", jsonIndent(withHistory(rows)))
			return nil
		}
		say("%s", jsonIndent(rows))
		return nil
	}
	if len(rows) == 0 {
		msg := "no receipts"
		if sha != "" {
			msg += " for " + sha
		}
		if kind != "" {
			msg += " of kind " + kind
		}
		say("%s", msg)
		return nil
	}
	for _, r := range rows {
		if m := fleet.S(r, "malformed"); m != "" {
			say("MALFORMED  %s", m)
			continue
		}
		say("%s", receiptLine(r))
		sayHistory(r, all)
	}
	return nil
}

// receiptLine is one verdict as a reader sees it.
func receiptLine(r fleet.Rec) string {
	s := fleet.S(r, "session")
	if s == "" {
		s = "?"
	}
	tail := ""
	if sl := fleet.S(r, "slot"); sl != "" {
		tail += "  slot " + sl
	}
	tail += "  — " + fleet.S(r, "observable")
	if c := fleet.S(r, "card"); c != "" {
		tail += "  [card " + c + "]"
	}
	return fmt.Sprintf("%s  %-8s %-4s  %6s ago  %s %s%s", cut(fleet.S(r, "head"), 10), fleet.S(r, "kind"), fleet.S(r, "verdict"), ago(fleet.F(r, "at")), roleOr(r, "session"), fleet.Short(s), tail)
}

// sayHistory prints the verdicts this one replaced, newest first, indented under it.
func sayHistory(latest fleet.Rec, all bool) {
	if !all {
		return
	}
	prior := supersededBy(latest)
	for i := len(prior) - 1; i >= 0; i-- {
		say("  superseded  %s", receiptLine(prior[i]))
	}
}

// withHistory is each latest record carrying the verdicts it replaced, oldest first,
// under `superseded`. The record itself keeps every field it had.
func withHistory(rows []fleet.Rec) []fleet.Rec {
	out := make([]fleet.Rec, 0, len(rows))
	for _, r := range rows {
		if fleet.S(r, "malformed") != "" {
			out = append(out, r)
			continue
		}
		copied := fleet.Rec{}
		for k, v := range r {
			copied[k] = v
		}
		copied["superseded"] = orEmptyRecs(supersededBy(r))
		out = append(out, copied)
	}
	return out
}

func orEmptyRecs(xs []fleet.Rec) []fleet.Rec {
	if xs == nil {
		return []fleet.Rec{}
	}
	return xs
}

func jsonIndent(v any) string {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return "null"
	}
	return string(b)
}

// pullRecords is the cached change-number -> branch facts, filtered by number and
// local repo id.
func pullRecords(number int, hasNumber bool, rid string) []fleet.Rec {
	d := fleet.Path("prs")
	ents, _ := os.ReadDir(d)
	var out []fleet.Rec
	for _, e := range ents {
		r := fleet.ReadJSON(filepath.Join(d, e.Name()))
		if r == nil || fleet.S(r, "branch") == "" {
			continue
		}
		if hasNumber && int(fleet.F(r, "number")) != number {
			continue
		}
		if rid != "" && fleet.S(r, "repo") != rid {
			continue
		}
		out = append(out, r)
	}
	return out
}

// checkoutFor is a local checkout of repo rid: the cwd when it is that repo, else
// any roled path in roles.map that is.
func checkoutFor(rid string) string {
	if fleet.RepoID(cwd()) == rid {
		return cwd()
	}
	_, rows := fleet.MapRows(fleet.RolesMap())
	for _, r := range rows {
		if isDir(r.Path) && fleet.RepoID(r.Path) == rid {
			return r.Path
		}
	}
	return ""
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// ghJSON is the parsed output of a `gh ... --json` command line, or (nil, why).
func ghJSON(args ...string) (any, string) {
	cmd := exec.Command(args[0], args[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return nil, "gh unavailable (OSError)"
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
			return nil, last
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return nil, "gh unavailable (TimeoutExpired)"
	}
	var v any
	if err := json.Unmarshal([]byte(stdout.String()), &v); err != nil {
		return nil, "gh returned no JSON"
	}
	return v, ""
}

// isChangeNumber: `#123` is always a change number. A bare digit string is one only
// up to six digits: longer is a sha prefix that happens to have no letters.
func isChangeNumber(arg string) bool {
	a := strings.TrimSpace(arg)
	if strings.HasPrefix(a, "#") {
		return isDigits(a[1:])
	}
	return isDigits(a) && len(a) <= 6
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// resolveChange is (sha, branch, how) for a revision named as a sha prefix, a change
// number, or a branch in the cwd's repo. Every hop is stated in how; nothing is
// guessed.
func resolveChange(arg string) (string, string, string, error) {
	a := strings.TrimPrefix(strings.TrimSpace(arg), "#")
	rid := fleet.RepoID(cwd())
	// A name that IS a local branch here is a branch, whatever else it looks like.
	if rid != "" && !strings.HasPrefix(strings.TrimSpace(arg), "#") {
		if rc, _ := gitTry(cwd(), gitTimeout, "rev-parse", "--verify", "--quiet", "refs/heads/"+a); rc == 0 {
			return resolveBranchHead(rid, a, fmt.Sprintf("branch %s in the repo at this cwd", a))
		}
	}
	if !isChangeNumber(arg) && shaRe.MatchString(a) {
		return a, "", "sha as given", nil
	}
	if isChangeNumber(arg) {
		return resolveChangeNumber(a, rid)
	}
	if rid == "" {
		return "", "", "", refuse("fleet: %s is not inside a git repo, so `%s` names no branch here", cwd(), a)
	}
	return resolveBranchHead(rid, a, fmt.Sprintf("branch %s in the repo at this cwd", a))
}

// resolveChangeNumber is #n as a revision: the local cache first, else gh, and a
// number cached for several repos is ambiguous rather than guessed.
func resolveChangeNumber(a, rid string) (string, string, string, error) {
	n, _ := strconv.Atoi(a)
	hits := pullRecords(n, true, rid)
	if len(hits) > 1 {
		var names []string
		for _, h := range hits {
			g := fleet.S(h, "github")
			if g == "" {
				g = fleet.S(h, "repo")
			}
			names = append(names, g)
		}
		return "", "", "", refuse("fleet: change #%s is cached for %d repos (%s); run this inside the repo you mean", a, len(hits), strings.Join(names, ", "))
	}
	if len(hits) == 1 {
		h := hits[0]
		branch := fleet.S(h, "branch")
		return resolveBranchHead(fleet.S(h, "repo"), branch, fmt.Sprintf("#%s -> %s (cached from a gh pr call %s ago)", a, branch, ago(fleet.F(h, "at"))))
	}
	data, why := ghJSON("gh", "pr", "view", a, "--json", "headRefName,headRefOid,url")
	dm, _ := data.(map[string]any)
	if dm == nil {
		return "", "", "", refuse("fleet: change #%s is not in the local cache and gh could not resolve it (%s); run `gh pr view %s` in its repo once and retry", a, why, a)
	}
	branch := fleet.S(dm, "headRefName")
	how := fmt.Sprintf("#%s -> %s (via gh)", a, branch)
	if oid := fleet.S(dm, "headRefOid"); oid != "" && rid == "" {
		return oid, branch, how + ", head via gh", nil
	}
	return resolveBranchHead(rid, branch, how)
}

// resolveBranchHead is the revision a branch names. `origin/<branch>` first — the
// change is what is on the remote, and a local ref is whatever a slot checked out
// however long ago.
func resolveBranchHead(rid, branch, how string) (string, string, string, error) {
	co := ""
	if rid != "" {
		co = checkoutFor(rid)
	}
	if co == "" {
		r := rid
		if r == "" {
			r = "that repo"
		}
		return "", "", "", refuse("fleet: no local checkout of %s on this machine to read the head of %s from", r, branch)
	}
	rcR, remote := gitTry(co, gitTimeout, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	rcL, local := gitTry(co, gitTimeout, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if rcR == 0 && remote != "" {
		note := ""
		if rcL == 0 && local != "" && local != remote {
			note = fmt.Sprintf(", local ref is at %s (differs; origin/ wins)", cut(local, 10))
		}
		return remote, branch, fmt.Sprintf("%s, head from origin/%s%s in %s", how, branch, note, co), nil
	}
	if rcL == 0 && local != "" {
		return local, branch, fmt.Sprintf("%s, local-only branch (no origin/%s) in %s", how, branch, co), nil
	}
	return "", "", "", refuse("fleet: branch %s has no local or origin/ head in %s; fetch it, then retry", branch, co)
}

// producedKinds is every receipt kind some lane manifest produces.
func producedKinds() []string {
	d := fleet.LanesDir()
	ents, _ := os.ReadDir(d)
	seen := map[string]bool{}
	var kinds []string
	for _, e := range ents {
		m := fleet.ReadJSON(filepath.Join(d, e.Name(), "manifest.json"))
		if p, ok := m["produces"].(string); ok && !seen[p] {
			seen[p] = true
			kinds = append(kinds, p)
		}
	}
	sort.Strings(kinds)
	return kinds
}

type doneResult struct {
	sha     string
	kinds   map[string]fleet.Rec
	wanted  []string
	missing []string
	failed  []string
	ok      bool
}

// doneVerdict is the latest receipt per kind for a revision, and whether the
// expected kinds all pass. Expected = kind when given, else every kind a lane
// produces — never the kinds that happen to be on disk.
func doneVerdict(sha, kind string) doneResult {
	latest := map[string]fleet.Rec{}
	for _, r := range receiptRows(sha, "", 0, false) {
		if fleet.S(r, "malformed") != "" {
			continue
		}
		k := fleet.S(r, "kind")
		if _, ok := latest[k]; !ok {
			latest[k] = r // rows are newest first
		}
	}
	wanted := producedKinds()
	if kind != "" {
		wanted = []string{kind}
	}
	var missing, failed []string
	for _, k := range wanted {
		r, ok := latest[k]
		if !ok {
			missing = append(missing, k)
		} else if fleet.S(r, "verdict") != "pass" {
			failed = append(failed, k)
		}
	}
	return doneResult{sha, latest, wanted, missing, failed, len(wanted) > 0 && len(missing) == 0 && len(failed) == 0}
}

// CmdDone exits 0 when a passing receipt of every expected kind exists for the
// revision; 1 when one is still missing (pending); 3 when the latest receipt of an
// expected kind FAILED; 2 when the revision cannot be resolved or nothing is expected.
// The verdict is always the LATEST receipt of each kind; --all shows what it replaced
// and changes no exit code.
func CmdDone(arg, kind string, asJSON, all bool) error {
	if arg == "" {
		return exitCode(2, "")
	}
	sha, branch, how, err := resolveChange(arg)
	if err != nil {
		if r, ok := err.(*Refusal); ok {
			return exitCode(2, r.Msg) // unresolvable is its own exit code, distinct from "not done"
		}
		return exitCode(2, err.Error())
	}
	v := doneVerdict(sha, kind)
	if len(v.wanted) == 0 {
		return exitCode(2, "fleet done: no lane produces a receipt kind and no --kind was given; nothing is expected, so nothing can be done")
	}
	if asJSON {
		kinds := map[string]any{}
		for k, r := range v.kinds {
			kinds[k] = r
			if all {
				kinds[k] = withHistory([]fleet.Rec{r})[0]
			}
		}
		say("%s", jsonIndent(map[string]any{"sha": sha, "kinds": kinds, "wanted": v.wanted, "missing": orEmpty(v.missing), "failed": orEmpty(v.failed), "ok": v.ok, "resolution": how}))
	} else {
		printDone(v, sha, branch, how, all)
	}
	switch {
	case v.ok:
		return nil
	case len(v.failed) > 0:
		return exitCode(3, "")
	default:
		return exitCode(1, "")
	}
}

// printDone is the verdict as text: one line per receipt kind, and what is missing.
func printDone(v doneResult, sha, branch, how string, all bool) {
	head := cut(sha, 10)
	if branch != "" {
		head += " (" + branch + ")"
	}
	if len(v.kinds) == 0 {
		say("NOT DONE  %s: no receipt of any kind  [%s]", head, how)
	}
	var ks []string
	for k := range v.kinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		r := v.kinds[k]
		label := "FAILED  "
		if fleet.S(r, "verdict") == "pass" {
			label = "DONE    "
		}
		s := fleet.S(r, "session")
		if s == "" {
			s = "?"
		}
		line := fmt.Sprintf("%s  %s  %s %s %s ago by %s %s — %s", label, head, k, fleet.S(r, "verdict"), ago(fleet.F(r, "at")), roleOr(r, "session"), fleet.Short(s), fleet.S(r, "observable"))
		if c := fleet.S(r, "card"); c != "" {
			line += "  [card " + c + "]"
		}
		say("%s", line)
		sayHistory(r, all)
	}
	if len(v.missing) > 0 && len(v.kinds) > 0 {
		say("NOT DONE  %s: no receipt of kind %s", head, strings.Join(v.missing, ", "))
	}
}

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
