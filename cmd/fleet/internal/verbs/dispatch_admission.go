package verbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// DispatchOptions selects the repository and, optionally, the evidence a
// receiving responsibility demands. It does not prescribe how work is done.
type DispatchOptions struct {
	Repo     string
	Requires string
	Head     string
}

func (o DispatchOptions) requirements(slot string) ([]string, error) {
	if o.Requires == "" && o.Head == "" {
		return nil, nil
	}
	if o.Requires == "" || len(o.Head) != 40 || !shaRe.MatchString(o.Head) {
		return nil, refuse("fleet dispatch: --requires needs receipt kinds and --head needs the full 40-character commit SHA")
	}
	if slot != "" {
		return nil, refuse("fleet dispatch: receipt admission currently declares work without --slot; placement needs its own revision check")
	}
	seen := map[string]bool{}
	var required []string
	for _, name := range strings.Split(o.Requires, ",") {
		name = strings.TrimSpace(name)
		if !kindRe.MatchString(name) || seen[name] {
			return nil, refuse("fleet dispatch: --requires wants distinct receipt kinds, got %q", o.Requires)
		}
		seen[name] = true
		required = append(required, name)
	}
	return required, nil
}

// admitDispatch consumes recorded evidence under the receipts lock. The existing
// action journal must accept the decision BEFORE the work row is published.
// A satisfied admission is evidence for that publication, not proof it happened.
func admitDispatch(repo, branch, relationship, by, head, expected string, required []string) (fleet.Rec, error) {
	if len(required) == 0 {
		return nil, nil
	}
	receipts, checkErr := dispatchEvidence(repo, head, expected, required)
	decision := fleet.Rec{
		"repo": repo, "change": branch, "relationship": relationship, "by": by,
		"head": head, "expected_head": expected, "requires": required, "receipts": receipts,
		"at": fleet.Now(), "result": "satisfied",
	}
	if checkErr != nil {
		decision["result"], decision["reason"] = "refused", checkErr.Error()
	}
	if err := fleet.AppendJSONL(fleet.Path("actions.jsonl"), fleet.Rec{"at": decision["at"], "action": "dispatch_admission", "row": decision}); err != nil {
		return nil, fmt.Errorf("record dispatch admission before declaring work: %w", err)
	}
	if checkErr != nil {
		return nil, refuse("fleet dispatch: receipt admission refused: %s", checkErr)
	}
	return decision, nil
}

func dispatchEvidence(repo, head, expected string, required []string) ([]fleet.Rec, error) {
	receipts := []fleet.Rec{}
	if head != expected {
		return receipts, fmt.Errorf("branch resolves to %s, expected %s; recheck the intended revision", head, expected)
	}
	for _, kind := range required {
		r, err := admissionReceipt(repo, head, kind)
		if err != nil {
			return receipts, err
		}
		receipts = append(receipts, r)
		if fleet.S(r, "verdict") != "pass" {
			return receipts, fmt.Errorf("latest %s receipt failed at %s: %s", kind, head, fleet.S(r, "observable"))
		}
	}
	return receipts, nil
}

// The canonical history joins short and full SHA spellings. Its append order,
// not caller timestamps or the latest-file cache, determines the latest verdict.
// Unlike listing views, admission cannot skip unreadable or malformed evidence.
func admissionReceipt(repo, head, kind string) (fleet.Rec, error) {
	_, path := receiptPaths(head, head, kind)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s receipt history at %s (record a local receipt if missing): %w", kind, head, err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		return nil, fmt.Errorf("incomplete %s receipt history at %s", kind, head)
	}
	var latest fleet.Rec
	for i, line := range bytes.Split(b[:len(b)-1], []byte{'\n'}) {
		r, err := admissionRecord(line, head, kind)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		if fleet.S(r, "repo") == repo {
			latest = r
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("no %s receipt for repository %s at %s", kind, repo, head)
	}
	return latest, nil
}

func admissionRecord(line []byte, head, kind string) (fleet.Rec, error) {
	var r fleet.Rec
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, fmt.Errorf("malformed receipt: %w", err)
	}
	if fleet.S(r, "head") != head || fleet.S(r, "kind") != kind {
		return nil, fmt.Errorf("receipt does not name this full head and kind")
	}
	if err := receiptProvenance(r); err != nil {
		return nil, err
	}
	return r, nil
}

func receiptProvenance(r fleet.Rec) error {
	for _, field := range []string{"repo", "session", "observable", "cwd", "worktree"} {
		if strings.TrimSpace(fleet.S(r, field)) == "" {
			return fmt.Errorf("receipt is missing %s", field)
		}
	}
	sha := fleet.S(r, "sha")
	if !shaRe.MatchString(sha) || !strings.HasPrefix(fleet.S(r, "head"), sha) {
		return fmt.Errorf("receipt SHA spelling does not match its full head")
	}
	dirty, present := r["dirty"].(bool)
	if !present || dirty || fleet.F(r, "at") <= 0 {
		return fmt.Errorf("receipt lacks a clean-tree observation and timestamp")
	}
	verdict := fleet.S(r, "verdict")
	if verdict != "pass" && verdict != "fail" {
		return fmt.Errorf("receipt has no pass or fail verdict")
	}
	return nil
}
