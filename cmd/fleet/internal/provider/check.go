package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Check discovers Codex configuration without creating a thread or a model turn.
// The existing bridge owns the app-server lifecycle; its files are temporary.
func Check(cwd string) (map[string]any, error) {
	dir, err := os.MkdirTemp("", "fleet-check-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cmd, err := Command(filepath.Join(dir, "request.json"), map[string]any{
		"provider": "codex", "check": true, "cwd": cwd, "attempt": filepath.Base(dir),
		"state_file": filepath.Join(dir, "state.json"), "cancel_file": filepath.Join(dir, "cancel"),
		"output": filepath.Join(dir, "output"),
	})
	if err != nil {
		return nil, err
	}
	cmd.Dir = cwd
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("codex setup inspection failed; check provider compatibility and availability: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode setup inspection: %w", err)
	}
	if result["type"] != "setup" {
		return nil, fmt.Errorf("codex returned no setup inspection")
	}
	return result, nil
}
