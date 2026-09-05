package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FileTools are the tools whose call is a write to the tree.
var FileTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// cmdPos is the command position: start of the command or after a shell operator,
// past env assignments. A git write is a git SUBCOMMAND read there — the old
// bag-of-words form denied `git log -- src/apply.ts` on a held branch while letting
// `git branch -D` through.
const cmdPos = `(?:^|&&|\|\||;|\||\()\s*(?:\w+=(?:"[^"]*"|'[^']*'|\S+)\s+)*`
const gitOpts = `(?:-[cC]\s+\S+\s+)*`

var (
	gitWriteRe = regexp.MustCompile(cmdPos + `git\s+` + gitOpts +
		`(?:(?:push|commit|merge|rebase|reset|checkout|switch|stash|cherry-pick|am|apply|update-ref)\b` +
		`|branch\s+(?:-[dDfmM]\b|--delete|--force|--move)` +
		`|worktree\s+(?:remove|move|prune)\b)`)
	ghWriteRe    = regexp.MustCompile(`\bgh\s+pr\s+(merge|close|edit|ready|checkout)\b`)
	bashTargetRe = regexp.MustCompile(cmdPos + `git\s+(?:-c\s+\S+\s+)*-C\s+(\S+)`)
	cdTargetRe   = regexp.MustCompile(`\A\s*cd\s+(\S+)\s*&&`)
	switchRe     = regexp.MustCompile(cmdPos + `git\s+` + gitOpts + `(checkout|switch)\b([^&|;]*)`)
	pullURLRe    = regexp.MustCompile(`https://github\.com/([\w.-]+)/([\w.-]+)/pull/(\d+)`)
	ghPullRe     = regexp.MustCompile(cmdPos + `gh\s+pr\s+(create|view|checkout)\b([^&|;]*)`)
	envAssignRe  = regexp.MustCompile(`\A\s*(\w+=("[^"]*"|'[^']*'|\S+)\s+)+`)
	leadingCdRe  = regexp.MustCompile(`\A\s*cd\s+\S+\s*&&\s*`)
	// exemptRe is the five substrate verbs a `requires` guard must let through, and
	// only as a standalone command. An allowlist, not a delimiter blacklist. Every
	// separator is horizontal whitespace and the match is anchored: `\s` and `$` both
	// admit a newline, and a shell runs `fleet slots\ntouch x` as two commands.
	exemptRe = regexp.MustCompile(`\A[^\S\r\n]*(?:fleet|(?:python3?|py)(?:\.exe)?[^\S\r\n]+[A-Za-z0-9_./~:\\-]*fleet\.py)` +
		`[^\S\r\n]+(?:take|drop|slots|leases|sessions)` +
		`(?:[^\S\r\n]+[A-Za-z0-9:_./@=~-]+|[^\S\r\n]+"[^"$` + "`" + `\\\r\n]*")*[^\S\r\n]*\z`)
)

var (
	switchValued = map[string]bool{"-b": true, "-B": true, "-c": true, "-C": true, "--orphan": true, "--conflict": true, "--pathspec-from-file": true, "-t": true, "--track": true}
	switchDetach = map[string]bool{"--detach": true, "-d": true}
)

// IsWrite reports whether a tool call writes to the tree.
func IsWrite(tool, cmd string) bool {
	if FileTools[tool] {
		return true
	}
	return tool == "Bash" && (gitWriteRe.MatchString(cmd) || ghWriteRe.MatchString(cmd))
}

// BashTarget is the checkout a shell command acts on: `git -C <path>` or a leading
// `cd <path> &&`, else the cwd. Command position only: `echo "git -C x push"` names
// no target and is no write.
func BashTarget(cmd, cwd string) string {
	var t string
	if m := bashTargetRe.FindStringSubmatch(cmd); m != nil {
		t = m[1]
	} else if m := cdTargetRe.FindStringSubmatch(cmd); m != nil {
		t = m[1]
	} else {
		return cwd
	}
	t = expand(strings.Trim(t, `'"`))
	if filepath.IsAbs(t) {
		return t
	}
	if cwd == "" {
		cwd = "."
	}
	return filepath.Join(cwd, t)
}

// CommandHead is the command's execution position: the leading words a shell would
// actually run. Cost rules match against this, not the whole string, because the
// question is "is this command the suite" and not "does it mention the suite".
func CommandHead(cmd string) string {
	cmd = envAssignRe.ReplaceAllString(cmd, "")
	cmd = leadingCdRe.ReplaceAllString(cmd, "")
	var head []string
	for _, tok := range strings.Fields(cmd) {
		if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "$") || strings.HasPrefix(tok, "'") || strings.HasPrefix(tok, `"`) || strings.Contains(tok, "/") || strings.Contains(tok, `\`) {
			break
		}
		head = append(head, tok)
		if len(head) == 4 {
			break
		}
	}
	return strings.Join(head, " ")
}

// Signature normalises a shell command to a cost-ledger key: two tokens.
func Signature(cmd string) string {
	cmd = envAssignRe.ReplaceAllString(cmd, "")
	cmd = leadingCdRe.ReplaceAllString(cmd, "")
	toks := strings.Fields(cmd)
	if len(toks) > 2 {
		toks = toks[:2]
	}
	return strings.Join(toks, " ")
}

// MatchedRule is the first expensive rule this command trips, or nil. `pattern`
// matches the execution position; `unless` matches the whole command. A rule missing
// `pattern` is skipped rather than raised.
func MatchedRule(cmd string) Rec {
	head := CommandHead(cmd)
	if head == "" {
		return nil
	}
	rules, _ := readAny(Path("expensive.json")).([]any)
	for _, raw := range rules {
		r, ok := raw.(map[string]any)
		if !ok || S(r, "pattern") == "" {
			continue
		}
		pat, err := regexp.Compile(S(r, "pattern"))
		if err != nil || !pat.MatchString(head) {
			continue
		}
		if u := S(r, "unless"); u != "" {
			if ure, err := regexp.Compile(u); err == nil && ure.MatchString(cmd) {
				continue
			}
		}
		return r
	}
	return nil
}

// ---------- policy: the checks, in order ----------

// CheckStop is the stand-down text for a stopped key, or "".
func CheckStop(key, branch, sid string) string {
	flag := StopFlag(key)
	if flag == nil || S(flag, "except") == sid {
		return ""
	}
	who := S(flag, "by")
	if who == "" {
		who = "the operator"
	}
	what, lift := "branch "+branch, fmt.Sprintf("`fleet resume %s` in that repo", branch)
	if IsResource(key) {
		what, lift = key, fmt.Sprintf("`fleet resume %s`", key)
	}
	reason := S(flag, "reason")
	if reason == "" {
		reason = "no reason given"
	}
	return fmt.Sprintf("STAND DOWN on %s (set by %s %s ago): %s. Take no further action on it. "+
		"Report your current state from context — what you changed, what is uncommitted — and end your turn. "+
		"Do not retry; the flag lifts only via %s.", what, who, FmtAge(Now()-F(flag, "at")), reason, lift)
}

// HeldState is the classification of a key's holder from one read.
type HeldState string

const (
	HeldFree      HeldState = ""          // this session may proceed (free, or ours)
	HeldMalformed HeldState = "malformed" //
	HeldOrphaned  HeldState = "orphaned"  // dead resource holder
	HeldDead      HeldState = "dead"      // dead branch holder
	HeldLive      HeldState = "live"      // a live foreign holder
)

// HeldByOther is (state, record) from ONE read of key. The record is returned, never
// re-read by the caller: a caller that re-read the key to find out who it had just
// classified could be handed a replacement — a faster racer's live lease — and act
// on that. Callers that ACT on the answer read it inside KeyLock.
func HeldByOther(key, sid string) (HeldState, Rec) {
	cur := Lease(key)
	TestPause("AFTER_KEY_READ") // every path below decides from `cur` and nothing else
	if cur == nil {
		return HeldFree, nil
	}
	if IsMalformed(cur) {
		return HeldMalformed, cur
	}
	if S(cur, "session") == sid {
		return HeldFree, cur
	}
	if SessionAlive(ReadJSON(Path("sessions", S(cur, "session")+".json"))) {
		return HeldLive, cur
	}
	if IsResource(key) {
		return HeldOrphaned, cur
	}
	return HeldDead, cur
}

// TestPause is the substrate's one test seam. A TOCTOU race cannot be proven by
// running N processes and hoping; the suite sets FLEET_TEST_PAUSE_<WHERE> to force
// the exact interleaving. Unset in every real session, where this is one lookup.
func TestPause(where string) {
	d := os.Getenv("FLEET_TEST_PAUSE_" + where)
	if d == "" {
		return
	}
	if f, err := strconv.ParseFloat(d, 64); err == nil {
		time.Sleep(time.Duration(f * float64(time.Second)))
	}
}

// CheckLease is one holder per key: read, decide and write inside the key's lock, so
// the decision is acted on against the state it was made from. Returns the refusal,
// or "" when this session holds the key.
//
// This is the one place fail-open is wrong. Every error — a lock that cannot be
// taken, a store that cannot be written, a hand-edited record that will not format —
// is a refusal, never an authorization.
func CheckLease(key, branch, sid, role, cwd string) (reason string) {
	defer func() {
		if r := recover(); r != nil {
			reason = substrateError(key, fmt.Sprint(r))
		}
	}()
	// Nearly every call is the holder writing its own branch again. That needs no
	// lock: the lock is released before the tool runs either way, so a revoke landing
	// after this read lands one more write in both versions and is caught by the stop
	// flag at the next action.
	if mine := Lease(key); mine != nil && !IsMalformed(mine) && S(mine, "session") == sid {
		return ""
	}
	err := KeyLock(key, func() error {
		cur := Lease(key)
		TestPause("AFTER_OBSERVE") // inside the lock: a rival now BLOCKS rather than races
		if cur == nil {
			return WriteLease(key, LeaseRecord(key, sid, role, cwd, "claimed on first write"))
		}
		if IsMalformed(cur) {
			reason = fmt.Sprintf("lease file for %s is malformed: %s. Nothing is inferred from garbage; it is not free and not taken over. Next action: have the operator inspect and remove it.", KeyLabel(key), S(cur, "malformed"))
			return nil
		}
		if S(cur, "session") == sid {
			return nil // no heartbeat: freshness lives on the session record
		}
		holderRole := S(cur, "role")
		if holderRole == "" {
			holderRole = "a session"
		}
		if SessionAlive(ReadJSON(Path("sessions", S(cur, "session")+".json"))) {
			what := "branch " + branch
			if IsResource(key) {
				what = key
			}
			reason = fmt.Sprintf("%s is held by %s %s since %s ago. One holder per key. Either work elsewhere, or ask the operator to run `fleet revoke %s --to %s \"<reason>\"`, which stands the holder down at its next action.",
				what, holderRole, Short(S(cur, "session")), FmtAge(Now()-F(cur, "since")), KeyLabel(key), Short(sid))
			return nil
		}
		if IsResource(key) {
			reason = fmt.Sprintf("%s is held by dead session %s (since %s ago). A dead holder's resource is not taken over automatically: it may still be running. Next action: confirm it is quiet, then `fleet take %s --takeover \"<what you checked>\"`.",
				key, Short(S(cur, "session")), FmtAge(Now()-F(cur, "since")), key)
			return nil
		}
		return WriteLease(key, LeaseRecord(key, sid, role, cwd, "took over from dead session "+Short(S(cur, "session"))))
	})
	if err == ErrKeyBusy {
		return fmt.Sprintf("another fleet process has held the lock on %s for over a second, so nothing was written. Next action: `fleet leases` to see who holds it and retry; if it persists, `fleet sessions` for a hung session.", KeyLabel(key))
	}
	if err != nil {
		return substrateError(key, err.Error())
	}
	return reason
}

func substrateError(key, msg string) string {
	return fmt.Sprintf("the fleet store could not be read or written for %s (%s), so this session does not hold it and nothing was written. A lease is not assumed on a substrate error. Next action: check ~/.fleet is writable, then retry.", KeyLabel(key), msg)
}

// ExemptFromRequires reports a standalone substrate verb the guard lets through.
func ExemptFromRequires(tool, cmd string) bool {
	if tool != "Bash" || strings.ContainsAny(cmd, "\n\r") {
		return false
	}
	return exemptRe.MatchString(cmd)
}

// CheckRequires is the refusal when a lane's `requires` names a resource this session
// does not hold, or "". Deny on evidence only; skipped when the session has no cached
// lane.
func CheckRequires(rec Rec, sid, tool, cmd string) string {
	lane := M(rec, "lane")
	requires := Strs(lane, "requires")
	if len(requires) == 0 || ExemptFromRequires(tool, cmd) {
		return ""
	}
	kind := S(lane, "kind")
	for _, r := range requires {
		// One observation, and the lease must be OURS by that observation.
		state, cur := HeldByOther(r, sid)
		if state == HeldFree && cur != nil && S(cur, "session") == sid {
			continue
		}
		if state == HeldMalformed {
			return fmt.Sprintf("%s needs %s, whose lease file is malformed: %s. Next action: have the operator inspect and remove it.", kind, r, S(cur, "malformed"))
		}
		var who string
		switch state {
		case HeldFree:
			who = "nobody"
		case HeldOrphaned, HeldDead:
			who = "orphaned by dead session " + Short(S(cur, "session"))
		default:
			role := S(cur, "role")
			if role == "" {
				role = "a session"
			}
			who = fmt.Sprintf("%s %s since %s ago", role, Short(S(cur, "session")), FmtAge(Now()-F(cur, "since")))
		}
		return fmt.Sprintf("%s needs %s before this action; it is held by %s. Run: fleet take %s \"<why>\" — or wait for `fleet leases` to show it free.", kind, r, who, r)
	}
	return ""
}

// CheckCost is the refusal for an expensive command, or "".
func CheckCost(cmd, sid string) string {
	r := MatchedRule(cmd)
	if r == nil {
		return ""
	}
	name := S(r, "name")
	if name == "" {
		name = CommandHead(cmd)
	}
	if name == "" {
		name = "an expensive command"
	}
	instead := S(r, "instead")
	if instead == "" {
		instead = "a targeted run of just what your diff touches"
	}
	lock := ReadJSON(Path("locks", Safe(name)+".json"))
	if lock != nil && S(lock, "session") != sid && SessionAlive(ReadJSON(Path("sessions", S(lock, "session")+".json"))) {
		return fmt.Sprintf("`%s` is already running in session %s (started %s ago). Do not run it again in parallel; do the targeted check instead: %s", name, Short(S(lock, "session")), FmtAge(Now()-F(lock, "at")), instead)
	}
	if strings.Contains(cmd, "FLEET_ALLOW_SLOW=") {
		_ = AppendJSONL(Path("overrides.jsonl"), Rec{"at": Now(), "session": sid, "cmd": cut(cmd, 200)})
		return ""
	}
	secs := "?"
	if s, ok := r["seconds"]; ok && s != nil {
		secs = fmt.Sprint(s)
		if f, ok := s.(float64); ok && f == float64(int64(f)) {
			secs = strconv.FormatInt(int64(f), 10)
		}
	}
	return fmt.Sprintf("`%s` costs ~%ss on this machine and runs on push anyway. Instead: %s. If this run is genuinely needed, prefix the command with FLEET_ALLOW_SLOW=\"<why>\" so the override is recorded.", name, secs, instead)
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// SwitchTargets is every local branch the `git checkout|switch` commands in cmd may
// land on, in command order, or nil when none is a branch switch. Read from .git, no
// spawn. Not a switch: a path restore, a detached target, `switch -`, or a token that
// names no local or remote-tracking branch and is not being created with -b/-c.
func SwitchTargets(cmd, start string) []string {
	var out []string
	_, common := GitDirs(start)
	if common == "" {
		return out
	}
	for _, m := range switchRe.FindAllStringSubmatch(cmd, -1) {
		verb, toks := m[1], shellWords(m[2])
		if contains(toks, "--") {
			continue
		}
		detached := false
		for _, t := range toks {
			if switchDetach[t] {
				detached = true
			}
		}
		if detached {
			continue
		}
		target := ""
		haveTarget := false
		skip := false
		var plain []string
		track := false
		for _, t := range toks {
			if t == "-t" || t == "--track" || strings.HasPrefix(t, "--track=") {
				track = true
			}
			if skip {
				skip = false
				if !haveTarget {
					target, haveTarget = t, true
				}
				continue
			}
			if switchValued[t] {
				skip = true
				continue
			}
			if strings.HasPrefix(t, "-") {
				if k, v, ok := strings.Cut(t, "="); ok && switchValued[k] && !haveTarget {
					target, haveTarget = v, true
				}
				continue
			}
			plain = append(plain, t)
		}
		if haveTarget {
			name := target
			if track && strings.Contains(target, "/") {
				name = strings.SplitN(target, "/", 2)[1]
			}
			if name != "" && name != "-" {
				out = append(out, name)
			}
			continue
		}
		if len(plain) == 0 || plain[0] == "-" {
			continue
		}
		if verb == "checkout" && len(plain) > 1 {
			continue // `git checkout <tree-ish> <paths...>` restores paths
		}
		cand := plain[0]
		if spelled := BranchSpelling(common, cand); spelled != "" {
			out = append(out, spelled)
			continue
		}
		if RemoteNames(common)[strings.SplitN(cand, "/", 2)[0]] {
			continue // `git checkout origin/x` detaches; it is not a branch switch
		}
		if RemoteBranchExists(common, cand) {
			out = append(out, cand)
		}
	}
	return out
}

// SettleHandoff closes a branch switch this session has in flight. The switch took
// the new branch's lease BEFORE git ran and kept the old one; here <gitdir>/HEAD says
// which one the tree is on. Over-hold between the two hooks, never under-hold.
//
// This runs before the stop and lease verdicts, so nothing in it may propagate: an
// error here would reach the fail-open catch and the tool call would proceed with no
// lease check at all. On any failure the handoff record is left in place.
func SettleHandoff(sid string, rec Rec) {
	h := M(rec, "handoff")
	if h == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logError(Rec{"session": sid, "error": fmt.Sprintf("settle_handoff left in flight: %v", r)})
		}
	}()
	start := S(h, "start")
	if start == "" {
		start = S(rec, "cwd")
	}
	if start == "" {
		start = "."
	}
	landed := Scope(start, BranchOf(start))
	var tos []string
	if list, ok := h["to"].([]any); ok {
		for _, t := range list {
			if s, ok := t.(string); ok && s != "" {
				tos = append(tos, s)
			}
		}
	} else if s := S(h, "to"); s != "" {
		tos = []string{s}
	}
	fail := func(err error) bool {
		if err == nil {
			return false
		}
		logError(Rec{"session": sid, "error": fmt.Sprintf("settle_handoff left in flight: %v", err)})
		return true
	}
	if contains(tos, landed) {
		if from := S(h, "from"); from != "" {
			if _, err := DropLease(from, sid); fail(err) {
				return
			}
		}
	}
	for _, t := range tos {
		if t != landed {
			if _, err := DropLease(t, sid); fail(err) {
				return
			}
		}
	}
	// Clearing the record is a read-modify-write on the session file, under its lock
	// like every other one; a parallel hook's touch must not be lost here either.
	err := KeyLock("session:"+sid, func() error {
		p := Path("sessions", sid+".json")
		cur := ReadJSON(p)
		if cur == nil {
			cur = Rec{}
		}
		cur["handoff"] = nil
		return WriteJSON(p, cur)
	})
	if fail(err) {
		return
	}
}

// CachePullRequest derives the change-number -> branch fact from a `gh pr
// create|view|checkout` the session ran anyway. The BRANCH is read from the tree,
// never from the command's output: output is untrusted text. The NUMBER comes from
// the command's explicit target when it has one, else from the single URL a create
// or bare view printed.
func CachePullRequest(cmd string, response any, start, sid string) {
	m := ghPullRe.FindStringSubmatch(cmd)
	if m == nil {
		return
	}
	verb, toks := m[1], shellWords(m[2])
	var owner, name, number string
	explicit := false
	for _, t := range toks {
		if strings.HasPrefix(t, "-") {
			continue
		}
		if u := pullURLRe.FindStringSubmatch(t); u != nil {
			owner, name, number, explicit = u[1], u[2], u[3], true
		} else if isDigits(t) {
			owner, name, number, explicit = "", "", t, true
		}
	}
	if verb == "view" && explicit {
		return
	}
	if verb == "checkout" && !explicit {
		return
	}
	var text string
	if s, ok := response.(string); ok {
		text = s
	} else if response != nil {
		text = string(DumpJSON(response))
	}
	var urls [][]string
	seen := map[string]bool{}
	for _, u := range pullURLRe.FindAllStringSubmatch(text, -1) {
		if !seen[u[0]] {
			seen[u[0]] = true
			urls = append(urls, u)
		}
	}
	if !explicit && len(urls) != 1 {
		return // a body that links other changes names no single change; no guess
	}
	if !explicit {
		owner, name, number = urls[0][1], urls[0][2], urls[0][3]
	}
	if owner == "" && len(urls) > 0 {
		owner, name = urls[0][1], urls[0][2]
	}
	branch, rid := BranchOf(start), RepoID(start)
	if branch == "" || rid == "" {
		return
	}
	n, err := strconv.Atoi(number)
	if err != nil {
		return
	}
	var gh, url any
	if owner != "" {
		gh = owner + "/" + name
		url = fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, name, n)
	}
	_ = WriteJSON(PullFile(rid, n), Rec{"number": float64(n), "repo": rid, "github": gh, "branch": branch, "url": url, "at": Now(), "session": sid})
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
