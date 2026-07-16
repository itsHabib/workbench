package tracelens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// rawStep mirrors the on-disk JSONL schema. Unknown fields are ignored by
// encoding/json, so producers may add their own without breaking ingestion.
type rawStep struct {
	Index       *int           `json:"i"`
	Role        string         `json:"role"`
	Thought     string         `json:"thought"`
	Tool        string         `json:"tool"`
	Args        map[string]any `json:"args"`
	Observation string         `json:"observation"`
	OK          *bool          `json:"ok"`
	Error       string         `json:"error"`
	TokensIn    int            `json:"tokens_in"`
	TokensOut   int            `json:"tokens_out"`
	CostUSD     float64        `json:"cost_usd"`
	LatencyMS   int            `json:"latency_ms"`
}

// ParseJSONL reads a JSONL trace, one step object per line. Blank lines and
// lines beginning with '#' are skipped. Malformed JSON fails with the offending
// line number so a producer can find it. Missing "i" indices are filled in
// sequentially in file order.
func ParseJSONL(r io.Reader) (Trajectory, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var steps []Step
	seenIndices := map[int]int{}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		var rs rawStep
		if err := json.Unmarshal([]byte(text), &rs); err != nil {
			return Trajectory{}, fmt.Errorf("line %d: %w", line, err)
		}
		step := toStep(rs, len(steps))
		if firstLine, exists := seenIndices[step.Index]; exists {
			return Trajectory{}, fmt.Errorf("line %d: duplicate step index %d (first used on line %d)", line, step.Index, firstLine)
		}
		seenIndices[step.Index] = line
		steps = append(steps, step)
	}
	if err := sc.Err(); err != nil {
		return Trajectory{}, err
	}
	return Trajectory{Steps: steps}, nil
}

func toStep(rs rawStep, seq int) Step {
	idx := seq
	if rs.Index != nil {
		idx = *rs.Index
	}
	return Step{
		Index:       idx,
		Role:        rs.Role,
		Thought:     rs.Thought,
		Tool:        rs.Tool,
		Args:        rs.Args,
		Observation: rs.Observation,
		OK:          rs.OK,
		Error:       rs.Error,
		TokensIn:    rs.TokensIn,
		TokensOut:   rs.TokensOut,
		CostUSD:     rs.CostUSD,
		LatencyMS:   rs.LatencyMS,
	}
}
