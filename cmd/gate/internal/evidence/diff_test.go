package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFetchPrimaryDiffPinsViewedHead(t *testing.T) {
	const (
		base = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		head = "1111111111111111111111111111111111111111"
	)
	result, err := fetchPrimaryDiff(PRRef{Repo: "o/r", Number: 7}, head, primaryDiffFetchers{
		pull: func(PRRef) (json.RawMessage, error) {
			return pullHeads(base, head), nil
		},
		compare: func(_ PRRef, gotBase, gotHead string) (json.RawMessage, error) {
			if gotBase != base || gotHead != head {
				t.Fatalf("compare pair = %s...%s, want %s...%s", gotBase, gotHead, base, head)
			}
			return []byte("the diff"), nil
		},
	})
	if err != nil {
		t.Fatalf("fetchPrimaryDiff: %v", err)
	}
	if result.Diff != "the diff" || result.Head != head {
		t.Fatalf("result = %+v, want diff and exact viewed head", result)
	}
}

// This is the moved-head mutant: the pull read reports a different head than
// the view. No diff fetch may run and no bytes may escape as evidence.
func TestFetchPrimaryDiffRefusesMovedHead(t *testing.T) {
	const (
		viewed = "1111111111111111111111111111111111111111"
		moved  = "2222222222222222222222222222222222222222"
	)
	compareCalled := false
	result, err := fetchPrimaryDiff(PRRef{Repo: "o/r", Number: 7}, viewed, primaryDiffFetchers{
		pull: func(PRRef) (json.RawMessage, error) {
			return pullHeads("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", moved), nil
		},
		compare: func(PRRef, string, string) (json.RawMessage, error) {
			compareCalled = true
			return []byte("diff for the moved head"), nil
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
	if compareCalled {
		t.Fatal("moved head reached the compare diff fetch")
	}
}

// This is the double-force-push mutant from review: the PR begins at A, moves
// to B during the diff read, then returns to A. The compare fetch can still
// receive only A's immutable commit pair.
func TestFetchPrimaryDiffPinsABARaceToCommitPair(t *testing.T) {
	const (
		base   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		viewed = "1111111111111111111111111111111111111111"
		moved  = "2222222222222222222222222222222222222222"
	)
	liveHead := viewed
	result, err := fetchPrimaryDiff(PRRef{Repo: "o/r", Number: 7}, viewed, primaryDiffFetchers{
		pull: func(PRRef) (json.RawMessage, error) { return pullHeads(base, liveHead), nil },
		compare: func(_ PRRef, gotBase, gotHead string) (json.RawMessage, error) {
			liveHead = moved
			defer func() { liveHead = viewed }()
			if gotBase != base || gotHead != viewed {
				t.Fatalf("mutable pair reached compare: %s...%s", gotBase, gotHead)
			}
			return []byte("diff for immutable A"), nil
		},
	})
	if err != nil {
		t.Fatalf("fetchPrimaryDiff under A-B-A race: %v", err)
	}
	if liveHead != viewed || result.Head != viewed || result.Diff != "diff for immutable A" {
		t.Fatalf("A-B-A result = %+v, live head %s", result, liveHead)
	}
}

func pullHeads(base, head string) json.RawMessage {
	return []byte(`{"base":{"sha":"` + base + `"},"head":{"sha":"` + head + `"}}`)
}
