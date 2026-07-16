package tracelens

import (
	"fmt"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/contracts"
)

// Severity ranks how much a finding should worry an operator.
type Severity int

// The severity ladder, least to most alarming.
const (
	Info Severity = iota
	Warn
	Critical
)

func (s Severity) String() string {
	switch s {
	case Critical:
		return "CRITICAL"
	case Warn:
		return "WARN"
	default:
		return "INFO"
	}
}

// MarshalJSON emits the severity as its label so `tracelens -json` is readable.
func (s Severity) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// Finding is one diagnosed issue: what it is, how bad, which steps prove it,
// how much money it wasted, and a concrete, evidence-filled repair. This is
// tracelens's own rich finding — the detector output. The shared
// contracts.Finding on the emitted contracts.Verdict is the aligned wire
// surface.
type Finding struct {
	Kind      string
	Severity  Severity
	Summary   string
	Steps     []int
	WastedUSD float64
	Repair    string
}

// Report is the internal analyzer result over a whole run: totals, the two
// orthogonal verdict axes, a one-line headline, and the ranked rich findings.
// It backs the human render; the emitted, gate-aligned surface is
// contracts.Verdict (see the Verdict method).
type Report struct {
	Steps     int
	TotalUSD  float64
	WastedUSD float64
	Decision  string
	Tier      string
	Headline  string
	Findings  []Finding
}

// Tiers, best to worst: T0 < T1 < T2 < T3. An unknown tier ranks highest so a
// garbage value fails closed, matching gate's tier.Rank default.
const (
	TierT0 = "T0"
	TierT1 = "T1"
	TierT2 = "T2"
	TierT3 = "T3"
)

// impl is the provenance stamp on every verdict tracelens emits — the analyzer
// identity. It stays dialect-neutral because the same analyzer runs on every
// normalized trace path.
const impl = "tracelens"

// tierRank orders tiers so the max wins in composition; an unknown or empty
// tier ranks highest (fail closed), matching gate's tier.Rank default.
func tierRank(tier string) int {
	switch tier {
	case TierT0:
		return 0
	case TierT1:
		return 1
	case TierT2:
		return 2
	case TierT3:
		return 3
	default:
		return 4
	}
}

// Config holds the detector thresholds. Zero values fall back to sane defaults
// inside each detector, so DefaultConfig is a convenience, not a requirement.
type Config struct {
	MinLoopRepeats int
	RetryThreshold int
	StuckWindow    int
	HotspotFrac    float64
	// KeepVolatileArgs disables volatile-argument normalization in the loop
	// detector. The zero value keeps normalization on — the documented
	// contract is that a zero Config behaves like DefaultConfig.
	KeepVolatileArgs bool
}

// DefaultConfig is the tuned baseline used by the CLI and demo.
func DefaultConfig() Config {
	return Config{MinLoopRepeats: 3, RetryThreshold: 3, StuckWindow: 4, HotspotFrac: 0.4}
}

// Analyze runs the detector pipeline over a trajectory and folds the findings
// into a verdict. This composition — mechanism (detectors) plus policy
// (buildReport) — is the observability agent: it reasons over an agent's own
// trace and diagnoses what went wrong.
func Analyze(t Trajectory, cfg Config) Report {
	detectors := []Detector{
		RunFailureDetector{},
		LoopDetector{MinRepeats: cfg.MinLoopRepeats, KeepVolatileArgs: cfg.KeepVolatileArgs},
		RedundancyDetector{},
		RetryStormDetector{Threshold: cfg.RetryThreshold},
		CostHotspotDetector{Frac: cfg.HotspotFrac},
		StuckDetector{Window: cfg.StuckWindow},
	}
	var findings []Finding
	for _, d := range detectors {
		findings = append(findings, d.Detect(t)...)
	}
	return buildReport(t, findings)
}

// buildReport is the policy layer: it ranks findings and decides the two
// orthogonal verdict axes. Decision and Tier are stored independently — they
// correlate here because one analyzer sets both, but the contract is that the
// axes are orthogonal (as gate's are), so nothing re-derives one from the other.
func buildReport(t Trajectory, findings []Finding) Report {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity > findings[j].Severity
		}
		return findings[i].WastedUSD > findings[j].WastedUSD
	})
	wasted := 0.0
	for _, f := range findings {
		wasted += f.WastedUSD
	}
	decision, tier := decisionTier(findings)
	r := Report{
		Steps:     len(t.Steps),
		TotalUSD:  t.TotalCost(),
		WastedUSD: wasted,
		Decision:  decision,
		Tier:      tier,
		Findings:  findings,
	}
	r.Headline = headline(r)
	return r
}

// decisionTier is the single source of truth for the two axes. A Critical
// finding blocks at T3; a Warn (no Critical) escalates at T2; Info-only passes
// at T1; a clean run passes at T0.
func decisionTier(findings []Finding) (decision, tier string) {
	worst := severityFloor(findings)
	switch worst {
	case Critical:
		return contracts.DecisionBlock, TierT3
	case Warn:
		return contracts.DecisionEscalate, TierT2
	case Info:
		return contracts.DecisionPass, TierT1
	default:
		return contracts.DecisionPass, TierT0
	}
}

// severityFloor reports the highest severity present, or -1 when there are no
// findings, so callers can distinguish "Info only" from "empty".
func severityFloor(findings []Finding) Severity {
	worst := Severity(-1)
	for _, f := range findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

// buildVerdict composes the detector findings into the emitted, gate-aligned
// contracts.Verdict. It is the policy entrypoint the CLI and gate consume.
func buildVerdict(t Trajectory, findings []Finding) contracts.Verdict {
	return buildReport(t, findings).Verdict()
}

// Verdict maps the rich internal report onto the shared wire type. It copies
// the two axes computed by the policy layer (never re-deriving one from the
// other), stamps the deterministic code producer, and folds the reasoning
// into Why so a downstream reader can act on Why alone.
func (r Report) Verdict() contracts.Verdict {
	return contracts.Verdict{
		Source:     "tracelens",
		Producer:   contracts.Producer{Class: contracts.ClassCode, Impl: impl},
		Decision:   r.Decision,
		Tier:       r.Tier,
		Confidence: 1,
		Findings:   verdictFindings(r.Findings),
		Why:        why(r),
	}
}

// verdictFindings maps tracelens's rich findings onto the shared slice:
// summary→Title, severity label→Severity, evidence steps→Locus. Evidence is
// deliberately left empty — tracelens's evidence lives in Locus step refs, and
// omitempty keeps the emitted JSON unchanged.
func verdictFindings(findings []Finding) []contracts.Finding {
	if len(findings) == 0 {
		return nil
	}
	out := make([]contracts.Finding, 0, len(findings))
	for _, f := range findings {
		out = append(out, contracts.Finding{
			Title:      f.Summary,
			Severity:   f.Severity.String(),
			Locus:      locus(f.Steps),
			Confidence: 1,
		})
	}
	return out
}

// locus renders a finding's evidence steps as a human-readable locator, e.g.
// "steps 3,4,5"; empty when the finding cites no steps.
func locus(steps []int) string {
	if len(steps) == 0 {
		return ""
	}
	return "steps " + joinInts(steps)
}

// why is the aggregated reasoning for the verdict: the headline plus every
// contributing finding's summary. On an escalate this is the full question a
// downstream reader must be able to act on without any other field.
func why(r Report) string {
	if len(r.Findings) == 0 {
		return r.Headline
	}
	lines := make([]string, 0, len(r.Findings)+1)
	lines = append(lines, r.Headline)
	for _, f := range r.Findings {
		lines = append(lines, "- "+f.Summary)
	}
	return strings.Join(lines, "\n")
}

func headline(r Report) string {
	if len(r.Findings) == 0 {
		return fmt.Sprintf("clean run: no pathologies across %d steps ($%.4f)", r.Steps, r.TotalUSD)
	}
	return r.Findings[0].Summary
}
