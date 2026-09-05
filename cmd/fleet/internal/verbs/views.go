package verbs

// Views: pure functions over records the hooks already write. Nothing in this file
// is maintained by an agent. Every answer is derived at read time from sessions/,
// leases/, receipts/ and roles.map, plus git for the tree and gh for what is open
// remotely. Three records are written by verbs here and each states how it is
// superseded: assign/<slot>.json (spent when a session is told of it at SessionStart,
// or when the tree leaves its branch); warm/<slot>.json (stale when older than
// pools.json); prs/<repo>__<n>.json (overwritten by the next gh pr call about that
// change).

import (
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

// sessionsAt is the session records whose cwd is path or below it, newest event
// first. PRESENCE, not role. Ended records are kept so a caller can tell "was here,
// left" from "never here".
func sessionsAt(path string, rows []fleet.Rec) []fleet.Rec {
	if rows == nil {
		rows = sessionRows()
	}
	want := strings.TrimRight(canon(path), "/")
	var hits []fleet.Rec
	for _, r := range rows {
		c := fleet.S(r, "cwd")
		if c == "" {
			continue
		}
		cc := canon(c)
		if cc == want || strings.HasPrefix(cc, want+"/") {
			hits = append(hits, r)
		}
	}
	sortBy(hits, func(a, b fleet.Rec) bool { return fleet.F(a, "last_event_at") > fleet.F(b, "last_event_at") })
	return hits
}

// sessionState is one of the board's states from a session record, what it holds,
// and its lane's cadence: dead, idle-holding-work, idle, busy, busy-and-overdue.
func sessionState(rec fleet.Rec, holds []string, cadence float64) string {
	if !fleet.SessionAlive(rec) {
		return "dead"
	}
	if !fleet.B(rec, "turn_open") {
		if len(holds) > 0 {
			return "idle-holding-work"
		}
		return "idle"
	}
	opened := fleet.F(rec, "turn_open_at")
	if opened == 0 {
		opened = fleet.F(rec, "last_event_at")
	}
	if cadence > 0 && fleet.Now()-opened > cadence {
		return "busy-and-overdue"
	}
	return "busy"
}

func laneCadence(rec fleet.Rec, role string) float64 {
	if lane := fleet.M(rec, "lane"); lane != nil && fleet.Has(lane, "cadence") {
		return fleet.F(lane, "cadence")
	}
	if live := fleet.LaneOf(role); live != nil {
		return fleet.F(live, "cadence")
	}
	return 0
}

// BoardRow is one roled path with its observed state.
type BoardRow = map[string]any

// BoardRows is the join: every roled path in roles.map against the sessions observed
// there and the leases they hold. `vacant` is a roled path with no session record.
func BoardRows() []BoardRow {
	_, rows := fleet.MapRows(fleet.RolesMap())
	sessions, leases := sessionRows(), leaseRows()
	bySession := map[string][]string{}
	for _, l := range leases {
		// The occupancy lease for the seat itself is not work.
		if !fleet.B(l, "occupancy") {
			s := fleet.S(l, "session")
			bySession[s] = append(bySession[s], fleet.S(l, "key"))
		}
	}
	var out []BoardRow
	for _, mr := range rows {
		at := sessionsAt(mr.Path, sessions)
		var here, live []fleet.Rec
		for _, r := range at {
			if !fleet.B(r, "ended") {
				here = append(here, r)
				if fleet.SessionAlive(r) {
					live = append(live, r)
				}
			}
		}
		// A LIVE one first, else the newest not-ended record (a dead one).
		var rec fleet.Rec
		if len(live) > 0 {
			rec = live[0]
		} else if len(here) > 0 {
			rec = here[0]
		}
		var holds []string
		if rec != nil {
			holds = bySession[fleet.S(rec, "session")]
		}
		if holds == nil {
			holds = []string{}
		}
		cadence := laneCadence(rec, mr.Role)
		state := "vacant"
		if rec != nil {
			state = sessionState(rec, holds, cadence)
		}
		row := BoardRow{"path": mr.Path, "tenant": mr.Tenant, "role": mr.Role, "slot": nilIfEmpty(mr.Slot), "state": state,
			"session": nil, "branch": nil, "holds": holds, "last_event_at": nil, "last_event": nil, "turn_open_at": nil,
			"cadence": nilIfZero(cadence), "others": 0, "left": 0}
		if rec != nil {
			row["session"] = fleet.S(rec, "session")
			row["branch"] = nilIfEmpty(fleet.S(rec, "branch"))
			row["last_event_at"] = fleet.F(rec, "last_event_at")
			row["last_event"] = nilIfEmpty(fleet.S(rec, "last_event"))
			if fleet.B(rec, "turn_open") {
				opened := fleet.F(rec, "turn_open_at")
				if opened == 0 {
					opened = fleet.F(rec, "last_event_at")
				}
				row["turn_open_at"] = opened
			}
		}
		if len(here) > 0 {
			row["others"] = len(here) - 1
		}
		if len(at) > 0 {
			row["left"] = len(at) - len(here)
		}
		out = append(out, row)
	}
	return out
}

func nilIfZero(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

func cmdBoard(asJSON bool) error {
	rows := BoardRows()
	if asJSON {
		say("%s", jsonIndent(rows))
		return nil
	}
	if len(rows) == 0 {
		say("no roled paths in %s", fleet.RolesMap())
		return nil
	}
	order := map[string]int{"busy-and-overdue": 0, "dead": 1, "idle-holding-work": 2, "busy": 3, "idle": 4, "vacant": 5}
	sortBy(rows, func(a, b BoardRow) bool {
		oa, ob := order[fleet.S(a, "state")], order[fleet.S(b, "state")]
		if oa != ob {
			return oa < ob
		}
		return fleet.S(a, "role") < fleet.S(b, "role")
	})
	for _, r := range rows {
		who := "-"
		if s := fleet.S(r, "session"); s != "" {
			who = fleet.Short(s)
		}
		age := ""
		if at := fleet.F(r, "last_event_at"); at > 0 {
			age = fmt.Sprintf("last %s ago (%s)", ago(at), fleet.S(r, "last_event"))
		}
		holds := holdsOf(r)
		held := ""
		if len(holds) > 0 {
			var labels []string
			for _, k := range holds {
				labels = append(labels, fleet.KeyLabel(k))
			}
			held = fmt.Sprintf("holds %d: %s", len(holds), strings.Join(labels, ", "))
		}
		over := ""
		if fleet.S(r, "state") == "busy-and-overdue" {
			opened := fleet.F(r, "turn_open_at")
			if opened == 0 {
				opened = fleet.Now()
			}
			over = fmt.Sprintf("turn open %s, cadence %s", ago(opened), fleet.FmtAge(fleet.F(r, "cadence")))
		}
		extra := ""
		if others := int(fleet.F(r, "others")); others > 0 {
			extra += fmt.Sprintf("  (+%d more session(s) here)", others)
		}
		if left := int(fleet.F(r, "left")); fleet.S(r, "state") == "vacant" && left > 0 {
			extra += fmt.Sprintf("  (%d ended here)", left)
		}
		slot := fleet.S(r, "slot")
		if slot == "" {
			slot = "-"
		}
		branch := fleet.S(r, "branch")
		if branch == "" {
			branch = "-"
		}
		tail := held
		if tail == "" {
			tail = over
		}
		say("%-18s %-26s %-18s %-8s %-28s %-32s %s%s", fleet.S(r, "state"), fleet.S(r, "role"), slot, who, branch, age, tail, extra)
	}
	return nil
}

func holdsOf(r BoardRow) []string {
	switch h := r["holds"].(type) {
	case []string:
		return h
	case []any:
		var out []string
		for _, x := range h {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ---------- slots: pre-built, pre-roled, named worktrees ----------

// poolsConfig is ~/.fleet/pools.json: {"<checkout basename>": {"warm": ..., "slots": {"<kind>": n}}}.
// Per-repo domain data lives here and never in a lane manifest.
func poolsConfig() map[string]any {
	if cfg := fleet.ReadJSON(fleet.Path("pools.json")); cfg != nil {
		return cfg
	}
	return map[string]any{}
}

func warmCommands(cfg map[string]any) []string {
	switch w := cfg["warm"].(type) {
	case string:
		if strings.TrimSpace(w) != "" {
			return []string{w}
		}
	case []any:
		var out []string
		for _, c := range w {
			if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func warmRecord(slot string) fleet.Rec { return fleet.ReadJSON(fleet.Path("warm", fleet.Safe(slot)+".json")) }

// warmSlot runs the repo's warm commands in a slot, logs to ~/.fleet/warm/<slot>.log,
// and records the outcome. Synchronous and reported: a slot whose warm failed is
// listed `cold`, never silently slow. The commands run in the platform shell.
func warmSlot(dir, slot string, cmds []string) fleet.Rec {
	if len(cmds) == 0 {
		return nil
	}
	logp := fleet.Path("warm", fleet.Safe(slot)+".log")
	_ = os.MkdirAll(filepath.Dir(logp), 0o755)
	log, err := os.OpenFile(logp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil
	}
	t0 := fleet.Now()
	code := 0
	for _, c := range cmds {
		_, _ = log.WriteString("$ " + c + "\n")
		var cmd *exec.Cmd
		if fleet.IsWindows() {
			cmd = exec.Command("cmd", "/c", c)
		} else {
			cmd = exec.Command("sh", "-c", c)
		}
		cmd.Dir = dir
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			code = 1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			break
		}
	}
	log.Close()
	cmdsAny := make([]any, len(cmds))
	for i, c := range cmds {
		cmdsAny[i] = c
	}
	rec := fleet.Rec{"slot": slot, "commands": cmdsAny, "exit": float64(code), "at": fleet.Now(), "seconds": roundTo1(fleet.Now() - t0), "log": logp}
	_ = fleet.WriteJSON(fleet.Path("warm", fleet.Safe(slot)+".json"), rec)
	return rec
}

func roundTo1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

// SlotRow is one pooled worktree with its observed state.
type SlotRow = map[string]any

// SlotRows is every pooled worktree (a roles.map row with a fourth column) with its
// observed state: missing, busy(sid, branch), orphaned(sid), dirty, or free — plus
// `cold` and `assigned(branch)`. `git status --porcelain` is the one spawn: it
// reports staged changes as well as worktree ones, so index != HEAD is dirty.
func SlotRows(repo string) []SlotRow {
	_, rows := fleet.MapRows(fleet.RolesMap())
	sessions := sessionRows()
	var out []SlotRow
	for _, mr := range rows {
		if mr.Slot == "" {
			continue
		}
		rrepo := ""
		if i := strings.Index(mr.Role, ":"); i >= 0 {
			rrepo = mr.Role[i+1:]
		}
		if repo != "" && repo != rrepo && repo != filepath.Base(strings.TrimRight(mr.Path, "/")) {
			continue
		}
		row := SlotRow{"slot": mr.Slot, "path": mr.Path, "role": mr.Role, "repo": nilIfEmpty(rrepo), "state": "free", "session": nil,
			"branch": nil, "dirty": []string{}, "cold": false, "assigned": nil, "pooled": true}
		a := fleet.ReadJSON(fleet.Path("assign", fleet.Safe(mr.Slot)+".json"))
		if a != nil && fleet.S(a, "branch") != "" && fleet.S(a, "delivered_to") == "" {
			row["assigned"] = a // undelivered; retired below if the tree has already left the branch
		}
		if !isDir(mr.Path) {
			row["state"] = "missing"
			out = append(out, row)
			continue
		}
		var here, live []fleet.Rec
		for _, r := range sessionsAt(mr.Path, sessions) {
			if !fleet.B(r, "ended") {
				here = append(here, r)
				if fleet.SessionAlive(r) {
					live = append(live, r)
				}
			}
		}
		// The occupancy lease names the occupant — the same answer `fleet who` gives.
		seat := fleet.Lease("slot:" + mr.Slot)
		var occ fleet.Rec
		if seat != nil && !fleet.IsMalformed(seat) {
			for _, r := range live {
				if fleet.S(r, "session") == fleet.S(seat, "session") {
					occ = r
					break
				}
			}
		}
		if occ == nil && len(live) > 0 {
			occ = live[0]
		}
		if occ != nil {
			var others []string
			for _, r := range live {
				if fleet.S(r, "session") != fleet.S(occ, "session") {
					others = append(others, fleet.S(r, "session"))
				}
			}
			if others == nil {
				others = []string{}
			}
			row["state"], row["session"], row["branch"], row["others"] = "busy", fleet.S(occ, "session"), nilIfEmpty(fleet.S(occ, "branch")), others
		} else if len(here) > 0 {
			row["state"], row["session"], row["branch"] = "orphaned", fleet.S(here[0], "session"), nilIfEmpty(fleet.S(here[0], "branch"))
		}
		rc, status := gitTry(mr.Path, gitTimeout, "status", "--porcelain", "--untracked-files=normal")
		if rc != 0 {
			row["state"] = "broken"
			if status == "" {
				status = "git status failed"
			}
			row["dirty"] = []string{status}
		} else if status != "" {
			row["dirty"] = strings.Split(status, "\n")
			if row["state"] == "free" {
				row["state"] = "dirty"
			}
		}
		if row["state"] == "free" {
			rc, head := gitTry(mr.Path, gitTimeout, "rev-parse", "--abbrev-ref", "HEAD")
			if rc == 0 && head != "HEAD" {
				row["branch"] = head
			}
		}
		if am, ok := row["assigned"].(fleet.Rec); ok && fleet.S(row, "branch") != "" && fleet.S(am, "branch") != fleet.S(row, "branch") {
			row["assigned"] = nil // the tree left the assigned branch: the assignment is spent
		}
		cfg, _ := poolsConfig()[rrepo].(map[string]any)
		if len(warmCommands(cfg)) > 0 {
			w := warmRecord(mr.Slot)
			var cfgAt float64
			if st, err := os.Stat(fleet.Path("pools.json")); err == nil {
				cfgAt = float64(st.ModTime().UnixNano()) / 1e9
			}
			row["cold"] = !(w != nil && fleet.F(w, "exit") == 0 && fleet.F(w, "at") >= cfgAt)
		}
		out = append(out, row)
	}
	return out
}

func cmdSlots(repo string, asJSON bool) error {
	rows := SlotRows(repo)
	if asJSON {
		say("%s", jsonIndent(rows))
		return nil
	}
	if len(rows) == 0 {
		msg := "no slots"
		if repo != "" {
			msg += " for " + repo
		}
		say("%s; `fleet pool <checkout> <kind> <n>` creates them", msg)
		return nil
	}
	for _, r := range rows {
		var st string
		switch fleet.S(r, "state") {
		case "busy":
			branch := fleet.S(r, "branch")
			if branch == "" {
				branch = "detached"
			}
			st = fmt.Sprintf("busy(%s, %s)", fleet.Short(fleet.S(r, "session")), branch)
			if others, ok := r["others"].([]string); ok && len(others) > 0 {
				st += fmt.Sprintf(" +%d more", len(others))
			}
		case "orphaned":
			st = fmt.Sprintf("orphaned(%s)", fleet.Short(fleet.S(r, "session")))
		case "dirty":
			st = fmt.Sprintf("dirty (%d path(s))", len(dirtyOf(r)))
		default:
			st = fleet.S(r, "state")
		}
		tail := ""
		if fleet.B(r, "cold") {
			tail += " cold"
		}
		if a, ok := r["assigned"].(fleet.Rec); ok && a != nil {
			tail += fmt.Sprintf(" assigned(%s", fleet.S(a, "branch"))
			// The accountable role is named only when it is not the dispatcher.
			if f := fleet.S(a, "for"); f != "" && f != fleet.S(a, "by") {
				tail += " for " + f
			}
			tail += ")"
		}
		say("%-28s %-36s %-24s %s%s", fleet.S(r, "slot"), st, fleet.S(r, "role"), fleet.S(r, "path"), tail)
	}
	return nil
}

func dirtyOf(r SlotRow) []string {
	if d, ok := r["dirty"].([]string); ok {
		return d
	}
	return nil
}

// registeredWorktrees is the paths git itself registers as worktrees of checkout.
func registeredWorktrees(checkout string) map[string]bool {
	out := map[string]bool{}
	rc, text := gitTry(checkout, gitTimeout, "worktree", "list", "--porcelain")
	if rc != 0 {
		return out
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "worktree ") {
			out[canon(l[len("worktree "):])] = true
		}
	}
	return out
}

// cmdPool creates or tops up N slots for kind beside checkout:
// `<parent>/<basename>-<kind>-<i>`, each a detached worktree roled `<kind>:<basename>`
// with the slot name in the map's fourth column, written here and nowhere else.
// Idempotent: existing slots are kept, a slot with a live session is not touched.
func cmdPool(checkout, kind, nArg string, rewarm bool, tenant string) error {
	checkout = mapPath(checkout)
	if !isDir(checkout) || fleet.RepoID(checkout) == "" {
		return refuse("fleet pool: %s is not a git checkout", checkout)
	}
	base := filepath.Base(strings.TrimRight(checkout, "/"))
	parent := filepath.Dir(strings.TrimRight(checkout, "/"))
	// Beside, never nested: settings inherit down a tree, so a slot inside any checkout
	// would wear that checkout's role.
	if g, _ := fleet.GitDirs(parent); g != "" {
		return refuse("fleet pool: %s is inside a git checkout, so slots placed beside %s would be nested; move the checkout to a directory that is not inside a repo", parent, base)
	}
	cfg, _ := poolsConfig()[base].(map[string]any)
	wanted := map[string]int{}
	var order []string
	if kind != "" {
		n, err := strconv.Atoi(nArg)
		if nArg == "" || err != nil || !isDigits(nArg) {
			return refuse("usage: fleet pool <checkout> <kind> <n> [--rewarm] [--tenant <t>]")
		}
		wanted[kind] = n
		order = []string{kind}
	} else {
		slots, _ := cfg["slots"].(map[string]any)
		var ks []string
		for k := range slots {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			if f, ok := slots[k].(float64); ok && f == float64(int64(f)) {
				wanted[k] = int(f)
				order = append(order, k)
			}
		}
		if len(wanted) == 0 {
			return refuse("fleet pool: no kind given and pools.json has no slots for %s; run `fleet pool %s <kind> <n>`", base, checkout)
		}
	}
	for _, k := range order {
		if _, _, err := loadManifest(k); err != nil {
			return err
		}
	}
	// The tenant is settled BEFORE any worktree exists.
	if tenant == "" {
		tenant = fleet.TenantOf(checkout)
	}
	if tenant == "" {
		tenant = os.Getenv("ORG_TENANT")
	}
	if tenant == "" {
		return refuse("fleet pool: no tenant for slots of %s: %s has no roles.map line to inherit from and ORG_TENANT is unset. Next action: fleet pool %s ... --tenant <tenant>", base, checkout, checkout)
	}
	_, rows := fleet.MapRows(fleet.RolesMap())
	named := map[string]string{}
	for _, r := range rows {
		if r.Slot != "" {
			named[r.Slot] = r.Path
		}
	}
	cmds := warmCommands(cfg)
	var live []fleet.Rec
	for _, r := range sessionRows() {
		if fleet.S(r, "cwd") != "" && !fleet.B(r, "ended") && fleet.SessionAlive(r) {
			live = append(live, r)
		}
	}
	worktrees := registeredWorktrees(checkout)
	var made, kept, warmed []string
	for _, k := range order {
		for i := 1; i <= wanted[k]; i++ {
			slot := fmt.Sprintf("%s-%s-%d", base, k, i)
			p := filepath.Join(parent, slot)
			if other, ok := named[slot]; ok && canon(other) != canon(p) {
				return refuse("fleet pool: slot name %s already names %s in roles.map; two checkouts share the basename %s. Rename or move one of them, then retry", slot, other, fleet.PyRepr(base))
			}
			if seat := fleet.Lease("slot:" + slot); seat != nil && !fleet.IsMalformed(seat) && !fleet.B(seat, "occupancy") {
				s := fleet.S(seat, "session")
				if s == "" {
					s = "?"
				}
				return refuse("fleet pool: slot:%s is held as a machine resource (by %s); a seat cannot share a machine's name. Rename the checkout or the resource", slot, fleet.Short(s))
			}
			if isDir(p) && len(sessionsAt(p, live)) > 0 {
				kept = append(kept, slot) // occupied: never disturbed, not even re-roled or warmed
				continue
			}
			fresh := !isDir(p)
			if !fresh && !worktrees[canon(p)] {
				return refuse("fleet pool: %s exists but is not a worktree of %s (git worktree list does not know it); move it aside, or `git worktree prune` if it was one, then retry", p, checkout)
			}
			if fresh {
				rc, out := gitTry(checkout, 300*time.Second, "worktree", "add", "--detach", "-q", p)
				if rc != 0 {
					return refuse("fleet pool: git worktree add %s failed: %s", p, out)
				}
			}
			if err := cmdRole(p, k+":"+base, false, tenant, slot); err != nil {
				return err
			}
			if fresh {
				made = append(made, slot)
			} else {
				kept = append(kept, slot)
			}
			w := warmRecord(slot)
			if len(cmds) > 0 && (fresh || rewarm || !(w != nil && fleet.F(w, "exit") == 0)) {
				r := warmSlot(p, slot, cmds)
				status := "ok"
				if fleet.F(r, "exit") != 0 {
					status = fmt.Sprintf("FAILED exit %d", int(fleet.F(r, "exit")))
				}
				warmed = append(warmed, fmt.Sprintf("%s (%s, %vs)", slot, status, fleet.F(r, "seconds")))
			}
		}
	}
	var parts []string
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", k, wanted[k]))
	}
	line := fmt.Sprintf("pool %s: %s", base, strings.Join(parts, ", "))
	if len(made) > 0 {
		line += "; created " + strings.Join(made, ", ")
	}
	if len(kept) > 0 {
		line += "; kept " + strings.Join(kept, ", ")
	}
	if len(warmed) > 0 {
		line += "; warmed " + strings.Join(warmed, ", ")
	} else if len(cmds) == 0 {
		line += "; no warm command in pools.json"
	}
	say("%s", line)
	return nil
}

var branchNameRe = regexp.MustCompile(`\A[A-Za-z0-9._/@+-]+\z`)

// CmdAssign places work into a named slot: checks the branch out there and records
// the assignment. This is where *assigned* is written, attached to the act of
// dispatching. The decision and the checkout happen under the seat's own lock, the
// same lock a SessionStart in that slot takes; the fetch runs before it.
func CmdAssign(slot, branch, brief, by, forRole string) error {
	r := slotRow(slot)
	if r == nil {
		var names []string
		for _, x := range SlotRows("") {
			names = append(names, fleet.S(x, "slot"))
		}
		tail := "; `fleet pool` creates some"
		if len(names) > 0 {
			tail = "; slots: " + strings.Join(names, ", ")
		}
		return refuse("fleet assign: no slot named %s%s", fleet.PyRepr(slot), tail)
	}
	path := fleet.S(r, "path")
	rc, _ := gitTry(path, gitTimeout, "check-ref-format", "--branch", branch)
	if !branchNameRe.MatchString(branch) || strings.HasPrefix(branch, "-") || rc != 0 || shaRe.MatchString(branch) {
		return refuse("fleet assign: %s is not a branch name (a revision or a path is not assignable)", fleet.PyRepr(branch))
	}
	if err := refuseUnlessFree(slot, r); err != nil {
		return err
	}
	gitTry(path, 120*time.Second, "fetch", "--quiet", "origin", branch)
	var out error
	lerr := fleet.KeyLock("slot:"+slot, func() error {
		r := slotRow(slot)
		if r == nil {
			out = refuse("fleet assign: no slot named %s", fleet.PyRepr(slot))
			return nil
		}
		if err := refuseUnlessFree(slot, r); err != nil {
			out = err
			return nil
		}
		key := fleet.Scope(path, branch)
		cur := fleet.Lease(key)
		if fleet.IsMalformed(cur) {
			out = refuse("fleet assign: the lease file for %s is malformed (%s); inspect and remove it first", branch, fleet.S(cur, "malformed"))
			return nil
		}
		if cur != nil && fleet.SessionAlive(fleet.ReadJSON(fleet.Path("sessions", fleet.S(cur, "session")+".json"))) {
			out = refuse("fleet assign: %s is held by %s %s right now; assigning it to %s would put two sessions on one branch", branch, roleOr(cur, "a session"), fleet.Short(fleet.S(cur, "session")), slot)
			return nil
		}
		rc, txt := gitTry(path, gitTimeout, "checkout", "--quiet", branch)
		if rc != 0 {
			rc, txt = gitTry(path, gitTimeout, "checkout", "--quiet", "-b", branch, "origin/"+branch)
		}
		if rc != 0 {
			out = refuse("fleet assign: could not check out %s in %s: %s; nothing was assigned", branch, slot, txt)
			return nil
		}
		_, landed := gitTry(path, gitTimeout, "rev-parse", "--abbrev-ref", "HEAD")
		if landed != branch {
			out = refuse("fleet assign: after checkout %s is on %s, not %s; nothing was assigned", slot, fleet.PyRepr(landed), fleet.PyRepr(branch))
			return nil
		}
		if by == "" {
			by = "operator"
		}
		// `by` is who dispatched; `for` is the role accountable until the work is done. The
		// same session today, and they diverge the moment there are two hubs — a column on
		// the row, never a tree in configuration, is what makes splitting a hub two commands.
		if forRole == "" {
			forRole = by
		}
		return fleet.WriteJSON(fleet.Path("assign", fleet.Safe(slot)+".json"), fleet.Rec{
			"slot": slot, "branch": branch, "brief": nilIfEmpty(strings.TrimSpace(brief)), "at": fleet.Now(),
			"by": by, "for": forRole, "repo": nilIfEmpty(fleet.RepoID(path)), "path": path})
	})
	if lerr == fleet.ErrKeyBusy {
		return refuse("fleet assign: %s is being occupied or assigned right now and this command could not take its lock in time; nothing was assigned. `fleet slots`, then retry.", slot)
	}
	if lerr != nil {
		return lerr
	}
	if out != nil {
		return out
	}
	tail := ""
	if strings.TrimSpace(brief) != "" {
		tail = " — " + strings.TrimSpace(brief)
	}
	say("%s: assigned %s%s; tree at %s is on it", slot, branch, tail, path)
	return nil
}

func slotRow(slot string) SlotRow {
	for _, r := range SlotRows("") {
		if fleet.S(r, "slot") == slot {
			return r
		}
	}
	return nil
}

func refuseUnlessFree(slot string, r SlotRow) error {
	if fleet.S(r, "state") == "free" {
		return nil
	}
	who := ""
	if s := fleet.S(r, "session"); s != "" {
		who = fmt.Sprintf(" (%s)", fleet.Short(s))
	}
	return refuse("fleet assign: %s is %s%s; only a free slot takes an assignment. `fleet slots` to pick another.", slot, fleet.S(r, "state"), who)
}

func cmdUnassign(slot string) error {
	p := fleet.Path("assign", fleet.Safe(slot)+".json")
	if _, err := os.Stat(p); err != nil {
		return refuse("fleet unassign: %s has no assignment", slot)
	}
	fleet.Unlink(p)
	say("%s: assignment cleared", slot)
	return nil
}

// ---------- who / unowned: the lookup plane ----------

// describeHolder is a lease's holder as a resolution: (resolved, text, session
// record). Never a substitute: a dead or absent holder is said to be dead or absent.
func describeHolder(cur fleet.Rec) (bool, string, fleet.Rec) {
	holder := fleet.ReadJSON(fleet.Path("sessions", fleet.S(cur, "session")+".json"))
	alive := fleet.SessionAlive(holder)
	last := fleet.F(holder, "last_event_at")
	if last == 0 {
		last = fleet.F(cur, "since")
		if last == 0 {
			last = fleet.Now()
		}
	}
	age := ago(last)
	role := roleOr(cur, "no role")
	if alive {
		state := "idle"
		if fleet.B(holder, "turn_open") {
			state = "busy"
		}
		slot := ""
		if s := fleet.S(holder, "slot"); s != "" {
			slot = ", slot " + s
		}
		cwd := fleet.S(holder, "cwd")
		if cwd == "" {
			cwd = fleet.S(cur, "cwd")
		}
		return true, fmt.Sprintf("session %s (%s, %s, last event %s ago%s, cwd %s)", fleet.S(cur, "session"), role, state, age, slot, cwd), holder
	}
	return false, fmt.Sprintf("dead session %s (%s) last seen %s ago; nothing resolves to it", fleet.Short(fleet.S(cur, "session")), role, age), holder
}

// Who resolves a slot name, a lease key, a change number or a branch to the live
// session that holds it. The slot lease IS the name table; a slot whose lease is
// absent or whose holder is dead resolves to nobody, loudly, rather than to the
// nearest live session.
func Who(arg string) (bool, string, map[string]any) {
	a := strings.TrimSpace(arg)
	slots := fleet.PooledSlotNames()
	slotList := strings.Join(sortedKeys(slots), ", ")
	if slotList == "" {
		slotList = "none"
	}
	if strings.HasPrefix(a, "slot:") || slots[a] {
		key := a
		if !strings.HasPrefix(a, "slot:") {
			key = "slot:" + a
		}
		name := key[len("slot:"):]
		cur := fleet.Lease(key)
		if fleet.IsMalformed(cur) {
			return false, fmt.Sprintf("%s: lease file malformed (%s); resolve by hand", a, fleet.S(cur, "malformed")), map[string]any{"key": key}
		}
		if cur == nil {
			if slots[name] {
				return false, fmt.Sprintf("%s is unoccupied (no session has started there)", name), map[string]any{"key": key}
			}
			return false, fmt.Sprintf("%s is not held and is not a pooled slot; slots: %s", a, slotList), map[string]any{"key": key}
		}
		if slots[name] && !fleet.B(cur, "occupancy") {
			s := fleet.S(cur, "session")
			if s == "" {
				s = "?"
			}
			return false, fmt.Sprintf("%s: slot:%s is held as a machine resource by %s, not occupied; a seat's name is bound by the hook at SessionStart, never by `fleet take`", name, name, fleet.Short(s)), map[string]any{"key": key, "lease": cur}
		}
		ok, text, holder := describeHolder(cur)
		if ok {
			return true, fmt.Sprintf("%s -> %s", a, text), map[string]any{"key": key, "lease": cur, "session": holder}
		}
		return false, fmt.Sprintf("%s is unoccupied: %s", name, text), map[string]any{"key": key, "lease": cur, "session": holder}
	}
	var key string
	switch {
	case strings.HasPrefix(a, "repo:"):
		key = a
	case isChangeNumber(a):
		n, _ := strconv.Atoi(strings.TrimPrefix(a, "#"))
		hits := pullRecords(n, true, fleet.RepoID(cwd()))
		if len(hits) == 0 {
			return false, fmt.Sprintf("change #%d is not in this machine's cache (prs/); `gh pr view %d` in its repo once, or `fleet done %s`", n, n, a), map[string]any{}
		}
		if len(hits) > 1 {
			return false, fmt.Sprintf("change #%d is cached for %d repos; run this inside the one you mean", n, len(hits)), map[string]any{"hits": hits}
		}
		key = fmt.Sprintf("repo:%s:%s", fleet.S(hits[0], "repo"), fleet.S(hits[0], "branch"))
		a = fmt.Sprintf("#%d (%s, cached from a gh pr call %s ago)", int(fleet.F(hits[0], "number")), fleet.S(hits[0], "branch"), ago(fleet.F(hits[0], "at")))
	default:
		key = fleet.Scope(cwd(), a)
		if key == "" {
			return false, fmt.Sprintf("%s is not a slot, not a key, and %s is not inside a repo where it could be a branch; slots: %s", a, cwd(), slotList), map[string]any{}
		}
	}
	cur := fleet.Lease(key)
	if fleet.IsMalformed(cur) {
		return false, fmt.Sprintf("%s: lease file malformed (%s)", a, fleet.S(cur, "malformed")), map[string]any{"key": key}
	}
	if cur == nil {
		tail := ""
		if len(slots) > 0 && !strings.HasPrefix(a, "repo:") {
			tail = "; slots: " + slotList
		}
		return false, fmt.Sprintf("%s: nobody holds %s on this machine%s", a, fleet.KeyLabel(key), tail), map[string]any{"key": key}
	}
	ok, text, holder := describeHolder(cur)
	if ok {
		return true, fmt.Sprintf("%s -> %s", a, text), map[string]any{"key": key, "lease": cur, "session": holder}
	}
	return false, fmt.Sprintf("%s: held by %s", a, text), map[string]any{"key": key, "lease": cur, "session": holder}
}

func cmdWho(arg string, asJSON bool) error {
	if arg == "" {
		return refuse("usage: fleet who <slot|key|#change|branch>")
	}
	ok, text, detail := Who(arg)
	if asJSON {
		out := map[string]any{"resolved": ok, "text": text}
		for k, v := range detail {
			if k != "session" || v != nil {
				out[k] = v
			}
		}
		say("%s", jsonIndent(out))
	} else {
		say("%s", text)
	}
	if ok {
		return nil
	}
	return exitCode(1, "")
}

var originRe = regexp.MustCompile(`github\.com[:/]([\w.-]+)/([\w.-]+?)(?:\.git)?$`)

// githubReposHere is github owner/name -> local repo ids this machine knows: from
// the cache, and from the cwd's origin remote when it is a GitHub URL.
func githubReposHere() map[string]map[string]bool {
	pairs := map[string]map[string]bool{}
	add := func(gh, rid string) {
		if pairs[gh] == nil {
			pairs[gh] = map[string]bool{}
		}
		pairs[gh][rid] = true
	}
	for _, r := range pullRecords(0, false, "") {
		if g := fleet.S(r, "github"); g != "" {
			add(g, fleet.S(r, "repo"))
		}
	}
	if rid := fleet.RepoID(cwd()); rid != "" {
		if rc, url := gitTry(cwd(), gitTimeout, "remote", "get-url", "origin"); rc == 0 {
			if m := originRe.FindStringSubmatch(url); m != nil {
				add(m[1]+"/"+m[2], rid)
			}
		}
	}
	return pairs
}

// Unowned is open changes with nobody working on them, on this machine. *Working*
// derives from the lease on the change's head branch held by a live session. The
// scope is stated in the result because it is a real limit.
func Unowned(repo string) map[string]any {
	pairs := githubReposHere()
	if repo != "" {
		filtered := map[string]map[string]bool{}
		for g, rids := range pairs {
			if repo == g || repo == g[strings.LastIndex(g, "/")+1:] || rids[repo] {
				filtered[g] = rids
			}
		}
		pairs = filtered
	}
	assigns := map[[2]string]fleet.Rec{}
	d := fleet.Path("assign")
	ents, _ := os.ReadDir(d)
	for _, e := range ents {
		a := fleet.ReadJSON(filepath.Join(d, e.Name()))
		if a != nil && fleet.S(a, "branch") != "" {
			assigns[[2]string{fleet.S(a, "repo"), fleet.S(a, "branch")}] = a
		}
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "?"
	}
	out := map[string]any{"scope": fmt.Sprintf("this machine (%s); leases here cannot see sessions elsewhere", host),
		"repos": map[string]any{}, "unowned": []map[string]any{}, "working": []map[string]any{}}
	repos := out["repos"].(map[string]any)
	var unowned, working []map[string]any
	var ghs []string
	for g := range pairs {
		ghs = append(ghs, g)
	}
	sort.Strings(ghs)
	for _, gh := range ghs {
		rids := pairs[gh]
		data, why := ghJSON("gh", "pr", "list", "--repo", gh, "--state", "open", "--json", "number,headRefName,url,title", "--limit", "100")
		list, _ := data.([]any)
		var changes []map[string]any
		if data == nil {
			var cached []fleet.Rec
			for rid := range rids {
				cached = append(cached, pullRecords(0, false, rid)...)
			}
			repos[gh] = fmt.Sprintf("gh could not list open changes (%s); falling back to %d cached entries of unknown state", why, len(cached))
			for _, c := range cached {
				changes = append(changes, map[string]any{"number": fleet.F(c, "number"), "headRefName": fleet.S(c, "branch"), "url": c["url"], "title": nil, "cached": true})
			}
		} else {
			repos[gh] = fmt.Sprintf("%d open", len(list))
			for _, c := range list {
				if cm, ok := c.(map[string]any); ok {
					changes = append(changes, cm)
				}
			}
		}
		var ridList []string
		for rid := range rids {
			ridList = append(ridList, rid)
		}
		sort.Strings(ridList)
		for _, c := range changes {
			branch := fleet.S(c, "headRefName")
			var holders []fleet.Rec
			last := ""
			for _, rid := range ridList {
				cur := fleet.Lease("repo:" + rid + ":" + branch)
				if cur != nil && !fleet.IsMalformed(cur) && fleet.S(cur, "session") != "" {
					last = fleet.S(cur, "session")
					if h := fleet.ReadJSON(fleet.Path("sessions", last+".json")); fleet.SessionAlive(h) {
						holders = append(holders, h)
					}
				}
			}
			var assigned any
			for _, rid := range ridList {
				if a, ok := assigns[[2]string{rid, branch}]; ok && fleet.S(a, "delivered_to") == "" {
					assigned = a
					break
				}
			}
			row := map[string]any{"repo": gh, "number": c["number"], "branch": branch, "url": c["url"], "title": c["title"],
				"cached_only": fleet.B(c, "cached"), "assigned": assigned}
			if len(holders) > 0 {
				row["session"], row["role"], row["slot"] = fleet.S(holders[0], "session"), holders[0]["role"], holders[0]["slot"]
				working = append(working, row)
				continue
			}
			row["last_holder"] = nilIfEmpty(last)
			unowned = append(unowned, row)
		}
	}
	if unowned != nil {
		out["unowned"] = unowned
	}
	if working != nil {
		out["working"] = working
	}
	return out
}

func cmdUnowned(repo string, asJSON bool) error {
	u := Unowned(repo)
	if asJSON {
		say("%s", jsonIndent(u))
		return nil
	}
	say("unowned on %s", fleet.S(u, "scope"))
	repos, _ := u["repos"].(map[string]any)
	var ghs []string
	for g := range repos {
		ghs = append(ghs, g)
	}
	sort.Strings(ghs)
	for _, g := range ghs {
		say("  %s: %s", g, repos[g])
	}
	if len(repos) == 0 {
		say("  no GitHub repos known here: run inside a checkout with a github origin, or after a session has run `gh pr view`")
	}
	unowned, _ := u["unowned"].([]map[string]any)
	for _, r := range unowned {
		a := ""
		if am, ok := r["assigned"].(fleet.Rec); ok && am != nil {
			a = fmt.Sprintf("  assigned to %s %s ago, nobody there", fleet.S(am, "slot"), ago(fleet.F(am, "at")))
			if f := fleet.S(am, "for"); f != "" {
				a += " (for " + f + ")"
			}
		}
		last := ""
		if lh := fleet.S(r, "last_holder"); lh != "" {
			last = "  last held by dead session " + fleet.Short(lh)
		}
		say("  UNOWNED  #%-6v %-40s %s%s%s", numOf(r["number"]), fleet.S(r, "branch"), cut(fleet.S(r, "title"), 50), a, last)
	}
	working, _ := u["working"].([]map[string]any)
	for _, r := range working {
		tail := ""
		if s := fleet.S(r, "slot"); s != "" {
			tail = " in " + s
		}
		say("  working  #%-6v %-40s %s %s%s", numOf(r["number"]), fleet.S(r, "branch"), roleOr(r, "session"), fleet.Short(fleet.S(r, "session")), tail)
	}
	if len(unowned) == 0 && len(repos) > 0 {
		say("  nothing open is unowned here")
	}
	return nil
}

func numOf(v any) any {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return int64(f)
	}
	return v
}
