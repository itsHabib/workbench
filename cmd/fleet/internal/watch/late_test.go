package watch

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// lateBox is the mail one lateness fact produced for an address.
func lateBox(t *testing.T, address string) []fleet.Rec {
	t.Helper()
	rows, err := fleet.MailboxRecords("t1", address)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// A row past due with nobody on it becomes mail to the role accountable for it,
// once per deadline.
func TestLateRowBecomesMailToTheAccountableRole(t *testing.T) {
	deliverEnv(t)
	now := fleet.Now()
	work := []fleet.Rec{{"repo": "r1", "change": "topic", "relationship": "check", "for": "hub:lead",
		"state": "dispatched", "due": now - 300, "hands": nil, "head": "abc123", "at": now - 900}}
	observed := lateMail(now, work)
	if len(observedWhat(observed, "late-mail")) != 1 {
		t.Fatalf("no report sent: %v", observed)
	}
	rows := lateBox(t, "hub:lead")
	if len(rows) != 1 {
		t.Fatalf("mailbox: %v", rows)
	}
	r := rows[0]
	if fleet.S(r, "kind") != "report" || fleet.S(r, "from_role") != watchAddress {
		t.Fatalf("record: %v", r)
	}
	if fleet.S(r, "subject") != "late: topic/check" {
		t.Fatalf("subject: %q", fleet.S(r, "subject"))
	}
	for _, want := range []string{"due 5m ago", "abc123", "hands: nobody"} {
		if !strings.Contains(fleet.S(r, "body"), want) {
			t.Fatalf("body missing %q: %q", want, fleet.S(r, "body"))
		}
	}
	if observed := lateMail(fleet.Now(), work); len(observedWhat(observed, "late-mail")) != 0 {
		t.Fatalf("the same deadline was reported twice: %v", observed)
	}
	if rows := lateBox(t, "hub:lead"); len(rows) != 1 {
		t.Fatalf("mailbox grew: %v", rows)
	}
}

// Live hands, a passing receipt, or no accountable role: nothing to say.
func TestLateRowDerivationIsSilentWhenItShouldBe(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  fleet.Rec
	}{
		{"live hands", fleet.Rec{"state": "working", "hands": "s1"}},
		{"idle hands", fleet.Rec{"state": "idle", "hands": "s1"}},
		{"passing receipt", fleet.Rec{"state": "done"}},
		{"no accountable role", fleet.Rec{"state": "dispatched", "for": ""}},
		{"not yet due", fleet.Rec{"state": "dispatched", "due": 600.0}},
		{"unknown after a sleep", fleet.Rec{"state": "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deliverEnv(t)
			now := fleet.Now()
			row := fleet.Rec{"repo": "r1", "change": "topic", "relationship": "check", "for": "hub:lead", "due": now - 300, "at": now - 900}
			for k, v := range tc.row {
				if f, ok := v.(float64); ok {
					v = now + f
				}
				row[k] = v
			}
			if observed := lateMail(now, []fleet.Rec{row}); len(observedWhat(observed, "late-mail")) != 0 {
				t.Fatalf("reported anyway: %v", observed)
			}
		})
	}
}

// A seat's unanswered question goes to the role its row is for.
func TestUnansweredSeatQuestionGoesToTheRowsRole(t *testing.T) {
	deliverEnv(t)
	now := fleet.Now()
	putSeatMail(t, "seat-1", "q1", now-3600, "question")
	work := []fleet.Rec{{"repo": "r1", "change": "topic", "relationship": "check", "for": "hub:lead",
		"state": "working", "hands": "s1", "slot": "seat-1", "at": now - 900}}
	observed := lateMail(now, work)
	if len(observedWhat(observed, "late-mail")) != 1 {
		t.Fatalf("no report sent: %v", observed)
	}
	rows := lateBox(t, "hub:lead")
	if len(rows) != 1 || !strings.Contains(fleet.S(rows[0], "subject"), "late: question q1 to seat-1") {
		t.Fatalf("mailbox: %v", rows)
	}
	if observed := lateMail(fleet.Now(), work); len(observedWhat(observed, "late-mail")) != 0 {
		t.Fatalf("reported twice: %v", observed)
	}
}

// A lead's unanswered escalation goes to the LATE_TO in its delivery entry, and with
// no parent recorded the fold says it had nowhere to send it.
func TestUnansweredLeadEscalationUsesTheRecordedParent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lateTo  string
		want    string
		reports int
	}{
		{"parent recorded", "hub:b", "late-mail", 1},
		{"no parent", "", "late-no-recipient", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := deliverEnv(t)
			cfg := map[string]any{"hub:lead": map[string]any{"cwd": home, "cmd": []any{"true"}, "LATE_TO": tc.lateTo}}
			if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
				t.Fatal(err)
			}
			now := fleet.Now()
			putStoreMail(t, "hub:lead", "e1", now-3600, fleet.Rec{"kind": "escalation"})
			observed := lateMail(now, nil)
			if len(observedWhat(observed, tc.want)) != 1 {
				t.Fatalf("want one %s: %v", tc.want, observed)
			}
			if got := len(lateBox(t, "hub:b")); got != tc.reports {
				t.Fatalf("parent mailbox: %d, want %d", got, tc.reports)
			}
		})
	}
}

// Inside the grace, or already acknowledged, nothing is said.
func TestUnansweredMailWaitsForTheReplyGrace(t *testing.T) {
	home, _ := deliverEnv(t)
	cfg := map[string]any{"hub:lead": map[string]any{"cwd": home, "cmd": []any{"true"}, "LATE_TO": "hub:b"}}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
		t.Fatal(err)
	}
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "q1", now-60, nil)
	putStoreMail(t, "hub:lead", "q2", now-3600, fleet.Rec{"acked_at": now - 3000})
	putStoreMail(t, "hub:lead", "r1", now-3600, fleet.Rec{"kind": "report"})
	if observed := lateMail(now, nil); len(observedWhat(observed, "late-mail")) != 0 {
		t.Fatalf("reported early: %v", observed)
	}
	t.Setenv("FLEET_REPLY_GRACE", "30s")
	if observed := lateMail(now, nil); len(observedWhat(observed, "late-mail")) != 1 {
		t.Fatalf("the grace was not honoured: %v", observed)
	}
}

// putSeatMail writes one record into a seat's typed mailbox.
func putSeatMail(t *testing.T, address, id string, at float64, kind string) {
	t.Helper()
	r := fleet.Rec{"id": id, "to": address, "tenant": "t1", "to_kind": "seat", "from_role": "hub:lead",
		"from_address": "hub:lead", "from_kind": "role", "kind": kind, "subject": "unit?", "body": "ms or s", "at": at}
	if err := fleet.WriteJSON(filepath.Join(fleet.MailStoreDirs("t1", address)[1], id+".json"), r); err != nil {
		t.Fatal(err)
	}
}
