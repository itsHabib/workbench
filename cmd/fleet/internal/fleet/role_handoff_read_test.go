package fleet

import (
	"os"
	"strings"
	"testing"
)

func TestReadRoleHandoffDistinguishesMissingFromDamaged(t *testing.T) {
	root := continuityFixture(t)
	if err := os.WriteFile(RolesMap(), []byte(root+" one lead:demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	caller := Rec{"session": "original", "launch_dir": root}
	r, err := ReadRoleHandoff("one", "lead:demo")
	if r != nil || err != nil {
		t.Fatal("missing checkpoint", r, err)
	}
	// JSON escaping may expand every accepted body byte to six bytes on disk.
	body := "start" + strings.Repeat("\x00", roleHandoffBodyBytes-len("start"))
	if err := WriteRoleHandoff(caller, body, ""); err != nil {
		t.Fatal(err)
	}
	r, err = ReadRoleHandoff("one", "lead:demo")
	if err != nil || S(r, "conclusion") != body {
		t.Fatal("accepted body could not be recovered", err)
	}
	path := roleHandoffPath("one", "lead:demo")
	for _, data := range []string{`null`, `[]`, `{"conclusion":`, strings.Repeat("x", roleHandoffFileBytes+1)} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadRoleHandoff("one", "lead:demo"); got != nil || err == nil {
			t.Fatal("damaged checkpoint appeared absent or complete", err)
		}
	}
	for _, field := range []string{"tenant", "role", "session", "conclusion", "next", "at"} {
		bad := Rec{"tenant": "one", "role": "lead:demo", "session": "original", "conclusion": "saved", "next": "continue", "at": Now()}
		bad[field] = nil
		if err := WriteJSON(path, bad); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadRoleHandoff("one", "lead:demo"); got != nil || err == nil {
			t.Fatal("invalid checkpoint accepted", field, got, err)
		}
	}
}

func TestReadRoleHandoffDoesNotCrossRoleTenantOrSeat(t *testing.T) {
	root := continuityFixture(t)
	if err := os.WriteFile(RolesMap(), []byte(root+" one lead:demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	caller := Rec{"session": "original", "launch_dir": root}
	if err := WriteRoleHandoff(caller, "private checkpoint", "next"); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []string{"two lead:demo", "one lead:other", "one lead:demo seat-a"} {
		if err := os.WriteFile(RolesMap(), []byte(root+" "+binding+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := RoleHandoffLine(caller); got != "" {
			t.Fatal("checkpoint crossed current binding", binding, got)
		}
	}
	// A caller that already captured one/lead:demo reads that identity even if
	// roles.map is changed before the file read. No second lookup can replace it.
	got, err := ReadRoleHandoff("one", "lead:demo")
	if err != nil || S(got, "conclusion") != "private checkpoint" {
		t.Fatal("captured identity changed during retrieval", got, err)
	}
}

func TestReadRoleHandoffRejectsUnpairedUnicodeEscapes(t *testing.T) {
	continuityFixture(t)
	for _, tc := range []struct {
		text string
		ok   bool
	}{{`\ud800`, false}, {`\udc00`, false}, {`\ud800\u0041`, false}, {`\ud800\ud800`, false},
		{`\ud83d\ude80`, true}, {`literal \\ud800`, true}, {`\ufffd`, true}, {`normal`, true}} {
		r := Rec{"tenant": "one", "role": "lead:demo", "session": "author", "at": Now(), "conclusion": "PLACEHOLDER", "next": ""}
		data := strings.Replace(string(DumpJSON(r)), "PLACEHOLDER", tc.text, 1)
		path := roleHandoffPath("one", "lead:demo")
		if err := WriteJSON(path, r); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadRoleHandoff("one", "lead:demo")
		if (err == nil) != tc.ok || (got != nil) != tc.ok {
			t.Fatal("unexpected Unicode escape result", tc, got, err)
		}
	}
}
