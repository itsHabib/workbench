package verbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// RequestOptions names an optional receiving relationship and its input contract.
// Head and Requires are paired; Requires is a comma-separated set of receipt kinds.
type RequestOptions struct {
	Relationship string
	Head         string
	Requires     string
}

func requestOptions(options []RequestOptions) (string, fleet.Rec, error) {
	var opt RequestOptions
	if len(options) > 1 {
		return "", nil, refuse("fleet request: expected one set of options")
	}
	if len(options) == 1 {
		opt = options[0]
	}
	if opt.Relationship == "" {
		opt.Relationship = "implementation"
	}
	if !relationshipRe.MatchString(opt.Relationship) {
		return "", nil, refuse("fleet request: --as wants a short lowercase receipt kind")
	}
	if opt.Head == "" && opt.Requires == "" {
		return opt.Relationship, nil, nil
	}
	if len(opt.Head) != 40 || !shaRe.MatchString(opt.Head) || opt.Requires == "" {
		return "", nil, refuse("fleet request: --head (full lowercase commit SHA) and --requires must be supplied together")
	}
	kinds := strings.Split(opt.Requires, ",")
	sort.Strings(kinds)
	required := make([]any, len(kinds))
	for i, kind := range kinds {
		if !relationshipRe.MatchString(kind) || (i > 0 && kinds[i-1] == kind) {
			return "", nil, refuse("fleet request: --requires wants distinct comma-separated receipt kinds")
		}
		required[i] = kind
	}
	return opt.Relationship, fleet.Rec{"head": opt.Head, "requires": required}, nil
}

func sameEntry(a, b fleet.Rec) bool {
	x, y := fleet.M(a, "entry"), fleet.M(b, "entry")
	return fleet.S(x, "head") == fleet.S(y, "head") &&
		strings.Join(fleet.Strs(x, "requires"), ",") == strings.Join(fleet.Strs(y, "requires"), ",")
}

// admitRequest is called under dispatch, branch and receipt publication locks.
// There is one effect: publishing the assignment together with its input evidence.
func admitRequest(wanted fleet.Rec) error {
	entry := fleet.M(wanted, "entry")
	if err := entryHead(wanted); err != nil {
		return err
	}
	receipts := []fleet.Rec{}
	for _, kind := range fleet.Strs(entry, "requires") {
		r, err := entryReceipt(fleet.S(wanted, "repo"), fleet.S(entry, "head"), kind)
		if err != nil {
			return refuse("fleet request: required %s evidence: %v", kind, err)
		}
		receipts = append(receipts, r)
	}
	entry["receipts"] = receipts
	// Git refs are external to Fleet's locks. Narrow the read-to-publication gap;
	// this remains a historical admission, never a reservation of the Git ref.
	if err := entryHead(wanted); err != nil {
		return err
	}
	return publishRequest(wanted, fleet.S(entry, "head"))
}

func entryHead(wanted fleet.Rec) error {
	head, err := gitOut("rev-parse", "--verify", "refs/heads/"+fleet.S(wanted, "change")+"^{commit}")
	if err != nil {
		return refuse("fleet request: cannot read the required local branch revision: %v", err)
	}
	expected := fleet.S(fleet.M(wanted, "entry"), "head")
	if strings.TrimSpace(head) != expected {
		return refuse("fleet request: stale input revision; expected %s, branch is at %s", expected, strings.TrimSpace(head))
	}
	return nil
}

// entryReceipt reads the authoritative append and requires its last complete
// record to have been published too. A crash between append and index publication
// is a gap, not a passing input. Existing display readers are deliberately looser.
func entryReceipt(repo, head, kind string) (fleet.Rec, error) {
	_, history := receiptPaths(head, head, kind)
	b, err := os.ReadFile(history)
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	if !bytes.HasSuffix(b, []byte("\n")) {
		return nil, fmt.Errorf("history is empty or has a torn tail")
	}
	var last fleet.Rec
	for _, line := range bytes.Split(b[:len(b)-1], []byte("\n")) {
		var r fleet.Rec
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("malformed history: %w", err)
		}
		if err := validateInputReceipt(r, repo, head, kind); err != nil {
			return nil, err
		}
		if last != nil && (fleet.F(r, "at") < fleet.F(last, "at") || (fleet.F(r, "at") == fleet.F(last, "at") && !reflect.DeepEqual(r, last))) {
			return nil, fmt.Errorf("contradictory history ordering")
		}
		last = r
	}
	latest, _ := receiptPaths(fleet.S(last, "sha"), head, kind)
	if !reflect.DeepEqual(last, fleet.ReadJSON(latest)) {
		return nil, fmt.Errorf("history and published receipt disagree; inspect receipt publication")
	}
	if fleet.S(last, "verdict") != "pass" {
		return nil, fmt.Errorf("latest receipt is fail: %s", fleet.S(last, "observable"))
	}
	return last, nil
}

func validateInputReceipt(r fleet.Rec, repo, head, kind string) error {
	if fleet.S(r, "repo") != repo || fleet.S(r, "head") != head || fleet.S(r, "kind") != kind {
		return fmt.Errorf("receipt repository, revision or kind does not match the input")
	}
	sha := fleet.S(r, "sha")
	if !shaRe.MatchString(sha) || !strings.HasPrefix(head, sha) {
		return fmt.Errorf("receipt revision spelling does not match its head")
	}
	dirty, ok := r["dirty"].(bool)
	if !ok || dirty || fleet.F(r, "at") <= 0 || fleet.S(r, "session") == "" || strings.TrimSpace(fleet.S(r, "observable")) == "" {
		return fmt.Errorf("receipt needs clean-tree provenance, timestamp, session and observable")
	}
	if v := fleet.S(r, "verdict"); v != "pass" && v != "fail" {
		return fmt.Errorf("receipt verdict must be pass or fail")
	}
	return nil
}

// validateRecordedEntry checks the retained snapshot, without consulting today's
// mutable inputs. Historical admission survives a changed branch or new verdict.
func validateRecordedEntry(row fleet.Rec) error {
	if _, present := row["entry"]; !present {
		return nil
	}
	entry := fleet.M(row, "entry")
	_, normalized, err := requestOptions([]RequestOptions{{
		Relationship: fleet.S(row, "relationship"), Head: fleet.S(entry, "head"), Requires: strings.Join(fleet.Strs(entry, "requires"), ","),
	}})
	if err != nil || normalized == nil || !reflect.DeepEqual(entry["requires"], normalized["requires"]) || fleet.S(entry, "head") != fleet.S(row, "head_at_dispatch") {
		return fmt.Errorf("invalid entry contract")
	}
	receipts, ok := entry["receipts"].([]any)
	kinds := fleet.Strs(entry, "requires")
	if !ok || len(receipts) != len(kinds) {
		return fmt.Errorf("entry receipt snapshots are missing")
	}
	for i, value := range receipts {
		r, _ := value.(map[string]any)
		if err := validateInputReceipt(r, fleet.S(row, "repo"), fleet.S(entry, "head"), kinds[i]); err != nil {
			return err
		}
		if fleet.S(r, "verdict") != "pass" {
			return fmt.Errorf("entry snapshot is not pass")
		}
	}
	return nil
}
