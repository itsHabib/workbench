package policy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/itsHabib/workbench/contracts/automode"
)

var supportedEnvelopes = map[string]struct{}{
	"shell": {},
	"local": {},
	"mcp":   {},
	"code":  {},
}

func normalize(envelope Envelope) (normalized, error) {
	kind := safeName(envelope.Kind)
	_, ok := supportedEnvelopes[kind]
	if !ok {
		return normalized{}, errUnsupported
	}
	out, err := normalizeKnown(kind, envelope)
	if err != nil {
		return normalized{}, err
	}
	out.envelope = kind
	return out, nil
}

func normalizeKnown(kind string, envelope Envelope) (normalized, error) {
	switch kind {
	case "shell":
		return normalizeShell(envelope)
	case "local":
		return normalizeLocal(envelope)
	case "mcp":
		return normalizeMCP(envelope)
	case "code":
		return normalizeCode(envelope)
	}
	return normalized{}, errUnsupported
}

func normalizeShell(envelope Envelope) (normalized, error) {
	shell := strings.ToLower(strings.TrimSpace(envelope.Shell))
	if shell == "" || shell == "direct" {
		return classifyCommand(envelope.Command)
	}
	switch shell {
	case "bash", "sh":
		command, err := unwrapPOSIX(envelope.Command)
		if err != nil {
			return normalized{}, err
		}
		return classifyCommand(command)
	case "powershell", "pwsh":
		command, err := unwrapPowerShell(envelope.Command)
		if err != nil {
			return normalized{}, err
		}
		return classifyCommand(command)
	case "cmd":
		command, err := unwrapCMD(envelope.Command)
		if err != nil {
			return normalized{}, err
		}
		return classifyCommand(command)
	}
	return normalized{}, errUnsupported
}

func normalizeLocal(envelope Envelope) (normalized, error) {
	tool := strings.TrimPrefix(envelope.Tool, "functions.")
	switch tool {
	case "shell_command":
		var args struct {
			Command string `json:"command"`
			Shell   string `json:"shell"`
		}
		if err := json.Unmarshal(envelope.Arguments, &args); err != nil || args.Command == "" {
			return normalized{}, errUnsupported
		}
		if args.Shell == "" {
			args.Shell = "direct"
		}
		return normalizeShell(Envelope{Kind: "shell", Shell: args.Shell, Command: args.Command})
	case "read_file", "view_image":
		return normalized{operation: "read", parameters: []automode.NamedValue{{Name: "tool", Value: tool}}}, nil
	}
	return normalized{}, errUnsupported
}

var readOnlyMCPTools = map[string]struct{}{
	"mcp__dossier__overview":             {},
	"mcp__dossier__task_get":             {},
	"mcp__github__get_pull_request":      {},
	"mcp__github__get_pull_request_diff": {},
	"mcp__workbench__driver_rollup":      {},
	"mcp__workbench__driver_state":       {},
	"mcp__workbench__driver_verify":      {},
}

func normalizeMCP(envelope Envelope) (normalized, error) {
	if _, ok := readOnlyMCPTools[envelope.Tool]; ok {
		return normalized{operation: "read", parameters: []automode.NamedValue{{Name: "tool", Value: envelope.Tool}}}, nil
	}
	if envelope.Tool == "shell_command" || envelope.Tool == "mcp__local__shell_command" {
		return normalizeLocal(Envelope{Kind: "local", Tool: "shell_command", Arguments: envelope.Arguments})
	}
	return normalized{}, errUnsupported
}

func normalizeCode(envelope Envelope) (normalized, error) {
	tool, args, ok := staticToolCall(envelope.Code)
	if !ok {
		return normalized{}, errUnsupported
	}
	kind := "mcp"
	localTool := strings.TrimPrefix(tool, "functions.")
	if localTool == "shell_command" || localTool == "read_file" || localTool == "view_image" {
		kind = "local"
	}
	return normalize(Envelope{Kind: kind, Tool: tool, Arguments: args})
}

func staticToolCall(code string) (string, json.RawMessage, bool) {
	code = strings.TrimSpace(code)
	for _, prefix := range []string{"return await ", "await ", "return "} {
		code = strings.TrimPrefix(code, prefix)
	}
	code = strings.TrimSuffix(strings.TrimSpace(code), ";")
	if !strings.HasPrefix(code, "tools.") || !strings.HasSuffix(code, ")") {
		return "", nil, false
	}
	open := strings.IndexByte(code, '(')
	if open < len("tools.")+1 {
		return "", nil, false
	}
	tool := code[len("tools."):open]
	if strings.ContainsAny(tool, " \t\r\n(){}[];") {
		return "", nil, false
	}
	raw := strings.TrimSpace(code[open+1 : len(code)-1])
	if raw == "" {
		raw = "{}"
	}
	var check map[string]any
	if err := json.Unmarshal([]byte(raw), &check); err != nil {
		return "", nil, false
	}
	return tool, json.RawMessage(raw), true
}

func unwrapPOSIX(command string) (string, error) {
	words, err := splitWords(command)
	if err != nil {
		return "", err
	}
	if len(words) < 3 || (words[0] != "bash" && words[0] != "sh") {
		return command, nil
	}
	if words[1] != "-c" && words[1] != "-lc" {
		return "", errUnsupported
	}
	if len(words) != 3 {
		return "", errUnsupported
	}
	return words[2], nil
}

func unwrapPowerShell(command string) (string, error) {
	words, err := splitWords(command)
	if err != nil {
		return "", err
	}
	if len(words) >= 3 && isPowerShell(words[0]) {
		switch strings.ToLower(words[1]) {
		case "-command", "-c":
			if len(words) != 3 {
				return "", errUnsupported
			}
			command = words[2]
		case "-encodedcommand", "-enc":
			if len(words) != 3 {
				return "", errUnsupported
			}
			command, err = decodePowerShell(words[2])
			if err != nil {
				return "", err
			}
		default:
			return "", errUnsupported
		}
	}
	trimmed := strings.TrimSpace(command)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "invoke-expression ") || strings.HasPrefix(lower, "iex ") ||
		strings.HasPrefix(lower, "start-process ") {
		return "", errUnsupported
	}
	if strings.HasPrefix(trimmed, "& ") {
		return strings.TrimSpace(trimmed[2:]), nil
	}
	return command, nil
}

func isPowerShell(word string) bool {
	word = strings.ToLower(word)
	return word == "powershell" || word == "powershell.exe" || word == "pwsh" || word == "pwsh.exe"
}

func decodePowerShell(value string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data)%2 != 0 {
		return "", errUnsupported
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
	}
	decoded := string(utf16.Decode(units))
	if !utf8.ValidString(decoded) {
		return "", errUnsupported
	}
	return decoded, nil
}

func unwrapCMD(command string) (string, error) {
	words, err := splitWords(command)
	if err != nil {
		return "", err
	}
	if len(words) < 3 || (strings.ToLower(words[0]) != "cmd" && strings.ToLower(words[0]) != "cmd.exe") {
		return command, nil
	}
	if strings.ToLower(words[1]) != "/c" || len(words) != 3 {
		return "", errUnsupported
	}
	return words[2], nil
}

func splitWords(command string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote rune
	escaped := false
	have := false
	flush := func() {
		if have {
			words = append(words, word.String())
			word.Reset()
			have = false
		}
	}
	for _, r := range command {
		if escaped {
			word.WriteRune(r)
			have = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			word.WriteRune(r)
			have = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			have = true
			continue
		}
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			flush()
			continue
		}
		word.WriteRune(r)
		have = true
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated command quoting")
	}
	flush()
	return words, nil
}

func parseNumber(value string) (int, bool) {
	n, err := strconv.Atoi(value)
	return n, err == nil && n > 0
}
