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
	r, err := ReadRoleHandoff(caller)
	if r != nil || err != nil {
		t.Fatal("missing checkpoint", r, err)
	}
	// JSON escaping may expand every accepted body byte to six bytes on disk.
	body := "start" + strings.Repeat("\x00", roleHandoffBodyBytes-len("start"))
	if err := WriteRoleHandoff(caller, body, ""); err != nil {
		t.Fatal(err)
	}
	r, err = ReadRoleHandoff(caller)
	if err != nil || S(r, "conclusion") != body {
		t.Fatal("accepted body could not be recovered", err)
	}
	path := roleHandoffPath("one", "lead:demo")
	for _, data := range []string{`null`, `[]`, `{"conclusion":`, strings.Repeat("x", roleHandoffFileBytes+1)} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadRoleHandoff(caller); got != nil || err == nil {
			t.Fatal("damaged checkpoint appeared absent or complete", err)
		}
	}
	for _, field := range []string{"tenant", "role", "session", "conclusion", "next", "at"} {
		bad := Rec{"tenant": "one", "role": "lead:demo", "session": "original", "conclusion": "saved", "next": "continue", "at": Now()}
		bad[field] = nil
		if err := WriteJSON(path, bad); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadRoleHandoff(caller); got != nil || err == nil {
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
		if got, err := ReadRoleHandoff(caller); got != nil || err != nil {
			t.Fatal("checkpoint crossed current binding", binding, got, err)
		}
	}
}
