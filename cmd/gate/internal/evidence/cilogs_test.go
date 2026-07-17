package evidence

import (
	"fmt"
	"strings"
	"testing"
)

func logLine(job, step, rest string) string {
	return job + "\t" + step + "\t2026-07-08T12:00:00.0000000Z " + rest
}

func TestChunkFailedLogGroupsAndStrips(t *testing.T) {
	raw := strings.Join([]string{
		logLine("build", "compile", "go build ./..."),
		logLine("build", "compile", "ok"),
		logLine("test", "unit", "--- FAIL: TestX"),
		"a line with no tabs is metadata, skipped",
	}, "\n")
	chunks, dropped := chunkFailedLog(raw)
	if dropped != 0 {
		t.Fatalf("nothing should drop: %d", dropped)
	}
	if len(chunks) != 2 {
		t.Fatalf("want 2 steps, got %d", len(chunks))
	}
	if chunks[0].Step != "build / compile" || chunks[1].Step != "test / unit" {
		t.Fatalf("step keys: %q, %q", chunks[0].Step, chunks[1].Step)
	}
	if chunks[0].Text != "go build ./...\nok" {
		t.Fatalf("timestamp prefix must be stripped: %q", chunks[0].Text)
	}
	if chunks[0].Truncated || chunks[1].Truncated {
		t.Fatalf("untrimmed chunks must not read as truncated")
	}
}

func TestChunkFailedLogTailsLongSteps(t *testing.T) {
	var lines []string
	for i := 0; i < ciTailLines+10; i++ {
		lines = append(lines, logLine("test", "unit", fmt.Sprintf("line %d", i)))
	}
	chunks, _ := chunkFailedLog(strings.Join(lines, "\n"))
	got := strings.Split(chunks[0].Text, "\n")
	if len(got) != ciTailLines {
		t.Fatalf("want the %d-line tail, got %d", ciTailLines, len(got))
	}
	if got[0] != "line 10" {
		t.Fatalf("tail must keep the end: starts at %q", got[0])
	}
	if !chunks[0].Truncated {
		t.Fatalf("a line-trimmed chunk must be marked truncated")
	}
}

func TestCapRunBytesKeepsNewestSteps(t *testing.T) {
	chunks := []CIChunk{
		{Step: "a", Text: strings.Repeat("x", 500)},
		{Step: "b", Text: strings.Repeat("y", ciMaxChars+1000)},
	}
	kept, dropped := capRunBytes(chunks)
	if dropped != 1 {
		t.Fatalf("older step must drop when the budget is spent: dropped=%d", dropped)
	}
	if len(kept) != 1 || kept[0].Step != "b" {
		t.Fatalf("newest step must survive: %+v", kept)
	}
	if len(kept[0].Text) != ciMaxChars {
		t.Fatalf("surviving step must be capped to the budget: %d", len(kept[0].Text))
	}
	if !kept[0].Truncated {
		t.Fatalf("a byte-capped chunk must be marked truncated")
	}
}

func TestCapRunBytesExactFill(t *testing.T) {
	chunks := []CIChunk{
		{Step: "a", Text: "older"},
		{Step: "b", Text: strings.Repeat("y", ciMaxChars)},
	}
	kept, dropped := capRunBytes(chunks)
	if dropped != 1 || len(kept) != 1 || kept[0].Step != "b" {
		t.Fatalf("an exactly-filling newest step must spend the whole budget: %+v dropped=%d", kept, dropped)
	}
	if kept[0].Truncated {
		t.Fatalf("an exact fill is not a truncation")
	}
}

func TestCapRunBytesUnderBudgetUntouched(t *testing.T) {
	chunks := []CIChunk{{Step: "a", Text: "short"}, {Step: "b", Text: "also short"}}
	kept, dropped := capRunBytes(chunks)
	if dropped != 0 || len(kept) != 2 || kept[0].Truncated || kept[1].Truncated {
		t.Fatalf("under-budget chunks must pass through untouched: %+v dropped=%d", kept, dropped)
	}
}

func TestRedConclusion(t *testing.T) {
	for _, c := range []string{"failure", "startup_failure", "timed_out", "cancelled"} {
		if !redConclusion(c) {
			t.Fatalf("%s is red", c)
		}
	}
	for _, c := range []string{"success", "skipped", "neutral", ""} {
		if redConclusion(c) {
			t.Fatalf("%s is not red", c)
		}
	}
}
