package tracelens

import (
	"strings"
	"testing"
)

func TestParseJSONL_BasicFields(t *testing.T) {
	in := `
# a comment line, skipped
{"role":"assistant","thought":"look it up","tool":"search","args":{"q":"weather"},"observation":"sunny","ok":true,"cost_usd":0.01}

{"tool":"search","args":{"q":"weather"},"ok":false,"error":"timeout","cost_usd":0.02}
`
	tr, err := ParseJSONL(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tr.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(tr.Steps))
	}
	s0 := tr.Steps[0]
	if s0.Index != 0 || s0.Tool != "search" || s0.Observation != "sunny" {
		t.Fatalf("step0 mismatch: %+v", s0)
	}
	if s0.OK == nil || !*s0.OK {
		t.Fatalf("step0 ok should be true")
	}
	if !tr.Steps[1].Failed() {
		t.Fatalf("step1 should be a failed tool result")
	}
	if got := tr.TotalCost(); got < 0.029 || got > 0.031 {
		t.Fatalf("total cost want ~0.03, got %v", got)
	}
}

func TestParseJSONL_SequentialIndexWhenMissing(t *testing.T) {
	in := `{"thought":"a"}
{"thought":"b"}
{"thought":"c"}`
	tr, err := ParseJSONL(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for want, s := range tr.Steps {
		if s.Index != want {
			t.Fatalf("step %d got index %d", want, s.Index)
		}
	}
}

func TestParseJSONL_MalformedReportsLine(t *testing.T) {
	in := `{"tool":"ok"}
{"tool": bad json}`
	_, err := ParseJSONL(strings.NewReader(in))
	if err == nil {
		t.Fatal("expected error on malformed line")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error should name line 2, got: %v", err)
	}
}

func TestParseJSONL_DuplicateIndexFails(t *testing.T) {
	in := `{"i":7,"tool":"first"}
{"i":7,"tool":"second"}`
	_, err := ParseJSONL(strings.NewReader(in))
	if err == nil || !strings.Contains(err.Error(), "line 2: duplicate step index 7") {
		t.Fatalf("duplicate index error = %v", err)
	}
}

func TestStepSignatures_StableAcrossArgOrder(t *testing.T) {
	a := Step{Tool: "http", Args: map[string]any{"url": "x", "method": "GET"}}
	b := Step{Tool: "http", Args: map[string]any{"method": "GET", "url": "x"}}
	if a.callSig() != b.callSig() {
		t.Fatalf("callSig should be order-independent:\n a=%s\n b=%s", a.callSig(), b.callSig())
	}
	c := Step{Tool: "http", Args: map[string]any{"url": "y", "method": "GET"}}
	if a.callSig() == c.callSig() {
		t.Fatal("different args must yield different callSig")
	}
}
