package fleet

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TranscriptLine renders visible assistant text, tool calls or a terminal result.
// It deliberately skips reasoning blocks and does not infer task success.
func TranscriptLine(r Rec) string {
	if S(r, "method") == "item/completed" || S(r, "method") == "item/started" {
		return providerItem(r)
	}
	var parts []string
	if text := assistantText(r); text != "" {
		parts = append(parts, "assistant: "+text)
	}
	if S(r, "type") == "result" {
		if answer := S(r, "result"); answer != "" {
			parts = append(parts, "assistant: "+clipTranscript(answer, 8000))
		}
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
		return strings.Join(append(parts, result), "\n")
	}
	if S(r, "type") == "response_item" {
		p := M(r, "payload")
		if S(p, "type") == "function_call" {
			return "tool: " + S(p, "name") + " " + clipTranscript(S(p, "arguments"), 1000)
		}
	}
	if S(r, "type") != "assistant" {
		return strings.Join(parts, "\n")
	}
	blocks, _ := M(r, "message")["content"].([]any)

	for _, block := range blocks {
		b, _ := block.(map[string]any)
		if S(b, "type") == "tool_use" {
			parts = append(parts, "tool: "+S(b, "name")+" "+clipTranscript(string(DumpJSON(b["input"])), 1000))
		}
	}
	return strings.Join(parts, "\n")
}

func clipTranscript(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit] + "…"
}

func providerItem(r Rec) string {
	item := M(M(r, "params"), "item")
	if S(item, "type") == "agentMessage" && S(r, "method") == "item/completed" {
		return "assistant: " + clipTranscript(S(item, "text"), 8000)
	}
	if S(r, "method") != "item/started" {
		return ""
	}
	switch S(item, "type") {
	case "commandExecution":
		return "tool: commandExecution " + clipTranscript(S(item, "command"), 1000)
	case "fileChange", "mcpToolCall", "dynamicToolCall":
		return "tool: " + S(item, "type") + " " + clipTranscript(S(item, "tool"), 1000)
	}
	return ""
}
