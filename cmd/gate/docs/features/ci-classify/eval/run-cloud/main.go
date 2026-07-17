// run-cloud emits ci-classify eval JSONL via gate's cloud Model backend.
// Usage (from repo root, ANTHROPIC_API_KEY set):
//
//	go run ./cmd/gate/docs/features/ci-classify/eval/run-cloud -out cmd/gate/docs/features/ci-classify/eval/ci-eval-raw.haiku-cloud.jsonl
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

func main() {
	outPath := flag.String("out", "ci-eval-raw.haiku-cloud.jsonl", "output JSONL path")
	evalDir := flag.String("eval-dir", filepath.Join("docs", "features", "ci-classify", "eval"), "eval bundle directory")
	flag.Parse()

	prompt, err := os.ReadFile(filepath.Join(*evalDir, "ci-classify.prompt.txt"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	schema, err := os.ReadFile(filepath.Join(*evalDir, "ci-classify.schema.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	model, err := verify.ModelBackend("cloud")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	f, err := os.Open(filepath.Join(*evalDir, "ci-lines-v2.jsonl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	out, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer out.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var row struct {
			Input    string         `json:"input"`
			Expected string         `json:"expected"`
			Meta     map[string]any `json:"meta"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		content, err := verify.ModelChat(context.Background(), model, string(prompt), row.Input, schema)
		if err != nil {
			fmt.Fprintf(os.Stderr, "row error: %v\n", err)
			os.Exit(1)
		}
		var output map[string]any
		if err := json.Unmarshal([]byte(content), &output); err != nil {
			fmt.Fprintf(os.Stderr, "bad model json: %v\n", err)
			os.Exit(1)
		}
		rec := map[string]any{
			"expected": row.Expected,
			"meta":     row.Meta,
			"output":   output,
		}
		b, err := json.Marshal(rec)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		out.Write(b)
		out.Write([]byte{'\n'})
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
