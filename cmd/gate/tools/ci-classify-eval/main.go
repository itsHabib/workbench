// ci-classify-eval emits ci-classify eval JSONL via gate's cloud Model backend.
// Usage (from any working directory, ANTHROPIC_API_KEY set):
//
//	go run ./cmd/gate/tools/ci-classify-eval -out cmd/gate/docs/features/ci-classify/eval/ci-eval-raw.haiku-cloud.jsonl
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	outPath := flag.String("out", "ci-eval-raw.haiku-cloud.jsonl", "output JSONL path")
	evalDir := flag.String("eval-dir", "", "eval bundle directory (default: cmd/gate/docs/features/ci-classify/eval under repo root)")
	repoRoot := flag.String("repo-root", "", "workbench module root (default: derived from command source location)")
	flag.Parse()

	opts := pathOptions{
		repoRootFlag: *repoRoot,
		evalDirFlag:  *evalDir,
		outFlag:      *outPath,
		sourceDir:    commandSourceDir(),
	}
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "eval-dir" {
			opts.evalDirExplicit = true
		}
	})

	paths, err := resolvePaths(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := runCloudEval(paths); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
