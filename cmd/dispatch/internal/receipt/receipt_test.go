package receipt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/dispatch/internal/placement"
)

func sampleReceipt() Receipt {
	loc := 100
	return Receipt{
		DecidedAt:    time.Unix(0, 0).UTC(),
		Rule:         "small-mechanical",
		PolicySHA256: "deadbeef",
		Descriptor:   placement.Descriptor{Repo: "workbench", TaskClass: "mechanical", WeightedLOC: &loc, RiskTier: "T0"},
		Placement:    placement.Placement{SchemaVersion: placement.SchemaVersion},
	}
}

func TestAppendLineCountEqualsInvocations(t *testing.T) {
	// The phase-2 gate runs on this data: after N appends the file must have
	// exactly N lines, no more, no fewer.
	path := filepath.Join(t.TempDir(), "receipts.jsonl")
	const n = 5
	for range n {
		if err := Append(path, sampleReceipt()); err != nil {
			t.Fatalf("append must succeed: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("receipts file has %d lines, want %d", len(lines), n)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "{") || !strings.Contains(ln, `"rule":"small-mechanical"`) {
			t.Fatalf("line %d is not a well-formed receipt: %q", i, ln)
		}
	}
}

func TestAppendFailsOnBadPath(t *testing.T) {
	// A receipts path whose parent directory does not exist cannot be opened —
	// the forced failure the caller turns into a fail-closed exit 5.
	bad := filepath.Join(t.TempDir(), "nonexistent-dir", "receipts.jsonl")
	if err := Append(bad, sampleReceipt()); err == nil {
		t.Fatal("Append to a path under a missing directory must error")
	}
}
