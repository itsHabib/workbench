package floor

import (
	"bufio"
	"errors"
	"strings"
	"testing"
)

func TestParseUnifiedDiff_Empty(t *testing.T) {
	_, err := ParseUnifiedDiff(strings.NewReader(""))
	if err == nil {
		t.Fatal("empty input: want error, got nil")
	}
}

func TestParseUnifiedDiff_LineTooLong(t *testing.T) {
	// Scanner max token size is 16 MiB; one longer line must fail closed.
	const maxToken = 16 * 1024 * 1024
	raw := strings.Repeat("x", maxToken+1) + "\n"
	_, err := ParseUnifiedDiff(strings.NewReader(raw))
	if err == nil {
		t.Fatal("oversized line: want error, got nil")
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("want wrapped ErrTooLong, got %v", err)
	}
}
