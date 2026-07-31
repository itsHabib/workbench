// codexguard applies deterministic policy to one Codex tool call.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/codexguard/internal/hook"
	"github.com/itsHabib/workbench/cmd/codexguard/internal/policy"
	"github.com/itsHabib/workbench/cmd/codexguard/internal/projection"
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

//go:embed assets/rules/workbench-codexguard.rules
var projectionAssets embed.FS

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	switch args[0] {
	case "decide":
		if len(args) != 1 {
			return usage(stderr)
		}
		return runDecide(stdin, stdout, stderr)
	case "hook":
		if len(args) != 1 {
			return usage(stderr)
		}
		return runHook(stdin, stdout, stderr)
	case "projection":
		return runProjection(args[1:], stdout, stderr)
	}
	return usage(stderr)
}

func usage(stderr io.Writer) int {
	fmt.Fprintln(stderr, "usage: codexguard <decide|hook> < request.json")
	fmt.Fprintln(stderr, "       codexguard projection <status|apply> -home <path> [-expect <sha256>]")
	return codeError
}

func runDecide(stdin io.Reader, stdout, stderr io.Writer) int {
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

func runHook(stdin io.Reader, stdout, stderr io.Writer) int {
	const maxHookInput = 8 << 20
	data, err := io.ReadAll(io.LimitReader(stdin, maxHookInput+1))
	if err != nil {
		fmt.Fprintf(stderr, "codexguard: read hook input: %v\n", err)
		return codeError
	}
	if len(data) > maxHookInput {
		fmt.Fprintln(stderr, "codexguard: hook input exceeds 8 MiB")
		return codeError
	}
	path, err := hook.DefaultAuditPath()
	if err != nil {
		fmt.Fprintf(stderr, "codexguard: resolve hook audit: %v\n", err)
		return codeError
	}
	evaluator := policy.New(policy.ExecGateReader{}, policy.ExecPullRequestReader{})
	adapter := hook.New(evaluator, hook.NewFileAudit(path), os.Getenv("GATE_STATE"), "")
	ctx, cancel := context.WithTimeout(context.Background(), evidenceTimeout)
	defer cancel()
	response, err := adapter.Handle(ctx, data)
	if err != nil {
		fmt.Fprintf(stderr, "codexguard: hook: %v\n", err)
		return codeError
	}
	if response == nil {
		return codePass
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		fmt.Fprintf(stderr, "codexguard: encode hook response: %v\n", err)
		return codeError
	}
	return codePass
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

func runProjection(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	flags := flag.NewFlagSet("projection "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "target Codex home")
	expect := flags.String("expect", "", "exact plan digest from projection status")
	if err := flags.Parse(args[1:]); err != nil {
		return codeError
	}
	if flags.NArg() != 0 {
		return usage(stderr)
	}
	assets, err := reviewedProjectionAssets()
	if err != nil {
		fmt.Fprintf(stderr, "codexguard: projection assets: %v\n", err)
		return codeError
	}
	switch args[0] {
	case "status":
		if *expect != "" {
			fmt.Fprintln(stderr, "codexguard: projection status does not accept -expect")
			return codeError
		}
		plan, err := projection.Inspect(*home, assets)
		return emitProjection(plan, err, stdout, stderr)
	case "apply":
		if *expect == "" {
			fmt.Fprintln(stderr, "codexguard: projection apply requires -expect from status")
			return codeRefused
		}
		plan, err := projection.Apply(*home, *expect, assets)
		return emitProjection(plan, err, stdout, stderr)
	default:
		return usage(stderr)
	}
}

func reviewedProjectionAssets() ([]projection.Asset, error) {
	executable, err := trustedExecutablePath()
	if err != nil {
		return nil, err
	}
	hooks, err := renderedHooks(executable)
	if err != nil {
		return nil, err
	}
	rules, err := projectionAssets.ReadFile("assets/rules/workbench-codexguard.rules")
	if err != nil {
		return nil, err
	}
	return []projection.Asset{
		{Path: "hooks.json", Data: hooks},
		{Path: "rules/workbench-codexguard.rules", Data: rules},
	}, nil
}

func trustedExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve running executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve executable links: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("resolve absolute executable: %w", err)
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return "", fmt.Errorf("inspect executable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("running executable is not a regular file")
	}
	return filepath.Clean(executable), nil
}

func renderedHooks(executable string) ([]byte, error) {
	if !filepath.IsAbs(executable) {
		return nil, errors.New("hook executable path must be absolute")
	}
	command, windowsCommand, err := hookCommands(executable)
	if err != nil {
		return nil, err
	}
	type commandHook struct {
		Type           string `json:"type"`
		Command        string `json:"command"`
		CommandWindows string `json:"commandWindows"`
		Timeout        int    `json:"timeout"`
		StatusMessage  string `json:"statusMessage"`
	}
	type matcher struct {
		Matcher string        `json:"matcher"`
		Hooks   []commandHook `json:"hooks"`
	}
	document := struct {
		Description string               `json:"description"`
		Hooks       map[string][]matcher `json:"hooks"`
	}{
		Description: "Native Codex lifecycle adapter for codexguard policy.",
		Hooks:       map[string][]matcher{},
	}
	for event, message := range map[string]string{
		"PreToolUse":        "Checking deterministic policy",
		"PermissionRequest": "Checking deterministic permission",
		"PostToolUse":       "Recording completion evidence",
	} {
		document.Hooks[event] = []matcher{{
			Matcher: ".*",
			Hooks: []commandHook{{
				Type: "command", Command: command, CommandWindows: windowsCommand,
				Timeout: 20, StatusMessage: message,
			}},
		}}
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode hooks: %w", err)
	}
	return append(data, '\n'), nil
}

func hookCommands(executable string) (string, string, error) {
	if strings.ContainsAny(executable, "\r\n\x00\"") {
		return "", "", errors.New("hook executable path contains unsupported characters")
	}
	posix := "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' hook"
	windows := `"` + executable + `" hook`
	if runtime.GOOS == "windows" {
		posix = windows
	}
	return posix, windows, nil
}

func emitProjection(plan projection.Plan, err error, stdout, stderr io.Writer) int {
	if plan.Version != "" {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if encodeErr := encoder.Encode(plan); encodeErr != nil {
			fmt.Fprintf(stderr, "codexguard: encode projection: %v\n", encodeErr)
			return codeError
		}
	}
	if err == nil {
		return codePass
	}
	fmt.Fprintf(stderr, "codexguard: %v\n", err)
	if errors.Is(err, projection.ErrDivergent) ||
		errors.Is(err, projection.ErrInvalidPlan) ||
		errors.Is(err, projection.ErrPlanChanged) ||
		errors.Is(err, projection.ErrUnsafePath) {
		return codeRefused
	}
	return codeError
}
