package verbs

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func dispatchPlan(args []string) error {
	if len(args) < 2 {
		return refuse("usage: fleet plan <intent.json> [--out plan.json] | fleet apply <plan.json> --expect-digest <digest>")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	output := fs.String("out", "", "save compiled plan (new file only)")
	digest := fs.String("expect-digest", "", "approved plan digest")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return refuse("unexpected plan/apply arguments")
	}
	if args[0] == "apply" {
		if *output != "" {
			return refuse("apply does not accept --out")
		}
		return applyWorkFile(args[1], *digest)
	}
	if *digest != "" {
		return refuse("plan does not accept --expect-digest")
	}
	var intent workIntent
	if err := decodePlanJSON(args[1], &intent); err != nil {
		return err
	}
	p, err := buildWorkPlan(intent)
	if err != nil {
		return err
	}
	if *output != "" {
		if err := saveWorkPlan(*output, p); err != nil {
			return err
		}
	}
	say("Plan %s\nState: %s\nCaller: %s\nUnseated work only; no worker launch or completion claim.", p.Digest, p.State, p.By)
	for _, a := range p.Actions {
		say("%-8s %s: %s %s/%s for %s\n  head %s; due %s\n  %s\n  %s", a.Action, a.Work.Name, a.Work.Repo, a.Work.Change, a.Work.As, a.Work.For, a.Head, a.Work.Due, a.Work.Brief, a.Reason)
	}
	return nil
}
func saveWorkPlan(path string, p workPlan) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	err = enc.Encode(p)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	return closeErr
}

func applyWorkFile(path, digest string) error {
	var p workPlan
	if err := decodePlanJSON(path, &p); err != nil {
		return err
	}
	results, err := applyWorkPlan(p, digest)
	if results == nil {
		return err
	}
	if writeErr := json.NewEncoder(Out).Encode(results); writeErr != nil {
		return writeErr
	}
	return err
}
