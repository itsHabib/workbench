package verbs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func cmdStop(arg, reason, by, exc, key, holder string) error {
	if key == "" {
		k, err := keyFor(arg)
		if err != nil {
			return err
		}
		key = k
	}
	parts := fleet.KeyParts(key)
	branch := fleet.S(parts, "branch")
	if branch == "" {
		branch = key
	}
	rec := fleet.Rec{"key": key, "branch": branch, "repo": nilIfEmpty(fleet.S(parts, "repo")), "reason": reason,
		"by": by, "at": fleet.Now(), "except": nilIfEmpty(exc), "holder": nilIfEmpty(holder)}
	if err := fleet.WriteJSON(fleet.KeyFile("stop", key), rec); err != nil {
		return err
	}
	tail := ""
	if exc != "" {
		tail = fmt.Sprintf(" (except %s)", fleet.Short(exc))
	}
	say("stop flag set on %s%s", describeKey(key), tail)
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func cmdResume(arg string) error {
	key, err := keyFor(arg)
	if err != nil {
		return err
	}
	fleet.Unlink(fleet.KeyFile("stop", key))
	say("stop flag lifted on %s", describeKey(key))
	return nil
}

func cmdRevoke(arg, toPrefix, reason string) error {
	key, err := keyFor(arg)
	if err != nil {
		return err
	}
	sid, err := findSession(toPrefix)
	if err != nil {
		return err
	}
	rec := fleet.SessionRecord(sid)
	if strings.HasPrefix(key, "slot:") && fleet.PooledSlotNames()[key[len("slot:"):]] {
		// A seat names the session sitting in that worktree. Handing it to a session
		// elsewhere would make `fleet who` point at a tab in another directory.
		_, rows := fleet.MapRows(fleet.RolesMap())
		slotPath := ""
		for _, r := range rows {
			if r.Slot == key[len("slot:"):] {
				slotPath = r.Path
			}
		}
		if slotPath == "" || len(sessionsAt(slotPath, []fleet.Rec{rec})) == 0 {
			where := slotPath
			if where == "" {
				where = "that slot"
			}
			return refuse("fleet revoke: %s is a seat; it can only be handed to a session whose cwd is in %s, and %s is at %s", key, where, fleet.Short(sid), fleet.S(rec, "cwd"))
		}
	}
	cur := fleet.Lease(key)
	if fleet.IsMalformed(cur) {
		return refuse("fleet revoke: the lease file for %s is malformed (%s); inspect and remove it first", key, fleet.S(cur, "malformed"))
	}
	// Take the lease first and name the stand-down after the session it ACTUALLY
	// displaced, read inside TakeLease's own critical section.
	was, err := fleet.TakeLease(key, fleet.KeyLabel(key), sid, fleet.S(rec, "role"), fleet.S(rec, "cwd"), "revoked: "+reason)
	if err != nil {
		return err
	}
	displaced := ""
	if s := fleet.S(was, "session"); s != "" && s != sid {
		displaced = s
	}
	if err := cmdStop(arg, reason, "operator", sid, key, displaced); err != nil {
		return err
	}
	say("lease on %s -> %s %s", describeKey(key), roleOr(rec, "session"), fleet.Short(sid))
	return nil
}

// CmdTake leases a resource for this session: refused if held.
func CmdTake(key, why string, takeover bool, session string) error {
	if !strings.HasPrefix(key, "slot:") {
		return refuse("fleet take: only resources are taken by hand (slot:<name>); a branch is leased by writing to it")
	}
	key, err := keyFor(key)
	if err != nil {
		return err
	}
	name := key[len("slot:"):]
	if fleet.PooledSlotNames()[name] {
		return refuse("fleet take: %s is a pooled worktree's seat, not a machine; a seat is occupied by starting a session in it (`fleet slots`, `fleet who %s`), never taken by hand", key, name)
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	rec := fleet.SessionRecord(sid)
	var out error
	var taken bool
	// Read, decide and write inside the key's lock.
	lerr := fleet.KeyLock(key, func() error {
		state, cur := fleet.HeldByOther(key, sid)
		switch {
		case state == fleet.HeldMalformed:
			out = refuse("fleet take: the lease file for %s is malformed (%s); inspect and remove it first", key, fleet.S(cur, "malformed"))
			return nil
		case state == fleet.HeldFree && cur != nil:
			say("%s: already held by you (%s)", key, fleet.Short(sid))
			return nil
		case state == fleet.HeldOrphaned && !takeover:
			out = refuse("fleet take: %s is held by dead session %s (since %s ago). A dead holder's resource may still be running. Confirm it is quiet, then: fleet take %s --takeover \"<what you checked>\"", key, fleet.Short(fleet.S(cur, "session")), ago(fleet.F(cur, "since")), key)
			return nil
		case state != fleet.HeldFree && state != fleet.HeldOrphaned:
			out = refuse("fleet take: %s is held by %s %s since %s ago. Wait for `fleet leases` to show it free, or ask the operator for `fleet revoke %s --to %s \"<reason>\"`.", key, roleOr(cur, "a session"), fleet.Short(fleet.S(cur, "session")), ago(fleet.F(cur, "since")), key, fleet.Short(sid))
			return nil
		}
		var note any
		if state == fleet.HeldOrphaned {
			w := why
			if w == "" {
				w = "no reason given"
			}
			s := fleet.S(cur, "session")
			if s == "" {
				s = "?"
			}
			note = fmt.Sprintf("takeover of dead session %s: %s", fleet.Short(s), w)
		} else if why != "" {
			note = why
		}
		taken = true
		return fleet.WriteLease(key, fleet.LeaseRecord(key, sid, fleet.S(rec, "role"), fleet.S(rec, "cwd"), note))
	})
	if lerr == fleet.ErrKeyBusy {
		return refuse("fleet take: %s is being handed over right now and this command could not take it in time; nothing was written. Next action: `fleet leases`, then retry.", key)
	}
	if lerr != nil {
		return lerr
	}
	if out != nil {
		return out
	}
	if taken {
		tail := ""
		if why != "" {
			tail = " — " + why
		}
		say("%s: taken by %s %s%s", key, roleOr(rec, "session"), fleet.Short(sid), tail)
	}
	return nil
}

// CmdDrop releases a resource this session holds.
func CmdDrop(key, session string) error {
	if !strings.HasPrefix(key, "slot:") {
		return refuse("fleet drop: only resources are dropped by hand (slot:<name>)")
	}
	key, err := keyFor(key)
	if err != nil {
		return err
	}
	if fleet.PooledSlotNames()[key[len("slot:"):]] {
		return refuse("fleet drop: %s is a pooled worktree's seat; it is released when its session ends, never dropped by hand (`fleet revoke %s --to <session>` moves it)", key, key)
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	// Read for the message; DropLease re-confirms ownership under the key's lock.
	state, cur := fleet.HeldByOther(key, sid)
	if cur == nil {
		say("%s: not held", key)
		return nil
	}
	if state == fleet.HeldMalformed {
		return refuse("fleet drop: the lease file for %s is malformed (%s); inspect and remove it first", key, fleet.S(cur, "malformed"))
	}
	if state != fleet.HeldFree {
		return refuse("fleet drop: %s is held by %s %s, not by you (%s); only the holder drops it (or the operator revokes it)", key, roleOr(cur, "a session"), fleet.Short(fleet.S(cur, "session")), fleet.Short(sid))
	}
	freed, err := fleet.DropLease(key, sid)
	if err != nil {
		return err
	}
	if !freed {
		return refuse("fleet drop: %s changed hands while this drop was being decided; nothing was removed. Next action: `fleet leases` to see who holds it now.", key)
	}
	say("%s: dropped by %s", key, fleet.Short(sid))
	return nil
}

func cmdSessions() error {
	d := fleet.Path("sessions")
	ents, _ := os.ReadDir(d)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		r := fleet.ReadJSON(filepath.Join(d, n))
		if r == nil || fleet.B(r, "ended") {
			continue
		}
		alive := fleet.SessionAlive(r)
		state := "stale"
		if alive && fleet.B(r, "turn_open") {
			state = "RUNNING"
		} else if alive {
			state = "idle"
		}
		where := "-"
		if b := fleet.S(r, "branch"); b != "" {
			repo := fleet.S(r, "repo")
			if repo == "" {
				repo = "?"
			}
			where = repo + ":" + b
		}
		say("%s  %-8s %-28s %-40s last %s ago (%s)", fleet.Short(fleet.S(r, "session")), state, roleOr(r, "-"), where, ago(fleet.F(r, "last_event_at")), fleet.S(r, "last_event"))
	}
	return nil
}

func cmdCosts() error {
	for _, r := range fleet.CostRows() {
		say("%-40s %6ds  n=%d", r.Sig, int(r.Med), r.N)
	}
	return nil
}

// cmdLeases lists every lease, resource rows first; a dead resource holder is
// orphaned, a dead branch holder is DEAD; a file that does not parse is MALFORMED and
// a leftover `.tmp.*` is STRAY. Nothing here infers free from garbage.
func cmdLeases() error {
	d := fleet.Path("leases")
	ents, _ := os.ReadDir(d)
	type row struct {
		pri  int
		line string
	}
	var rows []row
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if strings.HasPrefix(n, ".tmp.") {
			rows = append(rows, row{2, fmt.Sprintf("STRAY      %s  (leftover from an interrupted take; safe to delete)", n)})
			continue
		}
		p := filepath.Join(d, n)
		r := fleet.ReadJSON(p)
		if r != nil && fleet.S(r, "repo") != "" && fleet.S(r, "branch") != "" && fleet.S(r, "key") == "" {
			s := fleet.S(r, "session")
			if s == "" {
				s = "?"
			}
			rows = append(rows, row{3, fmt.Sprintf("LEGACY     %s  (%s %s, held by %s) — a pre-key record left in place because the new key is already taken. Two claims on one branch: decide which holder is real, then remove the other.", p, fleet.S(r, "repo"), fleet.S(r, "branch"), fleet.Short(s))})
			continue
		}
		if r == nil || fleet.S(r, "key") == "" || fleet.S(r, "session") == "" {
			rows = append(rows, row{3, fmt.Sprintf("MALFORMED  %s  (refused on every verb until an operator removes it)", p)})
			continue
		}
		alive := fleet.SessionAlive(fleet.ReadJSON(fleet.Path("sessions", fleet.S(r, "session")+".json")))
		res := fleet.S(r, "kind") == "resource"
		live := "live"
		if !alive {
			live = "DEAD holder"
			if res {
				live = "orphaned"
			}
		}
		what := fleet.S(r, "key")
		kind := "resource"
		pri := 0
		if !res {
			repo, branch := fleet.S(r, "repo"), fleet.S(r, "branch")
			if repo == "" {
				repo = "?"
			}
			if branch == "" {
				branch = "?"
			}
			what = fmt.Sprintf("%-26s %-28s", repo, branch)
			kind = "branch  "
			pri = 1
		}
		rows = append(rows, row{pri, fmt.Sprintf("%s  %-56s %-24s %s  %s  since %s ago", kind, what, roleOr(r, "-"), fleet.Short(fleet.S(r, "session")), live, ago(fleet.F(r, "since")))})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].pri < rows[j].pri })
	for _, r := range rows {
		say("%s", r.line)
	}
	return nil
}

func cmdDecide(kind, subject, text string) error {
	switch kind {
	case "drop", "park", "ignore", "rule":
	default:
		return refuse("fleet decide: kind must be drop | park | ignore | rule")
	}
	n := 1
	if b, err := os.ReadFile(fleet.Path("decisions.jsonl")); err == nil {
		n += strings.Count(string(b), "\n")
	}
	row := fleet.Rec{"id": fmt.Sprintf("d%d", n), "at": fleet.Now(), "kind": kind, "subject": subject, "text": text, "by": "operator"}
	if err := fleet.AppendJSONL(fleet.Path("decisions.jsonl"), row); err != nil {
		return err
	}
	say("%s %s %s: %s", fleet.S(row, "id"), kind, subject, text)
	return nil
}

func cmdUndecide(did string) error {
	if err := fleet.AppendJSONL(fleet.Path("decisions.jsonl"), fleet.Rec{"kind": "close", "id": did, "at": fleet.Now()}); err != nil {
		return err
	}
	say("%s retired", did)
	return nil
}

func cmdDecisions() error {
	for _, d := range fleet.OpenDecisions() {
		subject := fleet.S(d, "subject")
		if subject == "" {
			subject = "-"
		}
		say("%-5s %-7s %-28s %s  (%s ago)", fleet.S(d, "id"), fleet.S(d, "kind"), subject, fleet.S(d, "text"), ago(fleet.F(d, "at")))
	}
	return nil
}

func cmdHandoff(branch, conclusion, nxt string) error {
	if branch == "" || strings.TrimSpace(conclusion) == "" {
		return refuse(`usage: fleet handoff <branch> "<conclusion>" ["<next>"]`)
	}
	key, err := hereScope(branch)
	if err != nil {
		return err
	}
	sha, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	sha = strings.TrimSpace(sha)
	if sha == "" {
		sha = "?"
	}
	rec := fleet.Rec{"key": key, "branch": branch, "repo": nilIfEmpty(fleet.S(fleet.KeyParts(key), "repo")), "sha": sha,
		"conclusion": strings.TrimSpace(conclusion), "next": strings.TrimSpace(nxt), "at": fleet.Now()}
	if err := fleet.WriteJSON(fleet.KeyFile("handoff", key), rec); err != nil {
		return err
	}
	say("handoff on %s @ %s: %s", branch, fleet.Short(sha), strings.TrimSpace(conclusion))
	return nil
}

func sortBy[T any](xs []T, less func(a, b T) bool) {
	sort.SliceStable(xs, func(i, j int) bool { return less(xs[i], xs[j]) })
}
