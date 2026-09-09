package verbs

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const maxHookConfig = 4 << 20

var assignmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
var pythonName = regexp.MustCompile(`^python(?:[0-9]+(?:\.[0-9]+)*)?(?:\.exe)?$`)

type hookDescription struct {
	Event       string   `json:"event"`
	Group       int      `json:"group"`
	Index       int      `json:"index"`
	Kind        string   `json:"kind"`
	Harness     string   `json:"harness,omitempty"`
	Shadow      bool     `json:"shadow"`
	Environment []string `json:"environment_names"`
	CommandHash string   `json:"command_sha256"`
}

// Inspection reads only explicit config bytes. It never evaluates shell text,
// resolves variables, probes executables, or enters Fleet's migration path.
func cmdInspectHooks(args []string) error {
	if len(args) != 2 || args[0] != "--config" {
		return refuse("usage: fleet inspect-hooks --config <harness-json>")
	}
	f, err := os.Open(args[1])
	if err != nil {
		return fmt.Errorf("inspect hooks: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxHookConfig+1))
	if err != nil {
		return fmt.Errorf("inspect hooks: %w", err)
	}
	if len(raw) > maxHookConfig {
		return refuse("inspect hooks: config exceeds 4 MiB")
	}
	rows, err := inspectHookConfig(raw)
	if err != nil {
		return err
	}
	say("%s", jsonIndent(map[string]any{"schema": "fleet-hook-inspection-v1", "source": args[1], "source_sha256": fmt.Sprintf("%x", sha256.Sum256(raw)), "runtime": "unverified", "effective_state_roots": "unknown", "hooks": rows}))
	return nil
}

func inspectHookConfig(raw []byte) ([]hookDescription, error) {
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, refuse("inspect hooks: invalid hook configuration JSON or shape")
	}
	if config.Hooks == nil {
		return nil, refuse("inspect hooks: missing hooks object")
	}
	events := make([]string, 0, len(config.Hooks))
	for event := range config.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	rows := []hookDescription{}
	for _, event := range events {
		for gi, group := range config.Hooks[event] {
			if group.Hooks == nil {
				return nil, refuse("inspect hooks: group missing hooks array")
			}
			rows = append(rows, describeHookGroup(event, gi, group.Hooks)...)
		}
	}
	return rows, nil
}

func describeHookGroup(event string, gi int, hooks []struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}) []hookDescription {
	rows := []hookDescription{}
	for hi, hook := range hooks {
		row := describeHookCommand(hook.Command)
		row.Event, row.Group, row.Index = event, gi, hi
		if hook.Type != "command" {
			row.Kind = "unsupported-hook-type"
		}
		rows = append(rows, row)
	}
	return rows
}

func describeHookCommand(command string) hookDescription {
	row := hookDescription{Kind: "unknown", Environment: []string{}, CommandHash: fmt.Sprintf("%x", sha256.Sum256([]byte(command)))}
	words, ok := hookWords(command)
	if !ok {
		return row
	}
	for len(words) > 0 && assignmentName.MatchString(words[0]) {
		row.Environment = append(row.Environment, strings.SplitN(words[0], "=", 2)[0])
		words = words[1:]
	}
	if len(words) == 0 {
		return row
	}
	exe := hookBase(words[0])
	if pythonName.MatchString(exe) && len(words) == 2 {
		switch hookBase(words[1]) {
		case "hook.py":
			row.Kind, row.Harness = "legacy-python", "claude"
		case "codex-adapter.py":
			row.Kind, row.Harness = "legacy-python", "codex"
		}
		return row
	}
	if exe != "fleet" && exe != "fleet.exe" {
		return row
	}
	if len(words) != 3 && len(words) != 4 {
		return row
	}
	if words[1] != "hook" || (words[2] != "claude" && words[2] != "codex") {
		return row
	}
	if len(words) == 4 && words[3] != "--shadow" {
		return row
	}
	row.Kind, row.Harness, row.Shadow = "native-fleet", words[2], len(words) == 4
	return row
}

func hookBase(s string) string {
	parts := strings.Split(strings.ReplaceAll(s, `\`, "/"), "/")
	return strings.ToLower(parts[len(parts)-1])
}

// A conservative literal-token recognizer, not a shell parser. Backslashes stay
// literal for Windows paths; escapes and shell control syntax are unsupported.
func hookWords(command string) ([]string, bool) {
	if strings.Contains(command, `\"`) || strings.Contains(command, `\'`) {
		return nil, false
	}
	if strings.ContainsAny(command, "\n\r;&|<>`()") || strings.Contains(command, "${") || strings.Contains(command, "$'") {
		return nil, false
	}
	words := []string{}
	var word strings.Builder
	var quote rune
	for _, c := range command {
		if c == '"' || c == '\'' {
			if quote == 0 {
				quote = c
				continue
			}
			if quote == c {
				quote = 0
				continue
			}
		}
		if (c == ' ' || c == '\t') && quote == 0 {
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
			continue
		}
		word.WriteRune(c)
	}
	if quote != 0 {
		return nil, false
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words, true
}
