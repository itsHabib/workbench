package fleet

import (
	"fmt"
	"strings"
)

// TranscriptLine renders visible assistant text, tool calls or a terminal result.
// It deliberately skips reasoning blocks and does not infer task success.
func TranscriptLine(r Rec) string {
	var parts []string
	if text := assistantText(r); text != "" {
		parts = append(parts, "assistant: "+text)
	}
	if S(r, "type") == "result" {
		result := "result: " + S(r, "subtype")
		if Has(r, "is_error") {
			result += fmt.Sprintf(" · error=%t", B(r, "is_error"))
		}
		if Has(r, "num_turns") {
			result += fmt.Sprintf(" · turns=%.0f", F(r, "num_turns"))
		}
		if Has(r, "total_cost_usd") {
			result += fmt.Sprintf(" · cost=$%.4f", F(r, "total_cost_usd"))
		}
		return result
	}
	if S(r, "type") == "response_item" {
		p := M(r, "payload")
		if S(p, "type") == "function_call" {
			return "tool: " + S(p, "name") + " " + clipTool(S(p, "arguments"))
		}
	}
	if S(r, "type") != "assistant" {
		return strings.Join(parts, "\n")
	}
	blocks, _ := M(r, "message")["content"].([]any)

	for _, block := range blocks {
		b, _ := block.(map[string]any)
		if S(b, "type") == "tool_use" {
			parts = append(parts, "tool: "+S(b, "name")+" "+clipTool(string(DumpJSON(b["input"]))))
		}
	}
	return strings.Join(parts, "\n")
}

func clipTool(s string) string {
	if len(s) > 1000 {
		return s[:1000] + "…"
	}
	return s
}
