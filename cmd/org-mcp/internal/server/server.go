// Package server is the org-mcp stdio server: a JSON-RPC 2.0 / MCP surface
// over the org CLI, so an agent session gets the Baton verbs as native tools.
//
// It composes through org's CLI seam — shelling the binary, reading its JSON
// receipts and its exit codes — and never imports the home. That is the
// workbench boundary law doing its job: this package is transport + verb
// allowlist, org owns the append protocol, contracts/org owns the law. A
// kernel refusal (exit 1) comes back as an isError tool result carrying the
// refusal reason, so the driving agent sees `dangling_claim` and corrects,
// exactly as it would on the command line.
//
// The verb table IS the allowlist. Role-structure verbs — charter, takeover,
// revoke, retire, recharter, delegate — have no entry, so they cannot be
// reached over MCP; reshaping the org stays a deliberate operator act.
package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
)

// maxLine bounds one framed JSON-RPC message.
const maxLine = 1 << 20

const protocolVersion = "2024-11-05"

// Runner executes the org binary with args and returns its streams and exit
// code. It exists so tests swap the process boundary for a fake; the server
// has no other side effects.
type Runner func(ctx context.Context, args []string) (stdout, stderr []byte, code int, err error)

// Shell returns the production Runner for one resolved org binary.
func Shell(bin string) Runner {
	return func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
		var out, errBuf bytes.Buffer
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdout, cmd.Stderr = &out, &errBuf
		err := cmd.Run()
		if err == nil {
			return out.Bytes(), errBuf.Bytes(), 0, nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.Bytes(), errBuf.Bytes(), exit.ExitCode(), nil
		}
		return nil, nil, 0, fmt.Errorf("run %s: %w", bin, err)
	}
}

// Server dispatches MCP messages to org verbs through its Runner.
type Server struct {
	run Runner
}

// New returns a Server over the given Runner.
func New(run Runner) *Server { return &Server{run: run} }

// Serve runs the read-dispatch-write loop over in/out until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		resp, respond := s.handleMessage(ctx, scanner.Bytes())
		if !respond {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("org-mcp: write response: %w", err)
		}
	}
	return scanner.Err()
}

const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleMessage(ctx context.Context, line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(json.RawMessage("null"), codeParseError, "parse error"), true
	}
	if len(req.ID) == 0 {
		return rpcResponse{}, false
	}
	return s.dispatch(ctx, req), true
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return okResponse(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "org-mcp", "version": "0.1.0"},
		})
	case "ping":
		return okResponse(req.ID, struct{}{})
	case "tools/list":
		return okResponse(req.ID, toolsListResult())
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return errorResponse(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) rpcResponse {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, codeInvalidParams, "invalid tools/call params")
	}
	v, ok := lookupVerb(p.Name)
	if !ok {
		return errorResponse(req.ID, codeMethodNotFound, "unknown verb: "+p.Name)
	}
	args, err := v.args(p.Arguments)
	if err != nil {
		return okResponse(req.ID, errorResult(map[string]any{"code": "invalid_params", "detail": err.Error()}))
	}
	return okResponse(req.ID, s.invoke(ctx, args))
}

// invoke runs the org binary and maps its exit-code seam onto tool results:
// 0 → the receipt/JSON on stdout; 1 → isError with the kernel's refusal
// reason; anything else → isError with the error class and stderr.
func (s *Server) invoke(ctx context.Context, args []string) toolResult {
	stdout, stderr, code, err := s.run(ctx, args)
	if err != nil {
		return errorResult(map[string]any{"code": "exec_failed", "detail": err.Error()})
	}
	switch code {
	case 0:
		return textResult(string(stdout))
	case 1:
		return errorResult(map[string]any{
			"code": "refused", "reason": refusalReason(stderr), "detail": string(bytes.TrimSpace(stderr)),
		})
	default:
		return errorResult(map[string]any{
			"code": "error", "exit": code, "detail": string(bytes.TrimSpace(stderr)),
		})
	}
}

// refusalPattern extracts the reason id from the kernel's refusal message
// ("org: dangling_claim at seq 11: …"); refusalPrefix is the fallback for a
// refusal-shaped line without the seq clause, so a future message-format
// tweak degrades to a coarser reason instead of an empty one.
var (
	refusalPattern = regexp.MustCompile(`\b([a-z_]+) at seq \d+`)
	refusalPrefix  = regexp.MustCompile(`^org: ([a-z_]+):`)
)

func refusalReason(stderr []byte) string {
	if m := refusalPattern.FindSubmatch(stderr); m != nil {
		return string(m[1])
	}
	if m := refusalPrefix.FindSubmatch(stderr); m != nil {
		return string(m[1])
	}
	return ""
}

func textResult(text string) toolResult {
	return toolResult{Content: []textContent{{Type: "text", Text: text}}}
}

func errorResult(v any) toolResult {
	data, _ := json.Marshal(v)
	return toolResult{Content: []textContent{{Type: "text", Text: string(data)}}, IsError: true}
}

func okResponse(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// itoa is strconv.Itoa under a name that keeps call sites short in verbs.go.
func itoa(n int) string { return strconv.Itoa(n) }
