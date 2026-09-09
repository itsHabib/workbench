package verbs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestHookCommandRecognition(t *testing.T) {
	for _, tc := range []struct{ command, kind, harness string }{
		{`ORG_STATE="$HOME/dev/org-work/state" python3 "$HOME/.fleet/hook.py"`, "legacy-python", "claude"},
		{`C:\Tools\python.exe C:\Users\Example\.fleet\codex-adapter.py`, "legacy-python", "codex"},
		{`"C:\Program Files\fleet.exe" hook codex --shadow`, "native-fleet", "codex"},
		{`"C:\Program Files (x86)\fleet.exe" hook codex`, "native-fleet", "codex"},
		{`(fleet hook codex)`, "unknown", ""},
		{`"C:\$(touch marker)\fleet.exe" hook codex`, "unknown", ""},
		{`TOKEN=do-not-print /opt/fleet hook claude`, "native-fleet", "claude"},
		{`sh -c 'fleet hook claude'`, "unknown", ""},
		{`fleet hook claude; touch /tmp/no`, "unknown", ""},
		{`fleet hook $(echo claude)`, "unknown", ""},
		{`python3 -u /tmp/hook.py`, "unknown", ""},
		{`fleet hook codex extra`, "unknown", ""},
		{`"fleet hook codex`, "unknown", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			row := describeHookCommand(tc.command)
			if row.Kind != tc.kind || row.Harness != tc.harness {
				t.Fatalf("got %+v", row)
			}
			encoded, _ := json.Marshal(row)
			if strings.Contains(string(encoded), "do-not-print") || strings.Contains(string(encoded), "org-work/state") {
				t.Fatal("environment value exposed")
			}
		})
	}
}

func TestInspectHooksDoesNotMigrateOrExecute(t *testing.T) {
	oldState, oldOut := fleet.State, Out
	root := t.TempDir()
	fleet.State = filepath.Join(root, "absent-state")
	var out bytes.Buffer
	Out = &out
	t.Cleanup(func() { fleet.State = oldState; Out = oldOut })
	sentinel := filepath.Join(root, "executed")
	command := `ORG_STATE=private TOKEN=secret python3 "/tmp/hook.py"`
	config := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{
		map[string]any{"type": "command", "command": command},
		map[string]any{"type": "command", "command": "touch " + sentinel},
	}}}}}
	raw, _ := json.Marshal(config)
	path := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch([]string{"inspect-hooks", "--config", path}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("config changed")
	}
	for _, p := range []string{fleet.State, sentinel} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("unexpected write: %s", p)
		}
	}
	if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "private") {
		t.Fatal("environment values leaked")
	}
	var report struct {
		Runtime string            `json:"runtime"`
		Hooks   []hookDescription `json:"hooks"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Runtime != "unverified" || len(report.Hooks) != 2 || report.Hooks[0].Kind != "legacy-python" || report.Hooks[1].Kind != "unknown" {
		t.Fatalf("bad report: %+v", report)
	}
	if strings.Join(report.Hooks[0].Environment, ",") != "ORG_STATE,TOKEN" {
		t.Fatal(report.Hooks[0])
	}
}

func TestInspectHookConfigRefusesInvalidShape(t *testing.T) {
	for _, raw := range []string{`{`, `null`, `{}`, `{"hooks":[]}`, `{"hooks":{"Start":null}}`, `{"hooks":{"Start":[{}]}}`} {
		if _, err := inspectHookConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestHookShadowFlag(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		command := `"C:\Program Files (x86)\fleet.exe" hook codex`
		if shadow {
			command += " --shadow"
		}
		got := describeHookCommand(command)
		if got.Kind != "native-fleet" || got.Shadow != shadow {
			t.Fatalf("shadow=%v: %+v", shadow, got)
		}
	}
}
