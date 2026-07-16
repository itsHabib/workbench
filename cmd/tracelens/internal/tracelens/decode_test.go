package tracelens

import (
	"strings"
	"testing"
)

func TestDecodeShipEvents_DetectsAllDialects(t *testing.T) {
	cases := map[string]struct {
		input   string
		dialect Dialect
	}{
		"cursor": {
			input:   `{"type":"tool_call","call_id":"c","name":"read","status":"completed","result":"ok"}`,
			dialect: DialectShipCursor,
		},
		"claude": {
			input: strings.Join([]string{
				`{"type":"system","subtype":"init"}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`,
			}, "\n"),
			dialect: DialectShipClaude,
		},
		"codex": {
			input: strings.Join([]string{
				`{"type":"thread.started","thread_id":"t"}`,
				`{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"done"}}`,
			}, "\n"),
			dialect: DialectShipCodex,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeShipEvents(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("DecodeShipEvents: %v", err)
			}
			if got.Dialect != tc.dialect {
				t.Fatalf("dialect = %q, want %q", got.Dialect, tc.dialect)
			}
		})
	}
}

func TestDecodeShipEvents_MixedDialectFailsClosed(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"tool_call","call_id":"c","name":"read","status":"running"}`,
		`{"type":"thread.started","thread_id":"t"}`,
	}, "\n")
	_, err := DecodeShipEvents(strings.NewReader(in))
	if err == nil || !strings.Contains(err.Error(), "mixed") {
		t.Fatalf("mixed stream error = %v", err)
	}
}

func TestDecodeShipEvents_MalformedLineHasContext(t *testing.T) {
	_, err := DecodeShipEvents(strings.NewReader("{\n"))
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("malformed stream error = %v", err)
	}
}

func TestDecodeShipEvents_ShipControlEventsAreDialectNeutral(t *testing.T) {
	cases := map[string]struct {
		input   string
		dialect Dialect
	}{
		"resumed claude": {
			input: strings.Join([]string{
				`{"type":"ship.resumed","attempt":2}`,
				`{"type":"system","subtype":"init"}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`,
			}, "\n"),
			dialect: DialectShipClaude,
		},
		"resumed cursor": {
			input: strings.Join([]string{
				`{"type":"ship.resumed","attempt":2}`,
				`{"type":"tool_call","call_id":"c","name":"read","status":"completed","result":"ok"}`,
			}, "\n"),
			dialect: DialectShipCursor,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeShipEvents(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("DecodeShipEvents: %v", err)
			}
			if got.Dialect != tc.dialect {
				t.Fatalf("dialect = %q, want %q", got.Dialect, tc.dialect)
			}
		})
	}
}

func TestDecodeShipEvents_MarkerlessClaudeLifecycleDetects(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"read","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"ok"}]}}`,
	}, "\n")
	got, err := DecodeShipEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("DecodeShipEvents: %v", err)
	}
	if got.Dialect != DialectShipClaude {
		t.Fatalf("dialect = %q, want %q", got.Dialect, DialectShipClaude)
	}
}

func TestDecodeShipEvents_ControlOnlyStreamFailsClosed(t *testing.T) {
	_, err := DecodeShipEvents(strings.NewReader(`{"type":"ship.resumed","attempt":2}`))
	if err == nil || !strings.Contains(err.Error(), "unrecognized") {
		t.Fatalf("control-only stream error = %v", err)
	}
}

func TestDecodeTrace_NeutralRejectsShipStream(t *testing.T) {
	in := `{"type":"tool_call","call_id":"c","name":"read","status":"completed","result":"ok"}`
	_, err := DecodeTrace(strings.NewReader(in), DialectNeutral)
	if err == nil || !strings.Contains(err.Error(), "detected") {
		t.Fatalf("ship stream declared neutral must fail: %v", err)
	}
}

func TestDecodeTrace_NeutralFailsClosedWithoutAnalyzableSteps(t *testing.T) {
	for name, input := range map[string]string{
		"empty":          "",
		"foreign schema": `{"foo":1}` + "\n" + `{"bar":2}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeTrace(strings.NewReader(input), DialectNeutral)
			if err == nil {
				t.Fatal("stream with no analyzable steps must not decode cleanly")
			}
		})
	}
}
