package watch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestCheckDoesNotReserveDeliverOrMigrate(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	home, _ := deliverEnv(t)
	bin := t.TempDir()
	// Use a native executable on every platform, including Windows. The helper
	// rejects every RPC except discovery, even if harmless in a fixture.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "codex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), raw, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_TEST_CHECK_PROVIDER", "1")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), fleet.Rec{"hub:lead": fleet.Rec{"cwd": home, "provider": "codex"}, "broken": fleet.Rec{}}); err != nil {
		t.Fatal(err)
	}
	before := checkFiles(t, fleet.State, fleet.OrgState)
	result, err := Check("hub:lead")
	if err != nil {
		t.Fatal(err)
	}
	if result["configuration_warning"] == nil || result["address"] != "hub:lead" || result["effective_permissions"] != "unknown until thread start" {
		t.Fatal(result)
	}
	if after := checkFiles(t, fleet.State, fleet.OrgState); !reflect.DeepEqual(before, after) {
		t.Fatalf("inspection changed Fleet/Org state: before=%v after=%v", before, after)
	}
}

func checkFiles(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			out[path] = string(raw)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestMain(m *testing.M) {
	if os.Getenv("FLEET_TEST_CHECK_PROVIDER") == "1" {
		os.Exit(checkProviderFixture())
	}
	os.Exit(m.Run())
}

func checkProviderFixture() int {
	in, out := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var msg struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Cwds []string `json:"cwds"`
			} `json:"params"`
		}
		if err := in.Decode(&msg); err != nil {
			return 0
		}
		var result any
		switch msg.Method {
		case "initialize":
			result = fleet.Rec{}
		case "initialized":
			continue
		case "config/read":
			result = fleet.Rec{"config": fleet.Rec{"sandbox_mode": nil, "approval_policy": nil}}
		case "hooks/list":
			result = fleet.Rec{"data": []fleet.Rec{{"cwd": msg.Params.Cwds[0], "hooks": []any{}, "errors": []any{}, "warnings": []any{}}}}
		default:
			return 42
		}
		if err := out.Encode(fleet.Rec{"id": msg.ID, "result": result}); err != nil {
			return 43
		}
	}
}
