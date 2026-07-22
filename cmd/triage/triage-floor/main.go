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
	"flag"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/triage/internal/floor"
)

func main() {
	// Explicit ContinueOnError FlagSet, not the default ExitOnError: this binary
	// is a load-bearing seam whose contract is exit 0 = classification, 1 =
	// operational failure. stdlib ExitOnError would exit 0 on -h and 2 on a bad
	// flag, both of which gate's exit-code ladder misreads. Map any parse error
	// (including -h) to exit 1 so the seam only ever returns 0 for a real result.
	fs := flag.NewFlagSet("triage-floor", flag.ContinueOnError)
	repo := fs.String("repo", "", "repository identity owner/name; enables that repo's compiled-in path overrides (absent = none)")
	verbose := fs.Bool("v", false, "human-readable output")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}

	d, err := floor.ParseUnifiedDiff(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "triage-floor:", err)
		os.Exit(1)
	}
	res := floor.ClassifyRepo(d, *repo)

	if *verbose {
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
