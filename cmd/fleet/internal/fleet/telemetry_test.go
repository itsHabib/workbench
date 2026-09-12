package fleet

import (
	"strings"
	"testing"
)

func TestCapLineCountsOnlyActualTruncation(t *testing.T) {
	old := PromptTruncated
	PromptTruncated = 0
	t.Cleanup(func() { PromptTruncated = old })
	capLine(strings.Repeat("a", 700))
	if PromptTruncated != 0 {
		t.Fatal("counted exact limit")
	}
	got := capLine(strings.Repeat("a", 701))
	if PromptTruncated != 1 || len(got) != 703 {
		t.Fatalf("%d %d", PromptTruncated, len(got))
	}
}
