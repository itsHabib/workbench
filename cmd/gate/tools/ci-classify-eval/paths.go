package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const workbenchModule = "github.com/itsHabib/workbench"

var defaultEvalRel = filepath.Join("cmd", "gate", "docs", "features", "ci-classify", "eval")

type pathOptions struct {
	repoRootFlag    string
	evalDirFlag     string
	outFlag         string
	evalDirExplicit bool
	sourceDir       string
}

type resolvedPaths struct {
	repoRoot string
	evalDir  string
	outPath  string
}

func commandSourceDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(file)
}

func findModuleRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("ci-classify-eval: resolve source directory %q: %w", start, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("ci-classify-eval: could not find workbench module root from %q; pass -repo-root explicitly", start)
		}
		dir = parent
	}
}

func validateWorkbenchModule(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("ci-classify-eval: read go.mod at %q: %w", root, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		mod := strings.TrimSpace(strings.TrimPrefix(line, "module "))
		if mod != workbenchModule {
			return fmt.Errorf(
				"ci-classify-eval: %q is not the workbench module (go.mod declares %q); pass -repo-root to the workbench checkout",
				root,
				mod,
			)
		}
		return nil
	}
	return fmt.Errorf("ci-classify-eval: no module directive in go.mod at %q", root)
}

func resolvePaths(opts pathOptions) (resolvedPaths, error) {
	root := opts.repoRootFlag
	if root == "" {
		found, err := findModuleRoot(opts.sourceDir)
		if err != nil {
			return resolvedPaths{}, err
		}
		root = found
	}
	if err := validateWorkbenchModule(root); err != nil {
		return resolvedPaths{}, err
	}

	evalDir := opts.evalDirFlag
	if !opts.evalDirExplicit {
		evalDir = filepath.Join(root, defaultEvalRel)
	}

	return resolvedPaths{
		repoRoot: root,
		evalDir:  evalDir,
		outPath:  opts.outFlag,
	}, nil
}
