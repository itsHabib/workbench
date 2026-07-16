package tracelens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/itsHabib/workbench/contracts"
)

// Corpus is a versioned collection of labeled trace cases and its validation
// policy. Paths are resolved relative to the manifest file.
type Corpus struct {
	SchemaVersion int        `json:"schema_version"`
	Policy        EvalPolicy `json:"policy"`
	Cases         []EvalCase `json:"cases"`
}

// EvalPolicy is the checked-in quality gate for a corpus.
type EvalPolicy struct {
	MinMacroPrecision   float64  `json:"min_macro_precision"`
	RequiredRecallKinds []string `json:"required_recall_kinds"`
	NoCriticalOnHealthy bool     `json:"no_critical_on_healthy"`
}

// EvalCase labels one fixture. Expected kinds count as positives; tolerated
// kinds neither earn a true positive nor count as a false positive.
type EvalCase struct {
	ID               string   `json:"id"`
	Dialect          Dialect  `json:"dialect"`
	Trace            string   `json:"trace"`
	Provenance       string   `json:"provenance"`
	Expected         []string `json:"expected"`
	Tolerated        []string `json:"tolerated,omitempty"`
	ExpectedDecision string   `json:"expected_decision,omitempty"`
	Rationale        string   `json:"rationale"`
	Tags             []string `json:"tags"`
	Limitations      string   `json:"limitations,omitempty"`
}

// KindMetrics counts labeled case outcomes for one pathology. Precision and
// recall are nil when their denominator is zero.
type KindMetrics struct {
	TruePositive  int      `json:"true_positive"`
	FalsePositive int      `json:"false_positive"`
	FalseNegative int      `json:"false_negative"`
	Precision     *float64 `json:"precision,omitempty"`
	Recall        *float64 `json:"recall,omitempty"`
}

// CaseEvaluation is the expected-vs-observed result for one corpus case.
type CaseEvaluation struct {
	ID               string   `json:"id"`
	Dialect          Dialect  `json:"dialect"`
	Expected         []string `json:"expected"`
	Observed         []string `json:"observed"`
	FalsePositive    []string `json:"false_positive,omitempty"`
	FalseNegative    []string `json:"false_negative,omitempty"`
	Decision         string   `json:"decision"`
	DecisionMismatch bool     `json:"decision_mismatch,omitempty"`
	HealthyCritical  bool     `json:"healthy_critical,omitempty"`
	Pass             bool     `json:"pass"`
}

// Evaluation is the complete deterministic result for a corpus.
type Evaluation struct {
	SchemaVersion  int                    `json:"schema_version"`
	Cases          []CaseEvaluation       `json:"cases"`
	Metrics        map[string]KindMetrics `json:"metrics"`
	MacroPrecision *float64               `json:"macro_precision,omitempty"`
	Policy         EvalPolicy             `json:"policy"`
	Failures       []string               `json:"failures,omitempty"`
	Pass           bool                   `json:"pass"`
}

// EvaluateCorpus loads and evaluates a manifest or a directory containing
// corpus.json. Infrastructure errors fail immediately; label mismatches are
// returned as a non-passing Evaluation.
func EvaluateCorpus(path string, cfg Config) (Evaluation, error) {
	manifestPath := path
	info, err := os.Stat(path)
	if err != nil {
		return Evaluation{}, err
	}
	if info.IsDir() {
		manifestPath = filepath.Join(path, "corpus.json")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Evaluation{}, err
	}
	var corpus Corpus
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&corpus); err != nil {
		return Evaluation{}, fmt.Errorf("%s: %w", manifestPath, err)
	}
	if err := validateCorpus(corpus); err != nil {
		return Evaluation{}, err
	}
	return evaluateCases(filepath.Dir(manifestPath), corpus, cfg)
}

// knownPathologies is the closed set of finding kinds a corpus may reference.
var knownPathologies = map[string]bool{"loop": true, "redundancy": true, "retry_storm": true, "cost_hotspot": true, "stuck": true, "run_failure": true}

func validateCorpus(c Corpus) error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("unsupported corpus schema_version %d", c.SchemaVersion)
	}
	if err := validatePolicy(c.Policy); err != nil {
		return err
	}
	if len(c.Cases) == 0 {
		return fmt.Errorf("corpus contains no cases")
	}
	known := knownPathologies
	dialects := map[Dialect]bool{DialectNeutral: true, DialectShipCursor: true, DialectShipClaude: true, DialectShipCodex: true}
	decisions := map[string]bool{"": true, contracts.DecisionPass: true, contracts.DecisionEscalate: true, contracts.DecisionBlock: true}
	ids := map[string]bool{}
	for _, tc := range c.Cases {
		if tc.ID == "" || tc.Trace == "" || tc.Dialect == "" || tc.Provenance == "" || tc.Rationale == "" {
			return fmt.Errorf("case %q is missing required metadata", tc.ID)
		}
		if ids[tc.ID] {
			return fmt.Errorf("duplicate case id %q", tc.ID)
		}
		ids[tc.ID] = true
		if tc.Provenance != "real-sanitized" && tc.Provenance != "derived" && tc.Provenance != "synthetic" {
			return fmt.Errorf("case %q has invalid provenance %q", tc.ID, tc.Provenance)
		}
		if !dialects[tc.Dialect] {
			return fmt.Errorf("case %q has unsupported dialect %q", tc.ID, tc.Dialect)
		}
		if !decisions[tc.ExpectedDecision] {
			return fmt.Errorf("case %q has invalid expected_decision %q", tc.ID, tc.ExpectedDecision)
		}
		for _, kind := range append(append([]string{}, tc.Expected...), tc.Tolerated...) {
			if !known[kind] {
				return fmt.Errorf("case %q names unsupported pathology %q", tc.ID, kind)
			}
		}
	}
	return nil
}

// validatePolicy rejects a disarmed gate. A zero-value or out-of-range policy
// would silently turn every corpus-level check off — the gate's own config must
// fail closed, matching the detector philosophy.
func validatePolicy(p EvalPolicy) error {
	if p.MinMacroPrecision <= 0 || p.MinMacroPrecision > 1 {
		return fmt.Errorf("corpus policy min_macro_precision %v outside (0, 1] — gate would be disarmed", p.MinMacroPrecision)
	}
	for _, kind := range p.RequiredRecallKinds {
		if !knownPathologies[kind] {
			return fmt.Errorf("corpus policy names unsupported pathology %q", kind)
		}
	}
	return nil
}

func evaluateCases(base string, corpus Corpus, cfg Config) (Evaluation, error) {
	result := Evaluation{SchemaVersion: 1, Policy: corpus.Policy, Metrics: map[string]KindMetrics{}}
	for _, tc := range corpus.Cases {
		tr, err := decodeEvalCase(filepath.Join(base, filepath.FromSlash(tc.Trace)), tc.Dialect)
		if err != nil {
			return Evaluation{}, fmt.Errorf("case %s: %w", tc.ID, err)
		}
		report := Analyze(tr, cfg)
		ce := compareCase(tc, report)
		result.Cases = append(result.Cases, ce)
		accumulateMetrics(result.Metrics, ce)
	}
	finalizeMetrics(&result)
	applyEvalPolicy(&result)
	return result, nil
}

func decodeEvalCase(path string, dialect Dialect) (Trajectory, error) {
	f, err := os.Open(path)
	if err != nil {
		return Trajectory{}, err
	}
	defer f.Close()
	decoded, err := DecodeTrace(f, dialect)
	if err != nil {
		return Trajectory{}, err
	}
	return decoded.Trajectory, nil
}

func compareCase(tc EvalCase, report Report) CaseEvaluation {
	expected := stringSet(tc.Expected)
	tolerated := stringSet(tc.Tolerated)
	observed := map[string]bool{}
	critical := false
	for _, finding := range report.Findings {
		observed[finding.Kind] = true
		critical = critical || finding.Severity == Critical
	}
	ce := CaseEvaluation{
		ID: tc.ID, Dialect: tc.Dialect, Expected: sortedKeys(expected), Observed: sortedKeys(observed), Decision: report.Decision,
	}
	for kind := range expected {
		if !observed[kind] {
			ce.FalseNegative = append(ce.FalseNegative, kind)
		}
	}
	for kind := range observed {
		if !expected[kind] && !tolerated[kind] {
			ce.FalsePositive = append(ce.FalsePositive, kind)
		}
	}
	sort.Strings(ce.FalsePositive)
	sort.Strings(ce.FalseNegative)
	ce.DecisionMismatch = tc.ExpectedDecision != "" && tc.ExpectedDecision != report.Decision
	ce.HealthyCritical = hasTag(tc.Tags, "healthy") && critical
	ce.Pass = len(ce.FalsePositive) == 0 && len(ce.FalseNegative) == 0 && !ce.DecisionMismatch
	return ce
}

func accumulateMetrics(metrics map[string]KindMetrics, ce CaseEvaluation) {
	expected := stringSet(ce.Expected)
	observed := stringSet(ce.Observed)
	for kind := range expected {
		m := metrics[kind]
		if !observed[kind] {
			m.FalseNegative++
			metrics[kind] = m
			continue
		}
		m.TruePositive++
		metrics[kind] = m
	}
	for _, kind := range ce.FalsePositive {
		m := metrics[kind]
		m.FalsePositive++
		metrics[kind] = m
	}
}

func finalizeMetrics(result *Evaluation) {
	var sum float64
	defined := 0
	for kind, m := range result.Metrics {
		if denom := m.TruePositive + m.FalsePositive; denom > 0 {
			v := float64(m.TruePositive) / float64(denom)
			m.Precision = &v
			sum += v
			defined++
		}
		if denom := m.TruePositive + m.FalseNegative; denom > 0 {
			v := float64(m.TruePositive) / float64(denom)
			m.Recall = &v
		}
		result.Metrics[kind] = m
	}
	if defined > 0 {
		v := sum / float64(defined)
		result.MacroPrecision = &v
	}
}

// macroPrecisionFailure reports why the macro-precision gate failed, or ""
// when it holds. An undefined metric is a failure — the gate never passes on
// missing evidence.
func macroPrecisionFailure(result *Evaluation) string {
	if result.MacroPrecision == nil {
		return "macro precision unavailable"
	}
	if *result.MacroPrecision < result.Policy.MinMacroPrecision {
		return fmt.Sprintf("macro precision %.3f below %.3f", *result.MacroPrecision, result.Policy.MinMacroPrecision)
	}
	return ""
}

func applyEvalPolicy(result *Evaluation) {
	for _, ce := range result.Cases {
		if !ce.Pass {
			result.Failures = append(result.Failures, fmt.Sprintf("case %s: fp=%v fn=%v decision_mismatch=%t", ce.ID, ce.FalsePositive, ce.FalseNegative, ce.DecisionMismatch))
		}
		if result.Policy.NoCriticalOnHealthy && ce.HealthyCritical {
			result.Failures = append(result.Failures, fmt.Sprintf("case %s: critical finding on healthy case", ce.ID))
		}
	}
	if f := macroPrecisionFailure(result); f != "" {
		result.Failures = append(result.Failures, f)
	}
	for _, kind := range result.Policy.RequiredRecallKinds {
		m, ok := result.Metrics[kind]
		if !ok || m.Recall == nil {
			result.Failures = append(result.Failures, fmt.Sprintf("required recall for %s unavailable", kind))
			continue
		}
		if *m.Recall < 1 {
			result.Failures = append(result.Failures, fmt.Sprintf("required recall for %s is %.3f, want 1.000", kind, *m.Recall))
		}
	}
	result.Pass = len(result.Failures) == 0
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
