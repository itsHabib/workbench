package observe

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

const fixtureRun = "run_explain_fixture"

var fixtureNow = time.Date(2026, 7, 13, 14, 30, 0, 0, time.UTC)

// buildFixtureStore appends one artifact of each kind for explain tests.
func buildFixtureStore(t *testing.T) (string, *state.Store) {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	st, err := state.Open(stateDir, func() time.Time { return fixtureNow })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindGrant, fixtureRun, nil, capability.Grant{
		Repo: "o/r", Action: "merge", MaxTier: "T1", MaxCycles: 3,
		ExpiresAt: fixtureNow.Add(time.Hour), MintedBy: "test", Sig: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, fixtureRun, nil, map[string]any{
		"diff": "line one\nline two\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	vrd, err := verify.Record(st, fixtureRun, []string{evd.ID}, verify.Verdict{
		Subject:    verify.Subject{Repo: "o/r", Number: 42, HeadSHA: "abc123"},
		Source:     "floor",
		Producer:   verify.Producer{Class: verify.ClassCode, Impl: "triage-floor"},
		Decision:   verify.DecisionBlock,
		Tier:       "T1",
		Confidence: 0.95,
		Why:        "test failure in CI",
		Findings: []verify.Finding{
			{Title: "missing test", Severity: "high", Locus: "ci", Evidence: "FAIL pkg/foo"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindJudgment, fixtureRun, []string{vrd.ID}, verify.Verdict{
		Subject:    verify.Subject{Repo: "o/r", Number: 42, HeadSHA: "abc123"},
		Source:     "operator",
		Producer:   verify.Producer{Class: verify.ClassJudgment},
		Decision:   verify.DecisionPass,
		Tier:       "T0",
		Confidence: 1.0,
		Why:        "acceptable risk",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindEscalation, fixtureRun, []string{vrd.ID}, map[string]any{
		"question": "tier exceeds ceiling",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindAction, fixtureRun, []string{vrd.ID}, map[string]any{
		"outcome": "blocked",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindEvidence, fixtureRun, nil, []string{"unparseable"}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(stateDir, "log.jsonl"), st
}

func openFixtureStore(t *testing.T) *state.Store {
	t.Helper()
	dir := filepath.Join("testdata", "fixture")
	st, err := state.Open(dir, func() time.Time { return fixtureNow })
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestWriteFixture regenerates testdata/fixture/log.jsonl. Run with:
//
//	go test ./internal/observe -run TestWriteFixture -args write
func TestWriteFixture(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "write" {
		t.Skip("pass -args write to regenerate fixture")
	}
	logPath, _ := buildFixtureStore(t)
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log.jsonl"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExplainGolden(t *testing.T) {
	st := openFixtureStore(t)
	var got bytes.Buffer
	if err := Explain(&got, st, fixtureRun); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "explain.golden")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("explain output changed; run TestWriteGolden to refresh:\n--- got\n%s\n--- want\n%s", got.Bytes(), want)
	}
}

// TestWriteGolden regenerates testdata/explain.golden. Run with:
//
//	go test ./internal/observe -run TestWriteGolden -args write
func TestWriteGolden(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "write" {
		t.Skip("pass -args write to regenerate golden")
	}
	st := openFixtureStore(t)
	var buf bytes.Buffer
	if err := Explain(&buf, st, fixtureRun); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "explain.golden"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectJSONRoundTrip(t *testing.T) {
	st := openFixtureStore(t)
	proj, err := Project(st, fixtureRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.Artifacts) != 7 {
		t.Fatalf("want 7 artifacts (grant + 6 run kinds), got %d", len(proj.Artifacts))
	}

	var buf bytes.Buffer
	if err := ExplainJSON(&buf, st, fixtureRun); err != nil {
		t.Fatal(err)
	}
	var decoded Run
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Run != fixtureRun || len(decoded.Artifacts) != len(proj.Artifacts) {
		t.Fatalf("round-trip run/length mismatch: %+v", decoded)
	}
	assertProjectionKinds(t, decoded.Artifacts)
}

func assertProjectionKinds(t *testing.T, nodes []Node) {
	t.Helper()
	kinds := map[string]int{}
	var unparseable int
	for _, n := range nodes {
		if n.ID == "" || n.Kind == "" || n.Time == "" {
			t.Fatalf("node missing base fields: %+v", n)
		}
		kinds[n.Kind]++
		if n.Unparseable {
			unparseable++
			continue
		}
		switch n.Kind {
		case state.KindEvidence:
			if n.Evidence == nil || n.Evidence.Type == "" {
				t.Fatalf("evidence node missing summary: %+v", n)
			}
		case state.KindVerdict, state.KindJudgment:
			if n.Verdict == nil {
				t.Fatalf("verdict node missing fields: %+v", n)
			}
		case state.KindGrant, state.KindEscalation, state.KindAction:
			if n.Flat == nil {
				t.Fatalf("flat node missing body: %+v", n)
			}
		}
	}
	for _, kind := range []string{
		state.KindEvidence, state.KindVerdict, state.KindJudgment,
		state.KindGrant, state.KindEscalation, state.KindAction,
	} {
		if kinds[kind] == 0 {
			t.Fatalf("missing kind %q in projection", kind)
		}
	}
	if kinds[state.KindEvidence] < 2 {
		t.Fatal("want at least two evidence nodes including unparseable")
	}
	if unparseable != 1 {
		t.Fatalf("want exactly one unparseable node, got %d", unparseable)
	}
}

func TestExplainEmptyRun(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	err = Explain(&bytes.Buffer{}, st, "run_missing")
	if err == nil || err.Error() != "observe: run run_missing has no artifacts" {
		t.Fatalf("got err %v", err)
	}
}

func TestParseFlatObjectRejectsTrailingData(t *testing.T) {
	for _, body := range []string{
		`{"a":1} {"b":2}`,
		`{"a":1}[]`,
	} {
		if _, _, err := parseFlatObject([]byte(body)); err == nil {
			t.Fatalf("parseFlatObject(%q) = nil error, want trailing-data rejection", body)
		}
	}
	if _, _, err := parseFlatObject([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("parseFlatObject clean object err = %v, want nil", err)
	}
}
