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

// TestCardTranslation checks registration and read requests through JSON-RPC.
func TestCardTranslation(t *testing.T) {
	var got []string
	receipt := `{"role":"lead:platform","card":"/cards/platform.md"}`
	res := call(t, fake(t, &got, receipt, "", 0), "org_charter",
		map[string]any{"role": "lead:platform", "file": "/cards/platform.md", "parent": "human:op"})
	want := []string{"charter", "-role", "lead:platform", "-json", "-file", "/cards/platform.md", "-parent", "human:op"}
	if strings.Join(got, " ") != strings.Join(want, " ") || res.IsError || res.Content[0].Text != receipt {
		t.Fatalf("args=%v result=%+v", got, res)
	}
	res = call(t, fake(t, &got, "", "missing card file", 4), "org_boot", map[string]any{"role": "lead:platform"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "missing card file") {
		t.Fatalf("lost error: %+v", res)
	}
}

func TestMissingCardArgumentDoesNotRun(t *testing.T) {
	ran := false
	runner := func(context.Context, []string) ([]byte, []byte, int, error) { ran = true; return nil, nil, 0, nil }
	res := call(t, runner, "org_charter", map[string]any{"role": "lead:platform"})
	if !res.IsError || ran || !strings.Contains(res.Content[0].Text, "file is required") {
		t.Fatalf("result=%+v ran=%v", res, ran)
	}
}

func TestSurfaceHasNoJournalOrWorkProtocol(t *testing.T) {
	if len(verbs) != 3 {
		t.Fatalf("unexpected surface: %v", verbs)
	}
	for _, name := range []string{"org_charter", "org_boot", "org_status"} {
		if _, ok := lookupVerb(name); !ok {
			t.Fatalf("missing %s", name)
		}
	}
	for _, name := range []string{"org_attach", "org_claim", "org_checkpoint", "org_message", "org_intent", "org_legacy"} {
		if _, ok := lookupVerb(name); ok {
			t.Fatalf("legacy operation exposed: %s", name)
		}
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
