package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StopFlag is the stop flag on key, or nil when there is none. A file that exists
// but cannot be read is reported as malformed, never as absent: ReadJSON maps any
// failure to nil, so a permissions error on a real stand-down would have read as
// "no flag" and the session it was addressed to would have carried on.
func StopFlag(key string) Rec {
	if key == "" {
		return nil
	}
	p := KeyFile("stop", key)
	if !exists(p) {
		return nil
	}
	if rec := ReadJSON(p); rec != nil {
		return rec
	}
	return Rec{"malformed": p, "reason": fmt.Sprintf("the stop flag file %s does not parse", p)}
}

// Lease is the record for key: nil when free, the record when held, or a
// {"malformed": <path>} record when a file exists that does not parse, lacks
// key/session, or names a different key. Malformed is never free and never taken
// over: every reader refuses naming the file.
func Lease(key string) Rec {
	if key == "" {
		return nil
	}
	p := KeyFile("leases", key)
	if !exists(p) {
		return nil
	}
	rec := ReadJSON(p)
	if rec == nil || S(rec, "key") == "" || S(rec, "session") == "" {
		return Rec{"malformed": p}
	}
	if S(rec, "key") != key {
		// The record must name the key it was read under. Safe() maps both `/` and `:`
		// to `__`, so `feat/x` and `feat__x` — both legal git names — share one
		// filename. Refused by name, like everything else that cannot be trusted.
		return Rec{"malformed": fmt.Sprintf("%s holds key %s but was read as %s", p, PyRepr(S(rec, "key")), PyRepr(key))}
	}
	return rec
}

// PyRepr renders a string the way a Python f-string's !r did, since refusal texts
// that carried it are asserted by the suite.
func PyRepr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

// IsMalformed reports a malformed marker record.
func IsMalformed(rec Rec) bool { return rec != nil && Has(rec, "malformed") }

// LeaseRecord is a complete lease for key held by sid.
func LeaseRecord(key, sid, role, cwd string, note any) Rec {
	parts := KeyParts(key)
	kind := S(parts, "kind")
	if kind == "" {
		kind = "branch"
	}
	return Rec{"key": key, "kind": kind, "repo": nilIfEmpty(S(parts, "repo")),
		"branch": nilIfEmpty(S(parts, "branch")), "name": nilIfEmpty(S(parts, "name")),
		"session": sid, "role": nilIfEmpty(role), "cwd": nilIfEmpty(cwd), "since": Now(), "note": note}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// WriteLease publishes a complete record at key. Only ever called with the key's
// lock held, which is why a plain replace is right. The temp-then-rename is still
// used so the final path never names a partial file to a reader, which takes no lock.
func WriteLease(key string, rec Rec) error {
	if ReadOnly {
		return nil
	}
	final := KeyFile("leases", key)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp := Path("leases", fmt.Sprintf(".tmp.%s.%d", Safe(key), os.Getpid()))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(DumpJSON(rec)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// AcquireLease takes key if it is free. True when this session now holds it.
func AcquireLease(key string, rec Rec) (bool, error) {
	got := false
	err := KeyLock(key, func() error {
		if Lease(key) != nil {
			return nil
		}
		got = true
		return WriteLease(key, rec)
	})
	return got, err
}

// TakeLease is the operator override: hand key to sid whatever it currently says
// (`fleet revoke --to`). Unconditional in effect, but it takes the key's lock like
// every other mutation, and it can afford to: there is nothing a kill can leave
// behind. Returns the record it displaced, read inside the same critical section, so
// the caller can name the right session on the stand-down flag.
func TakeLease(key, branch, sid, role, cwd string, note any) (Rec, error) {
	var displaced Rec
	err := KeyLock(key, func() error {
		cur := Lease(key)
		rec := LeaseRecord(key, sid, role, cwd, note)
		if cur != nil && !IsMalformed(cur) {
			displaced = cur
			if B(cur, "occupancy") {
				// A seat stays a seat when it changes hands.
				rec["occupancy"] = true
			}
		}
		return WriteLease(key, rec)
	})
	return displaced, err
}

// DropLease frees key if and only if sid holds it. True when it was freed — checked,
// not assumed: on Windows an unlink can fail on a file another process has open.
func DropLease(key, sid string) (bool, error) {
	freed := false
	err := KeyLock(key, func() error {
		cur := Lease(key)
		if cur == nil || IsMalformed(cur) || S(cur, "session") != sid {
			return nil
		}
		p := KeyFile("leases", key)
		Unlink(p)
		freed = !exists(p)
		return nil
	})
	return freed, err
}

// RestoreLease undoes this session's claim on key, putting old back or freeing the
// key. For the Codex adapter, which speculatively evaluates several paths and must
// unwind the leases a denied patch took. It re-reads under the lock and acts only if
// the key is still ours.
func RestoreLease(key, sid string, old Rec) (bool, error) {
	acted := false
	err := KeyLock(key, func() error {
		cur := Lease(key)
		if cur == nil || IsMalformed(cur) || S(cur, "session") != sid {
			return nil
		}
		acted = true
		if old == nil {
			Unlink(KeyFile("leases", key))
			return nil
		}
		return WriteLease(key, old)
	})
	return acted, err
}

// removeOwned removes this session's records from sub. Per-key records are removed
// under their key's lock and re-checked inside it: reading a lease, then removing it
// by pathname, is how an operator revoke landing in the window got its fresh lease
// deleted — and this runs at EVERY session end.
func removeOwned(sub, sid, field string, keep func(Rec) bool) {
	d := Path(sub)
	if !isDir(d) {
		return
	}
	for _, name := range listDir(d) {
		p := filepath.Join(d, name)
		rec := ReadJSON(p)
		if rec == nil || S(rec, field) != sid || (keep != nil && keep(rec)) {
			continue
		}
		key := S(rec, "key")
		// `key` must be a LEASE key. A cost lock stores the inflight id in a field of
		// the same name, so trusting the field alone took a lock on a tool-use id and
		// left a lock file per command, for ever.
		if key == "" || KeyParts(key) == nil {
			Unlink(p)
			continue
		}
		_ = KeyLock(key, func() error {
			cur := ReadJSON(p) // re-read INSIDE the lock; it may have changed hands
			if cur != nil && S(cur, field) == sid && !(keep != nil && keep(cur)) {
				Unlink(p)
			}
			return nil
		})
	}
}

// RemoveOwned is removeOwned without a keep predicate, for the test-support verbs.
func RemoveOwned(sub, sid, field string) { removeOwned(sub, sid, field, nil) }

// ReleaseLeases releases a session's branch leases. A resource lease is released
// only by `fleet drop` or `fleet revoke`: the session ending proves nothing about the
// resource. The one exception is an occupancy lease — the hook's own record that this
// session sits in a pooled worktree — where the session IS the occupant.
func ReleaseLeases(sid string) {
	removeOwned("leases", sid, "session", func(r Rec) bool {
		return S(r, "kind") == "resource" && !B(r, "occupancy")
	})
}

// ReleaseSessionState is everything a session owns dying with it: its leases, its
// suite locks, its inflight records, and any revoke flag written in its favour.
func ReleaseSessionState(sid string) {
	ReleaseLeases(sid)
	removeOwned("locks", sid, "session", nil)
	removeOwned("inflight", sid, "session", nil)
	removeOwned("stop", sid, "except", nil)
}

// SessionRecord is sessions/<sid>.json, or an empty record.
func SessionRecord(sid string) Rec {
	if r := ReadJSON(Path("sessions", sid+".json")); r != nil {
		return r
	}
	return Rec{}
}

// SessionAlive is observed liveness: a harness pid that answers, or a recent event
// from a session whose pid could not be verified.
func SessionAlive(rec Rec) bool {
	if rec == nil || len(rec) == 0 || B(rec, "ended") {
		return false
	}
	if S(rec, "pid_kind") == "harness" {
		return PidAlive(int(F(rec, "pid")))
	}
	return Now()-F(rec, "last_event_at") < float64(StaleS)
}

// changedSince reports whether any of the store's subdirectories has a newer
// modification time than the marker file.
func changedSince(marker string, subs ...string) bool {
	mst, err := os.Stat(marker)
	if err != nil {
		return true
	}
	for _, sub := range subs {
		if st, err := os.Stat(Path(sub)); err == nil && st.ModTime().After(mst.ModTime()) {
			return true
		}
	}
	return false
}

// MigrateLegacyKeys re-keys per-branch state written before keys became strings.
// Idempotent, marker-guarded so the steady state is one stat.
//
// Installing new code over a live store without re-keying its runtime directories is
// FAIL-OPEN: the new code looks under the new name, finds nothing, and hands a second
// session a lease on a branch whose incumbent still holds the old one. So it runs on
// every event, before any key is read — a session already running when the install
// lands has long since passed its SessionStart.
//
// A record is re-keyed from its own repo and branch fields, never its filename
// (Safe() is not invertible), and recognised by shape (repo and branch, no key).
// Publication is under the key's lock with a rename; a name that already exists is a
// collision for an operator to resolve, and both files are left for `fleet leases`.
func MigrateLegacyKeys() {
	marker := Path("migrated-keys.v1")
	// The marker says a pass completed; it cannot say no old writer has published a
	// legacy record since. A directory that changed after the marker is rescanned —
	// one stat per directory — and the marker re-stamped after.
	if ReadOnly || (exists(marker) && !changedSince(marker, "leases", "stop", "handoff")) {
		return
	}
	done := true
	for _, sub := range []string{"leases", "stop", "handoff"} {
		d := Path(sub)
		for _, name := range listDir(d) {
			if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
				continue
			}
			old := filepath.Join(d, name)
			rec := ReadJSON(old)
			if rec == nil {
				done = false // exists but did not parse: unfinished, not absent
				continue
			}
			if S(rec, "key") != "" || S(rec, "repo") == "" || S(rec, "branch") == "" {
				continue
			}
			key := "repo:" + S(rec, "repo") + ":" + S(rec, "branch")
			rec["key"] = key
			if sub == "leases" {
				if !Has(rec, "kind") {
					rec["kind"] = "branch"
				}
				if !Has(rec, "name") {
					rec["name"] = nil
				}
			}
			err := KeyLock(key, func() error {
				dest := KeyFile(sub, key)
				landed := ReadJSON(dest)
				if landed == nil {
					if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
						return err
					}
					tmp := filepath.Join(d, fmt.Sprintf(".tmp.migrate.%d", os.Getpid()))
					f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
					if err != nil {
						return err
					}
					if _, err := f.Write(DumpJSON(rec)); err != nil {
						f.Close()
						return err
					}
					_ = f.Sync()
					if err := f.Close(); err != nil {
						return err
					}
					if err := os.Rename(tmp, dest); err != nil {
						return err
					}
					Unlink(old)
					return nil
				}
				if S(landed, "session") == S(rec, "session") {
					Unlink(old) // a kill after publish, before unlink; same holder
				}
				// else: a genuine collision. Leave both; `fleet leases` reports it as LEGACY.
				return nil
			})
			if err != nil {
				done = false
				logError(Rec{"error": fmt.Sprintf("migrate %s: %v", key, err)})
			}
		}
	}
	if !done {
		return
	}
	_ = os.WriteFile(marker, []byte(fmt.Sprint(Now())), 0o644)
}
