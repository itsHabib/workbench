package fleet

import (
	"os"
	"strings"
	"testing"
)

func TestBoardFreshnessEvidence(t *testing.T) {
	old := State
	State = t.TempDir()
	t.Cleanup(func() { State = old })
	for _, tc := range []struct {
		name      string
		heartbeat Rec
		want      string
	}{
		{"missing", nil, "freshness unknown"},
		{"empty", Rec{}, "freshness unknown"},
		{"zero", Rec{"at": 0, "interval": 60}, "freshness unknown"},
		{"stale", Rec{"at": Now() - 300, "interval": 60}, "no recent watcher heartbeat"},
		{"fresh", Rec{"at": Now(), "interval": 60}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			State = t.TempDir()
			writeAttentionFixtures(t)
			if tc.heartbeat != nil {
				if err := WriteJSON(Path("watch", "heartbeat.json"), tc.heartbeat); err != nil {
					t.Fatal(err)
				}
			}
			lines := strings.Join(BoardLines(0), "\n")
			if !strings.Contains(lines, "2 need a decision") {
				t.Fatalf("lost decision rows: %s", lines)
			}
			if strings.Contains(lines, "--once") {
				t.Fatalf("suggested unlocked recovery: %s", lines)
			}
			if tc.want == "" {
				if strings.Contains(lines, "fleet watch") {
					t.Fatalf("fresh heartbeat prompted recovery: %s", lines)
				}
				return
			}
			if !strings.Contains(lines, tc.want) || !strings.Contains(lines, "fleet watch") {
				t.Fatalf("missing evidence/recovery: %s", lines)
			}
		})
	}
}

func writeAttentionFixtures(t *testing.T) {
	t.Helper()
	for name, rows := range map[string][]Rec{
		"board.json": {{"state": "dead-holding-work", "role": "author:fixture"}},
		"work.json":  {{"state": "failed", "branch": "topic"}},
	} {
		if err := WriteJSON(Path("watch", name), rows); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(Path("watch", "heartbeat.json")); !os.IsNotExist(err) {
		t.Fatalf("heartbeat fixture not isolated: %v", err)
	}

}
