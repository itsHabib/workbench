package serve

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Dependency is one external binary the ingress needs to complete a resolution,
// and what breaks when it is missing. The consequence travels with the name so a
// startup refusal can say what stops working rather than only what is absent.
type Dependency struct {
	Name string
	// Why names what the ingress cannot do without it — the sentence an operator
	// reads at 2am off a restart loop in escalate-serve.err.log.
	Why string
}

// Dependencies are the binaries `serve` requires. They are checked together at
// startup, not lazily at first use, because every one of them is reached only
// AFTER a human taps a button: a lazily-discovered missing binary shows up as a
// failed approval, at the one moment the operator is least able to debug it.
//
// gh is on this list even though escalate never invokes it. escalate execs gate,
// and gate shells gh to post the gate/authorized status that is the PR-visible
// half of an approval. Under launchd the daemon's PATH is
// /usr/bin:/bin:/usr/sbin:/sbin and gh lives under /opt/homebrew/bin, so every
// phone approval logged `exec: "gh": executable file not found in $PATH` while
// the Slack card reported success. Checking a child's dependency is a small
// coupling; the alternative is an ingress that looks healthy and is not.
var Dependencies = []Dependency{
	{Name: "gh", Why: "gate cannot post the gate/authorized commit status, so an approval leaves no PR-visible receipt"},
}

// LookPath resolves a binary name to an absolute path. It is a seam so the
// startup check is testable without mutating the process PATH.
type LookPath func(string) (string, error)

// Preflight refuses to start when a required external binary is unresolvable.
//
// It fails CLOSED, matching the two checks it sits beside (SLACK_SIGNING_SECRET
// and the user allowlist): an ingress that cannot complete the work a tap starts
// is not a working ingress, and with KeepAlive + ThrottleInterval a refusal is a
// legible restart loop the operator sees, rather than a silent degradation
// discovered days later in a log nobody reads. That is the whole point — this
// class of failure was invisible precisely because it was tolerated.
//
// gateBin is checked separately from Dependencies because the plist renders it
// as an absolute path: an absolute path that does not exist must fail as a bad
// path, not be searched for on PATH.
func Preflight(gateBin string, look LookPath) error {
	if look == nil {
		look = exec.LookPath
	}
	if err := checkGateBinary(gateBin, look); err != nil {
		return err
	}
	var missing []string
	for _, d := range Dependencies {
		if _, err := look(d.Name); err != nil {
			missing = append(missing, fmt.Sprintf("%s (%s): %v", d.Name, d.Why, err))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"serve: required binaries are unresolvable:\n  %s\nPATH=%q\nlaunchd does not read a shell profile; render the agent's PATH in the plist (cmd/flare/scripts) rather than relying on the default",
		strings.Join(missing, "\n  "), os.Getenv("PATH"))
}

// checkGateBinary resolves the gate binary the ingress will exec. A path with a
// separator is taken literally — that is what the plist renders — so a stale
// absolute path reports as a missing file instead of being quietly searched for
// on PATH and resolving to a different build.
func checkGateBinary(gateBin string, look LookPath) error {
	if gateBin == "" {
		return fmt.Errorf("serve: no gate binary configured")
	}
	if !strings.ContainsRune(gateBin, os.PathSeparator) {
		if _, err := look(gateBin); err != nil {
			return fmt.Errorf("serve: gate binary %q is unresolvable on PATH=%q: %w", gateBin, os.Getenv("PATH"), err)
		}
		return nil
	}
	info, err := os.Stat(gateBin)
	if err != nil {
		return fmt.Errorf("serve: gate binary %q is not usable: %w", gateBin, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("serve: gate binary %q is not an executable file", gateBin)
	}
	return nil
}
