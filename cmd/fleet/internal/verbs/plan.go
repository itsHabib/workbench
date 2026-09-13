package verbs

// The plan POC records unseated work in the existing dispatch store only.
import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const intentSchema = "fleet-work.v0"
const planSchema = "fleet-plan.v0"

type workIntent struct {
	Schema string        `json:"schema"`
	Work   []desiredWork `json:"work"`
}
type desiredWork struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Change string `json:"change"`
	For    string `json:"for"`
	As     string `json:"as"`
	Brief  string `json:"brief"`
	Due    string `json:"due_at"`
}
type workPlan struct {
	Schema  string       `json:"schema"`
	State   string       `json:"state"`
	By      string       `json:"by"`
	Actions []workAction `json:"actions"`
	Digest  string       `json:"digest"`
}
type workAction struct {
	Work     desiredWork `json:"work"`
	RepoID   string      `json:"repo_id"`
	Head     string      `json:"head"`
	Expected string      `json:"expected"`
	Action   string      `json:"action"`
	Reason   string      `json:"reason,omitempty"`
}
type workResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func decodePlanJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
func planHash(value any) string {
	data, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func planDigest(p workPlan) string { p.Digest = ""; return planHash(p) }
func validateWork(w desiredWork) error {
	if !requestID.MatchString(w.Name) || !filepath.IsAbs(w.Repo) || strings.TrimSpace(w.For) == "" || strings.TrimSpace(w.Brief) == "" || !relationshipRe.MatchString(w.As) {
		return refuse("work %q: requires name, absolute repo checkout, for, as and nonempty brief", w.Name)
	}
	if w.Change == "" || strings.HasPrefix(w.Change, "-") {
		return refuse("work %q: requires a local branch", w.Name)
	}
	if _, err := time.Parse(time.RFC3339, w.Due); err != nil {
		return refuse("work %q: due_at must be an absolute RFC3339 deadline", w.Name)
	}
	return nil
}
func localWorkHead(w desiredWork) (string, error) {
	if rc, _ := gitTry(w.Repo, gitTimeout, "check-ref-format", "--branch", w.Change); rc != 0 {
		return "", refuse("work %q: invalid branch", w.Name)
	}
	rc, head := gitTry(w.Repo, gitTimeout, "rev-parse", "--verify", "refs/heads/"+w.Change+"^{commit}")
	if rc != 0 {
		return "", refuse("work %q: local branch %q unavailable", w.Name, w.Change)
	}
	return head, nil
}
func targetWork(a workAction) (fleet.Rec, error) {
	rows, err := scopedDispatchRows(a.RepoID, a.Work.Change, a.Work.As)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
func workFields(w desiredWork) fleet.Rec {
	due, _ := time.Parse(time.RFC3339, w.Due)
	return fleet.Rec{"for": w.For, "brief": w.Brief, "due": float64(due.Unix()), "slot": nil, "reply_to": nil}
}
func workDifferences(row fleet.Rec, w desiredWork) []string {
	var changed []string
	wanted := workFields(w)
	for _, key := range []string{"for", "brief", "due", "slot", "reply_to"} {
		if planHash(row[key]) != planHash(wanted[key]) {
			changed = append(changed, key)
		}
	}
	if fleet.S(row, "request_id") != "" {
		changed = append(changed, "request_id")
	}
	return changed
}
func prepareWork(w desiredWork) (workAction, error) {
	a := workAction{Work: w, Action: "add", Expected: "absent"}
	if err := validateWork(w); err != nil {
		return a, err
	}
	a.RepoID = fleet.RepoID(w.Repo)
	if a.RepoID == "" {
		return a, refuse("work %q: repository unavailable", w.Name)
	}
	head, err := localWorkHead(w)
	if err != nil {
		return a, err
	}
	a.Head = head
	row, err := targetWork(a)
	if err != nil {
		return a, err
	}
	if row == nil {
		return a, nil
	}
	a.Expected = planHash(row)
	a.Action = "keep"
	if diff := workDifferences(row, w); len(diff) > 0 {
		a.Action = "conflict"
		a.Reason = "existing row differs: " + strings.Join(diff, ", ")
	}
	return a, nil
}
func buildWorkPlan(intent workIntent) (workPlan, error) {
	state, err := filepath.Abs(fleet.State)
	p := workPlan{Schema: planSchema, State: state, By: dispatcher("")}
	if err != nil {
		return p, err
	}
	if intent.Schema != intentSchema || len(intent.Work) == 0 || len(intent.Work) > 100 {
		return p, refuse("expected %s with 1-100 work items", intentSchema)
	}
	names, targets := map[string]bool{}, map[string]bool{}
	for _, w := range intent.Work {
		a, err := prepareWork(w)
		if err != nil {
			return p, err
		}
		key := dispatchFile(a.RepoID, w.Change, w.As)
		if names[w.Name] || targets[key] {
			return p, refuse("duplicate name or work target: %s", w.Name)
		}
		names[w.Name], targets[key] = true, true
		p.Actions = append(p.Actions, a)
	}
	p.Digest = planDigest(p)
	return p, nil
}
func replayedWork(row fleet.Rec, p workPlan, a workAction) bool {
	return fleet.S(row, "plan_digest") == p.Digest && fleet.S(row, "plan_work") == a.Work.Name && fleet.S(row, "head_at_dispatch") == a.Head && fleet.S(row, "by") == p.By && len(workDifferences(row, a.Work)) == 0
}
func applyWork(p workPlan, a workAction) (string, error) {
	status := "conflict"
	err := fleet.KeyLock("dispatch", func() error {
		row, err := targetWork(a)
		if err != nil {
			return err
		}
		if row != nil && replayedWork(row, p, a) {
			status = "already recorded"
			return nil
		}
		if a.Expected != "absent" {
			if row == nil || planHash(row) != a.Expected || len(workDifferences(row, a.Work)) != 0 {
				return refuse("work %s: existing row changed since plan; replan", a.Work.Name)
			}
			status = "kept"
			return nil
		}
		if row != nil {
			return refuse("work %s: row appeared since plan; replan", a.Work.Name)
		}
		if fleet.RepoID(a.Work.Repo) != a.RepoID {
			return refuse("work %s: repository identity changed", a.Work.Name)
		}
		head, err := localWorkHead(a.Work)
		if err != nil {
			return err
		}
		if head != a.Head {
			return refuse("work %s: branch head changed; replan", a.Work.Name)
		}
		row = workFields(a.Work)
		row["repo"], row["change"], row["relationship"] = a.RepoID, a.Work.Change, a.Work.As
		row["by"], row["at"], row["head_at_dispatch"] = p.By, fleet.Now(), a.Head
		row["plan_digest"], row["plan_work"] = p.Digest, a.Work.Name
		status = "unknown"
		if err := fleet.WriteJSON(dispatchFile(a.RepoID, a.Work.Change, a.Work.As), row); err != nil {
			return err
		}
		status = "recorded"
		return nil
	})
	return status, err
}
func validateWorkPlan(p workPlan, digest string) error {
	if fleet.ReadOnly {
		return refuse("fleet apply: read-only mode")
	}
	state, err := filepath.Abs(fleet.State)
	if err != nil {
		return err
	}
	if p.Schema != planSchema || p.State != state || p.By != dispatcher("") || digest == "" || p.Digest != digest || planDigest(p) != digest {
		return refuse("fleet apply: plan digest, caller or state mismatch")
	}
	if len(p.Actions) == 0 || len(p.Actions) > 100 {
		return refuse("fleet apply: expected 1-100 actions")
	}
	names, targets := map[string]bool{}, map[string]bool{}
	for _, a := range p.Actions {
		if err := validateWork(a.Work); err != nil {
			return err
		}
		if a.Action != "add" && a.Action != "keep" {
			return refuse("fleet apply: resolve plan conflict for %s: %s", a.Work.Name, a.Reason)
		}
		if (a.Action == "add") != (a.Expected == "absent") || a.RepoID == "" || !shaRe.MatchString(a.Head) {
			return refuse("fleet apply: invalid action %s", a.Work.Name)
		}
		key := dispatchFile(a.RepoID, a.Work.Change, a.Work.As)
		if names[a.Work.Name] || targets[key] {
			return refuse("fleet apply: duplicate work")
		}
		names[a.Work.Name], targets[key] = true, true
	}
	return nil
}
func applyWorkPlan(p workPlan, digest string) ([]workResult, error) {
	if err := validateWorkPlan(p, digest); err != nil {
		return nil, err
	}
	var results []workResult
	var failure error
	for _, a := range p.Actions {
		r := workResult{Name: a.Work.Name, Status: "not attempted"}
		if failure == nil {
			r.Status, failure = applyWork(p, a)
			if failure != nil {
				r.Detail = failure.Error()
			}
		}
		results = append(results, r)
	}
	return results, failure
}
