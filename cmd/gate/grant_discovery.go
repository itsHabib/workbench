package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/readiness"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// Discovery reports eligibility to enter assessment. The deterministic floor
// is a minimum, not the final reduced tier or permission to merge. No signing
// material is exposed, and no state or grant is created by this path.
type grantDiscovery struct {
	Subject     verify.Subject   `json:"subject"`
	Action      string           `json:"action"`
	StateDir    string           `json:"state_dir"`
	KeyDir      string           `json:"key_dir"`
	FloorBin    string           `json:"floor_bin"`
	Status      string           `json:"status"`
	MinimumTier string           `json:"minimum_tier,omitempty"`
	FloorWhy    string           `json:"floor_why,omitempty"`
	NextCycle   int              `json:"next_cycle,omitempty"`
	GrantID     string           `json:"grant_id,omitempty"`
	Why         string           `json:"why"`
	Candidates  []grantCandidate `json:"candidates,omitempty"`
	MintRequest string           `json:"mint_request,omitempty"`
}

type grantCandidate struct {
	ID        string    `json:"id"`
	MaxTier   string    `json:"max_tier,omitempty"`
	MaxCycles int       `json:"max_cycles"`
	ExpiresAt time.Time `json:"expires_at"`
	Gaps      []string  `json:"gaps,omitempty"`
}

func requireDiscoveryState(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("grant_assessment_required: state %s: %w", absStateDir(dir), err)
	}
	if !info.IsDir() {
		return fmt.Errorf("grant_assessment_required: state %s is not a directory", absStateDir(dir))
	}
	return nil
}

func cmdDiscoverGrant(args []string) error {
	fs := flag.NewFlagSet("discover-grant", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	repo := fs.String("repo", "", "owner/repo")
	pr := fs.Int("pr", 0, "PR number")
	asJSON := fs.Bool("json", false, "structured discovery result")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *repo == "" || *pr < 1 {
		return errors.New("discover-grant: -repo and positive -pr required")
	}
	if err := requireDiscoveryState(*stateDir); err != nil {
		return err
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	d := discoverGrant(e, *repo, *pr)
	if *asJSON {
		printJSON(d)
		os.Exit(d.exitCode())
		return nil
	}
	fmt.Printf("%s: %s\n", d.Status, d.Why)
	for _, candidate := range d.Candidates {
		fmt.Printf("  %s: tier %s, cycle ceiling %d, gaps %v\n", candidate.ID, candidate.MaxTier, candidate.MaxCycles, candidate.Gaps)
	}
	if d.MintRequest != "" {
		fmt.Printf("Operator mint request: %s\n", d.MintRequest)
	}
	os.Exit(d.exitCode())
	return nil
}

func (d grantDiscovery) exitCode() int {
	if d.Status == "available" {
		return codeMerge // Discovery succeeded; this is never a merge verdict.
	}
	if d.Status == "uncovered" || d.Status == "not_applicable" {
		return codeRefused
	}
	return codeError
}

func (d grantDiscovery) failed(err error) grantDiscovery {
	d.Status = "assessment_required"
	d.Why = err.Error()
	d.GrantID = ""
	d.MintRequest = ""
	return d
}

func discoverGrant(e env, repo string, pr int) grantDiscovery {
	d := grantDiscovery{Subject: verify.Subject{Repo: repo, Number: pr}, Action: "merge", StateDir: absStateDir(e.stateDir), KeyDir: filepath.Dir(e.keyPath), FloorBin: e.floorBin}
	var err error
	d.KeyDir, err = filepath.Abs(d.KeyDir)
	if err != nil {
		return d.failed(err)
	}
	if strings.ContainsAny(d.FloorBin, `/\`) {
		d.FloorBin, err = filepath.Abs(d.FloorBin)
		if err != nil {
			return d.failed(err)
		}
	}
	ref := evidence.PRRef{Repo: repo, Number: pr}
	head, status, err := evidence.CurrentSubject(ref)
	if err != nil {
		return d.failed(err)
	}
	d.Subject.HeadSHA = head
	if status != "open" {
		d.Status, d.Why = "not_applicable", "PR is closed; no new merge authority is needed"
		return d
	}
	diff, err := evidence.SubjectDiff(ref, head)
	if err != nil {
		return d.failed(err)
	}
	floor, err := verify.AssessFloor(e.floorBin, d.Subject, diff)
	if err != nil {
		return d.failed(err)
	}
	d.MinimumTier, d.FloorWhy = floor.Tier, floor.Why
	current, status, err := evidence.CurrentSubject(ref)
	if err != nil {
		return d.failed(err)
	}
	if current != head {
		return d.failed(fmt.Errorf("PR head moved during grant discovery: %s to %s; reassess the current head", head, current))
	}
	if status != "open" {
		d.Status, d.Why = "not_applicable", "PR closed during discovery; no new merge authority is needed"
		return d
	}
	return selectGrant(e, d)
}

// selectGrant reads one fresh audited snapshot for both inventory and cycle
// accounting. Selection considers every matching candidate, not just the newest
// or the widest before checking its other limits. The normal Gate path checks
// the selected capability again before gathering and before recording an action.
func selectGrant(e env, d grantDiscovery) grantDiscovery {
	if !tier.Valid(d.MinimumTier) || d.Subject.HeadSHA == "" || d.Subject.Number < 1 {
		return d.failed(errors.New("current subject and deterministic floor must be assessed before selecting a grant"))
	}
	if err := capability.CheckKey(e.keyPath); err != nil {
		return d.failed(err)
	}
	audit, err := e.st.Audit()
	if err != nil {
		return d.failed(err)
	}
	if !audit.OK {
		return d.failed(fmt.Errorf("%w: %s", errLogTampered, audit.Reason))
	}
	used, err := countCycles(audit.All, d.Subject, "")
	if err != nil {
		return d.failed(err)
	}
	d.NextCycle = used + 1
	var available []grantCandidate
	var unreadable error
	for _, a := range audit.All {
		candidate, err := assessGrant(e, a, d)
		if err != nil {
			unreadable = err
		}
		if candidate.ID == "" {
			continue
		}
		d.Candidates = append(d.Candidates, candidate)
		if len(candidate.Gaps) == 0 {
			available = append(available, candidate)
		}
	}
	if len(available) == 0 && unreadable != nil {
		return d.failed(unreadable)
	}
	if len(available) == 0 {
		d.Status = "uncovered"
		d.Why = fmt.Sprintf("no authenticated existing grant covers %s#%d at %s for merge, minimum %s and cycle %d; candidate gaps are listed; full Gate assessment may raise the tier", d.Subject.Repo, d.Subject.Number, d.Subject.HeadSHA, d.MinimumTier, d.NextCycle)
		d.MintRequest = shellJoin([]string{"gate", "grant", "-repo", d.Subject.Repo, "-action", d.Action, "-max-tier", d.MinimumTier, "-max-cycles", fmt.Sprint(d.NextCycle), "-ttl", "24h", "-state", d.StateDir, "-key", d.KeyDir})
		return d
	}
	sort.Slice(available, func(i, j int) bool { return broaderGrant(available[i], available[j]) })
	d.Status, d.GrantID = "available", available[0].ID
	d.Why = fmt.Sprintf("reuse %s: authenticated scope and expiry, minimum %s and cycle %d fit; widest eligible ceiling %s leaves room for the full Gate assessment, which still decides readiness and authority", d.GrantID, d.MinimumTier, d.NextCycle, available[0].MaxTier)
	return d
}

func assessGrant(e env, a state.Artifact, d grantDiscovery) (grantCandidate, error) {
	if a.Kind != state.KindGrant {
		return grantCandidate{}, nil
	}
	var metadata capability.Grant
	if err := json.Unmarshal(a.Body, &metadata); err != nil {
		return grantCandidate{ID: a.ID, Gaps: []string{err.Error()}}, fmt.Errorf("read grant %s: %w", a.ID, err)
	}
	if metadata.Repo != d.Subject.Repo {
		return grantCandidate{}, nil
	}
	candidate := grantCandidate{ID: a.ID, MaxTier: metadata.MaxTier, MaxCycles: metadata.MaxCycles, ExpiresAt: metadata.ExpiresAt}
	g, err := capability.CheckArtifactSubject(a, e.keyPath, d.Subject.Repo, d.Action, d.Subject.HeadSHA, d.Subject.Number, e.now)
	if err != nil {
		candidate.Gaps = []string{err.Error()}
		if errors.Is(err, capability.ErrExpired) || errors.Is(err, capability.ErrScope) || errors.Is(err, capability.ErrHeadMismatch) || errors.Is(err, capability.ErrSubject) {
			return candidate, nil
		}
		return candidate, fmt.Errorf("cannot authenticate grant %s: %w", a.ID, err)
	}
	if !tier.Valid(g.MaxTier) || g.MaxCycles < 0 {
		candidate.Gaps = []string{"invalid grant ceiling"}
		return candidate, fmt.Errorf("grant %s has invalid ceilings", a.ID)
	}
	if !g.TierWithin(d.MinimumTier) {
		candidate.Gaps = append(candidate.Gaps, fmt.Sprintf("grant_tier_exceeded: minimum %s exceeds ceiling %s", d.MinimumTier, g.MaxTier))
	}
	if !g.CyclesWithin(d.NextCycle) {
		candidate.Gaps = append(candidate.Gaps, fmt.Sprintf("grant_cycle_exceeded: next cycle %d exceeds ceiling %d", d.NextCycle, g.MaxCycles))
	}
	return candidate, nil
}

func broaderGrant(a, b grantCandidate) bool {
	if a.MaxTier != b.MaxTier {
		return tier.Rank(a.MaxTier) > tier.Rank(b.MaxTier)
	}
	if a.MaxCycles != b.MaxCycles {
		return a.MaxCycles == 0 || b.MaxCycles != 0 && a.MaxCycles > b.MaxCycles
	}
	if !a.ExpiresAt.Equal(b.ExpiresAt) {
		return a.ExpiresAt.After(b.ExpiresAt)
	}
	return a.ID < b.ID
}

func runGateSelected(e env, repo string, pr int, grantID string, live bool, modelBackend string, reviewsOptional bool) (gateResult, int, error) {
	if grantID != "" {
		return runGate(e, repo, pr, grantID, live, modelBackend, reviewsOptional)
	}
	d := discoverGrant(e, repo, pr)
	if d.Status == "assessment_required" {
		return gateResult{Discovery: &d}, codeError, fmt.Errorf("grant_assessment_required: %s", d.Why)
	}
	if d.Status != "available" {
		return gateResult{PR: fmt.Sprintf("%s#%d", repo, pr), HeadSHA: d.Subject.HeadSHA,
			Outcome: "capability_refused", Code: "grant_" + d.Status, Why: d.Why, Discovery: &d}, d.exitCode(), nil
	}
	res, code, err := runGateBound(e, repo, pr, d.Subject.HeadSHA, d.GrantID, live, modelBackend, reviewsOptional)
	res.Discovery = &d
	return res, code, err
}

func readGateView(e env, run string, ref evidence.PRRef, head string) (string, json.RawMessage, error) {
	id, view, err := evidence.View(e.st, run, ref)
	if err != nil {
		return "", nil, err
	}
	return id, view, matchBoundView(head, view)
}

// A discovery or Slack request is about a captured head. Refuse a later view
// before model construction, even when the selected grant is repo-wide.
func matchBoundView(head string, view json.RawMessage) error {
	if head == "" {
		return nil
	}
	var fields struct {
		HeadSHA string `json:"headRefOid"`
	}
	if err := json.Unmarshal(view, &fields); err != nil {
		return err
	}
	if fields.HeadSHA != head {
		return fmt.Errorf("grant_assessment_required: PR head changed after selection: %s to %s", head, fields.HeadSHA)
	}
	return nil
}

func discoveryRoute(d grantDiscovery) *readiness.Route {
	if d.Status == "not_applicable" {
		return &readiness.Route{Why: d.Why, Next: shellJoin([]string{"gh", "pr", "view", fmt.Sprint(d.Subject.Number), "--repo", d.Subject.Repo})}
	}
	if d.Status == "uncovered" {
		return &readiness.Route{Why: d.Why + "; only the operator can mint", Next: d.MintRequest}
	}
	return &readiness.Route{Why: "repair the failed assessment before asking for authority: " + d.Why,
		Next: shellJoin([]string{"gate", "discover-grant", "-repo", d.Subject.Repo, "-pr", fmt.Sprint(d.Subject.Number), "-json", "-state", d.StateDir, "-key", d.KeyDir, "-floor", d.FloorBin})}
}
