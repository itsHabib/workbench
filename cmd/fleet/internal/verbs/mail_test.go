package verbs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// mailFixture: two roled directories under one tenant, `lead:a` (child of `lead:top`)
// and `lead:top`, each with a live session, and the charters that relate them.
func mailFixture(t *testing.T) (dirs map[string]string, sids map[string]string) {
	t.Helper()
	oldState, oldOrg, oldOut := fleet.State, fleet.OrgState, Out
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	fleet.State, fleet.OrgState, Out = filepath.Join(root, "state"), filepath.Join(root, "org"), io.Discard
	t.Setenv("FLEET_GITHUB", "off")
	t.Setenv("FLEET_WATCH", "off")
	t.Cleanup(func() { fleet.State, fleet.OrgState, Out = oldState, oldOrg, oldOut; _ = os.Chdir(oldCwd) })
	if err := os.MkdirAll(fleet.OrgState, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs, sids = map[string]string{}, map[string]string{}
	var mapLines []string
	for i, role := range []string{"lead:a", "lead:top", "lead:b"} {
		d := filepath.Join(root, strings.ReplaceAll(role, ":", "-"))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		mapLines = append(mapLines, d+" t "+role)
		sid := fmt.Sprintf("%d%d%d%d%d%d%d%d-sess", i, i, i, i, i, i, i, i)
		dirs[role], sids[role] = d, sid
		if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), fleet.Rec{"session": sid, "cwd": d, "launch_dir": d, "role": role,
			"last_event_at": fleet.Now(), "pid_kind": "harness", "pid": os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(fleet.RolesMap(), []byte(strings.Join(mapLines, "\n")+"\n"), 0o644)
	for role, sup := range map[string]string{"lead:top": "human:mh", "lead:a": "lead:top", "lead:b": "lead:top"} {
		p := filepath.Join(fleet.OrgState, "t", strings.ReplaceAll(role, ":", "--"), "chain.jsonl")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(fmt.Sprintf(`{"role":%q,"kind":"charter","terms":{"supervisors":[%q]}}`+"\n", role, sup)), 0o644)
	}
	return dirs, sids
}

func chdir(t *testing.T, d string) {
	t.Helper()
	if err := os.Chdir(d); err != nil {
		t.Fatal(err)
	}
}

func refused(t *testing.T, err error, want string) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Code != 1 {
		t.Fatalf("want a refusal containing %q, got %v", want, err)
	}
	if !strings.Contains(r.Msg, want) {
		t.Fatalf("refusal %q does not say %q", r.Msg, want)
	}
}

func TestSendScopedToContactsAndRetrySafe(t *testing.T) {
	dirs, sids := mailFixture(t)
	chdir(t, dirs["lead:a"])
	if err := CmdSend("lead:top", "q-1", "question", "which base?", "abc123", "main or release?", ""); err != nil {
		t.Fatal(err)
	}
	m := fleet.ReadJSON(fleet.MailFile("lead:top", "q-1"))
	if fleet.S(m, "from_role") != "lead:a" || fleet.S(m, "from_session") != sids["lead:a"] || fleet.S(m, "head") != "abc123" || fleet.F(m, "at") == 0 {
		t.Fatal(m)
	}
	before, _ := os.ReadFile(fleet.MailFile("lead:top", "q-1"))
	// Same id, same payload: a no-op that leaves the record byte-identical.
	if err := CmdSend("lead:top", "q-1", "question", "which base?", "abc123", "main or release?", ""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(fleet.MailFile("lead:top", "q-1"))
	if !bytes.Equal(before, after) {
		t.Fatal("replay rewrote the record")
	}
	// Same id, different payload: refused, record untouched.
	refused(t, CmdSend("lead:top", "q-1", "question", "which base?", "abc123", "something else", ""), "different message")
	after, _ = os.ReadFile(fleet.MailFile("lead:top", "q-1"))
	if !bytes.Equal(before, after) {
		t.Fatal("refused send rewrote the record")
	}
	// Siblings are contacts; a stranger is not, and the refusal names who is.
	if err := CmdSend("lead:b", "r-1", "report", "fyi", "", "done", ""); err != nil {
		t.Fatal(err)
	}
	refused(t, CmdSend("lead:zzz", "r-2", "report", "fyi", "", "done", ""), "allowed: lead:b, lead:top")
	refused(t, CmdSend("lead:top", "r-3", "gossip", "fyi", "", "done", ""), "--kind wants one of")
	// Only a roled session sends.
	chdir(t, filepath.Dir(dirs["lead:a"]))
	refused(t, CmdSend("lead:top", "r-4", "report", "fyi", "", "done", ""), "no live session")
}

func TestSeatSendsOnlyToItsAccountableRole(t *testing.T) {
	dirs, sids := mailFixture(t)
	seat := filepath.Join(filepath.Dir(dirs["lead:a"]), "seat-1")
	_ = os.MkdirAll(seat, 0o755)
	sid := "seatseat-sess"
	if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), fleet.Rec{"session": sid, "cwd": seat, "launch_dir": seat, "role": "worker:x", "slot": "seat-1",
		"repo": "r1", "branch": "feat/z", "last_event_at": fleet.Now(), "pid_kind": "harness", "pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	chdir(t, seat)
	refused(t, CmdSend("lead:a", "r-1", "report", "fyi", "", "done", ""), "allowed: none")
	if err := fleet.WriteJSON(dispatchFile("r1", "feat/z", "implementation"), fleet.Rec{"repo": "r1", "change": "feat/z", "relationship": "implementation", "for": "lead:b", "slot": "seat-1", "at": fleet.Now()}); err != nil {
		t.Fatal(err)
	}
	refused(t, CmdSend("lead:a", "r-1", "report", "fyi", "", "done", ""), "allowed: lead:b")
	if err := CmdSend("lead:b", "r-1", "report", "fyi", "", "done", ""); err != nil {
		t.Fatal(err)
	}
	if fleet.S(fleet.ReadJSON(fleet.MailFile("lead:b", "r-1")), "from_session") != sid {
		t.Fatal("sender not the seat")
	}
	_ = sids
}

func TestAckOnlyByTheAddressedRole(t *testing.T) {
	dirs, sids := mailFixture(t)
	chdir(t, dirs["lead:a"])
	if err := CmdSend("lead:top", "q-1", "question", "which base?", "", "main?", ""); err != nil {
		t.Fatal(err)
	}
	// The sender is not the addressee.
	refused(t, CmdAck("q-1", ""), "no message q-1 is addressed to lead:a")
	chdir(t, dirs["lead:top"])
	var out strings.Builder
	Out = &out
	if err := CmdAck("q-1", ""); err != nil {
		t.Fatal(err)
	}
	m := fleet.ReadJSON(fleet.MailFile("lead:top", "q-1"))
	if fleet.S(m, "acked_by") != sids["lead:top"] || fleet.F(m, "acked_at") == 0 {
		t.Fatal(m)
	}
	if err := CmdAck("q-1", ""); err != nil || !strings.Contains(out.String(), "already acked") {
		t.Fatalf("second ack: %v %q", err, out.String())
	}
	out.Reset()
	if err := CmdMail("", true, false); err != nil || !strings.Contains(out.String(), "no mail for lead:top") {
		t.Fatalf("unacked listing after ack: %v %q", err, out.String())
	}
	out.Reset()
	if err := CmdMail("lead:top", false, true); err != nil || !strings.Contains(out.String(), `"acked_by": "`+sids["lead:top"]+`"`) {
		t.Fatalf("json listing: %v %q", err, out.String())
	}
}
