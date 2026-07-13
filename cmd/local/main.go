// Command local is the agent-callable face of the shared local primitive.
//
// Pipe input on stdin, pass a task prompt and a JSON Schema, get a structured
// result from the local model on stdout as {"source","reason","result"}. An
// agent uses it as a co-processor — offload a cheap filter/extract/classify —
// and falls back to its own reasoning when the result is flagged low-confidence.
// (The agent is itself the cloud model, so it IS the escalation.)
//
//	local -prompt "<task>" -schema '<json-schema | @file>' [-min-confidence 0.7] < input
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/itsHabib/workbench/local"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "usage" {
		os.Exit(runUsage(os.Args[2:], os.Stdout))
	}

	prompt := flag.String("prompt", "", "system instruction (the task)")
	schema := flag.String("schema", "", "JSON Schema for the output (inline, or @file)")
	minConf := flag.Float64("min-confidence", 0, "flag the result if its confidence is below this")
	flag.Parse()

	if *prompt == "" || *schema == "" {
		fmt.Fprintln(os.Stderr, `usage: local -prompt "<task>" -schema '<json-schema|@file>' [-min-confidence 0.7] < input`)
		os.Exit(2)
	}

	schemaBytes, err := readMaybeFile(*schema)
	if err != nil {
		fmt.Fprintln(os.Stderr, "schema:", err)
		os.Exit(1)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stdin:", err)
		os.Exit(1)
	}

	res, err := local.Ask(context.Background(), local.Req{
		Prompt: *prompt,
		Input:  string(input),
		Schema: json.RawMessage(schemaBytes),
	}, local.Opts{MinConfidence: *minConf})
	if err != nil {
		fmt.Fprintln(os.Stderr, "local:", err)
		os.Exit(1)
	}

	// Reason is set only when the gate flagged the result (low confidence /
	// escalation) — if Ask ever populates it for anything else, pass an
	// explicit flagged bool through instead of piggybacking on this field.
	logUsage(*prompt, res.Source, res.Reason != "")

	out, err := json.Marshal(struct {
		Source string          `json:"source"`
		Reason string          `json:"reason,omitempty"`
		Result json.RawMessage `json:"result"`
	}{res.Source, res.Reason, res.Raw})
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode result:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

// readMaybeFile returns s verbatim, or the contents of the file named after a leading '@'.
func readMaybeFile(s string) ([]byte, error) {
	if len(s) > 0 && s[0] == '@' {
		return os.ReadFile(s[1:])
	}
	return []byte(s), nil
}
