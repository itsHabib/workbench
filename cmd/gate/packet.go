package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// buildRevision is set explicitly for local builds because Go can stamp the
// enclosing repository when building from a nested Git worktree.
var buildRevision string

// The real store's generated evidence ID width is checked by a regression.
const candidateEvidenceID = "evd_0000000000000000"

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
	if err := checkEvidenceRepair(arts, run, esc, nil); err != nil {
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
	// A path already collected at this exact head adds nothing; skip it rather
	// than record a second copy against the shared allowance.
	seen := recordedSourcePaths(arts, run)
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
		if bytes > verify.RequiredEvidenceBudget {
			return "", fmt.Errorf("evidence_budget_exceeded: %d KiB shared packet budget", verify.RequiredEvidenceBudget/1024)
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
		if err := checkEvidenceRepair(audit.All, run, esc, body.Sources); err != nil {
			return err
		}
		return checkPacketEvidenceBudget(audit.All, run, body)
	})
	return a.ID, err
}

func recordedSourcePaths(arts []state.Artifact, run string) map[string]bool {
	paths := make(map[string]bool)
	for _, a := range arts {
		var s verify.SourceEvidence
		if a.Run != run || a.Kind != state.KindEvidence || json.Unmarshal(a.Body, &s) != nil {
			continue
		}
		for _, f := range s.Sources {
			paths[f.Path] = true
		}
	}
	return paths
}

// checkEvidenceRepair bounds supplements by count and by raw source bytes, the
// early bound ahead of the rendered check. Each recorded path and blob counts
// once, as the packet renders it once.
func checkEvidenceRepair(arts []state.Artifact, run, esc string, added []verify.SourceFile) error {
	if esc == "" || newestTerminal(arts, run) != esc {
		return errors.New("evidence_repair_closed: no open escalation")
	}
	count, total := 0, 0
	counted := make(map[verify.SourceFile]bool)
	addSource := func(f verify.SourceFile) {
		if counted[f] {
			return
		}
		counted[f] = true
		total += len(f.Content)
	}
	for _, f := range added {
		addSource(f)
	}
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
			addSource(f)
		}
	}
	if count >= 3 {
		return errors.New("evidence_repair_limit: three supplements per unjudged run")
	}
	if total > verify.RequiredEvidenceBudget {
		return fmt.Errorf("evidence_budget_exceeded: %d exceeds %d KiB shared packet budget", total, verify.RequiredEvidenceBudget/1024)
	}
	return nil
}

// Check the same renderer while holding the append lock. Raw source size alone
// cannot account for required reviews, headers, or a concurrent supplement.
// Sources may displace a required diff while a run is still incomplete, since
// smaller exact-head source can then repair it. A packet that is already
// complete never admits a supplement that would leave it incomplete.
func checkPacketEvidenceBudget(arts []state.Artifact, run string, body verify.SourceEvidence) error {
	var current []state.Artifact
	for _, a := range arts {
		if a.Run == run {
			current = append(current, a)
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	// The placeholder has the same width as the store's generated evidence ID.
	candidate := append(append([]state.Artifact(nil), current...), state.Artifact{ID: candidateEvidenceID, Kind: state.KindEvidence, Run: run, Body: raw})
	packet, err := verify.JudgmentPacket(candidate, body.Subject)
	if err != nil {
		return err
	}
	if packet.EvidenceBudgetExceeded {
		return fmt.Errorf("evidence_budget_exceeded: required sources and reviews exceed %d KiB shared packet budget", verify.RequiredEvidenceBudget/1024)
	}
	if packet.Complete {
		return nil
	}
	before, err := verify.JudgmentPacket(current, body.Subject)
	if err != nil {
		return err
	}
	if before.Complete {
		return fmt.Errorf("evidence_budget_exceeded: supplement would displace required evidence from a complete packet: %s", strings.Join(packet.Missing, "; "))
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
