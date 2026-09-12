package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestMailMCPUsesCallerAndSameStore(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	a, b := t.TempDir(), t.TempDir()
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s t hub:a\n%s t hub:b\n", a, b)), 0600)
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
	r, err := fleet.ReadMail("hub:b", "m1", "t")
	if err != nil || fleet.S(r, "from_session") != "sender" || fleet.S(r, "body") != "-" {
		t.Fatal(r, err)
	}
	_ = os.Remove(fleet.Path("sessions", "sender.json"))
	_ = fleet.WriteJSON(fleet.Path("sessions", "replacement.json"), fleet.Rec{"session": "replacement", "cwd": a, "last_event_at": fleet.Now()})
	invoke("fleet_send", args)
	r, _ = fleet.ReadMail("hub:b", "m1", "t")
	if fleet.S(r, "from_session") != "sender" {
		t.Fatal("replacement MCP retry rewrote original session", r)
	}
	invoke("fleet_mail", map[string]any{"cwd": b, "unacked": true})
	invoke("fleet_ack", map[string]any{"id": "m1", "cwd": b})
	r, _ = fleet.ReadMail("hub:b", "m1", "t")
	if fleet.S(r, "acked_by") != "reader" {
		t.Fatal(r)
	}
	delete(args, "cwd")
	if _, _, err := call("fleet_send", args); err == nil {
		t.Fatal("MCP accepted missing caller cwd")
	}
	if _, err := os.Stat(fleet.Path("migrated-keys.v1")); !os.IsNotExist(err) {
		t.Fatal("MCP mail migrated store", err)
	}
}

func TestMailMCPSeatAddressesAndTenantIsolation(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	a, b, foreign := t.TempDir(), t.TempDir(), t.TempDir()
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s one worker:demo seat-a\n%s one worker:demo seat-b\n%s two worker:other seat-b\n", a, b, foreign)), 0600)
	for id, dir := range map[string]string{"a": a, "b": b, "foreign": foreign} {
		_ = fleet.WriteJSON(fleet.Path("sessions", id+".json"), fleet.Rec{"session": id, "cwd": dir, "last_event_at": fleet.Now()})
	}
	args := map[string]any{"cwd": a, "to": "seat-b", "id": "same", "kind": "report", "subject": "Hi", "body": "one"}
	if _, failed, err := call("fleet_send", args); err != nil || failed {
		t.Fatal(err)
	}
	if _, failed, err := call("fleet_ack", map[string]any{"cwd": a, "id": "same"}); err == nil && !failed {
		t.Fatal("MCP sibling ack")
	}
	if _, failed, err := call("fleet_ack", map[string]any{"cwd": foreign, "id": "same"}); err == nil && !failed {
		t.Fatal("MCP foreign tenant ack")
	}
	if _, failed, err := call("fleet_ack", map[string]any{"cwd": b, "id": "same"}); err != nil || failed {
		t.Fatal(err)
	}
	r, err := fleet.ReadMailFor("one", "seat-b", "same")
	if err != nil || fleet.S(r, "acked_by") != "b" || fleet.S(r, "from_address") != "seat-a" {
		t.Fatal(r, err)
	}
	// An identically named address in another tenant uses a separate mailbox.
	args["cwd"], args["body"] = foreign, "two"
	if _, failed, err := call("fleet_send", args); err != nil || failed {
		t.Fatal(err)
	}
	r, err = fleet.ReadMailFor("two", "seat-b", "same")
	if err != nil || fleet.Has(r, "acked_at") || fleet.S(r, "body") != "two" {
		t.Fatal(r, err)
	}
}
