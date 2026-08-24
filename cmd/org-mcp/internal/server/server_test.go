package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fake returns a Runner that records the args it was called with and replies
// with a canned process result.
func fake(t *testing.T, gotArgs *[]string, stdout, stderr string, code int) Runner {
	t.Helper()
	return func(_ context.Context, args []string) ([]byte, []byte, int, error) {
		*gotArgs = args
		return []byte(stdout), []byte(stderr), code, nil
	}
}

// call drives one tools/call through the full serve loop and returns the tool
// result.
func call(t *testing.T, run Runner, name string, args map[string]any) toolResult {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	params, _ := json.Marshal(toolCallParams{Name: name, Arguments: rawArgs})
	req, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})

	var out bytes.Buffer
	if err := New(run).Serve(context.Background(), bytes.NewReader(append(req, '\n')), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resp struct {
		Result toolResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", out.String(), err)
	}
	return resp.Result
}

// TestClaimTranslatesToCLI proves the verb layer is pure translation: the MCP
// arguments become the CLI invocation, and the receipt comes back verbatim.
func TestClaimTranslatesToCLI(t *testing.T) {
	var got []string
	receipt := `{"kind":"claim","seq":5,"phase":"active"}`
	res := call(t, fake(t, &got, receipt, "", 0), "org_claim",
		map[string]any{"role": "lead:platform", "work": "github:acme/api#88", "incarnation": "sha256:abc"})

	want := []string{"claim", "-role", "lead:platform", "-json", "-incarnation", "sha256:abc", "-work", "github:acme/api#88"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("cli args = %v, want %v", got, want)
	}
	if res.IsError || res.Content[0].Text != receipt {
		t.Fatalf("result = %+v", res)
	}
}

// TestRefusalSurfacesReason proves the exit-code seam maps onto isError with
// the kernel's reason id extracted, so a driving agent can branch on it.
func TestRefusalSurfacesReason(t *testing.T) {
	var got []string
	stderr := "org: dangling_claim at seq 11: a predecessor's claim on github:acme/api#88 is unresolved; yield, complete or abandon it first"
	res := call(t, fake(t, &got, "", stderr, 1), "org_claim",
		map[string]any{"role": "lead:platform", "work": "github:acme/api#88"})

	if !res.IsError {
		t.Fatalf("refusal did not set isError: %+v", res)
	}
	var body struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "refused" || body.Reason != "dangling_claim" {
		t.Fatalf("error body = %+v", body)
	}
}

// TestMissingArgumentIsToolError proves a missing required member never
// reaches the process boundary.
func TestMissingArgumentIsToolError(t *testing.T) {
	ran := false
	run := func(context.Context, []string) ([]byte, []byte, int, error) {
		ran = true
		return nil, nil, 0, nil
	}
	res := call(t, run, "org_claim", map[string]any{"role": "lead:platform"})
	if !res.IsError || ran {
		t.Fatalf("missing work: isError=%v ran=%v", res.IsError, ran)
	}
	if !strings.Contains(res.Content[0].Text, "work is required") {
		t.Fatalf("error body: %s", res.Content[0].Text)
	}
}

// TestAllowlistExcludesStructureVerbs pins the surface: the lifecycle and work
// verbs are present, and the org-reshaping verbs are unreachable.
func TestAllowlistExcludesStructureVerbs(t *testing.T) {
	names := map[string]bool{}
	for _, v := range verbs {
		names[v.name] = true
	}
	for _, want := range []string{"org_boot", "org_status", "org_attach", "org_claim", "org_yield", "org_checkpoint"} {
		if !names[want] {
			t.Fatalf("surface lacks %s", want)
		}
	}
	for _, banned := range []string{"org_charter", "org_takeover", "org_revoke", "org_retire", "org_recharter", "org_delegate"} {
		if names[banned] {
			t.Fatalf("%s must not be reachable over MCP", banned)
		}
	}
	if _, ok := lookupVerb("org_charter"); ok {
		t.Fatal("lookupVerb resolved a banned verb")
	}
}

// TestHandshakeAndUnknowns pins the protocol frame: initialize answers,
// notifications stay silent, unknown methods and verbs are MethodNotFound.
func TestHandshakeAndUnknowns(t *testing.T) {
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"no/such"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"org_frobnicate","arguments":{}}}`,
	}
	var out bytes.Buffer
	run := func(context.Context, []string) ([]byte, []byte, int, error) { return nil, nil, 0, nil }
	err := New(run).Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	responses := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(responses) != 4 {
		t.Fatalf("%d responses, want 4 (notification must be silent):\n%s", len(responses), out.String())
	}
	if !strings.Contains(responses[0], `"org-mcp"`) {
		t.Fatalf("initialize: %s", responses[0])
	}
	if !strings.Contains(responses[1], "org_boot") {
		t.Fatalf("tools/list lacks org_boot: %s", responses[1])
	}
	for i, wantErr := range map[int]string{2: "method not found", 3: "unknown verb"} {
		if !strings.Contains(responses[i], wantErr) {
			t.Fatalf("response %d lacks %q: %s", i, wantErr, responses[i])
		}
	}
}
