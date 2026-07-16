// Command triage-floor reads a unified diff on stdin and prints the
// deterministic risk floor as JSON.
//
//	gh pr diff <n> -R owner/repo | triage-floor
//	git diff origin/main... | triage-floor
//
// The floor is the reproducible safety layer. The agent advisory pass
// (the /pr-risk skill) may only escalate above it.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/triage/internal/floor"
)

func main() {
	d := floor.ParseUnifiedDiff(os.Stdin)
	res := floor.Classify(d)

	if len(os.Args) > 1 && os.Args[1] == "-v" {
		fmt.Printf("floor=%s  files=%d  +%d/-%d\n", res.FloorS, res.Files, res.Added, res.Removed)
		for _, s := range res.Signals {
			fmt.Printf("  %-20s %s  %s\n", s.TierS, s.Name, s.Why)
		}
		return
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}
