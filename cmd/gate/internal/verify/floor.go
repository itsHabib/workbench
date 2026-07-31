package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
)

// parseFloorOutput decodes the floor binary's JSON and refuses an absent or
// unknown tier. tier.Rank maps unknown values to the highest rank and the
// capability check only bounds the grant ceiling — so recording an invalid
// floor as a passing verdict would read as "assessed" when nothing was.
// No valid tier, no verdict: an operational error, fail closed.
func parseFloorOutput(out []byte) (floorResult, error) {
	var res floorResult
	if err := json.Unmarshal(out, &res); err != nil {
		return floorResult{}, fmt.Errorf("verify: parse floor output: %w", err)
	}
	if !tier.Valid(res.Floor) {
		return floorResult{}, fmt.Errorf("verify: triage-floor returned invalid tier %q", res.Floor)
	}
	return res, nil
}

// floorResult mirrors the triage floor binary's JSON output.
type floorResult struct {
	Floor   string        `json:"floor"`
	Signals []floorSignal `json:"signals"`
	Files   int           `json:"files"`
	Added   int           `json:"added"`
	Removed int           `json:"removed"`
}

type floorSignal struct {
	Signal string `json:"signal"`
	Tier   string `json:"tier"`
	Why    string `json:"why"`
}

const maxFloorDriversInSummary = 3

func floorWhy(res floorResult) string {
	var drivers []string
	seen := make(map[string]struct{})
	for _, signal := range res.Signals {
		if signal.Tier != res.Floor {
			continue
		}
		driver := signal.Signal + ": " + signal.Why
		if _, ok := seen[driver]; ok {
			continue
		}
		seen[driver] = struct{}{}
		drivers = append(drivers, driver)
	}
	if len(drivers) == 0 {
		return fmt.Sprintf("deterministic %s floor over %d files (+%d/-%d)", res.Floor, res.Files, res.Added, res.Removed)
	}

	remaining := len(drivers) - maxFloorDriversInSummary
	if remaining > 0 {
		drivers = drivers[:maxFloorDriversInSummary]
		drivers = append(drivers, fmt.Sprintf("+%d more max-tier drivers", remaining))
	}
	return fmt.Sprintf(
		"deterministic %s floor: %s (%d files, +%d/-%d)",
		res.Floor,
		strings.Join(drivers, "; "),
		res.Files,
		res.Added,
		res.Removed,
	)
}

func orderFloorSignals(signals []floorSignal) []floorSignal {
	ordered := append([]floorSignal(nil), signals...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return tier.Rank(ordered[i].Tier) > tier.Rank(ordered[j].Tier)
	})
	return ordered
}

// Floor runs the deterministic risk floor over recorded diff evidence.
// Producer class: code. It never blocks — it assigns the risk tier the
// capability ceiling is checked against.
func Floor(st *state.Store, run, diffEvidenceID, floorBin string, subject Subject) (state.Artifact, error) {
	a, err := st.Get(diffEvidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	var body struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: parse diff evidence: %w", err)
	}

	// Pass the PR's repo so triage-floor applies that repo's compiled-in path
	// overrides — the deterministic compensating control for the gate-machinery
	// blind spot. An empty repo is inert (the floor applies no overrides).
	cmd := exec.Command(floorBin, "-repo", subject.Repo)
	cmd.Stdin = strings.NewReader(body.Diff)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: triage-floor: %w", err)
	}
	res, err := parseFloorOutput(out.Bytes())
	if err != nil {
		return state.Artifact{}, err
	}

	v := Verdict{
		Subject:    subject,
		Source:     "triage-floor",
		Producer:   Producer{Class: ClassCode, Impl: "triage-floor"},
		Decision:   DecisionPass,
		Tier:       res.Floor,
		Confidence: 1.0,
		Why:        floorWhy(res),
	}
	for _, s := range orderFloorSignals(res.Signals) {
		v.Findings = append(v.Findings, Finding{Title: s.Signal + ": " + s.Why, Severity: s.Tier})
	}
	return Record(st, run, []string{diffEvidenceID}, v)
}
