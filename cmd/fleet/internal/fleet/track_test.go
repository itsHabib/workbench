package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWaitAnchorsAreSessionScopedReadOnlyAndBounded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLEET_TRACK_DIR", dir)
	now := time.Date(2026, 9, 6, 12, 47, 0, 0, time.UTC)
	rows := map[string]Rec{
		"oldest": {"session": "a", "label": "receipt on #284", "started_at": "2026-09-06T12:00:00+00:00", "last_tick_at": "2026-09-06T12:37:00+00:00"},
		"second": {"session": "a", "label": strings.Repeat("界", 200), "started_at": "2026-09-06T12:10:00+00:00"},
		"third":  {"session": "a", "label": "line\nwith\x00controls", "started_at": "2026-09-06T12:20:00+00:00"},
		"fourth": {"session": "a", "label": "omitted", "started_at": "2026-09-06T12:30:00+00:00"},
		"other":  {"session": "b", "label": "private to b", "started_at": "2026-09-06T12:00:00+00:00"},
		"legacy": {"label": "unbound", "started_at": "2026-09-06T12:00:00+00:00"},
		"bad":    {"session": "a", "label": "invalid date", "started_at": "not a date"},
		"future": {"session": "a", "label": "future", "started_at": "2027-09-06T12:00:00+00:00"},
	}
	before := map[string]string{}
	for name, r := range rows {
		p := filepath.Join(dir, name+".json")
		b := string(DumpJSON(r))
		if err := os.WriteFile(p, []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
		before[p] = b
	}
	if err := os.WriteFile(filepath.Join(dir, "torn.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := waitLines("a", now)
	if len(lines) != 4 || lines[0] != `[fleet] waiting 47m on: "receipt on #284"` {
		t.Fatalf("unexpected waits: %v", lines)
	}
	if !strings.Contains(lines[3], "+1 more") || !strings.Contains(lines[2], `"line with controls"`) {
		t.Fatalf("missing bounded context: %v", lines)
	}
	text := strings.Join(lines, "\n")
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 700 {
		t.Fatalf("invalid or unbounded context: %q", text)
	}
	for _, bad := range []string{"private to b", "unbound", "invalid date", "future", "omitted"} {
		if strings.Contains(text, bad) {
			t.Errorf("unexpected wait %q in %q", bad, text)
		}
	}
	for p, want := range before {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("reader changed anchor %s: %v", p, err)
		}
	}
}

func TestAbsentWaitStoreAndUnboundSessionsAreQuiet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "absent")
	t.Setenv("FLEET_TRACK_DIR", p)
	for _, sid := range []string{"", "unknown", "a"} {
		if lines := waitLines(sid, time.Now()); len(lines) != 0 {
			t.Fatalf("unexpected context: %v", lines)
		}
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("reader created store: %v", err)
	}
	if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if lines := waitLines("a", time.Now()); len(lines) != 0 {
		t.Fatalf("unavailable store generated context: %v", lines)
	}
}

func TestWaitContextCapIncludesEscaping(t *testing.T) {
	for name, char := range map[string]string{"backslash": `\`, "quote": `"`, "non-graphic": "\u200b"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("FLEET_TRACK_DIR", dir)
			now := time.Now()
			for i := range 4 {
				b := DumpJSON(Rec{"session": "a", "label": strings.Repeat(char, 160), "started_at": now.Add(-time.Hour).Format(time.RFC3339Nano)})
				if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".json"), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lines := waitLines("a", now)
			text := strings.Join(lines, "\n")
			if utf8.RuneCountInString(text) > 700 {
				t.Fatalf("quoted context is %d runes", utf8.RuneCountInString(text))
			}
			for _, line := range lines[:3] {
				_, label, _ := strings.Cut(line, " on: ")
				if _, err := strconv.Unquote(label); err != nil {
					t.Fatalf("truncated quote: %s: %v", label, err)
				}
			}
		})
	}
}
