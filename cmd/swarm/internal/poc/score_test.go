package poc

import (
	"testing"

	"github.com/itsHabib/workbench/cmd/swarm/internal/swarm"
)

// Kill condition 4 used to initialize pass and never clear it. It must be
// able to fail, and an unexercised fault must read as not tested.
func TestKillFaultACanFail(t *testing.T) {
	rows := map[string]swarm.Row{"x": {Branch: "x", State: "landed", Tip: "HEAD"}}
	main := sandboxWithBump(t)
	if k := killFaultA(main, rows, "x", map[string]bool{}); k.Passed || !k.Exposed {
		t.Fatalf("an unflagged post-RESULT bump passed: %+v", k)
	}
	if k := killFaultA(main, rows, "x", map[string]bool{"x": true}); !k.Passed || !k.Exposed {
		t.Fatalf("a flagged bump failed: %+v", k)
	}
	if k := killFaultA(main, rows, "missing", nil); k.Exposed || verdict(k) != "not tested" {
		t.Fatalf("an unplanted fault did not read as not tested: %+v", k)
	}
}

func sandboxWithBump(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir, Spec{}); err != nil {
		t.Fatal(err)
	}
	main := dir + "/main"
	src, _ := swarm.Git(main, "show", "HEAD:cmd/app/main.go")
	if src == "" {
		t.Fatal("no app")
	}
	writeFile(t, main+"/cmd/app/main.go", replaceVersion(src))
	if _, err := swarm.Git(main, "commit", "-qam", "bump"); err != nil {
		t.Fatal(err)
	}
	return main
}
