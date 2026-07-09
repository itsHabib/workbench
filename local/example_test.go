package local_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/itsHabib/workbench/local"
)

var filterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "relevant":   {"type": "array", "items": {"type": "integer"}},
    "confidence": {"type": "number"}
  },
  "required": ["relevant", "confidence"]
}`)

// Example uses the primitive as an agent's search-filter co-processor: the
// local model narrows a numbered file list, an in-range verifier catches a
// hallucinated selection, and the gate escalates (or flags) when the local
// answer isn't trusted. It has no output comment on purpose — it compiles
// under `go test` but is not executed, because it needs a live Ollama.
func Example() {
	files := []string{
		"local/local.go",
		"cmd/flare/internal/route/route.go",
		"cmd/eval/main.go",
	}
	input := "QUERY: the local Ollama model tooling\n\nFILES:\n"
	for i, f := range files {
		input += fmt.Sprintf("%d. %s\n", i, f)
	}

	// Verifier: every returned index must be in range. A model cannot invent
	// an index that is not in the list — a real, cheap check that catches a
	// hallucinated selection and outranks any self-reported confidence.
	inRange := func(raw json.RawMessage) bool {
		var out struct {
			Relevant []int `json:"relevant"`
		}
		if json.Unmarshal(raw, &out) != nil {
			return false
		}
		for _, i := range out.Relevant {
			if i < 0 || i >= len(files) {
				return false
			}
		}
		return true
	}

	res, err := local.Ask(context.Background(), local.Req{
		Prompt: "You are a file-relevance filter. Given a QUERY and a numbered FILES list, " +
			"return the 0-based indices of the files relevant to the query, and a confidence 0.0-1.0. " +
			"Use only indices from the list. Output JSON only.",
		Input:  input,
		Schema: filterSchema,
	}, local.Opts{
		Verify:        inRange,
		MinConfidence: 0.7,
		// Escalate: a real cloud call goes here; with none wired, a
		// distrusted result comes back flagged (Reason set), not fetched.
	})
	if err != nil {
		fmt.Println("local:", err)
		return
	}
	fmt.Println(res.Source, res.Reason)
}
