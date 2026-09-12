package fleetcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReadAllowsOnlyProjectionArguments(t *testing.T) {
	calls := 0
	c := New("fleet", "lens", "/private/state", func(_ context.Context, bin, state string, input []byte, args ...string) ([]byte, error) {
		calls++
		if bin != "fleet" || state != "/private/state" || input != nil {
			t.Fatal(bin, state, string(input))
		}
		if strings.Join(args, " ") != "inspect author:demo --json" {
			t.Fatal(args)
		}
		return []byte(`{"agent":{}}`), nil
	})
	if _, err := c.Read(context.Background(), "inspect", "author:demo"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"stop", "author:demo"}, {"inspect", "--help"}, {"inspect", "x\n--help"}, {"inspect", ""}} {
		if _, err := c.Read(context.Background(), pair[0], pair[1]); err == nil {
			t.Fatal("accepted", pair)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestDiagnosisUsesResolvedTraceAndPreservesPartialCoverage(t *testing.T) {
	calls := 0
	c := New("fleet", "lens", "state", func(_ context.Context, bin, _ string, input []byte, args ...string) ([]byte, error) {
		calls++
		if bin == "fleet" {
			return []byte(`{"source":"observed.jsonl","partial":true,"coverage":"partial window","data":"actual evidence"}`), nil
		}
		if bin != "lens" || string(input) != "actual evidence" || strings.Join(args, " ") != "-json -dialect auto" {
			t.Fatal(bin, string(input), args)
		}
		return []byte(`{"decision":"pass","findings":[]}`), nil
	})
	raw, err := c.Diagnose(context.Background(), "author:demo")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(raw, &got) != nil || got["partial"] != true || got["source"] != "observed.jsonl" || calls != 2 {
		t.Fatal(string(raw), calls)
	}
}

func TestUnavailableEvidenceDoesNotBecomeCleanDiagnosis(t *testing.T) {
	c := New("fleet", "lens", "", func(_ context.Context, bin, _ string, _ []byte, _ ...string) ([]byte, error) {
		if bin == "fleet" {
			return []byte(`{"data":"unsupported"}`), nil
		}
		return nil, fmt.Errorf("unsupported trace")
	})
	if _, err := c.Diagnose(context.Background(), "a"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal(err)
	}
}
