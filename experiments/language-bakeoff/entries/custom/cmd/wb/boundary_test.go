package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The extension claim, checked against the import graph: the language and
// the planner build without any adapter package, so adding a kind cannot
// require editing them.
func TestLanguageAndPlannerImportNoAdapters(t *testing.T) {
	const module = "github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom"
	out, err := exec.Command("go", "list", "-deps", module+"/internal/lang", module+"/internal/plan").Output()
	if err != nil {
		t.Fatal(err)
	}
	deps := strings.Fields(string(out))
	for _, dep := range deps {
		if strings.HasPrefix(dep, module+"/adapters/") {
			t.Errorf("%s depends on adapter package %s", "lang/plan", dep)
		}
	}
	if len(deps) == 0 {
		t.Fatal("go list returned no dependencies")
	}
}
