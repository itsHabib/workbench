package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestCheckDoesNotReserveDeliverOrMigrate(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	home, _ := deliverEnv(t)
	bin := t.TempDir()
	// Reject every RPC except discovery, even if it would be harmless in a fixture.
	script := `#!/usr/bin/env node
const rl=require('node:readline').createInterface({input:process.stdin});
rl.on('line',line=>{
 const m=JSON.parse(line), send=result=>process.stdout.write(JSON.stringify({id:m.id,result})+'\n');
 switch(m.method) {
 case 'initialize': return send({});
 case 'initialized': return;
 case 'config/read': return send({config:{sandbox_mode:null,approval_policy:null}});
 case 'hooks/list': return send({data:[{cwd:m.params.cwds[0],hooks:[],errors:[],warnings:[]}]});
 default: process.exit(42);
 }
});
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), fleet.Rec{"hub:lead": fleet.Rec{"cwd": home, "provider": "codex"}}); err != nil {
		t.Fatal(err)
	}
	before := checkFiles(t, fleet.State, fleet.OrgState)
	result, err := Check("hub:lead")
	if err != nil {
		t.Fatal(err)
	}
	if result["address"] != "hub:lead" || result["effective_permissions"] != "unknown until thread start" {
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
