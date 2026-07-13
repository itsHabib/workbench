package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than cap", "abc", 5, "abc"},
		{"equal to cap", "abcde", 5, "abcde"},
		{"longer than cap", "abcdefgh", 5, "abcde"},
		{"zero cap", "abc", 0, ""},
		{"multi-byte rune not split", "ab日本語", 5, "ab日"}, // 日 ends at byte 5
		{"cap lands mid-rune backs up", "ab日本語", 6, "ab日"},
		{"emoji dropped whole", "a😀b", 3, "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncate(c.in, c.n)
			if got != c.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
			}
		})
	}
}

func TestUsageLogPath(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		t.Setenv("LOCAL_USAGE_LOG", "/custom/path.jsonl")
		t.Setenv("XDG_STATE_HOME", "/xdg")
		if got := usageLogPath(); got != "/custom/path.jsonl" {
			t.Fatalf("usageLogPath() = %q, want the LOCAL_USAGE_LOG value", got)
		}
	})

	t.Run("falls back to XDG state dir", func(t *testing.T) {
		t.Setenv("LOCAL_USAGE_LOG", "")
		t.Setenv("XDG_STATE_HOME", "/xdg")
		want := filepath.Join("/xdg", "local", "usage.jsonl")
		if got := usageLogPath(); got != want {
			t.Fatalf("usageLogPath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to home when no env set", func(t *testing.T) {
		t.Setenv("LOCAL_USAGE_LOG", "")
		t.Setenv("XDG_STATE_HOME", "")
		got := usageLogPath()
		if !strings.HasSuffix(filepath.ToSlash(got), ".local/state/local/usage.jsonl") {
			t.Fatalf("usageLogPath() = %q, want a path ending in .local/state/local/usage.jsonl", got)
		}
	})
}

func TestLogUsageAppendsRecord(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "usage.jsonl")
	t.Setenv("LOCAL_USAGE_LOG", logPath)

	logUsage("classify these lines", "local", false)
	logUsage("extract the ids", "cloud", true)

	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var recs []usageRecord
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var r usageRecord
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			t.Fatalf("unmarshal line %q: %v", scan.Text(), err)
		}
		recs = append(recs, r)
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("scan log: %v", err)
	}

	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (one appended per call)", len(recs))
	}
	if recs[0].Prompt != "classify these lines" || recs[0].Source != "local" || recs[0].Flagged {
		t.Errorf("record 0 = %+v, want the first call's fields", recs[0])
	}
	if recs[1].Source != "cloud" || !recs[1].Flagged {
		t.Errorf("record 1 = %+v, want source=cloud flagged=true", recs[1])
	}
	if recs[0].TS == "" || recs[0].CWD == "" {
		t.Errorf("record 0 missing ts/cwd: %+v", recs[0])
	}
}

func TestLogUsageTruncatesPrompt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "usage.jsonl")
	t.Setenv("LOCAL_USAGE_LOG", logPath)

	long := strings.Repeat("x", 500)
	logUsage(long, "local", false)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var r usageRecord
	if err := json.Unmarshal(data[:len(data)-1], &r); err != nil { // trim trailing newline
		t.Fatalf("unmarshal: %v", err)
	}
	if len(r.Prompt) != 120 {
		t.Fatalf("prompt length = %d, want 120 (truncated)", len(r.Prompt))
	}
}
