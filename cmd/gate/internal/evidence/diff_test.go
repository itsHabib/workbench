package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFetchPrimaryDiffPinsViewedHead(t *testing.T) {
	const head = "1111111111111111111111111111111111111111"
	result, err := fetchPrimaryDiff(PRRef{Repo: "o/r", Number: 7}, head, primaryDiffFetchers{
		diff: func(PRRef) (json.RawMessage, error) { return []byte("the diff"), nil },
		pull: func(PRRef) (json.RawMessage, error) {
			return pullHeads("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", head), nil
		},
	})
	if err != nil {
		t.Fatalf("fetchPrimaryDiff: %v", err)
	}
	if result.Diff != "the diff" || result.Head != head {
		t.Fatalf("result = %+v, want diff and exact viewed head", result)
	}
}

// This is the moved-head mutant: gh pr diff returns bytes, then the pull read
// reports a different head. The bytes must never escape as recordable evidence.
func TestFetchPrimaryDiffRefusesMovedHead(t *testing.T) {
	const (
		viewed = "1111111111111111111111111111111111111111"
		moved  = "2222222222222222222222222222222222222222"
	)
	diffRead := false
	result, err := fetchPrimaryDiff(PRRef{Repo: "o/r", Number: 7}, viewed, primaryDiffFetchers{
		diff: func(PRRef) (json.RawMessage, error) {
			diffRead = true
			return []byte("diff for the moved head"), nil
		},
		pull: func(PRRef) (json.RawMessage, error) {
			if !diffRead {
				t.Fatal("pull head was read before the diff")
			}
			return pullHeads("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", moved), nil
		},
	})
	if err == nil {
		t.Fatalf("moved head returned recordable evidence: %+v", result)
	}
	if !strings.Contains(err.Error(), "pr head moved during gather") ||
		!strings.Contains(err.Error(), viewed) || !strings.Contains(err.Error(), moved) {
		t.Fatalf("moved-head refusal lost its evidence: %v", err)
	}
	if result != (diffResult{}) {
		t.Fatalf("moved head returned partial evidence: %+v", result)
	}
}

func pullHeads(base, head string) json.RawMessage {
	return []byte(`{"base":{"sha":"` + base + `"},"head":{"sha":"` + head + `"}}`)
}
