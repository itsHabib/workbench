package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invoke runs the CLI with args and the given stdin, returning exit code and
// captured stdout/stderr.
func invoke(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(append([]string{"dispatch"}, args...), &out, &errb, strings.NewReader(stdin))
	return code, out.String(), errb.String()
}

const catchAllPolicy = "testdata/policy-catchall.json"
const noCatchAllPolicy = "testdata/policy.json"

func writeTmpPolicy(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDecideExitCodes(t *testing.T) {
	small := `{"repo":"workbench","task_class":"mechanical","weighted_loc":100,"risk_tier":"T0"}`
	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantCode int
		wantOut  bool // stdout should carry a placement
	}{
		{name: "match via stdin (0)", args: []string{"decide", "--policy", catchAllPolicy}, stdin: small, wantCode: 0, wantOut: true},
		{name: "match via --task (0)", args: []string{"decide", "--policy", catchAllPolicy, "--task", small}, wantCode: 0, wantOut: true},
		{name: "missing policy flag (2)", args: []string{"decide"}, stdin: small, wantCode: 2},
		{name: "missing policy file (2)", args: []string{"decide", "--policy", "testdata/does-not-exist.json"}, stdin: small, wantCode: 2},
		{name: "no rule matched (3)", args: []string{"decide", "--policy", noCatchAllPolicy},
			stdin: `{"repo":"workbench","task_class":"generative","weighted_loc":3000,"risk_tier":"T3"}`, wantCode: 3},
		{name: "bad descriptor (4)", args: []string{"decide", "--policy", catchAllPolicy}, stdin: `{"repo":"r"}`, wantCode: 4},
		{name: "malformed descriptor (4)", args: []string{"decide", "--policy", catchAllPolicy}, stdin: `{`, wantCode: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, _ := invoke(tt.stdin, tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d (stdout=%q)", code, tt.wantCode, out)
			}
			if tt.wantOut && out == "" {
				t.Fatal("exit 0 must emit a placement on stdout")
			}
			if !tt.wantOut && out != "" {
				t.Fatalf("a non-zero exit must emit nothing on stdout, got %q", out)
			}
		})
	}
}

func TestDecideExit3CarriesUnmatchedValues(t *testing.T) {
	code, out, errb := invoke(
		`{"repo":"workbench","task_class":"generative","weighted_loc":3000,"risk_tier":"T3"}`,
		"decide", "--policy", noCatchAllPolicy)
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if out != "" {
		t.Fatalf("exit 3 must not emit a placement, got %q", out)
	}
	for _, want := range []string{"task_class=generative", "risk_tier=T3"} {
		if !strings.Contains(errb, want) {
			t.Fatalf("stderr = %q, want to contain %q", errb, want)
		}
	}
}

func TestDecidePlacementIsDeterministic(t *testing.T) {
	small := `{"repo":"workbench","task_class":"mechanical","weighted_loc":100,"risk_tier":"T0"}`
	_, first, _ := invoke(small, "decide", "--policy", catchAllPolicy, "--task", small)
	_, second, _ := invoke(small, "decide", "--policy", catchAllPolicy, "--task", small)
	if first != second {
		t.Fatalf("identical descriptor + policy must produce byte-identical stdout:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, `"schema_version":1`) || !strings.Contains(first, `"policy_sha256"`) {
		t.Fatalf("placement must carry schema_version and provenance: %q", first)
	}
}

func TestDecideReceiptWrittenBeforeStdout(t *testing.T) {
	// After N successful decides the receipts file has exactly N lines.
	dir := t.TempDir()
	receipts := filepath.Join(dir, "receipts.jsonl")
	small := `{"repo":"workbench","task_class":"mechanical","weighted_loc":100,"risk_tier":"T0"}`
	const n = 4
	for range n {
		code, out, _ := invoke(small, "decide", "--policy", catchAllPolicy, "--task", small, "--receipts", receipts)
		if code != 0 {
			t.Fatalf("decide with receipts must exit 0, got %d", code)
		}
		if out == "" {
			t.Fatal("a successful decide must still emit a placement")
		}
	}
	data, err := os.ReadFile(receipts)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("receipts file has %d lines, want %d", len(lines), n)
	}
}

func TestDecideReceiptFailureIsFailClosed(t *testing.T) {
	// --receipts pointing under a missing directory: the append fails, so exit 5
	// with nothing on stdout — no placement from a failed invocation.
	receipts := filepath.Join(t.TempDir(), "missing-dir", "receipts.jsonl")
	small := `{"repo":"workbench","task_class":"mechanical","weighted_loc":100,"risk_tier":"T0"}`
	code, out, _ := invoke(small, "decide", "--policy", catchAllPolicy, "--task", small, "--receipts", receipts)
	if code != 5 {
		t.Fatalf("exit = %d, want 5", code)
	}
	if out != "" {
		t.Fatalf("exit 5 must emit nothing on stdout, got %q", out)
	}
}

func TestValidateExitCodes(t *testing.T) {
	empty := writeTmpPolicy(t, `{"version":1,"rules":[]}`)
	tests := []struct {
		name     string
		policy   string
		wantCode int
	}{
		{name: "valid with catch-all (0)", policy: catchAllPolicy, wantCode: 0},
		{name: "valid but no catch-all warns (1)", policy: noCatchAllPolicy, wantCode: 1},
		{name: "empty rules invalid (2)", policy: empty, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errb := invoke("", "validate", "--policy", tt.policy)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d (stderr=%q)", code, tt.wantCode, errb)
			}
			if tt.wantCode != 2 && !strings.Contains(out, `"valid":true`) {
				t.Fatalf("a loadable policy must report valid:true on stdout, got %q", out)
			}
			if tt.wantCode == 1 && !strings.Contains(errb, "catch-all") {
				t.Fatalf("the no-catch-all warning must name the catch-all, got %q", errb)
			}
		})
	}
}

func TestUnknownVerb(t *testing.T) {
	if code, _, _ := invoke("", "frobnicate"); code != 2 {
		t.Fatalf("unknown verb must exit 2, got %d", code)
	}
}

func TestNegativeControlPolicyExit3(t *testing.T) {
	// The shipped example policy MUST exit-3 on its negative-control descriptor —
	// proof the fail-closed gate can actually fail.
	neg := `{"repo":"workbench","task_class":"generative","weighted_loc":3000,"risk_tier":"T3"}`
	code, out, _ := invoke(neg, "decide", "--policy", noCatchAllPolicy)
	if code != 3 {
		t.Fatalf("the example policy must exit 3 on the negative control, got %d", code)
	}
	if out != "" {
		t.Fatalf("exit 3 must place nothing, got %q", out)
	}
}
