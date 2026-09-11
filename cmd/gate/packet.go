package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"runtime/debug"

	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// buildRevision is set explicitly for local builds because Go can stamp the
// enclosing repository when building from a nested Git worktree.
var buildRevision string

func gateVersion() map[string]any {
	revision := buildRevision
	if revision == "" {
		revision = "unknown (unstamped local build)"
	}
	result := map[string]any{"revision": revision}
	if info, ok := debug.ReadBuildInfo(); ok {
		result["module_version"] = info.Main.Version
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				result["go_vcs_revision"] = setting.Value
			case "vcs.modified":
				result["go_vcs_modified"] = setting.Value == "true"
			}
		}
	}
	return result
}

func cmdPacket(args []string) error {
	fs := flag.NewFlagSet("packet", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	run := fs.String("run", "", "parked run to inspect")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *run == "" {
		return errors.New("packet: -run required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	arts, err := e.st.Run(*run)
	if err != nil {
		return err
	}
	_, esc, subject, err := runVerdicts(arts)
	if err != nil {
		return err
	}
	if esc == "" {
		return errors.New("packet: run has no escalation")
	}
	packet, err := verify.JudgmentPacket(arts, subject)
	if err != nil {
		return err
	}
	printJSON(map[string]any{"run": *run, "subject": subject, "gate": gateVersion(), "packet": packet})
	return nil
}

func cmdEvidence(args []string) error {
	fs := flag.NewFlagSet("evidence", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	run := fs.String("run", "", "unjudged run to supplement")
	grant := fs.String("grant", "", "live grant id")
	var paths []string
	fs.Func("path", "exact repository path; repeat, or omit to collect all required source", func(value string) error { paths = append(paths, value); return nil })
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *run == "" || *grant == "" || len(paths) > 32 {
		return errors.New("evidence: -run and -grant required; at most 32 -path values")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	id, err := supplementEvidence(e, *run, *grant, paths, evidence.CurrentHead, evidence.ExactSource, evidence.ExactPaths)
	if err != nil {
		return err
	}
	printJSON(map[string]any{"run": *run, "evidence": id, "gate": gateVersion(), "next": "gate packet -run " + *run + " -state " + shellQuote(*stateDir)})
	return nil
}

type sourceReader func(repo, head, path string) (string, string, error)

func supplementEvidence(e env, run, grant string, paths []string, head func(string, int) (string, error), read sourceReader, index func(string, string) ([]string, error)) (string, error) {
	arts, err := e.st.Run(run)
	if err != nil {
		return "", err
	}
	_, esc, subject, err := runVerdicts(arts)
	if err != nil {
		return "", err
	}
	grantCap, err := checkGateCapability(e, grant, subject, e.now)
	if err != nil {
		return "", err
	}
	if err := checkEvidenceRepair(arts, run, esc, 0); err != nil {
		return "", err
	}
	current, err := head(subject.Repo, subject.Number)
	if err != nil {
		return "", err
	}
	if current != subject.HeadSHA {
		return "", errors.New("evidence_head_changed: start a new review run")
	}
	fileIndex, err := index(subject.Repo, subject.HeadSHA)
	if err != nil {
		return "", err
	}
	body := verify.SourceEvidence{Subject: subject, FileIndex: fileIndex, IndexComplete: true}
	if len(paths) == 0 {
		raw, _ := json.Marshal(body)
		augmented := append(append([]state.Artifact(nil), arts...), state.Artifact{Kind: state.KindEvidence, Body: raw})
		packet, err := verify.JudgmentPacket(augmented, subject)
		if err != nil {
			return "", err
		}
		paths = packet.RequiredSources
	}
	if len(paths) > 32 {
		return "", fmt.Errorf("evidence_path_limit: %d required paths; select at most 32 with -path", len(paths))
	}
	bytes := 0
	seen := make(map[string]bool)
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		content, blob, err := read(subject.Repo, subject.HeadSHA, path)
		if err != nil {
			return "", err
		}
		bytes += len(content)
		if bytes > verify.SourceBudget {
			return "", errors.New("evidence_budget_exceeded: 256 KiB per run")
		}
		body.Sources = append(body.Sources, verify.SourceFile{Path: path, Blob: blob, Content: content})
	}
	current, err = head(subject.Repo, subject.Number)
	if err != nil {
		return "", err
	}
	if current != subject.HeadSHA {
		return "", errors.New("evidence_head_changed: start a new review run")
	}
	// One locked append: cannot supplement a settled judgment or race past the bound.
	a, err := e.st.AppendIfAbsentParentWhereAfterAudit(state.KindEvidence, []string{state.KindJudgment}, run, esc, []string{esc, grant}, body, nil, func(audit state.AuditResult) error {
		if e.now().After(grantCap.ExpiresAt) {
			return errors.New("grant_expired: evidence repair")
		}
		return checkEvidenceRepair(audit.All, run, esc, bytes)
	})
	return a.ID, err
}

func checkEvidenceRepair(arts []state.Artifact, run, esc string, added int) error {
	if esc == "" || newestTerminal(arts, run) != esc {
		return errors.New("evidence_repair_closed: no open escalation")
	}
	count, total := 0, added
	for _, a := range arts {
		if a.Run != run {
			continue
		}
		if a.Kind == state.KindJudgment {
			return errors.New("evidence_repair_closed: judgment is one-shot; substantive outcomes require a new run")
		}
		var s verify.SourceEvidence
		if a.Kind != state.KindEvidence || json.Unmarshal(a.Body, &s) != nil || (len(s.Sources) == 0 && !s.IndexComplete) {
			continue
		}
		count++
		for _, f := range s.Sources {
			total += len(f.Content)
		}
	}
	if count >= 3 {
		return errors.New("evidence_repair_limit: three supplements per unjudged run")
	}
	if total > verify.SourceBudget {
		return fmt.Errorf("evidence_budget_exceeded: %d exceeds 256 KiB per run", total)
	}
	return nil
}

func cmdPacketTools(command string, args []string) error {
	switch command {
	case "packet":
		return cmdPacket(args)
	case "evidence":
		return cmdEvidence(args)
	default:
		printJSON(gateVersion())
		return nil
	}
}
