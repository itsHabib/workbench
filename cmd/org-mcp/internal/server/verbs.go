package server

import (
	"encoding/json"
	"fmt"
)

// MCP translates the three role-card operations; Org owns the registry.
type verb struct {
	name, description string
	schema            json.RawMessage
	args              func(json.RawMessage) ([]string, error)
}

type callArgs struct {
	Role     string  `json:"role"`
	File     string  `json:"file"`
	Parent   *string `json:"parent"`
	MaxBytes int     `json:"max_bytes"`
}

func cardArgs(command string) func(json.RawMessage) ([]string, error) {
	return func(raw json.RawMessage) ([]string, error) {
		var a callArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		if a.Role == "" {
			return nil, fmt.Errorf("role is required")
		}
		args := []string{command, "-role", a.Role, "-json"}
		if command == "charter" {
			if a.File == "" {
				return nil, fmt.Errorf("file is required")
			}
			args = append(args, "-file", a.File)
			if a.Parent != nil {
				args = append(args, "-parent", *a.Parent)
			}
			return args, nil
		}
		if a.MaxBytes < 0 {
			return nil, fmt.Errorf("max_bytes must be nonnegative")
		}
		if a.MaxBytes > 0 {
			args = append(args, "-max-bytes", itoa(a.MaxBytes))
		}
		return args, nil
	}
}

var verbs = []verb{
	{
		name:        "org_charter",
		description: "Register or update a role's Markdown card and optional parent reference. Edit the prose file to change the role. This is configuration, not an authority grant or work assignment.",
		schema:      json.RawMessage(`{"type":"object","properties":{"role":{"type":"string"},"file":{"type":"string","description":"Path to the Markdown role card"},"parent":{"type":"string","description":"Optional parent reference; empty clears it"}},"required":["role","file"]}`),
		args:        cardArgs("charter"),
	},
	{
		name:        "org_boot",
		description: "Read the current prose for a registered role. No attach, claim or checkpoint is needed. The card and parent describe responsibilities, not runtime state or messaging permissions.",
		schema:      json.RawMessage(`{"type":"object","properties":{"role":{"type":"string"},"max_bytes":{"type":"integer","minimum":0}},"required":["role"]}`),
		args:        cardArgs("boot"),
	},
	{
		name:        "org_status",
		description: "List registered role cards and optional parents for the configured tenant. This directory does not track sessions, work, mail or journal history.",
		schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		args:        func(json.RawMessage) ([]string, error) { return []string{"status", "-json"}, nil },
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

func toolsListResult() map[string]any {
	tools := make([]map[string]any, 0, len(verbs))
	for _, v := range verbs {
		tools = append(tools, map[string]any{"name": v.name, "description": v.description, "inputSchema": v.schema})
	}
	return map[string]any{"tools": tools}
}
