package verify

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// The pinned composition test for escalation visibility under a block: block
// + escalate composes to blocked with BOTH reasons in Why, and the composed
// Findings stay nil — Reduce never lifts a rung's Findings into the composed
// verdict; they live on the rung's own recorded artifact.
func TestReduceBlockCarriesEscalationWhys(t *testing.T) {
	got, err := Reduce(subj, []Verdict{
		{Source: "readiness", Producer: code, Decision: DecisionBlock, Tier: "T0", Confidence: 1, Why: "check not green: CI (FAILURE)"},
		{Source: sourceCIClassify, Producer: code, Decision: DecisionEscalate, Tier: "T0", Confidence: 1, Why: "infra: failed to authenticate",
			Findings: []Finding{{Title: "infra: failed to authenticate — environment owner must act"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != DecisionBlock {
		t.Fatalf("block must dominate an escalation: %s", got.Decision)
	}
	if !strings.Contains(got.Why, "check not green") {
		t.Fatalf("blocked Why lost the block reason: %q", got.Why)
	}
	if !strings.Contains(got.Why, "infra: failed to authenticate") {
		t.Fatalf("blocked Why buried the escalation reason: %q", got.Why)
	}
	if got.Findings != nil {
		t.Fatalf("composed verdict must not lift rung findings: %v", got.Findings)
	}
}

func TestCIFloorSignatures(t *testing.T) {
	cases := []struct {
		line   string
		bucket string
	}{
		{"FATAL: the database system is starting up", bucketFlake},
		{"Error: EADDRINUSE: address already in use :::4000", bucketFlake},
		{"error EBUSY: resource busy or locked, rmdir 'C:\\tmp'", bucketFlake},
		{"##[error]failed to authenticate to the registry", bucketInfra},
		{"curl: (6) Could not resolve host: proxy.golang.org", bucketInfra},
		{"write /tmp/build: no space left on device", bucketInfra},
		{"POST https://api.github.com/app/installations/1/installation/token: 401", bucketInfra},
	}
	for _, c := range cases {
		m := ciFloor(c.line)
		if m == nil {
			t.Fatalf("floor abstained on %q", c.line)
		}
		if m.bucket != c.bucket {
			t.Fatalf("floor bucket for %q: got %s want %s", c.line, m.bucket, c.bucket)
		}
		if m.line != strings.TrimSpace(c.line) {
			t.Fatalf("floor evidence line mismatch: %q", m.line)
		}
	}
}

func TestCIFloorAbstainsOnRealBreakAndDemoted(t *testing.T) {
	for _, line := range []string{
		"FAIL: TestFoo assertion mismatch",
		"error TS2345: Argument of type 'string' is not assignable",
		// Demoted signatures: routine inside flaky integration tests, so the
		// floor must leave them to the advisory.
		"Error: connect ECONNREFUSED 127.0.0.1:5432",
		"Error: connect ETIMEDOUT 10.0.0.1:443",
	} {
		if m := ciFloor(line); m != nil {
			t.Fatalf("floor must abstain on %q, fired %s", line, m.bucket)
		}
	}
}

func TestCIFloorWrapperExclusion(t *testing.T) {
	// A signature word inside a wrapper/relay line must not fire: the wrapper
	// sits above the real cause and would shadow it.
	for _, line := range []string{
		"npm error code ELIFECYCLE while address already in use",
		"Process completed with exit code 1: failed to authenticate",
		"make: *** [test] Error 1 could not resolve host",
		"exit status 1: no space left on device",
	} {
		if m := ciFloor(line); m != nil {
			t.Fatalf("excluded line fired the floor: %q -> %s", line, m.bucket)
		}
	}
}

func TestCIFloorTeardownRegion(t *testing.T) {
	chunk := strings.Join([]string{
		"run tests",
		"Post job cleanup.",
		"docker stop: failed to authenticate", // teardown noise, not the cause
	}, "\n")
	if m := ciFloor(chunk); m != nil {
		t.Fatalf("teardown region fired the floor: %s", m.bucket)
	}
	// The same signature before the sentinel fires.
	chunk = strings.Join([]string{
		"##[error]failed to authenticate",
		"Post job cleanup.",
	}, "\n")
	m := ciFloor(chunk)
	if m == nil || m.bucket != bucketInfra {
		t.Fatalf("live-region signature must fire: %v", m)
	}
}

// ciStore opens a throwaway store and records a ci-logs evidence artifact.
func ciStore(t *testing.T, body any) (*state.Store, string) {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Append(state.KindEvidence, "run_test", nil, body)
	if err != nil {
		t.Fatal(err)
	}
	return st, a.ID
}

func ciEvidence(runs ...map[string]any) map[string]any {
	return map[string]any{"pr": map[string]any{"repo": "o/r", "number": 1}, "runs": runs}
}

func ciRun(id int, workflow string, chunks ...map[string]any) map[string]any {
	return map[string]any{"id": id, "workflow": workflow, "conclusion": "failure", "chunks": chunks}
}

func chunk(step, text string) map[string]any {
	return map[string]any{"step": step, "text": text}
}

func TestCIClassifyFloorInfraEscalates(t *testing.T) {
	st, evID := ciStore(t, ciEvidence(ciRun(1, "ci", chunk("build / auth", "##[error]failed to authenticate\nsetup exited"))))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("infra must escalate: %s", v.Decision)
	}
	if v.Producer.Class != ClassCode || v.Producer.Impl != ciFloorImpl {
		t.Fatalf("floor-only run must carry the code producer: %+v", v.Producer)
	}
	if v.Tier != "T0" {
		t.Fatalf("classification must not assert risk tier: %s", v.Tier)
	}
	f := v.Findings[0]
	if !strings.HasPrefix(f.Title, "infra: ") || !strings.HasSuffix(f.Title, "environment owner must act") {
		t.Fatalf("infra finding title: %q", f.Title)
	}
	if f.Evidence != "##[error]failed to authenticate" {
		t.Fatalf("finding must carry the verbatim matched line: %q", f.Evidence)
	}
	if f.Locus != "build / auth" {
		t.Fatalf("finding locus must name the step: %q", f.Locus)
	}
	if f.Severity != "" {
		t.Fatalf("bucket is not a severity: %q", f.Severity)
	}
}

func TestCIClassifyFlakePasses(t *testing.T) {
	st, evID := ciStore(t, ciEvidence(ciRun(1, "ci", chunk("test / db", "FATAL: the database system is starting up"))))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionPass {
		t.Fatalf("flake needs no judgment beyond the block: %s", v.Decision)
	}
	if !strings.HasPrefix(v.Findings[0].Title, "flake: ") {
		t.Fatalf("flake finding title: %q", v.Findings[0].Title)
	}
	if v.Confidence != 1 {
		t.Fatalf("floor hits carry confidence 1: %v", v.Confidence)
	}
}

func TestCIClassifyMixedRunOneFindingPerBucket(t *testing.T) {
	st, evID := ciStore(t, ciEvidence(ciRun(1, "ci",
		chunk("test / db", "FATAL: the database system is starting up"),
		chunk("build / auth", "##[error]failed to authenticate"),
	)))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("worst-wins across chunks: %s", v.Decision)
	}
	if len(v.Findings) != 2 {
		t.Fatalf("a mixed run must not flatten to one cause: %d findings", len(v.Findings))
	}
	if v.Findings[0].Locus == v.Findings[1].Locus {
		t.Fatalf("differing chunks must annotate their own steps")
	}
}

func TestCIClassifyAbsenceEscalates(t *testing.T) {
	// A red run with no failed-step log: never default to flake — a flake
	// misfire auto-retries and masks a real break.
	st, evID := ciStore(t, ciEvidence(ciRun(7, "ci")))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("absent log must escalate: %s", v.Decision)
	}
	if v.Findings[0].Title != "unclassifiable: no failed-step log" {
		t.Fatalf("absence finding: %q", v.Findings[0].Title)
	}

	// Zero red runs recorded at all (a red commit status with no workflow
	// run) is the same absence.
	st, evID = ciStore(t, ciEvidence())
	art, err = CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err = Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("zero runs must escalate: %s", v.Decision)
	}
}

// Pinned from the adversarial pass: a step dropped whole by the per-run byte
// cap is absent signal — the run must escalate, never read as the surviving
// chunks' story (a dropped infra step must not become a confident flake/pass).
func TestCIClassifyDroppedStepsEscalate(t *testing.T) {
	run := ciRun(1, "ci", chunk("test / big", "FATAL: the database system is starting up"))
	run["dropped_steps"] = 1
	st, evID := ciStore(t, ciEvidence(run))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("a byte-cap-dropped step must escalate: %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "dropped by byte cap") {
		t.Fatalf("escalation must name the drop: %q", v.Why)
	}
}

// Pinned from the adversarial pass: ci-classify's code-class signature path
// must not satisfy Reduce's floor-presence invariant — an enrichment rung
// that never gates cannot stand in for the deterministic floor.
func TestReduceCIClassifyAloneDoesNotSatisfyFloorPresence(t *testing.T) {
	got, err := Reduce(subj, []Verdict{
		{Source: sourceCIClassify, Producer: Producer{Class: ClassCode, Impl: ciFloorImpl},
			Decision: DecisionPass, Tier: "T0", Confidence: 1, Why: "classified 1 red runs: flake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != DecisionEscalate {
		t.Fatalf("enrichment alone must not compose a pass: %s", got.Decision)
	}
	if !strings.Contains(got.Why, "no code-floor verdict present") {
		t.Fatalf("must escalate for the missing floor: %q", got.Why)
	}
}

// Pinned from cycle-2 (cursor + codex converged): a failed run listing is
// recorded evidence, and the rung escalates it — the gate run itself must
// not abort on an enrichment read.
func TestCIClassifyListErrorEscalates(t *testing.T) {
	body := ciEvidence()
	body["list_error"] = "gh run list: HTTP 502"
	st, evID := ciStore(t, body)
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("an unavailable run list must escalate: %s", v.Decision)
	}
	if !strings.Contains(v.Why, "red-run list unavailable") {
		t.Fatalf("escalation must name the list failure: %q", v.Why)
	}
}

// Pinned from cycle-2 (codex): advisory calls are budgeted per run so a
// many-chunk run cannot stall the merge step; overflow escalates visibly.
func TestCIClassifyAdvisoryBudgetEscalates(t *testing.T) {
	fakeOllama(t, `{"bucket":"real-break","evidence":"parse_test.go:42: got 3, want 4","why":"TestParse assertion","confidence":0.9}`)
	var chunks []map[string]any
	for i := 0; i <= ciAdvisoryBudget; i++ {
		chunks = append(chunks, chunk(fmt.Sprintf("test / step%d", i), realBreakChunk))
	}
	st, evID := ciStore(t, ciEvidence(ciRun(1, "ci", chunks...)))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("budget overflow must escalate, not silently skip: %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "advisory budget exhausted") {
		t.Fatalf("escalation must name the budget: %q", v.Why)
	}
}

func TestCIClassifyCappedRunsNoteVisible(t *testing.T) {
	body := ciEvidence(ciRun(1, "ci", chunk("test / db", "FATAL: the database system is starting up")))
	body["omitted_runs"] = 2
	st, evID := ciStore(t, body)
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range v.Findings {
		if strings.HasPrefix(f.Title, "capped: 2 additional red runs") {
			found = true
		}
	}
	if !found {
		t.Fatalf("run cap must be finding-visible, got %+v", v.Findings)
	}
}

// fakeOllama serves an Ollama-shaped chat response whose content is the given
// advisory JSON, and restores the real URL on cleanup.
func fakeOllama(t *testing.T, content string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"message":{"content":%q}}`, content)
	}))
	old := ciAdvisoryURL
	ciAdvisoryURL = srv.URL
	t.Cleanup(func() { ciAdvisoryURL = old; srv.Close() })
}

const realBreakChunk = "--- FAIL: TestParse (0.01s)\n    parse_test.go:42: got 3, want 4"

func TestCIClassifyAdvisoryTrustedRealBreakPasses(t *testing.T) {
	fakeOllama(t, `{"bucket":"real-break","evidence":"parse_test.go:42: got 3, want 4","why":"TestParse assertion","confidence":0.9}`)
	st, evID := ciStore(t, ciEvidence(ciRun(1, "ci", chunk("test / go", realBreakChunk))))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionPass {
		t.Fatalf("real-break passes with a finding: %s (%s)", v.Decision, v.Why)
	}
	if v.Producer.Class != ClassLocal {
		t.Fatalf("advisory involvement must set the local producer: %+v", v.Producer)
	}
	if v.Confidence != 0.9 {
		t.Fatalf("confidence must carry the advisory's estimate: %v", v.Confidence)
	}
	f := v.Findings[0]
	if !strings.HasPrefix(f.Title, "real-break: ") {
		t.Fatalf("real-break finding title: %q", f.Title)
	}
	if f.Evidence != "parse_test.go:42: got 3, want 4" {
		t.Fatalf("finding must carry the verifier-checked quote: %q", f.Evidence)
	}
}

func TestCIClassifyAdvisoryDistrustedEscalates(t *testing.T) {
	cases := []string{
		`{"bucket":"real-break","evidence":"a line that appears nowhere","why":"x","confidence":0.9}`,
		`{"bucket":"real-break","evidence":"","why":"empty evidence bypass","confidence":0.95}`,
		`{"bucket":"maybe-flake","evidence":"parse_test.go:42: got 3, want 4","why":"x","confidence":0.9}`,
	}
	for _, content := range cases {
		fakeOllama(t, content)
		st, evID := ciStore(t, ciEvidence(ciRun(1, "ci", chunk("test / go", realBreakChunk))))
		art, err := CIClassify(st, "run_test", evID, subj, nil)
		if err != nil {
			t.Fatal(err)
		}
		v, err := Load(art)
		if err != nil {
			t.Fatal(err)
		}
		if v.Decision != DecisionEscalate {
			t.Fatalf("distrusted advisory must escalate (%s): %s", content, v.Decision)
		}
		if v.Producer.Class != ClassLocal {
			t.Fatalf("advisory involvement sets the class even when distrusted: %+v", v.Producer)
		}
	}
}

// The cloud backend runs through the full ci-classify rung and the ciTrusts
// verbatim-evidence verifier applies to its output exactly as it does to the
// local path: fabricated evidence (a quote absent from the log chunk) is
// distrusted and escalates. This is the cloud-model mirror of the
// fakeOllama-driven distrust case above — the trust chain is model-agnostic.
func TestCIClassifyCloudBackendFabricatedEvidenceEscalates(t *testing.T) {
	input := map[string]any{
		"bucket":     "real-break",
		"evidence":   "a line that appears nowhere in the log",
		"why":        "TestParse assertion",
		"confidence": 0.9,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"content":[{"type":"tool_use","id":"tu_1","name":"structured_output","input":%s}]}`, mustJSON(t, input))
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:  cloudModelDefault,
		apiKey: "test-key",
		url:    srv.URL,
		client: srv.Client(),
	}
	st, evID := ciStore(t, ciEvidence(ciRun(1, "ci", chunk("test / go", realBreakChunk))))
	art, err := CIClassify(st, "run_test", evID, subj, m)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("fabricated cloud evidence must escalate (distrust): %s (%s)", v.Decision, v.Why)
	}
	if v.Producer.Class != ClassLocal {
		t.Fatalf("advisory involvement sets the local producer even on cloud: %+v", v.Producer)
	}
	if v.Producer.Impl != cloudModelDefault {
		t.Fatalf("advisory producer impl must name the cloud model: %+v", v.Producer)
	}
}

func TestCIClassifyAdvisoryUnavailableEscalatesNotErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	old := ciAdvisoryURL
	ciAdvisoryURL = srv.URL
	t.Cleanup(func() { ciAdvisoryURL = old; srv.Close() })

	st, evID := ciStore(t, ciEvidence(ciRun(1, "ci", chunk("test / go", realBreakChunk))))
	art, err := CIClassify(st, "run_test", evID, subj, nil)
	if err != nil {
		t.Fatalf("a red check plus no classifier is a gateable state, not an error: %v", err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != DecisionEscalate {
		t.Fatalf("unavailable advisory must escalate: %s", v.Decision)
	}
	if !strings.Contains(v.Why, "advisory unavailable") {
		t.Fatalf("escalation must name the advisory failure: %q", v.Why)
	}
}

func TestRedChecks(t *testing.T) {
	cases := []struct {
		rollup []map[string]any
		want   bool
	}{
		{[]map[string]any{{"name": "ci", "conclusion": "FAILURE"}}, true},
		{[]map[string]any{{"name": "ci", "conclusion": "TIMED_OUT"}}, true},
		{[]map[string]any{{"name": "ci", "conclusion": "STARTUP_FAILURE"}}, true},
		{[]map[string]any{{"name": "ci", "conclusion": "CANCELLED"}}, true},
		{[]map[string]any{{"context": "lint", "state": "ERROR"}}, true},
		{[]map[string]any{{"name": "ci", "status": "IN_PROGRESS"}}, false},
		{[]map[string]any{{"name": "ci", "conclusion": "SUCCESS"}}, false},
		{nil, false},
	}
	for _, c := range cases {
		st, err := state.Open(t.TempDir(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		a, err := st.Append(state.KindEvidence, "run_test", nil,
			map[string]any{"data": map[string]any{"statusCheckRollup": c.rollup}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := RedChecks(st, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Fatalf("RedChecks(%v) = %v, want %v", c.rollup, got, c.want)
		}
	}
}
