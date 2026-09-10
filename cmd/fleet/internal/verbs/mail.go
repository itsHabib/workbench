package verbs

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Mail: send, list, ack. The address is a role; the sender is the session this verb
// runs in, resolved from its cwd like every other verb. Who may write to whom is
// derived from data that already exists (roles.map, the org charters, the dispatch
// rows), never declared to this verb.

// CmdSend writes one message from the caller's session to a role it is allowed to
// address. Retry-safe: the same id with the same payload is a no-op that reports the
// existing record; a different payload under the same id is refused.
func CmdSend(to, id, kind, subject, head, body, session string) error {
	if fleet.ReadOnly {
		return refuse("fleet send: cannot send in read-only mode")
	}
	if to == "" || !requestID.MatchString(id) || kind == "" || strings.TrimSpace(subject) == "" || strings.TrimSpace(body) == "" {
		return refuse("usage: fleet send <role> --id <id> --kind <%s> --subject <text> [--head <sha>] --body <text|-> [--session <id8>]", strings.Join(fleet.MailKinds, "|"))
	}
	if !fleet.IsMailKind(kind) {
		return refuse("fleet send: --kind wants one of %s, got %s", strings.Join(fleet.MailKinds, ", "), fleet.PyRepr(kind))
	}
	sid, rec, err := senderIdentity(session)
	if err != nil {
		return err
	}
	from := fleet.S(rec, "role")
	allowed := contactsOf(sid, rec)
	if !contains(allowed, to) {
		return refuse("fleet send: %s is not a contact of %s; allowed: %s", to, from, orNone(allowed))
	}
	wanted := fleet.Rec{"id": id, "to": to, "from_role": from, "from_session": sid, "kind": kind,
		"subject": strings.TrimSpace(subject), "head": nilIfEmpty(head), "body": body}
	replay := false
	err = fleet.KeyLock(fleet.MailLockKey(to), func() error {
		existing := fleet.ReadJSON(fleet.MailFile(to, id))
		if existing != nil {
			if !samePayload(existing, wanted) {
				return refuse("fleet send: id %s to %s already carries a different message; choose a new id", id, to)
			}
			replay = true
			return nil
		}
		wanted["at"] = fleet.Now()
		return fleet.WriteJSON(fleet.MailFile(to, id), wanted)
	})
	if err != nil {
		return err
	}
	if replay {
		say("already sent: %s to %s (%s): %s", id, to, kind, wanted["subject"])
		return nil
	}
	say("sent %s to %s (%s): %s", id, to, kind, wanted["subject"])
	return nil
}

// senderIdentity is the caller's session and record; a session with no role has no
// address to send from.
func senderIdentity(session string) (string, fleet.Rec, error) {
	sid, err := currentSession(session)
	if err != nil {
		return "", nil, err
	}
	rec := fleet.SessionRecord(sid)
	if fleet.S(rec, "role") == "" {
		return "", nil, refuse("fleet: session %s has no role bound at %s; a role is the address mail is sent from", fleet.Short(sid), cwd())
	}
	return sid, rec, nil
}

// samePayload is identity for a retried send: everything the caller chose. The
// sending session is not part of it — sessions are disposable, and a retry from the
// role's next session is the same message.
func samePayload(a, b fleet.Rec) bool {
	for _, k := range []string{"id", "to", "from_role", "kind", "subject", "head", "body"} {
		if fleet.S(a, k) != fleet.S(b, k) {
			return false
		}
	}
	return true
}

// contactsOf is who this session may write to. A seat's only contact is the role
// accountable for the work it was dispatched; every other role reaches its parent,
// its children and its siblings.
func contactsOf(sid string, rec fleet.Rec) []string {
	if fleet.S(rec, "slot") != "" {
		return seatContacts(sid, rec)
	}
	launch := fleet.S(rec, "launch_dir")
	if launch == "" {
		launch = fleet.S(rec, "cwd")
	}
	return fleet.Contacts(fleet.TenantOf(launch), fleet.S(rec, "role"))
}

// seatContacts is the accountable column of every dispatch row that placed work in
// this seat: by slot, by worker, or by the branch the seat is on.
func seatContacts(sid string, rec fleet.Rec) []string {
	var out []string
	for _, row := range dispatchRows() {
		placed := fleet.S(row, "slot") == fleet.S(rec, "slot") || fleet.S(row, "worker") == sid ||
			(fleet.S(row, "repo") == fleet.S(rec, "repo") && fleet.S(row, "change") == fleet.S(rec, "branch"))
		if acc := fleet.S(row, "for"); placed && acc != "" && !contains(out, acc) {
			out = append(out, acc)
		}
	}
	return out
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

// CmdMail lists a mailbox: the named role's, else the calling session's, else every
// mailbox on this machine.
func CmdMail(forRole string, unacked, asJSON bool) error {
	roles := []string{forRole}
	if forRole == "" {
		roles = callerOrAllRoles()
	}
	var rows []any
	for _, role := range roles {
		for _, m := range fleet.Mailbox(role) {
			if unacked && fleet.S(m, "acked_by") != "" {
				continue
			}
			rows = append(rows, map[string]any(m))
		}
	}
	if asJSON {
		say("%s", fleet.DumpJSON(rows))
		return nil
	}
	if len(rows) == 0 {
		say("no mail for %s", strings.Join(roles, ", "))
		return nil
	}
	for _, raw := range rows {
		m, _ := raw.(map[string]any)
		say("%s", mailRow(m, len(roles) > 1))
	}
	return nil
}

func callerOrAllRoles() []string {
	if sid, err := currentSession(""); err == nil {
		if role := fleet.S(fleet.SessionRecord(sid), "role"); role != "" {
			return []string{role}
		}
	}
	roles := fleet.MailRoles()
	if len(roles) == 0 {
		return []string{"(no role here)"}
	}
	return roles
}

func mailRow(m fleet.Rec, withTo bool) string {
	state := "unread"
	if by := fleet.S(m, "acked_by"); by != "" {
		state = "acked by " + fleet.Short(by)
	}
	to := ""
	if withTo {
		to = "to " + fleet.S(m, "to") + "  "
	}
	return fmt.Sprintf("%-12s %-10s %sfrom %s  %s ago  %s  %s", fleet.S(m, "id"), fleet.S(m, "kind"), to, fleet.S(m, "from_role"), ago(fleet.F(m, "at")), state, fleet.S(m, "subject"))
}

// CmdAck marks one message read by the caller's session. Only a session wearing the
// addressed role — by its record, or by the directory it stands in — may.
func CmdAck(id, session string) error {
	if id == "" {
		return refuse("usage: fleet ack <id> [--session <id8>]")
	}
	if fleet.ReadOnly {
		return refuse("fleet ack: cannot ack in read-only mode")
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	rec := fleet.SessionRecord(sid)
	role, m := findMail(id, fleet.S(rec, "role"), fleet.RoleOf(cwd()))
	if m == nil {
		return refuse("fleet ack: no message %s is addressed to %s; `fleet mail` lists yours", id, orNone(uniqStrs(fleet.S(rec, "role"), fleet.RoleOf(cwd()))))
	}
	already := ""
	err = fleet.KeyLock(fleet.MailLockKey(role), func() error {
		cur := fleet.ReadJSON(fleet.MailFile(role, id))
		if cur == nil {
			return refuse("fleet ack: message %s to %s vanished before it could be acked", id, role)
		}
		if by := fleet.S(cur, "acked_by"); by != "" {
			already = by
			return nil
		}
		cur["acked_at"], cur["acked_by"] = fleet.Now(), sid
		return fleet.WriteJSON(fleet.MailFile(role, id), cur)
	})
	if err != nil {
		return err
	}
	if already != "" {
		say("already acked %s by %s", id, fleet.Short(already))
		return nil
	}
	say("acked %s (%s from %s)", id, fleet.S(m, "kind"), fleet.S(m, "from_role"))
	return nil
}

// findMail is the message with id in the first of the roles that has it.
func findMail(id string, roles ...string) (string, fleet.Rec) {
	for _, role := range uniqStrs(roles...) {
		if m := fleet.ReadJSON(fleet.MailFile(role, id)); m != nil {
			return role, m
		}
	}
	return "", nil
}

func uniqStrs(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" && !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// dispatchMail is the argument surface of the three verbs.
func dispatchMail(verb string, rest []string, asJSON bool) (bool, error) {
	switch verb {
	case "send":
		vals := map[string]string{}
		for _, f := range []string{"--id", "--kind", "--subject", "--head", "--session"} {
			v, err := optValue(rest, f, verb)
			if err != nil {
				return true, err
			}
			vals[f] = v
		}
		body, err := bodyArg(rest)
		if err != nil {
			return true, err
		}
		pos := positional(rest, "--id", "--kind", "--subject", "--head", "--body", "--session")
		return true, CmdSend(first(pos), vals["--id"], vals["--kind"], vals["--subject"], vals["--head"], body, vals["--session"])
	case "mail":
		plain := without(rest, "--json", "--unacked")
		forRole, err := optValue(plain, "--for", verb)
		if err != nil {
			return true, err
		}
		return true, CmdMail(forRole, contains(rest, "--unacked"), asJSON)
	case "ack":
		sess, err := optValue(rest, "--session", verb)
		if err != nil {
			return true, err
		}
		return true, CmdAck(first(positional(rest, "--session")), sess)
	default:
		return false, nil
	}
}

// bodyArg is the message text after --body: `-` reads it from stdin, and is the one
// dash-led value a flag here accepts.
func bodyArg(rest []string) (string, error) {
	if i := index(rest, "--body"); i >= 0 && i+1 < len(rest) && rest[i+1] == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", refuse("fleet send: reading --body from stdin: %v", err)
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	return optValue(rest, "--body", "send")
}
