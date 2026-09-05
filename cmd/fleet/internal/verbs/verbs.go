// Package verbs is the operator's side of the hook: stop, resume, revoke, the
// board, and every other `fleet <verb>`. A port of the reference fleet.py.
//
// A verb refuses with a Refusal: code 1 and the reason on stderr, never a stack
// trace. Output goes to Out so the MCP face can capture it and say exactly what the
// terminal would have said.
package verbs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Out is where verbs print. The MCP face swaps it for a buffer for one call.
var Out io.Writer = os.Stdout

// Refusal is a verb declining, with the reason. Code is 1 unless a verb says
// otherwise (`done` uses 2 and 3; usage is 2).
type Refusal struct {
	Code int
	Msg  string
}

func (r *Refusal) Error() string { return r.Msg }

func refuse(format string, a ...any) error {
	return &Refusal{Code: 1, Msg: fmt.Sprintf(format, a...)}
}

func exitCode(code int, msg string) error { return &Refusal{Code: code, Msg: msg} }

func say(format string, a ...any) { fmt.Fprintf(Out, format+"\n", a...) }

const usage = `fleet — the operator's side of the hook: stop, resume, revoke, and the board.

Per-branch state (stop, lease, handoff) is keyed by repo as well as branch, so stop / resume /
revoke / handoff act on the repo you are standing in. ` + "`main`" + ` in two repos is two branches.

  fleet stop <branch|slot:name> "<reason>"       stand every session on that key down at its next action
  fleet resume <branch|slot:name>                lift the flag
  fleet revoke <branch|slot:name> --to <session8> "<why>"
                                                 stand the holder down and hand the lease to <session8>
  fleet take <slot:name> ["<why>"] [--takeover] [--session <id8>]
                                                 lease a resource for this session; refused if held (dead holder: --takeover)
  fleet drop <slot:name> [--session <id8>]       release it; holder only
  fleet sessions                                 live sessions, from hook-written records (no snapshot)
  fleet leases                                   who holds which key; resources first; orphaned/malformed/stray marked
  fleet decide <drop|park|ignore|rule> <subject> "<text>"
                                                 record an operator decision; every session sees it at its next turn
  fleet undecide <id>                            retire a decision
  fleet decisions                                the decisions in force
  fleet costs                                    measured command costs on this machine (median, n)
  fleet tier [--base <ref>] [--json]             verification tier of the diff against <ref> (default origin/main)
  fleet ready <sha> "<action>" "<observable>"     print the ready-to-run packet, or refuse an incomplete one
  fleet receipt <sha> <kind> pass|fail "<observable>" [--session <id8>] [--card <url>]
                                                 record a receipt: this session's lane must produce <kind>; tree at <sha>, clean
  fleet receipts [<sha>] [--kind <k>] [--since <2h>] [--json]
                                                 list receipts, newest first
  fleet done <sha|#n|branch> [--kind <k>] [--json]
                                                 exit 0 if a passing receipt (of <kind>, else of every kind seen) exists for that revision; 1 if not; 2 unresolvable
  fleet board [--json]                           every roled path with observed state: vacant · dead · idle · idle-holding-work · busy · busy-and-overdue
  fleet pool <checkout> [<kind> <n>] [--rewarm] [--tenant <t>]
                                                 create/top up N slots beside <checkout> (<basename>-<kind>-<i>), roled, named, warmed per pools.json
  fleet slots [<repo>] [--json]                  one line per slot: free · busy(<sid8>, <branch>) · dirty · orphaned(<sid8>) · missing [cold] [assigned(<branch>)]
  fleet assign <slot> <branch> ["<brief>"]       check <branch> out in a free slot and record the assignment (read at the slot's next SessionStart)
  fleet unassign <slot>                          clear it
  fleet dispatch <branch|#n> --as <rel> [--for <role>] [--due 45m] [--slot <name>] [--brief "…"] [--take]
                                                 the one declared act: an ownership row (change, relationship, accountable, due), placed in a slot when named
  fleet work [--for <role>] [--json]             every row with its observed state: dead · late · undeclared · working · idle · dispatched · done
  fleet reassign <branch|#n> --for <role>        move a change's rows to another accountable role (splitting a hub is this plus one roles.map line)
  fleet undispatch <branch|#n> [--as <rel>]      retire a change's rows
  fleet sync [--repo <r>]                        refresh the cache of open changes and the rows other machines declared on them
  fleet shadow-report [--since 24h] [--json]     the day's numbers from 'fleet hook <h> --shadow' running beside the installed hook
  fleet who <slot|key|#n|branch> [--json]        the live session holding it, or exit 1 saying who does not (never a substitute)
  fleet unowned [--repo <r>] [--json]            open changes whose head branch no live session here holds — scoped to this machine
  fleet handoff <branch> "<conclusion>" ["<next>"]  one-line handoff, replaced not appended; injected at the next SessionStart
  fleet role <checkout> <role> [--force] [--tenant <t>]
                                                 make a checkout a role for Claude and Codex - session-specific
                                                 instructions, hooks, and permissions per directory;
                                                 tenant: existing line, else inherited by prefix, else
                                                 $ORG_TENANT, else --tenant is required (no default);
                                                 --force to take over a path bound to a role that has no card
  fleet hook claude|codex                        the harness hook (reads one event on stdin)
  fleet mcp                                      the verbs as MCP tools over stdio`

// Usage prints the verb list and exits.
func Usage(code int) {
	fmt.Fprintln(os.Stderr, usage)
	os.Exit(code)
}

// Run dispatches one verb and applies its refusal, if any, to the process.
func Run(args []string) {
	err := Dispatch(args)
	if err == nil {
		os.Exit(0)
	}
	var r *Refusal
	if errors.As(err, &r) {
		if r.Msg != "" {
			fmt.Fprintln(os.Stderr, r.Msg)
		}
		os.Exit(r.Code)
	}
	fmt.Fprintln(os.Stderr, "fleet: "+err.Error())
	os.Exit(4)
}

// Dispatch runs one verb and returns its refusal, if any.
func Dispatch(args []string) error {
	if len(args) == 0 {
		return exitCode(2, usage)
	}
	fleet.MigrateLegacyKeys() // every entry into the substrate re-keys legacy state first
	verb, rest := args[0], args[1:]
	if strings.HasPrefix(verb, "x-") {
		return xtest(verb, rest)
	}
	arg := func(i int) string {
		if i < len(rest) {
			return rest[i]
		}
		return ""
	}
	switch verb {
	case "stop":
		reason := "no reason given"
		if len(rest) > 1 {
			reason = rest[1]
		}
		if arg(0) == "" {
			return exitCode(2, `usage: fleet stop <branch|slot:name> "<reason>"`)
		}
		return cmdStop(arg(0), reason, "operator", "", "", "")
	case "resume":
		if arg(0) == "" {
			return exitCode(2, "usage: fleet resume <branch|slot:name>")
		}
		return cmdResume(arg(0))
	case "revoke":
		m := regexp.MustCompile(`(\S+)\s+--to\s+(\S+)\s*(.*)`).FindStringSubmatch(strings.Join(rest, " "))
		if m == nil {
			return refuse(`usage: fleet revoke <branch> --to <session8> "<why>"`)
		}
		reason := m[3]
		if reason == "" {
			reason = "revoked"
		}
		return cmdRevoke(m[1], m[2], reason)
	case "sessions":
		return cmdSessions()
	case "leases":
		return cmdLeases()
	case "costs":
		return cmdCosts()
	case "decide":
		if len(rest) < 3 {
			return refuse(`usage: fleet decide <drop|park|ignore|rule> <subject> "<text>"`)
		}
		return cmdDecide(rest[0], rest[1], strings.Join(rest[2:], " "))
	case "undecide":
		return cmdUndecide(arg(0))
	case "decisions":
		return cmdDecisions()
	case "tier":
		base := "origin/main"
		if i := index(rest, "--base"); i >= 0 && i+1 < len(rest) {
			base = rest[i+1]
		}
		return cmdTier(base, contains(rest, "--json"))
	case "ready":
		return cmdReady(arg(0), arg(1), arg(2))
	}
	asJSON := contains(rest, "--json")
	plain := without(rest, "--json")
	parg := func(i int) string {
		if i < len(plain) {
			return plain[i]
		}
		return ""
	}
	switch verb {
	case "board":
		return cmdBoard(asJSON)
	case "slots":
		return cmdSlots(parg(0), asJSON)
	case "who":
		return cmdWho(parg(0), asJSON)
	case "unowned":
		repo, err := optValue(plain, "--repo", verb)
		if err != nil {
			return err
		}
		return cmdUnowned(repo, asJSON)
	case "receipts":
		since, err := optValue(plain, "--since", verb)
		if err != nil {
			return err
		}
		var secs float64
		if since != "" {
			secs = fleet.ParseDuration(since)
			if secs == 0 {
				return refuse("fleet receipts: --since wants a duration like 2h or 45m, got %s", fleet.PyRepr(since))
			}
		}
		kind, err := optValue(plain, "--kind", verb)
		if err != nil {
			return err
		}
		pos := positional(plain, "--kind", "--since")
		return cmdReceipts(first(pos), kind, secs, since != "", asJSON)
	case "done":
		kind, err := optValue(plain, "--kind", verb)
		if err != nil {
			return err
		}
		pos := positional(plain, "--kind")
		return CmdDone(first(pos), kind, asJSON)
	case "pool":
		tenant, err := optValue(plain, "--tenant", verb)
		if err != nil {
			return err
		}
		pos := positional(without(plain, "--rewarm"), "--tenant")
		if len(pos) == 0 {
			return refuse("usage: fleet pool <checkout> [<kind> <n>] [--rewarm] [--tenant <t>]")
		}
		return cmdPool(pos[0], at(pos, 1), at(pos, 2), contains(plain, "--rewarm"), tenant)
	case "assign":
		forRole, err := optValue(plain, "--for", verb)
		if err != nil {
			return err
		}
		pos := positional(plain, "--for")
		if len(pos) < 2 {
			return refuse(`usage: fleet assign <slot> <branch> ["<brief>"] [--for <role>]`)
		}
		return CmdAssign(pos[0], pos[1], strings.Join(pos[2:], " "), "", forRole)
	case "unassign":
		if parg(0) == "" {
			return refuse("usage: fleet unassign <slot>")
		}
		return cmdUnassign(parg(0))
	case "dispatch":
		vals := map[string]string{}
		for _, f := range []string{"--as", "--for", "--due", "--slot", "--brief"} {
			v, err := optValue(plain, f, verb)
			if err != nil {
				return err
			}
			vals[f] = v
		}
		pos := positional(without(plain, "--take"), "--as", "--for", "--due", "--slot", "--brief")
		return CmdDispatch(first(pos), vals["--as"], vals["--for"], vals["--due"], vals["--slot"], vals["--brief"], "", contains(plain, "--take"))
	case "reassign":
		forRole, err := optValue(plain, "--for", verb)
		if err != nil {
			return err
		}
		return CmdReassign(first(positional(plain, "--for")), forRole)
	case "undispatch":
		rel, err := optValue(plain, "--as", verb)
		if err != nil {
			return err
		}
		return cmdUndispatch(first(positional(plain, "--as")), rel)
	case "work":
		forRole, err := optValue(plain, "--for", verb)
		if err != nil {
			return err
		}
		return cmdWork(forRole, asJSON)
	case "sync":
		repo, err := optValue(plain, "--repo", verb)
		if err != nil {
			return err
		}
		return CmdSync(repo)
	case "shadow-report":
		since, err := optValue(plain, "--since", verb)
		if err != nil {
			return err
		}
		var from float64
		if since != "" {
			secs := fleet.ParseDuration(since)
			if secs == 0 {
				return refuse("fleet shadow-report: --since wants a duration like 24h, got %s", fleet.PyRepr(since))
			}
			from = fleet.Now() - secs
		}
		return cmdShadowReport(from, asJSON)
	case "receipt", "take", "drop":
		sess, err := optValue(rest, "--session", verb)
		if err != nil {
			return err
		}
		card, err := optValue(rest, "--card", verb)
		if err != nil {
			return err
		}
		takeover := contains(rest, "--takeover")
		a := positional(without(rest, "--takeover"), "--session", "--card")
		if verb == "receipt" {
			return cmdReceipt(at(a, 0), at(a, 1), at(a, 2), at(a, 3), sess, card, card != "")
		}
		if len(a) == 0 {
			return refuse("usage: fleet %s <slot:name>", verb)
		}
		if verb == "take" {
			return CmdTake(a[0], strings.Join(a[1:], " "), takeover, sess)
		}
		return CmdDrop(a[0], sess)
	case "handoff":
		return cmdHandoff(arg(0), arg(1), arg(2))
	case "role":
		u := "usage: fleet role <checkout> <role> [--force] [--tenant <t>]   e.g. fleet role ~/dev/mono-wt-1 <kind>:mono"
		tenant := ""
		if i := index(rest, "--tenant"); i >= 0 {
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				return refuse("%s\n  --tenant needs a value", u)
			}
			tenant = rest[i+1]
		}
		a := positional(without(rest, "--force"), "--tenant")
		if len(a) != 2 {
			return refuse("%s", u)
		}
		return cmdRole(a[0], a[1], contains(rest, "--force"), tenant, "")
	}
	return exitCode(2, usage)
}

// ---------- argument helpers ----------

func index(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

func contains(xs []string, s string) bool { return index(xs, s) >= 0 }

func without(xs []string, drop ...string) []string {
	var out []string
	for _, x := range xs {
		if !contains(drop, x) {
			out = append(out, x)
		}
	}
	return out
}

// positional drops the named flags and their values.
func positional(xs []string, flags ...string) []string {
	var out []string
	for i, x := range xs {
		if contains(flags, x) {
			continue
		}
		if i > 0 && contains(flags, xs[i-1]) {
			continue
		}
		if strings.HasPrefix(x, "--") && contains(flags, x) {
			continue
		}
		out = append(out, x)
	}
	return out
}

func first(xs []string) string { return at(xs, 0) }

func at(xs []string, i int) string {
	if i < len(xs) {
		return xs[i]
	}
	return ""
}

// optValue is the value after flag, or "". A flag with no value, or one followed by
// another option, is a typo and is refused rather than read as the value.
func optValue(args []string, flag, verb string) (string, error) {
	i := index(args, flag)
	if i < 0 {
		return "", nil
	}
	if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
		return "", refuse("fleet %s: %s needs a value", verb, flag)
	}
	return args[i+1], nil
}

// ---------- shared helpers ----------

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

func findSession(prefix string) (string, error) {
	d := fleet.Path("sessions")
	var hits []string
	ents, _ := os.ReadDir(d)
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".json") {
			hits = append(hits, strings.TrimSuffix(n, ".json"))
		}
	}
	if len(hits) != 1 {
		return "", refuse("fleet: session prefix %s matches %d sessions", fleet.PyRepr(prefix), len(hits))
	}
	return hits[0], nil
}

// hereScope keys per-branch state by the repo the operator is standing in. Refusing
// outside a repo is deliberate: `fleet stop main` from a home directory used to stop
// every repo's main at once.
func hereScope(branch string) (string, error) {
	key := fleet.Scope(cwd(), branch)
	if key == "" {
		return "", refuse("fleet: %s is not inside a git repo, so there is no `%s` here to act on.\n  Per-branch state is keyed by repo — cd to the checkout whose `%s` you mean.", cwd(), branch, branch)
	}
	return key, nil
}

var slotKeyRe = regexp.MustCompile(`\Aslot:[A-Za-z0-9._-]+\z`)

// keyFor is a verb's key argument: `slot:<name>` as given; anything else is a branch
// in the repo the operator is standing in.
func keyFor(arg string) (string, error) {
	if strings.HasPrefix(arg, "slot:") {
		if !slotKeyRe.MatchString(arg) {
			return "", refuse("fleet: %s is not a slot key; the form is slot:<name> with letters, digits, . _ -", fleet.PyRepr(arg))
		}
		return arg, nil
	}
	if strings.Contains(arg, ":") {
		return "", refuse("fleet: %s is neither a branch nor slot:<name>", fleet.PyRepr(arg))
	}
	return hereScope(arg)
}

func describeKey(key string) string {
	parts := fleet.KeyParts(key)
	if fleet.S(parts, "kind") == "resource" {
		return key
	}
	return fmt.Sprintf("%s in %s", fleet.S(parts, "branch"), fleet.S(parts, "repo"))
}

func roleOr(rec fleet.Rec, dflt string) string {
	if r := fleet.S(rec, "role"); r != "" {
		return r
	}
	return dflt
}

func ago(at float64) string { return fleet.FmtAge(fleet.Now() - at) }

// gitOut is git stdout, decoded as UTF-8. A failed diff (misspelled --base, no
// origin/main) used to read as an EMPTY diff, and an empty diff is T0: refuse instead.
func gitOut(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.quotepath=off"}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "no output"
		}
		head := args
		if len(head) > 2 {
			head = head[:2]
		}
		return "", refuse("fleet: git %s failed (%d): %s; there is no tier for a diff git cannot produce", strings.Join(head, " "), code, msg)
	}
	return string(out), nil
}

// gitTry is (rc, output) from git in dir; never refuses and never hangs.
func gitTry(dir string, timeout time.Duration, args ...string) (int, string) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = nil
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return 1, fmt.Sprintf("OSError: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return 0, strings.TrimSpace(stdout.String())
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), strings.TrimSpace(stderr.String())
		}
		return 1, fmt.Sprintf("OSError: %v", err)
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return 1, "TimeoutExpired: git " + strings.Join(args, " ")
	}
}

const gitTimeout = 60 * time.Second

func canon(p string) string { return fleet.CanonPath(p) }

// currentSession is the session this verb acts for: `--session <id8>` when given,
// else the live session whose record names this cwd, most recent event first. Two
// live sessions in one directory is a real ambiguity and is refused rather than
// guessed.
func currentSession(explicit string) (string, error) {
	if explicit != "" {
		sid, err := findSession(explicit)
		if err != nil {
			return "", err
		}
		rec := fleet.SessionRecord(sid)
		// --session disambiguates two live sessions in THIS directory; it is not a
		// way to borrow another session's identity.
		if !fleet.SessionAlive(rec) {
			return "", refuse("fleet: session %s is not live; --session names the tab you are running in, not a past one", fleet.Short(sid))
		}
		if canon(fleet.S(rec, "cwd")) != canon(cwd()) {
			return "", refuse("fleet: session %s is recorded at %s, not %s; --session only disambiguates live sessions in this directory", fleet.Short(sid), fleet.S(rec, "cwd"), cwd())
		}
		return sid, nil
	}
	want := canon(cwd())
	var live []fleet.Rec
	for _, r := range sessionRows() {
		if !fleet.B(r, "ended") && fleet.S(r, "cwd") != "" && canon(fleet.S(r, "cwd")) == want && fleet.SessionAlive(r) {
			live = append(live, r)
		}
	}
	if len(live) == 0 {
		return "", refuse("fleet: no live session is recorded at %s; run this from the session's own tab, or pass --session <id8>", cwd())
	}
	sortBy(live, func(a, b fleet.Rec) bool { return fleet.F(a, "last_event_at") > fleet.F(b, "last_event_at") })
	if len(live) > 1 && fleet.F(live[0], "last_event_at")-fleet.F(live[1], "last_event_at") < 2 {
		var ids []string
		for _, r := range live {
			ids = append(ids, fleet.Short(fleet.S(r, "session")))
		}
		return "", refuse("fleet: %d live sessions at %s (%s); pass --session <id8>", len(live), cwd(), strings.Join(ids, ", "))
	}
	return fleet.S(live[0], "session"), nil
}

func sessionRows() []fleet.Rec {
	d := fleet.Path("sessions")
	ents, _ := os.ReadDir(d)
	var out []fleet.Rec
	for _, e := range ents {
		r := fleet.ReadJSON(filepath.Join(d, e.Name()))
		if r != nil && fleet.S(r, "session") != "" {
			out = append(out, r)
		}
	}
	return out
}

func leaseRows() []fleet.Rec {
	d := fleet.Path("leases")
	ents, _ := os.ReadDir(d)
	var out []fleet.Rec
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp.") {
			continue
		}
		r := fleet.ReadJSON(filepath.Join(d, e.Name()))
		if r != nil && fleet.S(r, "key") != "" && fleet.S(r, "session") != "" {
			out = append(out, r)
		}
	}
	return out
}
