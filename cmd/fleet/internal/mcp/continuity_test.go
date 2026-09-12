package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestHandoffMCPUsesLaunchRoleAndSkipsMigration(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	a, b := t.TempDir(), t.TempDir()
	if err := os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s t hub:a\n%s t hub:b\n", a, b)), 0600); err != nil {
		t.Fatal(err)
	}
	rec := fleet.Rec{"session": "writer", "cwd": a, "launch_dir": a, "last_event_at": fleet.Now()}
	if err := fleet.WriteJSON(fleet.Path("sessions", "writer.json"), rec); err != nil {
		t.Fatal(err)
	}
	packet := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "fleet_handoff", "arguments": map[string]any{"cwd": a, "conclusion": "Use milliseconds", "next": "Check the parser", "session": "writer"}}}
	raw, _ := json.Marshal(packet)
	var out strings.Builder
	Serve(strings.NewReader(string(raw)+"\n"), &out)
	if strings.Contains(out.String(), `"isError":true`) {
		t.Fatal(out.String())
	}
	if line := fleet.RoleHandoffLine(rec); !strings.Contains(line, "Use milliseconds") || !strings.Contains(line, "Check the parser") {
		t.Fatal(line)
	}
	if line := fleet.RoleHandoffLine(fleet.Rec{"cwd": b, "launch_dir": b}); line != "" {
		t.Fatalf("other role received handoff: %s", line)
	}
	if _, err := os.Stat(fleet.Path("migrated-keys.v1")); !os.IsNotExist(err) {
		t.Fatal("handoff migrated legacy keys", err)
	}
	if _, _, err := call("fleet_handoff", map[string]any{"conclusion": "missing cwd"}); err == nil {
		t.Fatal("missing cwd accepted")
	}
}
