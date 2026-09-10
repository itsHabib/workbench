package verbs

import (
	"fmt"
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"io"
	"os"
	"strings"
)

const sendUsage = "usage: fleet send <role> --id <id> --kind <question|answer|escalation|report|order> --subject <text> [--head <sha>] --body <text|-> [--session <id8>]"

// CmdSend records mail with session-derived identity; it never launches a process.
func CmdSend(to, id, kind, subject, head, body, session string) error {
	if to == "" || id == "" || subject == "" || body == "" {
		return exitCode(2, sendUsage)
	}
	if !contains([]string{"question", "answer", "escalation", "report", "order"}, kind) {
		return exitCode(2, sendUsage)
	}
	if err := fleet.MailAddress(to, id); err != nil {
		return exitCode(2, err.Error())
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	rec := fleet.SessionRecord(sid)
	role, _, _ := fleet.MailIdentity(rec)
	payload := fleet.Rec{"id": id, "to": to, "from_role": role, "from_session": sid, "kind": kind, "subject": subject, "head": head, "body": body}
	// A retained retry is not a new send; changed contacts cannot invalidate it.
	old, err := fleet.ReadMail(to, id)
	if err != nil {
		return refuse("fleet send: %s", err)
	}
	if old != nil {
		return publishMail(payload)
	}
	allowed, err := fleet.MailContacts(rec)
	if err != nil {
		return refuse("fleet send: %s", err)
	}
	if !contains(allowed, to) {
		return refuse("fleet send: %s is outside contacts for %s; allowed: [%s]", to, role, strings.Join(allowed, ", "))
	}
	return publishMail(payload)
}

func publishMail(payload fleet.Rec) error {
	r, err := fleet.PutMail(payload)
	if err != nil {
		return refuse("fleet send: %s", err)
	}
	say("%s", fleet.DumpJSON(r))
	return nil
}

// CmdMail lists a named role, or the caller's role. This observation never migrates.
func CmdMail(role, session string, unacked, asJSON bool) error {
	if role == "" {
		sid, err := currentSession(session)
		if err != nil {
			return err
		}
		role, _, _ = fleet.MailIdentity(fleet.SessionRecord(sid))
		if role == "" {
			role = fleet.RoleOf(cwd())
		}
	}
	if _, err := fleet.MailRoleTenant(role); err != nil {
		return refuse("fleet mail: %s", err)
	}
	rows, err := fleet.Mail(role, unacked)
	if err != nil {
		return refuse("fleet mail: %s", err)
	}
	if asJSON {
		say("%s", fleet.DumpJSON(rows))
		return nil
	}
	for _, r := range rows {
		state := "unacked"
		if fleet.Has(r, "acked_at") {
			state = "acked"
		}
		say("%s [%s]", fleet.MailLine(r), state)
		if head := fleet.S(r, "head"); head != "" {
			say("head: %s", head)
		}
		say("%s", fleet.S(r, "body"))
	}
	return nil
}

// CmdAck only searches roles held by the caller or bound to its directory.
func CmdAck(id, session string) error {
	if id == "" {
		return exitCode(2, "usage: fleet ack <id> [--session <id8>]")
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	role, _, _ := fleet.MailIdentity(fleet.SessionRecord(sid))
	roles := []string{role}
	if here := fleet.RoleOf(cwd()); here != "" && here != role {
		roles = append(roles, here)
	}
	target := ""
	for _, r := range roles {
		if r == "" {
			continue
		}
		if _, err := fleet.MailRoleTenant(r); err != nil {
			return refuse("fleet ack: %s", err)
		}
		rec, err := fleet.ReadMail(r, id)
		if err != nil {
			return refuse("fleet ack: %s", err)
		}
		if rec == nil {
			continue
		}
		if target != "" {
			return refuse("fleet ack: %s is ambiguous between held and directory roles", id)
		}
		target = r
	}
	if target == "" {
		return refuse("fleet ack: no message %s addressed to the caller's role or directory", id)
	}
	r, err := fleet.AckMail(target, id, sid)
	if err != nil {
		return refuse("fleet ack: %s", err)
	}
	say("%s", fleet.DumpJSON(r))
	return nil
}

// parseMailArgs consumes each flag value as data, including text starting with --.
func parseMailArgs(args []string, values, booleans []string) ([]string, map[string]string, error) {
	var pos []string
	opts := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		if _, ok := opts[a]; ok {
			return nil, nil, exitCode(2, "duplicate option "+a)
		}
		if contains(booleans, a) {
			opts[a] = "true"
			continue
		}
		if !contains(values, a) || i+1 >= len(args) {
			return nil, nil, exitCode(2, "unknown option or missing value: "+a)
		}
		i++
		opts[a] = args[i]
	}
	return pos, opts, nil
}

func dispatchMail(verb string, args []string) error {
	values := []string{"--session"}
	booleans := []string{}
	switch verb {
	case "send":
		values = append(values, "--id", "--kind", "--subject", "--head", "--body")
	case "mail":
		values = append(values, "--for")
		booleans = []string{"--unacked", "--json"}
	}
	pos, o, err := parseMailArgs(args, values, booleans)
	if err != nil {
		return err
	}
	if verb == "mail" {
		if len(pos) != 0 {
			return exitCode(2, "usage: fleet mail [--for <role>] [--unacked] [--json] [--session <id8>]")
		}
		return CmdMail(o["--for"], o["--session"], o["--unacked"] != "", o["--json"] != "")
	}
	if len(pos) != 1 {
		return exitCode(2, fmt.Sprintf("usage: fleet %s requires one address or id", verb))
	}
	if verb == "ack" {
		return CmdAck(pos[0], o["--session"])
	}
	body := o["--body"]
	if body == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return refuse("fleet send: reading body: %s", err)
		}
		body = string(b)
	}
	return CmdSend(pos[0], o["--id"], o["--kind"], o["--subject"], o["--head"], body, o["--session"])
}
