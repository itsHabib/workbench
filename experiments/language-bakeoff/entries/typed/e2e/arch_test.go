package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed"

// The typed API, planner, CLI and foundation must not depend on any
// adapter or workflow. That is what lets a new node kind ship as a new
// package without editing them.
func TestCoreImportsNoAdapter(t *testing.T) {
	core := []string{module + "/wb", module + "/engine", module + "/cli", module + "/foundation"}
	out, err := exec.Command("go", append([]string{"list", "-deps", "-f", "{{.ImportPath}}"}, core...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.HasPrefix(pkg, module+"/adapters/") || strings.HasPrefix(pkg, module+"/workflows/") {
			t.Errorf("core depends on %s", pkg)
		}
	}
}
