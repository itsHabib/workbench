package tracelens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluateCorpus_CommittedGatePasses(t *testing.T) {
	e, err := EvaluateCorpus(filepath.Join("..", "..", "testdata", "corpus"), DefaultConfig())
	if err != nil {
		t.Fatalf("EvaluateCorpus: %v", err)
	}
	if !e.Pass {
		t.Fatalf("committed corpus gate failed: %v", e.Failures)
	}
	if e.MacroPrecision == nil || *e.MacroPrecision < 0.9 {
		t.Fatalf("macro precision = %v", e.MacroPrecision)
	}
	for _, kind := range []string{"loop", "redundancy", "retry_storm", "cost_hotspot", "stuck"} {
		m, ok := e.Metrics[kind]
		if !ok || m.TruePositive == 0 {
			t.Fatalf("%s lacks a true positive: %+v", kind, m)
		}
	}
}

func TestCommittedCorpus_CoversDialectsAndHardNegatives(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", "corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus Corpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	dialects := map[Dialect]bool{}
	hardNegatives := map[string]bool{}
	realCases := 0
	for _, tc := range corpus.Cases {
		dialects[tc.Dialect] = true
		if tc.Provenance == "real-sanitized" {
			realCases++
		}
		for _, tag := range tc.Tags {
			const prefix = "hard-negative-"
			if len(tag) > len(prefix) && tag[:len(prefix)] == prefix {
				hardNegatives[tag[len(prefix):]] = true
			}
		}
	}
	for _, dialect := range []Dialect{DialectNeutral, DialectShipCursor, DialectShipClaude, DialectShipCodex} {
		if !dialects[dialect] {
			t.Errorf("corpus lacks %s", dialect)
		}
	}
	for _, kind := range []string{"loop", "redundancy", "retry-storm", "cost-hotspot", "stuck"} {
		if !hardNegatives[kind] {
			t.Errorf("corpus lacks hard negative for %s", kind)
		}
	}
	if realCases == 0 {
		t.Error("corpus lacks a real-sanitized case")
	}
}

func TestEvaluateCorpus_MissingFixtureIsInfrastructureError(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"schema_version":1,"policy":{"min_macro_precision":0.9},"cases":[{"id":"missing","dialect":"neutral-jsonl","trace":"missing.jsonl","provenance":"synthetic","expected":[],"rationale":"missing on purpose","tags":["healthy"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "corpus.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateCorpus(dir, DefaultConfig()); err == nil {
		t.Fatal("missing fixture must be an infrastructure error")
	}
}

func TestEvaluateCorpus_UnknownManifestFieldRejected(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"schema_version":1,"polcy":{"min_macro_precision":0.9},"cases":[{"id":"x","dialect":"neutral-jsonl","trace":"x.jsonl","provenance":"synthetic","expected":[],"rationale":"typo probe","tags":["healthy"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "corpus.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateCorpus(dir, DefaultConfig()); err == nil {
		t.Fatal("a misspelled manifest key must not silently disarm the gate")
	}
}

func TestEvaluateCorpus_DisarmedPolicyRejected(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"schema_version":1,"policy":{},"cases":[{"id":"x","dialect":"neutral-jsonl","trace":"x.jsonl","provenance":"synthetic","expected":[],"rationale":"zero policy probe","tags":["healthy"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "corpus.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := EvaluateCorpus(dir, DefaultConfig())
	if err == nil || !contains(err.Error(), "min_macro_precision") {
		t.Fatalf("zero-value policy must be rejected: %v", err)
	}
}

func TestRenderEvaluation_UndefinedMetricsAreNA(t *testing.T) {
	e := Evaluation{Pass: true, Metrics: map[string]KindMetrics{"loop": {}}}
	got := RenderEvaluation(e)
	if got == "" || !contains(got, "n/a") {
		t.Fatalf("RenderEvaluation = %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
