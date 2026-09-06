// Package mcp is the fleet's verbs as MCP tools, over stdio, thin over the verbs.
//
// Two faces, one store. Every tool here calls the same function the CLI verb calls
// and reads the same files; nothing is reimplemented and nothing is cached in this
// process. The harness spawns one of these per session and kills it with the
// session, so it is not a daemon of the fleet's: if it dies, `fleet <verb>` at a
// terminal answers identically from the same records.
//
// What is exposed, and why only this. Questions (who, slots, unowned, board, done)
// are the lookup plane. Dispatch (assign) is where *assigned* gets written. take and
// drop are real acts on a machine resource. Nothing here exists for an agent to
// report what it is doing: the hooks derive that from actions already taken.
//
// Identity. Every tool takes cwd, the CALLER's working directory, and resolves there;
// the acting tools require it. This server's own cwd is wherever the harness launched
// it, which is not the caller's worktree and may be another session's.
//
// Wire: JSON-RPC 2.0, one message per line on stdin/stdout, MCP 2024-11-05.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
)

const protocol = "2024-11-05"

type schema = map[string]any

func str(desc string) schema { return schema{"type": "string", "description": desc} }

var cwdArg = str("the calling session's working directory (its worktree); identity and branch names resolve relative to it, never to this server's own cwd")

var tools = []schema{
	{"name": "fleet_who",
		"description": "Which live session holds a slot, lease key, change number (#n) or branch; says loudly when nobody does.",
		"inputSchema": schema{"type": "object", "properties": schema{"name": str("slot name, slot:<name>, repo:<id>:<branch>, #<n>, or a branch in cwd's repo"), "cwd": cwdArg},
			"required": []any{"name"}}},
	{"name": "fleet_slots",
		"description": "Every pooled slot with its observed state: free, busy(session, branch), dirty, orphaned, missing; cold/assigned flags.",
		"inputSchema": schema{"type": "object", "properties": schema{"repo": str("limit to one repo (basename or kind:<repo> suffix)"), "cwd": cwdArg}}},
	{"name": "fleet_unowned",
		"description": "Open changes whose head branch no live session on THIS machine holds; scope is stated in the answer.",
		"inputSchema": schema{"type": "object", "properties": schema{"repo": str("limit to one GitHub repo (owner/name or name)"), "cwd": cwdArg}}},
	{"name": "fleet_board",
		"description": "Every roled path with observed liveness: vacant, dead, idle, idle-holding-work, busy, busy-and-overdue (past the lane's cadence).",
		"inputSchema": schema{"type": "object", "properties": schema{"cwd": cwdArg}}},
	{"name": "fleet_done",
		"description": "Is there a passing receipt of every expected kind for this revision (sha, #n, branch)? ok/missing/failed per kind, plus the card URL.",
		"inputSchema": schema{"type": "object", "properties": schema{"revision": str("sha prefix, #<n>, or branch"), "kind": str("receipt kind expected; default: every kind seen"), "cwd": cwdArg},
			"required": []any{"revision"}}},
	{"name": "fleet_assign",
		"description": "Place work into a free slot: check the branch out there and record the assignment the slot's next session reads.",
		"inputSchema": schema{"type": "object", "properties": schema{"slot": str("slot name from fleet_slots"), "branch": str("branch to check out"), "brief": str("one line the session reads at start"), "for": str("the role accountable for this work until done; default: the dispatcher"), "cwd": cwdArg},
			"required": []any{"slot", "branch", "cwd"}}},
	{"name": "fleet_dispatch",
		"description": "The one declared act: write a change's ownership row (relationship, accountable role, due), placed in a slot when named; refused over live hands unless take.",
		"inputSchema": schema{"type": "object", "properties": schema{"change": str("branch name or #<n>"), "as": str("relationship: a short lowercase word; the receipt kind that means done"),
			"for": str("accountable role; default: the dispatcher"), "due": str("duration like 45m or 2h"), "slot": str("free slot to place the work in (fleet_slots)"),
			"brief": str("one line the slot's session reads at start"), "take": schema{"type": "boolean", "description": "rewrite a row that has live hands"}, "cwd": cwdArg},
			"required": []any{"change", "as", "cwd"}}},
	{"name": "fleet_work",
		"description": "Every ownership row on this machine with its observed state: dead, late, undeclared (need a decision); working, idle, dispatched, done.",
		"inputSchema": schema{"type": "object", "properties": schema{"for": str("only rows this role is accountable for"), "cwd": cwdArg}}},
	{"name": "fleet_reassign",
		"description": "Move every row of a change to another accountable role; the second command of splitting a hub.",
		"inputSchema": schema{"type": "object", "properties": schema{"change": str("branch name or #<n>"), "for": str("the role now accountable"), "cwd": cwdArg},
			"required": []any{"change", "for", "cwd"}}},
	{"name": "fleet_take",
		"description": "Lease a machine resource (slot:<name>) for this session; refused if a live session holds it, orphaned needs takeover.",
		"inputSchema": schema{"type": "object", "properties": schema{"resource": str("slot:<name>"), "why": str("one line, recorded on the lease"),
			"takeover": schema{"type": "boolean", "description": "take an orphaned resource you have confirmed is quiet"},
			"session":  str("session id prefix when two live sessions share cwd"), "cwd": cwdArg},
			"required": []any{"resource", "cwd"}}},
	{"name": "fleet_drop",
		"description": "Release a machine resource this session holds, after it is quiet; only the holder may.",
		"inputSchema": schema{"type": "object", "properties": schema{"resource": str("slot:<name>"), "session": str("session id prefix when two live sessions share cwd"), "cwd": cwdArg},
			"required": []any{"resource", "cwd"}}},
}

var byName = func() map[string]schema {
	m := map[string]schema{}
	for _, t := range tools {
		m[t["name"].(string)] = t
	}
	return m
}()

type badArguments struct{ msg string }

func (b *badArguments) Error() string { return b.msg }

var errUnknownTool = errors.New("unknown tool")

func checkArguments(name string, a map[string]any) error {
	tool, ok := byName[name]
	if !ok {
		return errUnknownTool
	}
	sch := tool["inputSchema"].(schema)
	props := sch["properties"].(schema)
	var missing []string
	if req, ok := sch["required"].([]any); ok {
		for _, k := range req {
			ks := k.(string)
			if !truthy(a[ks]) {
				missing = append(missing, ks)
			}
		}
	}
	if len(missing) > 0 {
		return &badArguments{fmt.Sprintf("%s: missing required argument(s) %s", name, strings.Join(missing, ", "))}
	}
	var unknown []string
	for k := range a {
		if _, ok := props[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return &badArguments{fmt.Sprintf("%s: unknown argument(s) %s", name, strings.Join(unknown, ", "))}
	}
	for k, v := range a {
		want := props[k].(schema)["type"].(string)
		_, isStr := v.(string)
		_, isBool := v.(bool)
		if (want == "string" && !isStr) || (want == "boolean" && !isBool) {
			return &badArguments{fmt.Sprintf("%s: %s must be a %s", name, k, want)}
		}
	}
	if cwd, ok := a["cwd"].(string); ok && cwd != "" {
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			return &badArguments{fmt.Sprintf("%s: cwd %s is not a directory", name, fleet.PyRepr(cwd))}
		}
	}
	return nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	}
	return true
}

// runVerb calls a CLI verb, capturing what it printed and how it exited. A refusal's
// text is the tool's error text, verbatim, so the MCP face and the terminal say the
// same thing.
func runVerb(fn func() error) (string, bool) {
	var buf strings.Builder
	prev := verbs.Out
	verbs.Out = &buf
	defer func() { verbs.Out = prev }()
	err := fn()
	if err == nil {
		return buf.String(), false
	}
	var r *verbs.Refusal
	if errors.As(err, &r) {
		if r.Code == 0 {
			return buf.String(), false
		}
		text := strings.TrimSpace(buf.String() + r.Msg)
		if text == "" {
			text = fmt.Sprintf("refused (exit %d)", r.Code)
		}
		return text, true
	}
	return err.Error(), true
}

// inCwd resolves the call where the CALLER is. One server serves one session and
// handles one call at a time, so a chdir for the call's duration is safe.
func inCwd(cwd string, fn func() (string, bool)) (string, bool) {
	if cwd == "" {
		return fn()
	}
	before, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		return err.Error(), true
	}
	defer func() { _ = os.Chdir(before) }()
	return fn()
}

func call(name string, a map[string]any) (string, bool, error) {
	if a == nil {
		a = map[string]any{}
	}
	if err := checkArguments(name, a); err != nil {
		return "", false, err
	}
	cwd, _ := a["cwd"].(string)
	text, isErr := inCwd(cwd, func() (string, bool) { return dispatch(name, a) })
	return text, isErr, nil
}

func js(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func dispatch(name string, a map[string]any) (string, bool) {
	s := func(k string) string { v, _ := a[k].(string); return v }
	switch name {
	case "fleet_who":
		ok, text, detail := verbs.Who(s("name"))
		out := map[string]any{"resolved": ok, "text": text}
		for _, k := range []string{"key", "lease"} {
			if v, ok := detail[k]; ok {
				out[k] = v
			}
		}
		return js(out), false
	case "fleet_slots":
		return js(verbs.SlotRows(s("repo"))), false
	case "fleet_unowned":
		return js(verbs.Unowned(s("repo"))), false
	case "fleet_board":
		return js(verbs.BoardRows()), false
	case "fleet_done":
		// Exit 1 (pending) and 3 (failed evidence) are answers the JSON states; 2 is the error.
		var buf strings.Builder
		prev := verbs.Out
		verbs.Out = &buf
		err := verbs.CmdDone(s("revision"), s("kind"), true)
		verbs.Out = prev
		var r *verbs.Refusal
		if errors.As(err, &r) && r.Code == 2 {
			msg := r.Msg
			if msg == "" {
				msg = "unresolvable"
			}
			return strings.TrimSpace(buf.String() + msg), true
		}
		return buf.String(), false
	case "fleet_assign":
		return runVerb(func() error { return verbs.CmdAssign(s("slot"), s("branch"), s("brief"), "mcp", s("for")) })
	case "fleet_dispatch":
		take, _ := a["take"].(bool)
		return runVerb(func() error {
			return verbs.CmdDispatch(s("change"), s("as"), s("for"), s("due"), s("slot"), s("brief"), "mcp", take)
		})
	case "fleet_work":
		return js(verbs.WorkRows(s("for"))), false
	case "fleet_reassign":
		return runVerb(func() error { return verbs.CmdReassign(s("change"), s("for")) })
	case "fleet_take":
		takeover, _ := a["takeover"].(bool)
		return runVerb(func() error { return verbs.CmdTake(s("resource"), s("why"), takeover, s("session")) })
	case "fleet_drop":
		return runVerb(func() error { return verbs.CmdDrop(s("resource"), s("session")) })
	}
	return "", true
}

func rpcError(id any, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func handle(msg map[string]any) map[string]any {
	method, _ := msg["method"].(string)
	id, hasID := msg["id"]
	params, _ := msg["params"].(map[string]any)
	if !hasID || id == nil {
		return nil // notifications need no answer
	}
	switch method {
	case "initialize":
		pv, _ := params["protocolVersion"].(string)
		if pv == "" {
			pv = protocol
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
			"protocolVersion": pv, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "fleet", "version": "0.1"}}}
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}}
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": tools}}
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		fleet.MigrateLegacyKeys()
		text, isErr, err := safeCall(name, args)
		if errors.Is(err, errUnknownTool) {
			return rpcError(id, -32602, fmt.Sprintf("unknown tool %s", fleet.PyRepr(name)))
		}
		var bad *badArguments
		if errors.As(err, &bad) {
			return rpcError(id, -32602, bad.msg)
		}
		if err != nil {
			text, isErr = err.Error(), true
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}}
	}
	return rpcError(id, -32601, "method not found: "+method)
}

// safeCall turns a tool's own panic into a tool error, not a dead server.
func safeCall(name string, args map[string]any) (text string, isErr bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			text, isErr, err = fmt.Sprintf("%v", r), true, nil
		}
	}()
	return call(name, args)
}

// Serve reads one JSON-RPC object per line and answers each.
func Serve(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var resp map[string]any
		var raw any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			resp = rpcError(nil, -32700, "parse error")
		} else if msg, ok := raw.(map[string]any); !ok {
			// A batch, `null`, a bare number are valid JSON and not requests.
			resp = rpcError(nil, -32600, "invalid request: expected one JSON-RPC object per line")
		} else {
			resp = handleSafe(msg)
		}
		if resp != nil {
			b, _ := json.Marshal(resp)
			w.Write(b)
			w.WriteByte('\n')
			w.Flush()
		}
	}
}

func handleSafe(msg map[string]any) (resp map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			resp = rpcError(msg["id"], -32603, fmt.Sprintf("%v", r))
		}
	}()
	return handle(msg)
}
