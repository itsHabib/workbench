package server

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 error codes used on the wire (a subset of the spec). MethodNotFound
// is what an unknown verb resolves to (spec §6 — "unknown verbs return MCP
// MethodNotFound").
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// protocolVersion is the MCP revision this server speaks in its initialize
// handshake.
const protocolVersion = "2024-11-05"

// rpcRequest is an inbound JSON-RPC message. A missing id marks a notification
// (no response is written). Params stay raw until a method decodes them.
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
	Data    any    `json:"data,omitempty"`
}

// handleMessage parses one framed message and dispatches it by method. The
// second return reports whether a response should be written: notifications
// (no id) are handled silently. A parse failure answers with a null-id error,
// per JSON-RPC.
func (s *Server) handleMessage(line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(json.RawMessage("null"), codeParseError, "parse error"), true
	}
	if len(req.ID) == 0 {
		s.handleNotification(req)
		return rpcResponse{}, false
	}
	return s.dispatch(req), true
}

// handleNotification handles the id-less messages. Only notifications/initialized
// is expected; anything else is ignored (a notification never gets a response).
func (s *Server) handleNotification(_ rpcRequest) {}

// dispatch routes a request with an id to its method handler.
func (s *Server) dispatch(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return okResponse(req.ID, initializeResult())
	case "ping":
		return okResponse(req.ID, struct{}{})
	case "tools/list":
		return okResponse(req.ID, toolsListResult())
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return errorResponse(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

// initializeResult is the server's half of the MCP handshake: the protocol
// revision it speaks, its (tools-only) capabilities, and its identity.
func initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "workbench-mcp", "version": "0.1.0"},
	}
}

// toolCallParams is the tools/call envelope: the verb name and its arguments.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolResult is the MCP tools/call result: a text content block carrying the
// verb's JSON, and isError flagging the structured-error path (spec §6 — the
// ErrIllegalTransition/ErrChainBroken/ErrLocked codes surface to the client).
type toolResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// handleToolsCall resolves the named verb and runs it. An unregistered verb is a
// JSON-RPC MethodNotFound (spec §6); a registered verb's own failure — bad
// params or a structured ledger error — comes back as an isError tool result so
// the driving agent sees and corrects it (spec §7 F2), never as a transport
// error.
func (s *Server) handleToolsCall(req rpcRequest) rpcResponse {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, codeInvalidParams, "invalid tools/call params")
	}
	v, ok := lookupVerb(p.Name)
	if !ok {
		return errorResponse(req.ID, codeMethodNotFound, "unknown verb: "+p.Name)
	}
	out, err := v.handle(s, p.Arguments)
	if err != nil {
		return okResponse(req.ID, errorToolResult(err))
	}
	return okResponse(req.ID, toolResultJSON(out))
}

// toolResultJSON wraps a verb's successful output as a text content block. A
// marshal failure is itself reported as an error result rather than a panic.
func toolResultJSON(out any) toolResult {
	data, err := json.Marshal(out)
	if err != nil {
		return errorToolResult(fmt.Errorf("workbench-mcp: encode result: %w", err))
	}
	return toolResult{Content: []textContent{{Type: "text", Text: string(data)}}}
}

// errorToolResult renders err as a structured, isError tool result — the stable
// code plus any detail fields (see classifyError).
func errorToolResult(err error) toolResult {
	ve := classifyError(err)
	data, _ := json.Marshal(ve)
	return toolResult{Content: []textContent{{Type: "text", Text: string(data)}}, IsError: true}
}

func okResponse(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
