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

func TestParseUnifiedDiff_NoHunksIsValid(t *testing.T) {
	// A structurally valid diff with file headers but no hunks (mode-only,
	// rename-only, binary) is real input, not an operational failure — it must
	// parse so the classifier, not the parser, decides its tier.
	raw := "diff --git a/scripts/deploy.sh b/scripts/deploy.sh\n" +
		"old mode 100644\n" +
		"new mode 100755\n"
	d, err := ParseUnifiedDiff(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("mode-only diff: want parse, got error %v", err)
	}
	if len(d.Files) != 1 || d.Files[0].Path != "scripts/deploy.sh" {
		t.Fatalf("mode-only diff: want 1 file scripts/deploy.sh, got %+v", d.Files)
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
