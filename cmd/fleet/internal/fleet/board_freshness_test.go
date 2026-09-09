package fleet

import (
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
			if tc.heartbeat != nil {
				if err := WriteJSON(Path("watch", "heartbeat.json"), tc.heartbeat); err != nil {
					t.Fatal(err)
				}
			}
			lines := strings.Join(BoardLines(0), "\n")
			if tc.want == "" {
				if strings.Contains(lines, "fleet watch") {
					t.Fatalf("fresh heartbeat prompted recovery: %s", lines)
				}
				return
			}
			if !strings.Contains(lines, tc.want) || !strings.Contains(lines, "fleet watch --once") {
				t.Fatalf("missing evidence/recovery: %s", lines)
			}
		})
	}
}
