package observe_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/observe"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// historicalVerdictLine is a verbatim verdict entry from a real append-only
// gate log, kept as a fixture because that log is custody state and cannot be
// rewritten to suit a reader. Its producer is the contract object — any reader
// that expects the pre-contract bare string silently stops projecting every
// verdict gate has ever recorded.
const historicalVerdictLine = `{"id":"vrd_dde8e243e057e157","kind":"verdict","run":"run_231f0693df2f6b04",` +
	`"time":"2026-08-02T06:45:35.926544Z","parents":["evd_9a14b6f54671e9b4"],` +
	`"body":{"subject":{"repo":"itsHabib/workbench","number":197,` +
	`"head_sha":"90819bb80efbecc2eb1ab7e2d99663135f490d8c"},"source":"review-consolidation",` +
	`"producer":{"class":"local-model","impl":"qwen2.5:7b"},"decision":"escalate","tier":"T0",` +
	`"confidence":1,"why":"no bot review comments for this head"}}`

// TestVerdictProducerSurvivesTheWriteReadSeam closes the seam the judge flow
// depends on but nothing exercised end to end: a verdict written through the
// verify producer path and read back through both readers that consume it —
// verify.Load (the judge's reader) and observe.Project (the explain/audit
// reader). The producer is a struct on the wire and a flattened
// "class/impl" string only in the projection; a reader that confuses the two
// is a decode failure at an authority boundary, not a rendering nit.
func TestVerdictProducerSurvivesTheWriteReadSeam(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	want := verify.Verdict{
		Subject:    verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "head"},
		Source:     "review-consolidation",
		Producer:   verify.Producer{Class: verify.ClassLocal, Impl: "qwen2.5:7b"},
		Decision:   verify.DecisionEscalate,
		Tier:       "T0",
		Confidence: 1,
		Why:        "no bot review comments for this head",
	}
	artifact, err := verify.Record(st, run, nil, want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := verify.Load(artifact)
	if err != nil {
		t.Fatalf("the judge's reader must decode what the producer wrote: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("verdict did not round-trip:\n got %+v\nwant %+v", got, want)
	}

	proj, err := observe.Project(st, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.Artifacts) != 1 {
		t.Fatalf("projection artifact count = %d, want 1", len(proj.Artifacts))
	}
	node := proj.Artifacts[0]
	if node.Unparseable {
		t.Fatal("the projection reader must not flag a verdict it produced itself as unparseable")
	}
	if node.Verdict == nil {
		t.Fatal("a verdict artifact must project a verdict summary")
	}
	if node.Verdict.Producer != "local-model/qwen2.5:7b" {
		t.Fatalf("projected producer = %q, want the flattened class/impl form", node.Verdict.Producer)
	}
}

// TestHistoricalVerdictLineStillDecodes pins the append-only guarantee: an
// entry recorded by an earlier gate build must keep decoding forever, because
// custody state is never rewritten to match a newer reader.
func TestHistoricalVerdictLineStillDecodes(t *testing.T) {
	var artifact state.Artifact
	if err := json.Unmarshal([]byte(historicalVerdictLine), &artifact); err != nil {
		t.Fatalf("a recorded log entry must decode: %v", err)
	}
	v, err := verify.Load(artifact)
	if err != nil {
		t.Fatalf("a recorded verdict must load: %v", err)
	}
	if v.Producer.Class != verify.ClassLocal || v.Producer.Impl != "qwen2.5:7b" {
		t.Fatalf("historical producer = %+v, want the contract object", v.Producer)
	}
	if v.Decision != verify.DecisionEscalate {
		t.Fatalf("historical decision = %q, want escalate", v.Decision)
	}
}
