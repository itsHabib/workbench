// Package provider supplies the installed provider transport, never scheduling.
package provider

import (
	_ "embed"
	"encoding/json"
	"os/exec"
	"strings"
)

//go:embed runtime.mjs
var bridge string

// Command runs one durable provider turn. The request goes over stdin, not argv.
func Command(request map[string]any) (*exec.Cmd, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("node", "--input-type=module", "-e", bridge)
	cmd.Stdin = strings.NewReader(string(raw))
	return cmd, nil
}
