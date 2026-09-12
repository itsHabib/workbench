package fleet

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestResultIncludesBoundedAssistantAnswer(t *testing.T) {
	answer := "Fixed the failing test. " + strings.Repeat("界", 3000)
	got := TranscriptLine(Rec{"type": "result", "subtype": "success", "result": answer, "is_error": false, "num_turns": 4.0})
	if !strings.HasPrefix(got, "assistant: Fixed the failing test.") || !strings.Contains(got, "…\nresult: success · error=false · turns=4") {
		t.Fatal(got)
	}
	if !utf8.ValidString(got) || len(got) > 8100 {
		t.Fatalf("answer was not bounded valid UTF-8: %d bytes", len(got))
	}
	if got := TranscriptLine(Rec{"type": "result", "subtype": "error_max_turns", "is_error": true}); got != "result: error_max_turns · error=true" {
		t.Fatal(got)
	}
}
