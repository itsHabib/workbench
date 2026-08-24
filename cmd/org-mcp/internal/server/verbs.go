package server

import (
	"encoding/json"
	"fmt"
)

// verb is one MCP tool: its wire name, description, argument schema, and the
// translation from MCP arguments to org CLI arguments. Translation is all a
// verb does — the CLI's own flag parsing and the kernel's laws stay the single
// source of truth.
type verb struct {
	name        string
	description string
	schema      json.RawMessage
	args        func(json.RawMessage) ([]string, error)
}

// callArgs is the superset of arguments the org verbs accept over MCP. Which
// members a verb requires is enforced per-verb in its args func; everything
// else is ignored, so one decode covers every tool.
type callArgs struct {
	Role        string `json:"role"`
	Work        string `json:"work"`
	Body        string `json:"body"`
	Pin         string `json:"pin"`
	Digest      string `json:"digest"`
	Effect      string `json:"effect"`
	Target      string `json:"target"`
	Incarnation string `json:"incarnation"`
	NextDue     string `json:"next_due"`
	MaxBytes    int    `json:"max_bytes"`
}

func decode(raw json.RawMessage) (callArgs, error) {
	var a callArgs
	if len(raw) == 0 {
		return a, nil
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("decode arguments: %w", err)
	}
	return a, nil
}

// base builds the shared CLI prefix: the verb, the chain, the identity, and a
// JSON receipt. State and tenant resolve from the server process environment
// (ORG_STATE / ORG_TENANT), which the child inherits.
func base(cliVerb string, a callArgs) []string {
	args := []string{cliVerb, "-role", a.Role, "-json"}
	if a.Incarnation != "" {
		args = append(args, "-incarnation", a.Incarnation)
	}
	if a.NextDue != "" {
		args = append(args, "-next-due", a.NextDue)
	}
	return args
}

// need returns an error naming the first missing required member.
func need(pairs ...[2]string) error {
	for _, p := range pairs {
		if p[1] == "" {
			return fmt.Errorf("%s is required", p[0])
		}
	}
	return nil
}

// roleVerb covers the verbs whose only required argument is the role.
func roleVerb(cliVerb string) func(json.RawMessage) ([]string, error) {
	return func(raw json.RawMessage) ([]string, error) {
		a, err := decode(raw)
		if err != nil {
			return nil, err
		}
		if err := need([2]string{"role", a.Role}); err != nil {
			return nil, err
		}
		args := base(cliVerb, a)
		if a.Body != "" {
			args = append(args, "-body", a.Body)
		}
		return args, nil
	}
}

// workVerb covers claim and its terminals: role + work, optional body.
func workVerb(cliVerb string) func(json.RawMessage) ([]string, error) {
	return func(raw json.RawMessage) ([]string, error) {
		a, err := decode(raw)
		if err != nil {
			return nil, err
		}
		if err := need([2]string{"role", a.Role}, [2]string{"work", a.Work}); err != nil {
			return nil, err
		}
		args := append(base(cliVerb, a), "-work", a.Work)
		if a.Body != "" {
			args = append(args, "-body", a.Body)
		}
		return args, nil
	}
}

// bodyVerb covers the advisory verbs: role + body, both required.
func bodyVerb(cliVerb string) func(json.RawMessage) ([]string, error) {
	return func(raw json.RawMessage) ([]string, error) {
		a, err := decode(raw)
		if err != nil {
			return nil, err
		}
		if err := need([2]string{"role", a.Role}, [2]string{"body", a.Body}); err != nil {
			return nil, err
		}
		return append(base(cliVerb, a), "-body", a.Body), nil
	}
}

const roleOnly = `{"type":"object","properties":{"role":{"type":"string"},"body":{"type":"string"},"incarnation":{"type":"string"},"next_due":{"type":"string"}},"required":["role"]}`
const roleWork = `{"type":"object","properties":{"role":{"type":"string"},"work":{"type":"string"},"body":{"type":"string"},"incarnation":{"type":"string"},"next_due":{"type":"string"}},"required":["role","work"]}`
const roleBody = `{"type":"object","properties":{"role":{"type":"string"},"body":{"type":"string"},"incarnation":{"type":"string"},"next_due":{"type":"string"}},"required":["role","body"]}`

// verbs is the exposed surface, and the list IS the allowlist: charter,
// takeover, revoke, retire, recharter and delegate have no entry, so the org's
// structure cannot be reshaped over MCP.
var verbs = []verb{
	{
		name:        "org_boot",
		description: "Render a role's re-entry index: charter, held work, obligations (a predecessor's dangling claim first), liveness, and the last incarnation's final word. Read this before acting as a role.",
		schema:      json.RawMessage(`{"type":"object","properties":{"role":{"type":"string"},"max_bytes":{"type":"integer"}},"required":["role"]}`),
		args: func(raw json.RawMessage) ([]string, error) {
			a, err := decode(raw)
			if err != nil {
				return nil, err
			}
			if err := need([2]string{"role", a.Role}); err != nil {
				return nil, err
			}
			args := []string{"boot", "-role", a.Role}
			if a.MaxBytes > 0 {
				args = append(args, "-max-bytes", itoa(a.MaxBytes))
			}
			return args, nil
		},
	},
	{
		name:        "org_status",
		description: "The org board: every role's phase, active work, held count, open obligations, and liveness.",
		schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		args: func(json.RawMessage) ([]string, error) {
			return []string{"status", "-json"}, nil
		},
	},
	{
		name:        "org_attach",
		description: "Become a role's incarnation (refused while another holds it). Returns the incarnation id in the receipt's holder field; present it on later writes. Optionally declare next_due (e.g. \"4h\") — your own liveness deadline.",
		schema:      json.RawMessage(roleOnly),
		args:        roleVerb("attach"),
	},
	{
		name:        "org_release",
		description: "Hand the role back cleanly (no active claim may be open).",
		schema:      json.RawMessage(roleOnly),
		args:        roleVerb("release"),
	},
	{
		name:        "org_assign",
		description: "Add a work item to the role's held set. Requires work (a URI with a scheme, e.g. github:owner/repo#88 or dossier:proj/phase/task) and pin or digest to fix the item's content, so a rewritten ticket reads as drift.",
		schema:      json.RawMessage(`{"type":"object","properties":{"role":{"type":"string"},"work":{"type":"string"},"pin":{"type":"string"},"digest":{"type":"string"},"incarnation":{"type":"string"}},"required":["role","work"]}`),
		args: func(raw json.RawMessage) ([]string, error) {
			a, err := decode(raw)
			if err != nil {
				return nil, err
			}
			if err := need([2]string{"role", a.Role}, [2]string{"work", a.Work}); err != nil {
				return nil, err
			}
			args := append(base("assign", a), "-work", a.Work)
			if a.Digest != "" {
				args = append(args, "-digest", a.Digest)
			}
			if a.Pin != "" {
				args = append(args, "-pin", a.Pin)
			}
			return args, nil
		},
	},
	{
		name:        "org_unassign",
		description: "Drop a held work item (refused while it is the active claim).",
		schema:      json.RawMessage(roleWork),
		args:        workVerb("unassign"),
	},
	{
		name:        "org_claim",
		description: "Make one held work item the active claim. Refused if a predecessor's dangling claim or an open effect is unresolved — discharge those first.",
		schema:      json.RawMessage(roleWork),
		args:        workVerb("claim"),
	},
	{
		name:        "org_yield",
		description: "End the active (or dangling) claim with the work unfinished and still held. Body is where you stopped — the next incarnation reads it.",
		schema:      json.RawMessage(roleWork),
		args:        workVerb("yield"),
	},
	{
		name:        "org_complete",
		description: "End the active (or dangling) claim asserting the work is done; the item leaves the held set.",
		schema:      json.RawMessage(roleWork),
		args:        workVerb("complete"),
	},
	{
		name:        "org_abandon",
		description: "End the active (or dangling) claim and drop the work, explicitly and on the record.",
		schema:      json.RawMessage(roleWork),
		args:        workVerb("abandon"),
	},
	{
		name:        "org_intent",
		description: "Record that an effect (a merge, a deploy, a send) is about to be attempted against this role. One outstanding effect at a time; resolve it before claiming further.",
		schema:      json.RawMessage(`{"type":"object","properties":{"role":{"type":"string"},"effect":{"type":"string"},"incarnation":{"type":"string"}},"required":["role","effect"]}`),
		args: func(raw json.RawMessage) ([]string, error) {
			a, err := decode(raw)
			if err != nil {
				return nil, err
			}
			if err := need([2]string{"role", a.Role}, [2]string{"effect", a.Effect}); err != nil {
				return nil, err
			}
			return append(base("intent", a), "-effect", a.Effect), nil
		},
	},
	{
		name:        "org_resolve",
		description: "Close an open effect (effect=<id>) or an open escalation (target=<record digest>), with an optional narrative body.",
		schema:      json.RawMessage(`{"type":"object","properties":{"role":{"type":"string"},"effect":{"type":"string"},"target":{"type":"string"},"body":{"type":"string"},"incarnation":{"type":"string"}},"required":["role"]}`),
		args: func(raw json.RawMessage) ([]string, error) {
			a, err := decode(raw)
			if err != nil {
				return nil, err
			}
			if err := need([2]string{"role", a.Role}); err != nil {
				return nil, err
			}
			args := base("resolve", a)
			if a.Effect != "" {
				args = append(args, "-effect", a.Effect)
			}
			if a.Target != "" {
				args = append(args, "-target", a.Target)
			}
			if a.Body != "" {
				args = append(args, "-body", a.Body)
			}
			return args, nil
		},
	},
	{
		name:        "org_escalate",
		description: "Open a question for a human against this role; it stays an obligation until a resolution closes it.",
		schema:      json.RawMessage(roleBody),
		args:        bodyVerb("escalate"),
	},
	{
		name:        "org_note",
		description: "Free narrative on the chain — findings, decisions, where things stand.",
		schema:      json.RawMessage(roleBody),
		args:        bodyVerb("note"),
	},
	{
		name:        "org_checkpoint",
		description: "The distilled state of this session's work on the role: what happened, what is open, what the next incarnation must know. Write one before stopping.",
		schema:      json.RawMessage(roleBody),
		args:        bodyVerb("checkpoint"),
	},
	{
		name:        "org_verify",
		description: "Refold the role's chain and verify it: record count, phase, tip.",
		schema:      json.RawMessage(`{"type":"object","properties":{"role":{"type":"string"}},"required":["role"]}`),
		args: func(raw json.RawMessage) ([]string, error) {
			a, err := decode(raw)
			if err != nil {
				return nil, err
			}
			if err := need([2]string{"role", a.Role}); err != nil {
				return nil, err
			}
			return []string{"verify", "-role", a.Role, "-json"}, nil
		},
	},
}

func lookupVerb(name string) (verb, bool) {
	for _, v := range verbs {
		if v.name == name {
			return v, true
		}
	}
	return verb{}, false
}

// toolsListResult renders the registry as an MCP tools/list result.
func toolsListResult() map[string]any {
	tools := make([]map[string]any, 0, len(verbs))
	for _, v := range verbs {
		tools = append(tools, map[string]any{
			"name":        v.name,
			"description": v.description,
			"inputSchema": v.schema,
		})
	}
	return map[string]any{"tools": tools}
}
