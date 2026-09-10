package verbs

import (
	"regexp"
	"testing"
)

func TestGeneratedCodexHooksSubscribeToWrites(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	_, data, err := codexHooks("fleet hook codex")
	if err != nil {
		t.Fatal(err)
	}
	hooks := data["hooks"].(map[string]any)
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		groups := hooks[event].([]any)
		group := groups[len(groups)-1].(map[string]any)
		matcher := regexp.MustCompile(group["matcher"].(string))
		for _, tool := range []string{"Bash", "Edit", "Write", "MultiEdit", "NotebookEdit", "apply_patch"} {
			if !matcher.MatchString(tool) {
				t.Errorf("%s drops %s", event, tool)
			}
		}
		if matcher.MatchString("Read") {
			t.Error("read subscribed as write")
		}
	}
}

func TestClaudeWriteHooksPreserveOtherHandlersOnRebind(t *testing.T) {
	other := map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": "other"}}}
	data := map[string]any{"hooks": map[string]any{"PostToolUse": []any{other}}}
	for range 2 {
		if err := claudeWriteHooks(data, "fixture", "fleet hook"); err != nil {
			t.Fatal(err)
		}
	}
	groups := data["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(groups) != 2 {
		t.Fatalf("lost or duplicated handlers: %v", groups)
	}
	matcher := regexp.MustCompile(groups[1].(map[string]any)["matcher"].(string))
	for _, tool := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
		if !matcher.MatchString(tool) {
			t.Errorf("drops %s", tool)
		}
	}
	if matcher.MatchString("Bash") {
		t.Fatal("duplicates global Bash hook")
	}
	if groups[0].(map[string]any)["matcher"] != "Read" {
		t.Fatal("unrelated hook changed")
	}
}
