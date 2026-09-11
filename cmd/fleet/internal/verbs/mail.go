package verbs

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

const sendUsage = "usage: fleet send <address> [--id <id>] --kind <question|answer|escalation|report|order> --subject <text> [--head <sha>] --body <text|-> [--session <id8>]"

// CmdSend records mail with session-derived identity; it never launches a process.
func CmdSend(to, id, kind, subject, head, body, session string) error {
	if to == "" || subject == "" || body == "" {
		return exitCode(2, sendUsage)
	}
	if id == "" {
		id = "m-" + strings.ToLower(rand.Text())
	}
	if len(subject) > fleet.MaxMailSubjectBytes {
		return exitCode(2, fmt.Sprintf("mail: subject exceeds %d bytes", fleet.MaxMailSubjectBytes))
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
	sender, role, err := fleet.MailSender(rec)
	if err != nil {
		return refuse("fleet send: %s", err)
	}
	if err := fleet.MailPeer(rec, to); err != nil {
		return refuse("fleet send: %s", err)
	}
	payload := fleet.Rec{"id": id, "to": to, "from_role": role, "from_address": sender.Address, "from_kind": sender.Kind, "tenant": sender.Tenant, "from_session": sid, "kind": kind, "subject": subject, "head": head, "body": body}
	return publishMail(payload, sender.Tenant)
}

func publishMail(payload fleet.Rec, tenant string) error {
	r, err := fleet.PutMail(payload, tenant)
	if err != nil {
		return refuse("fleet send: %s", err)
	}
	say("%s", fleet.DumpJSON(r))
	return nil
}

// CmdMail lists a named role, or the caller's role. This observation never migrates.
func CmdMail(role, session string, unacked, asJSON bool) error {
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	rec := fleet.SessionRecord(sid)
	sender, _, err := fleet.MailSender(rec)
	if err != nil {
		return refuse("fleet mail: %s", err)
	}
	if role == "" {
		role = sender.Address
	}
	rows, err := fleet.MailFor(sender.Tenant, role, unacked)
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

// CmdAck acknowledges only the caller's launch address, never a cd destination.
func CmdAck(id, session string) error {
	if id == "" {
		return exitCode(2, "usage: fleet ack <id> [--session <id8>]")
	}
	sid, err := currentSession(session)
	if err != nil {
		return err
	}
	sender, _, err := fleet.MailSender(fleet.SessionRecord(sid))
	if err != nil {
		return refuse("fleet ack: %s", err)
	}
	r, err := fleet.AckMailFor(sender.Tenant, sender.Address, id, sid)
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
			return exitCode(2, "usage: fleet mail [--for <address>] [--unacked] [--json] [--session <id8>]")
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
