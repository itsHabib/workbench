package mcp

import (
	"encoding/json"
	"fmt"
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"os"
	"strings"
	"testing"
)

func TestMailMCPUsesCallerAndSameStore(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	a, b := t.TempDir(), t.TempDir()
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s t hub:a\n%s t hub:b\n", a, b)), 0600)
	_ = fleet.WriteJSON(fleet.Path("contacts.json"), map[string][]string{"hub:a": {"hub:b"}})
	for id, cwd := range map[string]string{"sender": a, "reader": b} {
		_ = fleet.WriteJSON(fleet.Path("sessions", id+".json"), fleet.Rec{"session": id, "cwd": cwd, "last_event_at": fleet.Now()})
	}
	invoke := func(name string, args map[string]any) string {
		t.Helper()
		packet := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}
		raw, _ := json.Marshal(packet)
		var out strings.Builder
		Serve(strings.NewReader(string(raw)+"\n"), &out)
		if strings.Contains(out.String(), `"isError":true`) || strings.Contains(out.String(), `"error":`) {
			t.Fatal(out.String())
		}
		return out.String()
	}
	args := map[string]any{"to": "hub:b", "id": "m1", "kind": "report", "subject": "hello", "body": "-", "cwd": a}
	invoke("fleet_send", args)
	r, err := fleet.ReadMail("hub:b", "m1")
	if err != nil || fleet.S(r, "from_session") != "sender" || fleet.S(r, "body") != "-" {
		t.Fatal(r, err)
	}
	invoke("fleet_mail", map[string]any{"cwd": b, "unacked": true})
	invoke("fleet_ack", map[string]any{"id": "m1", "cwd": b})
	r, _ = fleet.ReadMail("hub:b", "m1")
	if fleet.S(r, "acked_by") != "reader" {
		t.Fatal(r)
	}
	delete(args, "cwd")
	if _, _, err := call("fleet_send", args); err == nil {
		t.Fatal("MCP accepted missing caller cwd")
	}
	if _, err := os.Stat(fleet.Path("keylocks")); !os.IsNotExist(err) {
		t.Fatal("MCP mail migrated store", err)
	}
}
