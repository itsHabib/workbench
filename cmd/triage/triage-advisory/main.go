// triage-advisory joins the deterministic floor with a verified agent advisory:
//
//	gh pr diff N -R owner/repo | triage-advisory -proposal '{"escalate":"T3",...}'
//	gh pr diff N -R owner/repo | triage-advisory -proposal @proposal.json -v
//
// stdin is the unified diff; -proposal is the host agent's escalation JSON
// (inline or @file). Output is the Verdict: floor, the proposal's verifier
// outcome, final = max(floor, trusted escalation), route. A rejected proposal
// is reported (with reasons the host can retry on) and contributes nothing.
// Exit 1 only on operational failure — never as a tier.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/triage/internal/advisory"
	"github.com/itsHabib/workbench/cmd/triage/internal/floor"
)

func main() {
	proposal := flag.String("proposal", "", "escalation proposal JSON (inline or @file); empty = none")
	verbose := flag.Bool("v", false, "human-readable output")
	flag.Parse()

	p, err := readProposal(*proposal)
	if err != nil {
		fmt.Fprintln(os.Stderr, "triage-advisory:", err)
		os.Exit(1)
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "triage-advisory: reading stdin:", err)
		os.Exit(1)
	}
	diffText := string(raw)
	if strings.TrimSpace(diffText) == "" {
		fmt.Fprintln(os.Stderr, "triage-advisory: empty diff on stdin (fail-closed: classify by hand at T2 per RUBRIC)")
		os.Exit(1)
	}

	d, err := floor.ParseUnifiedDiff(strings.NewReader(diffText))
	if err != nil {
		fmt.Fprintln(os.Stderr, "triage-advisory:", err)
		os.Exit(1)
	}
	res := floor.Classify(d)
	v := advisory.Merge(res, diffText, p)

	if *verbose {
		fmt.Printf("floor=%s advisory=%s final=%s route=%s\n", v.Floor, v.Escalate, v.Final, v.Route)
		for _, r := range v.Rejected {
			fmt.Printf("  rejected: %s\n", r)
		}
		return
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "triage-advisory: encode:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

// readProposal parses the -proposal value: empty means no escalation, @path
// reads a file, anything else is inline JSON.
func readProposal(arg string) (advisory.Proposal, error) {
	var p advisory.Proposal
	if arg == "" {
		return p, nil
	}
	raw := []byte(arg)
	if strings.HasPrefix(arg, "@") {
		b, err := os.ReadFile(arg[1:])
		if err != nil {
			return p, fmt.Errorf("reading proposal file: %w", err)
		}
		raw = b
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("parsing proposal JSON: %w", err)
	}
	return p, nil
}
