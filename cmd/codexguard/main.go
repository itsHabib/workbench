// codexguard applies deterministic policy to one Codex tool call.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/codexguard/internal/policy"
	"github.com/itsHabib/workbench/contracts/automode"
)

const evidenceTimeout = 15 * time.Second

const (
	codePass    = 0
	codeBlocked = 1
	codeParked  = 2
	codeRefused = 3
	codeError   = 4
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "decide" {
		fmt.Fprintln(stderr, "usage: codexguard decide < request.json")
		return codeError
	}
	var request policy.Request
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		fmt.Fprintf(stderr, "codexguard: decode request: %v\n", err)
		return codeRefused
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		fmt.Fprintln(stderr, "codexguard: decode request: expected exactly one JSON object")
		return codeRefused
	}
	if request.GateState == "" {
		request.GateState = os.Getenv("GATE_STATE")
	}
	evaluator := policy.New(policy.ExecGateReader{}, policy.ExecPullRequestReader{})
	ctx, cancel := context.WithTimeout(context.Background(), evidenceTimeout)
	defer cancel()
	decision, err := evaluator.Evaluate(ctx, request)
	if err != nil {
		fmt.Fprintf(stderr, "codexguard: evaluate: %v\n", err)
		return codeError
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(decision); err != nil {
		fmt.Fprintf(stderr, "codexguard: encode decision: %v\n", err)
		return codeError
	}
	return outcomeCode(decision.Outcome)
}

func outcomeCode(outcome string) int {
	switch outcome {
	case automode.OutcomePass:
		return codePass
	case automode.OutcomeBlock:
		return codeBlocked
	case automode.OutcomePark:
		return codeParked
	case automode.OutcomeRefuse:
		return codeRefused
	}
	panic(errors.New("validated decision has unknown outcome"))
}
