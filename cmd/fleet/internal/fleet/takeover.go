package fleet

import (
	"fmt"
	"sort"
)

// A branch lease blocks only a holder that is working on the branch. A live process
// is not that evidence: a desktop harness keeps one pid for every session it hosts,
// and a session that is out of usage keeps it for days without running a turn. On
// 2026-09-13 three such Codex sessions held five lanes' branches that way, and the
// only remedy was an operator command per branch. So a live holder that has shown no
// activity on a branch for IdleS is idle there, and the next writer takes the branch
// over, on the record. A resource lease is never idle: the machine it names can be
// busy with no session touching it.

// LastActiveOn is when a session last showed activity on key: the lease's own claim,
// its last recorded write there, or any hook event while its shell stood on that
// branch. A record that does not say where the session stands counts every event, so
// missing evidence is never read as idleness. Zero when the records show none.
func LastActiveOn(rec, lease Rec, key string) float64 {
	last := F(lease, "since")
	for _, w := range []Rec{M(M(rec, "last_writes"), key), M(rec, "last_write")} {
		if S(w, "key") == key && F(w, "at") > last {
			last = F(w, "at")
		}
	}
	if at := sessionKey(rec); (at == "" || at == key) && F(rec, "last_event_at") > last {
		last = F(rec, "last_event_at")
	}
	return last
}

// recentOn reports activity on key within IdleS: for a live holder, that it is active
// there rather than idle.
func recentOn(rec, lease Rec, key string) bool {
	return Now()-LastActiveOn(rec, lease, key) < float64(IdleS)
}

// sessionKey is the branch key of the checkout the session last stood in, or "".
func sessionKey(rec Rec) string {
	repo, branch := S(rec, "repo"), S(rec, "branch")
	if repo == "" || branch == "" {
		return ""
	}
	return "repo:" + repo + ":" + branch
}

// takeover is the fact a takeover leaves behind: on the new lease, in the hook's event
// row, and as the notice each side reads at its next event.
func takeover(key, branch, sid, role string, cur, holder Rec, state HeldState) Rec {
	why := "dead"
	if state == HeldIdle {
		why = "idle"
	}
	return Rec{"key": key, "branch": branch, "from": S(cur, "session"), "from_role": nilIfEmpty(S(cur, "role")),
		"to": sid, "to_role": nilIfEmpty(role), "why": why,
		"quiet_s": round1(Now() - LastActiveOn(holder, cur, key)), "at": Now()}
}

// takeoverNote is the takeover in the lease's one-line note.
func takeoverNote(t Rec) string {
	if S(t, "why") == "idle" {
		return fmt.Sprintf("took over from idle session %s (quiet on this branch for %s)", Short(S(t, "from")), FmtAge(F(t, "quiet_s")))
	}
	return "took over from dead session " + Short(S(t, "from"))
}

// announceTakeover leaves both sessions a notice for their next event. Best effort: the
// lease is the record, and each notice is checked against it when it is delivered.
func announceTakeover(t Rec) {
	defer func() {
		if r := recover(); r != nil {
			logError(Rec{"error": fmt.Sprintf("takeover notice: %v", r)})
		}
	}()
	for _, sid := range []string{S(t, "from"), S(t, "to")} {
		if err := noteLease(sid, t); err != nil {
			logError(Rec{"session": sid, "error": "takeover notice: " + err.Error()})
		}
	}
}

// noteLease files t under its key in sid's record, replacing any earlier notice for
// that key. A session with no record has nobody to tell, and none is made for it.
func noteLease(sid string, t Rec) error {
	return KeyLock("session:"+sid, func() error {
		p := Path("sessions", sid+".json")
		rec := ReadJSON(p)
		if rec == nil {
			return nil
		}
		notes := M(rec, "lease_notices")
		if notes == nil {
			notes = Rec{}
		}
		notes[S(t, "key")] = t
		rec["lease_notices"] = notes
		return WriteJSON(p, rec)
	})
}

// leaseNoticeLines is the takeovers sid has not been told about, one line each, and
// retires them. A notice the lease no longer bears out — an adapter unwound the
// takeover, or the branch has come back — retires unsaid.
func leaseNoticeLines(sid string, rec Rec) []string {
	notes := M(rec, "lease_notices")
	if len(notes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(notes))
	for k := range notes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		if line := noticeLine(sid, M(notes, k), Lease(k)); line != "" {
			lines = append(lines, line)
		}
	}
	retireNotices(sid, notes)
	return lines
}

func noticeLine(sid string, t, cur Rec) string {
	holder := S(cur, "session")
	switch {
	case IsMalformed(cur):
		return ""
	case S(t, "from") == sid && holder != sid:
		return takenLine(t, holder)
	case S(t, "to") == sid && holder == sid:
		return tookLine(t)
	}
	return ""
}

// takenLine tells a displaced session what happened to its branch while it was away.
func takenLine(t Rec, holder string) string {
	return fmt.Sprintf("[fleet] branch %s was taken from you %s ago by %s %s (%s; a quiet or stopped holder's branch goes to the next writer). %s. Your checkout was not touched: uncommitted work in it is still yours. Reconcile it with what the new holder committed before you write there again, and discard neither side.",
		S(t, "branch"), FmtAge(Now()-F(t, "at")), orSession(S(t, "to_role")), Short(S(t, "to")), displacedWhy(t), holderNow(t, holder))
}

// tookLine tells the taker whose work may still be in the branch's checkout.
func tookLine(t Rec) string {
	was := "whose session had stopped"
	if S(t, "why") == "idle" {
		was = "quiet on it for " + FmtAge(F(t, "quiet_s"))
	}
	return fmt.Sprintf("[fleet] you took branch %s from %s %s, %s; the lease records it. Anything it left uncommitted is still in the branch's checkout: preserve it — do not reset, clean, stash or check out over files you did not write.",
		S(t, "branch"), orSession(S(t, "from_role")), Short(S(t, "from")), was)
}

// displacedWhy is why the branch left its holder, in the holder's own terms.
func displacedWhy(t Rec) string {
	if S(t, "why") == "idle" {
		return "you had been quiet on it for " + FmtAge(F(t, "quiet_s"))
	}
	return "your session had stopped"
}

// holderNow is who holds the branch now, as a displaced session needs to hear it.
func holderNow(t Rec, holder string) string {
	if holder == S(t, "to") {
		return "It holds the branch now, and your writes there are refused while it stays active on it"
	}
	if holder == "" {
		return "Nobody holds it now"
	}
	return "It has since passed to " + Short(holder)
}

func orSession(role string) string {
	if role == "" {
		return "session"
	}
	return role
}

// retireNotices removes the notices that were read, under the record's lock. A notice
// filed for the same key after the read is kept.
func retireNotices(sid string, seen Rec) {
	err := KeyLock("session:"+sid, func() error {
		p := Path("sessions", sid+".json")
		rec := ReadJSON(p)
		notes := M(rec, "lease_notices")
		if notes == nil {
			return nil
		}
		for k := range seen {
			if F(M(notes, k), "at") == F(M(seen, k), "at") {
				delete(notes, k)
			}
		}
		if len(notes) == 0 {
			delete(rec, "lease_notices")
		}
		return WriteJSON(p, rec)
	})
	if err != nil {
		logError(Rec{"session": sid, "error": "retire lease notices: " + err.Error()})
	}
}
