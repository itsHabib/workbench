package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
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
	Floor   string `json:"floor"`
	Signals []struct {
		Signal string `json:"signal"`
		Tier   string `json:"tier"`
		Why    string `json:"why"`
	} `json:"signals"`
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
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

	cmd := exec.Command(floorBin)
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
		Why:        fmt.Sprintf("deterministic floor over %d files (+%d/-%d)", res.Files, res.Added, res.Removed),
	}
	for _, s := range res.Signals {
		v.Findings = append(v.Findings, Finding{Title: s.Signal + ": " + s.Why, Severity: s.Tier})
	}
	return Record(st, run, []string{diffEvidenceID}, v)
}
