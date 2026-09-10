package watch

// Lateness, said out loud.
//
// The board has always known which rows are past due and which questions nobody
// answered. Knowing is not telling: the board is a file, and a file is read by
// whoever opens it, which at 3am is nobody. A deadline that passes with no one
// there produces silence, and silence is indistinguishable from progress.
//
// So the fold derives two facts and writes each as ordinary mail, once per deadline:
//
//   - a row past its due date with no live hands and no passing receipt at its head,
//     addressed to the role accountable for it;
//   - a question or escalation nobody acknowledged past FLEET_REPLY_GRACE, addressed
//     to the addressee's parent — for a seat, the role the seat's row is for; for a
//     lead, the LATE_TO in its delivery entry. With no parent recorded, the fold logs
//     that it had nowhere to send it rather than inventing a recipient.
//
// The mail is a report, from `fleet:watch`. It carries evidence, not instructions:
// what was due, when, who had hands, what the head was. It grants nothing, it
// acknowledges nothing, and it is delivered like any other message — which is what
// makes the whole thing terminate: the parent that reads it is a session mail
// started, and the record it leaves is another row on the same board.
//
// Once per deadline. The deadline itself is the key, so a row that is re-dispatched
// with a new due date is late again, and a row that stays late is not mailed twice.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// watchAddress is the sender of everything the fold writes as mail.
const watchAddress = "fleet:watch"

// lateness is one derived fact: who hears about it, when it came due, and why.
type lateness struct {
	key      string
	to       string
	deadline float64
	subject  string
	body     string
}

// lateMail derives the fold's lateness, sends what has not been sent for this
// deadline, and returns what it observed.
func lateMail(now float64, work []fleet.Rec) []fleet.Rec {
	facts, observed := lateRows(now, work), []fleet.Rec{}
	unanswered, seen := lateReplies(now, work)
	facts = append(facts, unanswered...)
	observed = append(observed, seen...)
	if len(facts) == 0 {
		return observed
	}
	sent := lateSent()
	changed := false
	for _, f := range facts {
		if sent[f.key] == f.deadline {
			continue
		}
		r, err := sendLate(f)
		if err != nil {
			observed = append(observed, fleet.Rec{"at": fleet.Now(), "what": "late-mail-failed", "to": f.to, "subject": f.subject, "error": err.Error()})
			continue
		}
		sent[f.key] = f.deadline
		changed = true
		observed = append(observed, fleet.Rec{"at": fleet.Now(), "what": "late-mail", "to": f.to, "id": fleet.S(r, "id"), "subject": f.subject})
	}
	if changed {
		_ = fleet.WriteJSON(filepath.Join(dir(), "late.json"), sent)
	}
	return observed
}

// sendLate publishes one report. The id is derived from the fact and its deadline,
// so a retry after a failed fold republishes the same record rather than a second one.
func sendLate(f lateness) (fleet.Rec, error) {
	tenant, err := fleet.MailAddressTenant(f.to)
	if err != nil {
		return nil, err
	}
	payload := fleet.Rec{"id": lateID(f), "to": f.to, "from_role": watchAddress, "from_address": watchAddress, "from_kind": "role",
		"from_session": "", "kind": "report", "subject": f.subject, "head": "", "body": f.body}
	return fleet.PutMail(payload, tenant)
}

// lateID is stable for one fact at one deadline, and a valid mail id.
func lateID(f lateness) string {
	return fmt.Sprintf("late-%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%.0f", f.key, f.deadline))))[:20]
}

// lateSent is the deadlines already reported, or an empty set.
func lateSent() map[string]float64 {
	out := map[string]float64{}
	b, err := os.ReadFile(filepath.Join(dir(), "late.json"))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]float64{}
	}
	return out
}

// lateRows is every ownership row past due that nobody is working and nothing has
// passed. A row with no accountable role has no recipient and is left to the board.
func lateRows(now float64, work []fleet.Rec) []lateness {
	var out []lateness
	for _, w := range work {
		due, state := fleet.F(w, "due"), fleet.S(w, "state")
		if due <= 0 || now <= due || state == "done" || state == "working" || state == "idle" || state == "unknown" {
			continue
		}
		to := fleet.S(w, "for")
		if to == "" {
			continue
		}
		name := workName(w)
		out = append(out, lateness{
			key: "row|" + workKey(w), to: to, deadline: due,
			subject: "late: " + name,
			body: strings.Join([]string{
				fmt.Sprintf("%s in %s was due %s ago and has no passing receipt at its head.", name, fleet.S(w, "repo"), fleet.FmtAge(now-due)),
				"state: " + state,
				"hands: " + orNobody(fleet.S(w, "hands")),
				"head: " + orNobody(fleet.S(w, "head")),
				"accountable: " + to,
				"This is an observation from the fold, not an instruction; `fleet work` shows the row.",
			}, "\n"),
		})
	}
	return out
}

// lateReplies is every question or escalation nobody acknowledged past the grace,
// addressed to the addressee's parent.
func lateReplies(now float64, work []fleet.Rec) ([]lateness, []fleet.Rec) {
	replyGrace := grace("FLEET_REPLY_GRACE", defaultReplyGrace)
	var out []lateness
	var observed []fleet.Rec
	for _, a := range mailAddresses() {
		tenant, err := fleet.MailAddressTenant(a)
		if err != nil {
			continue
		}
		rows, err := fleet.MailboxRecords(tenant, a)
		if err != nil {
			continue
		}
		facts, seen := unanswered(rows, a, now, replyGrace, work)
		out = append(out, facts...)
		observed = append(observed, seen...)
	}
	return out, observed
}

// unanswered is one mailbox's overdue questions and escalations.
func unanswered(rows []fleet.Rec, address string, now, replyGrace float64, work []fleet.Rec) ([]lateness, []fleet.Rec) {
	var out []lateness
	var observed []fleet.Rec
	for _, r := range rows {
		kind, at := fleet.S(r, "kind"), fleet.F(r, "at")
		if kind != "question" && kind != "escalation" {
			continue
		}
		if fleet.Has(r, "acked_at") || now-at < replyGrace {
			continue
		}
		to := parentOf(address, work)
		id := fleet.S(r, "id")
		if to == "" {
			observed = append(observed, fleet.Rec{"at": now, "what": "late-no-recipient", "address": address, "id": id, "kind": kind})
			continue
		}
		out = append(out, lateness{
			key: "mail|" + address + "|" + id, to: to, deadline: at + replyGrace,
			subject: "late: " + kind + " " + id + " to " + address,
			body: strings.Join([]string{
				fmt.Sprintf("%s %s from %s has been unacknowledged for %s.", kind, id, senderOf(r), fleet.FmtAge(now-at)),
				"addressed to: " + address,
				"subject: " + fleet.S(r, "subject"),
				"This is an observation from the fold, not an instruction; the message is still queued for its addressee.",
			}, "\n"),
		})
	}
	return out, observed
}

// parentOf is who hears that an address did not answer: for a seat, the role its
// row is for; for anything else, the LATE_TO recorded in its delivery entry.
func parentOf(address string, work []fleet.Rec) string {
	newest := 0.0
	to := ""
	for _, w := range work {
		if fleet.S(w, "slot") != address || fleet.S(w, "for") == "" {
			continue
		}
		if at := fleet.F(w, "at"); at >= newest {
			newest, to = at, fleet.S(w, "for")
		}
	}
	if to != "" {
		return to
	}
	for _, t := range deliverTargets() {
		if t.address == address {
			return t.lateTo
		}
	}
	return ""
}

// mailAddresses is every address the fold can see: those bound in roles.map, as a
// role or as a seat, and those the operator configured for delivery.
func mailAddresses() []string {
	seen := map[string]bool{}
	_, rows := fleet.MapRows(fleet.RolesMap())
	for _, r := range rows {
		for _, a := range []string{r.Role, r.Slot} {
			if a != "" {
				seen[a] = true
			}
		}
	}
	for _, t := range deliverTargets() {
		seen[t.address] = true
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func senderOf(r fleet.Rec) string {
	if a := fleet.S(r, "from_address"); a != "" {
		return a
	}
	return fleet.S(r, "from_role")
}

func orNobody(s string) string {
	if s == "" {
		return "nobody"
	}
	return s
}
