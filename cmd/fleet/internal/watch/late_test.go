package watch

import (
	"os"
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

// Live hands, a passing receipt, or no accountable role: nothing to say. Past its
// due date the board renames a held row to `late` too, so the hands cases here are
// the ones a state-name predicate would have reported.
func TestLateRowDerivationIsSilentWhenItShouldBe(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  fleet.Rec
		live bool
	}{
		{"live hands mid-turn", fleet.Rec{"state": "late", "hands": "s1"}, true},
		{"live hands idle", fleet.Rec{"state": "late", "hands": "s1"}, true},
		{"passing receipt", fleet.Rec{"state": "done"}, false},
		{"passing receipt under another state name", fleet.Rec{"state": "late", "done_at": -60.0}, false},
		{"no accountable role", fleet.Rec{"state": "dispatched", "for": ""}, false},
		{"not yet due", fleet.Rec{"state": "dispatched", "due": 600.0}, false},
		{"unknown after a sleep", fleet.Rec{"state": "unknown"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deliverEnv(t)
			now := fleet.Now()
			if tc.live {
				putSession(t, "s1", now)
			}
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

// The same row, once its holder is no longer alive, is exactly what the report is
// for — and the report says which silence it found.
func TestLateRowWithADeadHolderIsReportedAndNamesTheStanding(t *testing.T) {
	deliverEnv(t)
	now := fleet.Now()
	putSession(t, "s1", now-float64(fleet.StaleS)-600)
	work := []fleet.Rec{{"repo": "r1", "change": "topic", "relationship": "check", "for": "hub:lead",
		"state": "late", "hands": "s1", "due": now - 300, "at": now - 900}}
	if observed := lateMail(now, work); len(observedWhat(observed, "late-mail")) != 1 {
		t.Fatalf("a dead holder was not reported: %v", observed)
	}
	rows := lateBox(t, "hub:lead")
	if len(rows) != 1 || !strings.Contains(fleet.S(rows[0], "body"), "no longer alive") {
		t.Fatalf("body does not say which silence it found: %v", rows)
	}
}

// putSession writes one session record whose last event is at the given time.
func putSession(t *testing.T, sid string, at float64) {
	t.Helper()
	rec := fleet.Rec{"session": sid, "pid_kind": "parent-unverified", "last_event_at": at}
	if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), rec); err != nil {
		t.Fatal(err)
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
		{"parent recorded", "seat-1", "late-mail", 1},
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
			if got := len(lateBox(t, "seat-1")); got != tc.reports {
				t.Fatalf("parent mailbox: %d, want %d", got, tc.reports)
			}
		})
	}
}

// Inside the grace, or already acknowledged, nothing is said.
func TestUnansweredMailWaitsForTheReplyGrace(t *testing.T) {
	home, _ := deliverEnv(t)
	cfg := map[string]any{"hub:lead": map[string]any{"cwd": home, "cmd": []any{"true"}, "LATE_TO": "seat-1"}}
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
	if err := fleet.WriteJSON(filepath.Join(storeDirs(t, "t1", address)[1], id+".json"), r); err != nil {
		t.Fatal(err)
	}
}

// A LATE_TO naming an address in another tenant is refused, logged, and never sent:
// the report carries tenant A's sender, subject and standing.
func TestLateReportNeverCrossesTheTenantBoundary(t *testing.T) {
	home, _ := deliverEnv(t)
	other := filepath.Join(t.TempDir(), "t2-lead")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(home+" t1 hub:lead\n"+other+" t2 other:lead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"hub:lead": map[string]any{"cwd": home, "cmd": []any{"true"}, "LATE_TO": "other:lead"}}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
		t.Fatal(err)
	}
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "e1", now-3600, fleet.Rec{"kind": "escalation"})
	observed := lateMail(now, nil)
	if len(observedWhat(observed, "late-mail")) != 0 {
		t.Fatalf("a cross-tenant report was sent: %v", observed)
	}
	if len(observedWhat(observed, "late-mail-refused")) != 1 {
		t.Fatalf("the refusal was not recorded: %v", observed)
	}
	rows, err := fleet.MailboxRecords("t2", "other:lead")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("tenant t2 received tenant t1's metadata: %v", rows)
	}
}

// A remote row is another machine's cached declaration: it carries no hands because
// none were cached, not because nobody is working. That is not evidence of silence.
func TestRemoteRowsAreNeverReportedLate(t *testing.T) {
	deliverEnv(t)
	now := fleet.Now()
	remote := fleet.Rec{"repo": "r1", "change": "topic", "relationship": "check", "for": "hub:lead",
		"state": "late", "hands": nil, "due": now - 300, "at": now - 900, "machine": "other-host", "cache_at": now - 120}
	if observed := lateMail(now, []fleet.Rec{remote}); len(observedWhat(observed, "late-mail")) != 0 {
		t.Fatalf("a remote row was reported unattended: %v", observed)
	}
	if rows := lateBox(t, "hub:lead"); len(rows) != 0 {
		t.Fatalf("mailbox: %v", rows)
	}
	// The same row declared locally is still reported: only the cached one is mute.
	local := fleet.Rec{}
	for k, v := range remote {
		local[k] = v
	}
	delete(local, "cache_at")
	delete(local, "machine")
	if observed := lateMail(now, []fleet.Rec{local}); len(observedWhat(observed, "late-mail")) != 1 {
		t.Fatalf("a local row stopped being reported: %v", observed)
	}
}
