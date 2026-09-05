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
	if (verdict != "pass" && verdict != "fail") || !shaRe.MatchString(sha) || !kindRe.MatchString(kind) {
		return refuse(`usage: fleet receipt <sha> <kind> pass|fail "<observable>" [--card <url>]`)
	}
	if strings.TrimSpace(observable) == "" {
		return refuse("fleet receipt: the observable is what would have read differently had the claim been false; it cannot be empty")
	}
	if hasCard && !urlRe.MatchString(card) {
		return refuse("fleet receipt: --card must be a URL, got %s", fleet.PyRepr(card))
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	rec := fleet.SessionRecord(sid)
	lane := fleet.M(rec, "lane")
	if lane == nil {
		return refuse("fleet receipt: session %s has no lane (role %s, no manifest); a receipt needs a lane that produces %s", fleet.Short(sid), roleOr(rec, "none"), fleet.PyRepr(kind))
	}
	if fleet.S(lane, "produces") != kind {
		return refuse("fleet receipt: lane %s produces %s, not %s; only a lane whose manifest produces %s may record it", fleet.S(lane, "kind"), pyReprOrNone(lane["produces"]), fleet.PyRepr(kind), fleet.PyRepr(kind))
	}
	root := fleet.RoledRoot(fleet.S(rec, "cwd"))
	here := canon(cwd())
	croot := ""
	if root != "" {
		croot = canon(root)
	}
	if croot == "" || !(here == croot || strings.HasPrefix(here, strings.TrimRight(croot, "/")+"/")) {
		where := root
		if where == "" {
			where = fleet.S(rec, "cwd")
		}
		return refuse("fleet receipt: this must run inside the session's roled worktree %s, not %s; the receipt's HEAD and cleanliness are read from the tree it names", where, here)
	}
	head, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if !strings.HasPrefix(head, sha) {
		return refuse("fleet receipt: the packet names %s but this tree is at %s; a receipt names the revision that ran, nothing else", sha, cut(head, 12))
	}
	dirty, err := gitOut("status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(dirty) != "" {
		return refuse("fleet receipt: the tree is not clean (%d path(s) per git status --porcelain); a dirty tree is not revision %s", len(strings.Split(strings.TrimSpace(dirty), "\n")), sha)
	}
	var cardV any
	if hasCard {
		cardV = card
	}
	err = fleet.WriteJSON(fleet.Path("receipts", sha+"."+kind+".json"), fleet.Rec{
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
	say("receipt: %s %s @ %s by %s %s%s", kind, verdict, sha, roleOr(rec, "session"), fleet.Short(sid), tail)
	return nil
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
		if sha != "" {
			rs := fleet.S(r, "sha")
			if !(strings.HasPrefix(fleet.S(r, "head"), sha) || (rs != "" && strings.HasPrefix(sha, rs))) {
				continue
			}
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

func cmdReceipts(sha, kind string, since float64, hasSince, asJSON bool) error {
	rows := receiptRows(sha, kind, since, hasSince)
	if asJSON {
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
		say("%s  %-8s %-4s  %6s ago  %s %s%s", cut(fleet.S(r, "head"), 10), fleet.S(r, "kind"), fleet.S(r, "verdict"), ago(fleet.F(r, "at")), roleOr(r, "session"), fleet.Short(s), tail)
	}
	return nil
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
	var branch, how string
	if isChangeNumber(arg) {
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
			branch, rid = fleet.S(h, "branch"), fleet.S(h, "repo")
			how = fmt.Sprintf("#%s -> %s (cached from a gh pr call %s ago)", a, branch, ago(fleet.F(h, "at")))
		} else {
			data, why := ghJSON("gh", "pr", "view", a, "--json", "headRefName,headRefOid,url")
			dm, _ := data.(map[string]any)
			if dm == nil {
				return "", "", "", refuse("fleet: change #%s is not in the local cache and gh could not resolve it (%s); run `gh pr view %s` in its repo once and retry", a, why, a)
			}
			branch = fleet.S(dm, "headRefName")
			how = fmt.Sprintf("#%s -> %s (via gh)", a, branch)
			if oid := fleet.S(dm, "headRefOid"); oid != "" && rid == "" {
				return oid, branch, how + ", head via gh", nil
			}
		}
	} else {
		branch, how = a, fmt.Sprintf("branch %s in the repo at this cwd", a)
		if rid == "" {
			return "", "", "", refuse("fleet: %s is not inside a git repo, so `%s` names no branch here", cwd(), a)
		}
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
func CmdDone(arg, kind string, asJSON bool) error {
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
		}
		out := map[string]any{"sha": sha, "kinds": kinds, "wanted": v.wanted, "missing": orEmpty(v.missing), "failed": orEmpty(v.failed), "ok": v.ok, "resolution": how}
		say("%s", jsonIndent(out))
	} else {
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
		}
		if len(v.missing) > 0 && len(v.kinds) > 0 {
			say("NOT DONE  %s: no receipt of kind %s", head, strings.Join(v.missing, ", "))
		}
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

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
