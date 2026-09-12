package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/cmd/standup/internal/standup"
	"github.com/itsHabib/workbench/filelock"
)

func mutates(verb string) bool {
	switch verb {
	case "agenda", "new", "draft", "confirm", "apply":
		return true
	}
	return false
}

func lockStore(env standup.Env) (func(), error) {
	if err := os.MkdirAll(env.Dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(env.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := filelock.TryLock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("standup store busy: %w", err)
	}
	return func() { _ = f.Close() }, nil
}

func expectDigest(r *standup.Record, expected string) error {
	if expected != "" && r.PlanDigest() != expected {
		return &standup.Refusal{Reason: "plan changed; read back the current version before confirming or applying"}
	}
	return nil
}

func printJSON(out, errOut io.Writer, v any) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail(errOut, err)
	}
	return codeOK
}

func printView(out, errOut io.Writer, path string, r *standup.Record) int {
	return printJSON(out, errOut, map[string]any{"path": path, "plan_digest": r.PlanDigest(), "record": r, "readback": r.Readback()})
}

func cmdDraft(env standup.Env, args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flags("draft", errOut)
	expected := fs.String("expect", "", "plan digest from show")
	file := fs.String("file", "", "JSON draft file, or - for stdin")
	if fs.Parse(positionalFirst(args)) != nil || fs.NArg() != 1 || *expected == "" || *file == "" {
		return codeUsage
	}
	if *file != "-" {
		f, err := os.Open(*file)
		if err != nil {
			return fail(errOut, err)
		}
		defer f.Close()
		in = f
	}
	d, err := standup.DecodeDraft(in)
	if err != nil {
		return fail(errOut, err)
	}
	path := env.ResolveRecord(fs.Arg(0))
	r, err := standup.LoadRecord(path)
	if err != nil {
		return fail(errOut, err)
	}
	if err := standup.UpdateDraft(r, d, *expected); err != nil {
		return fail(errOut, err)
	}
	if err := r.Save(path); err != nil {
		return fail(errOut, err)
	}
	return printView(out, errOut, path, r)
}

func cmdInspect(env standup.Env, verb string, args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		return codeUsage
	}
	cfg, err := env.LoadConfig()
	if err != nil {
		return fail(errOut, err)
	}
	r, err := standup.LoadRecord(env.ResolveRecord(args[0]))
	if err != nil {
		return fail(errOut, err)
	}
	if r.Tenant != cfg.Tenant || r.Lead != cfg.Lead {
		return fail(errOut, &standup.Refusal{Reason: "record identity differs from configured tenant/lead"})
	}
	if verb == "prepare" {
		result, err := standup.Prepare(env, cfg, r)
		if code := printJSON(out, errOut, result); code != 0 {
			return code
		}
		if err != nil {
			return fail(errOut, err)
		}
		return codeOK
	}
	result, err := standup.ReadStatus(env, cfg, r)
	if err != nil {
		return fail(errOut, err)
	}
	return printJSON(out, errOut, result)
}
