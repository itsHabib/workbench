// dispatch decides placement — which engine, provider, model, and effort a task
// gets — from a versioned, content-hashed policy file. It never dispatches,
// polls, or lands: it reads a policy and a task descriptor and emits a decision
// (ship's dispatch verb executes it). Policy vs mechanism, held as a name.
//
//	dispatch decide   --policy p [--task json | stdin] [--receipts p]
//	dispatch validate --policy p
//
// The exit-code contract is the load-bearing surface — every downstream
// consumer keys on it, and no non-zero exit ever emits a placement on stdout:
//
//	decide:   0 placed · 2 bad/missing/empty policy or unknown task_class in a
//	          match block · 3 no rule matched (values on stderr) · 4 bad
//	          descriptor · 5 --receipts append failed (nothing on stdout)
//	validate: 0 valid · 1 valid-with-warnings (no catch-all) · 2 invalid
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/dispatch/internal/placement"
	"github.com/itsHabib/workbench/cmd/dispatch/internal/policy"
	"github.com/itsHabib/workbench/cmd/dispatch/internal/receipt"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr, os.Stdin))
}

func run(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	if len(args) < 2 {
		return emitError(stderr, 2, "usage: dispatch decide|validate --policy <path>")
	}
	switch args[1] {
	case "decide":
		return decide(args[2:], stdout, stderr, stdin)
	case "validate":
		return validate(args[2:], stdout, stderr)
	}
	return emitError(stderr, 2, fmt.Sprintf("unknown verb %q (want decide|validate)", args[1]))
}

// decide runs the phase-1 happy path and its fail-closed exits (spec §7.1/§7.2).
// The receipt is appended before the placement reaches stdout, so exit 5 leaves
// stdout empty and a caller can never consume a placement from a failed
// invocation.
func decide(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	fs := flag.NewFlagSet("decide", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyPath := fs.String("policy", "", "path to the policy JSON file")
	taskJSON := fs.String("task", "", "task descriptor JSON (else read from stdin)")
	receiptsPath := fs.String("receipts", "", "append a decision receipt to this JSONL file")
	if err := fs.Parse(args); err != nil {
		return emitError(stderr, 2, err.Error())
	}
	if *policyPath == "" {
		return emitError(stderr, 2, "policy: --policy is required")
	}
	loaded, err := policy.Load(*policyPath)
	if err != nil {
		return emitError(stderr, 2, err.Error())
	}
	raw, err := readDescriptor(*taskJSON, stdin)
	if err != nil {
		return emitError(stderr, 4, fmt.Sprintf("descriptor: read: %v", err))
	}
	d, err := placement.ParseDescriptor(raw)
	if err != nil {
		return emitError(stderr, 4, err.Error())
	}
	pl, ok := placement.Decide(loaded, d)
	if !ok {
		return emitError(stderr, 3, "no rule matched: "+d.UnmatchedValues())
	}
	if code := writeReceipt(*receiptsPath, loaded, d, pl, stderr); code != 0 {
		return code
	}
	return emitPlacement(stdout, stderr, pl)
}

// writeReceipt appends the decision receipt when --receipts is set. Returns 0
// when there is nothing to write or the write succeeded, and 5 (fail-closed) on
// an append failure — the caller must not emit a placement after that.
func writeReceipt(path string, loaded policy.Loaded, d placement.Descriptor, pl placement.Placement, stderr io.Writer) int {
	if path == "" {
		return 0
	}
	rec := receipt.Receipt{
		DecidedAt:    time.Now().UTC(),
		Rule:         pl.Provenance.Rule,
		PolicySHA256: loaded.SHA256,
		Descriptor:   d,
		Placement:    pl,
	}
	if err := receipt.Append(path, rec); err != nil {
		return emitError(stderr, 5, err.Error())
	}
	return 0
}

func emitPlacement(stdout, stderr io.Writer, pl placement.Placement) int {
	b, err := json.Marshal(pl)
	if err != nil {
		return emitError(stderr, 5, fmt.Sprintf("placement: marshal: %v", err))
	}
	_, _ = stdout.Write(append(b, '\n'))
	return 0
}

// validate is the author pre-flight: the same loader as decide, plus a
// catch-all lint. A policy with no match:{} rule loads fine but will exit-3 some
// descriptor, so it is a warning (exit 1), not an error.
func validate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyPath := fs.String("policy", "", "path to the policy JSON file")
	if err := fs.Parse(args); err != nil {
		return emitError(stderr, 2, err.Error())
	}
	if *policyPath == "" {
		return emitError(stderr, 2, "policy: --policy is required")
	}
	loaded, err := policy.Load(*policyPath)
	if err != nil {
		return emitError(stderr, 2, err.Error())
	}
	writeValid(stdout, loaded)
	if !loaded.Policy.HasCatchAll() {
		fmt.Fprintln(stderr, `{"level":"warn","message":"policy has no catch-all rule (match: {}); a descriptor matching no rule will exit 3"}`)
		return 1
	}
	return 0
}

type validReport struct {
	Valid         bool   `json:"valid"`
	PolicyVersion int    `json:"policy_version"`
	PolicySHA256  string `json:"policy_sha256"`
	Rules         int    `json:"rules"`
}

func writeValid(stdout io.Writer, loaded policy.Loaded) {
	b, _ := json.Marshal(validReport{
		Valid:         true,
		PolicyVersion: loaded.Policy.Version,
		PolicySHA256:  loaded.SHA256,
		Rules:         len(loaded.Policy.Rules),
	})
	fmt.Fprintln(stdout, string(b))
}

// readDescriptor returns the descriptor bytes: the --task value when set, else
// all of stdin.
func readDescriptor(taskFlag string, stdin io.Reader) ([]byte, error) {
	if taskFlag != "" {
		return []byte(taskFlag), nil
	}
	return io.ReadAll(stdin)
}

type cliError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// emitError writes a single-line JSON error to stderr and returns the exit
// code, so a caller can `return emitError(...)`. Errors are values on stderr;
// no partial placement is ever emitted on a non-zero exit.
func emitError(stderr io.Writer, code int, msg string) int {
	b, _ := json.Marshal(cliError{Code: code, Message: msg})
	fmt.Fprintln(stderr, string(b))
	return code
}
