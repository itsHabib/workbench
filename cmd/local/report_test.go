package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseUsageLedger_empty(t *testing.T) {
	rep := parseUsageLedger(strings.NewReader(""))
	if rep.TotalInvocations != 0 || rep.SkippedLines != 0 {
		t.Fatalf("empty ledger = %+v, want zero report", rep)
	}
	if rep.Repos == nil || rep.PerDay == nil {
		t.Fatalf("empty ledger maps = %+v, want non-nil empty maps", rep)
	}
}

func TestParseUsageLedger_mixedSourcesAndRepos(t *testing.T) {
	ledger := strings.Join([]string{
		`{"ts":"2026-07-10T10:00:00Z","cwd":"/home/op/workbench","prompt":"a","source":"local","flagged":false}`,
		`{"ts":"2026-07-10T15:00:00Z","cwd":"/home/op/workbench","prompt":"b","source":"cloud","flagged":true}`,
		`{"ts":"2026-07-11T09:00:00Z","cwd":"/home/op/flare","prompt":"c","source":"local","flagged":false}`,
	}, "\n") + "\n"

	rep := parseUsageLedger(strings.NewReader(ledger))
	if rep.TotalInvocations != 3 {
		t.Fatalf("total = %d, want 3", rep.TotalInvocations)
	}
	if rep.FirstTS != "2026-07-10T10:00:00Z" || rep.LastTS != "2026-07-11T09:00:00Z" {
		t.Fatalf("span = %q → %q, want first/last ts from fixture", rep.FirstTS, rep.LastTS)
	}
	if rep.Repos["/home/op/workbench"] != 2 || rep.Repos["/home/op/flare"] != 1 {
		t.Fatalf("repos = %+v, want 2 workbench and 1 flare", rep.Repos)
	}
	if rep.PerDay["2026-07-10"] != 2 || rep.PerDay["2026-07-11"] != 1 {
		t.Fatalf("per_day = %+v, want 2 on 07-10 and 1 on 07-11", rep.PerDay)
	}
	if rep.SourceLocal != 2 || rep.SourceCloud != 1 {
		t.Fatalf("source split = local %d cloud %d, want 2/1", rep.SourceLocal, rep.SourceCloud)
	}
	if rep.FlaggedCount != 1 {
		t.Fatalf("flagged = %d, want 1", rep.FlaggedCount)
	}
	wantRate := 1.0 / 3.0
	if rep.FlaggedRate != wantRate {
		t.Fatalf("flagged rate = %v, want %v", rep.FlaggedRate, wantRate)
	}
}

func TestParseUsageLedger_malformedLines(t *testing.T) {
	ledger := strings.Join([]string{
		`not json`,
		`{"ts":"2026-07-10T10:00:00Z","cwd":"/home/op/workbench","source":"local","flagged":false}`,
		`{"cwd":"/home/op/workbench","source":"cloud","flagged":false}`,
		`{"ts":"2026-07-11T09:00:00Z","cwd":"/home/op/flare","source":"local","flagged":true}`,
	}, "\n") + "\n"

	rep := parseUsageLedger(strings.NewReader(ledger))
	if rep.SkippedLines != 2 {
		t.Fatalf("skipped = %d, want 2 malformed lines", rep.SkippedLines)
	}
	if rep.TotalInvocations != 2 {
		t.Fatalf("total = %d, want 2 valid records", rep.TotalInvocations)
	}
	if rep.SourceLocal != 2 || rep.SourceCloud != 0 || rep.FlaggedCount != 1 {
		t.Fatalf("stats = %+v, want two local (one flagged) and no cloud (missing-ts line skipped)", rep)
	}
}

func TestUsageReportFromPath_missingAndEmpty(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.jsonl")
		rep, err := usageReportFromPath(path)
		if err != nil {
			t.Fatalf("missing log err = %v, want nil", err)
		}
		if rep.TotalInvocations != 0 {
			t.Fatalf("missing log = %+v, want zero report", rep)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		rep, err := usageReportFromPath("")
		if err != nil {
			t.Fatalf("empty path err = %v, want nil", err)
		}
		if rep.TotalInvocations != 0 {
			t.Fatalf("empty path = %+v, want zero report", rep)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "usage.jsonl")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("write empty log: %v", err)
		}
		rep, err := usageReportFromPath(path)
		if err != nil {
			t.Fatalf("empty file err = %v, want nil", err)
		}
		if rep.TotalInvocations != 0 {
			t.Fatalf("empty file = %+v, want zero report", rep)
		}
	})

	t.Run("unreadable file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("mode 0o000 does not block opens on windows")
		}
		path := filepath.Join(t.TempDir(), "usage.jsonl")
		if err := os.WriteFile(path, []byte("{}\n"), 0o000); err != nil {
			t.Fatalf("write unreadable log: %v", err)
		}
		if _, err := usageReportFromPath(path); err == nil {
			t.Fatal("unreadable ledger = nil error, want surfaced open failure")
		}
	})
}

func TestParseUsageLedger_mixedOffsetSpan(t *testing.T) {
	// The -05:00 record is 2026-07-11T04:00:00Z — the later instant even though
	// it sorts earlier as a string.
	ledger := strings.Join([]string{
		`{"ts":"2026-07-10T23:00:00-05:00","cwd":"/repos/a","source":"local"}`,
		`{"ts":"2026-07-11T01:00:00Z","cwd":"/repos/a","source":"local"}`,
	}, "\n") + "\n"

	rep := parseUsageLedger(strings.NewReader(ledger))
	if rep.FirstTS != "2026-07-11T01:00:00Z" {
		t.Fatalf("first = %q, want the earlier instant, not the earlier string", rep.FirstTS)
	}
	if rep.LastTS != "2026-07-10T23:00:00-05:00" {
		t.Fatalf("last = %q, want the offset record recognized as later", rep.LastTS)
	}
}

func TestRunUsage_parseError(t *testing.T) {
	var out bytes.Buffer
	if code := runUsage([]string{"-definitely-not-a-flag"}, &out); code != 2 {
		t.Fatalf("runUsage bad flag exit = %d, want 2", code)
	}
}

func TestRunUsage_textAndJSON(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "usage.jsonl")
	ledger := strings.Join([]string{
		`{"ts":"2026-07-10T10:00:00Z","cwd":"/repos/workbench","prompt":"a","source":"local","flagged":false}`,
		`{"ts":"2026-07-10T12:00:00Z","cwd":"/repos/workbench","prompt":"b","source":"cloud","flagged":true}`,
		`{"ts":"2026-07-11T08:00:00Z","cwd":"/repos/flare","prompt":"c","source":"local","flagged":false}`,
		`garbled`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(ledger), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("LOCAL_USAGE_LOG", logPath)

	var text bytes.Buffer
	if code := runUsage(nil, &text); code != 0 {
		t.Fatalf("runUsage text exit = %d, want 0", code)
	}
	textOut := text.String()
	for _, want := range []string{
		"total invocations: 3",
		"span: 2026-07-10T10:00:00Z → 2026-07-11T08:00:00Z",
		"skipped lines: 1",
		"source: local=2 cloud=1",
		"flagged: 1 (33.3%)",
		"repos:",
		"  flare: 1",
		"  workbench: 2",
		"per day:",
		"  2026-07-10: 2",
		"  2026-07-11: 1",
	} {
		if !strings.Contains(textOut, want) {
			t.Fatalf("text output missing %q\n---\n%s", want, textOut)
		}
	}

	var jsonBuf bytes.Buffer
	if code := runUsage([]string{"-json"}, &jsonBuf); code != 0 {
		t.Fatalf("runUsage -json exit = %d, want 0", code)
	}
	var got usageReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal -json output: %v", err)
	}
	if got.TotalInvocations != 3 || got.SkippedLines != 1 {
		t.Fatalf("-json totals = %+v, want 3 invocations and 1 skipped", got)
	}
	if got.Repos["/repos/workbench"] != 2 || got.Repos["/repos/flare"] != 1 {
		t.Fatalf("-json repos = %+v, want full cwd paths", got.Repos)
	}
	if got.SourceLocal != 2 || got.SourceCloud != 1 || got.FlaggedCount != 1 {
		t.Fatalf("-json split/flagged = %+v, want local=2 cloud=1 flagged=1", got)
	}
	wantRate := 1.0 / 3.0
	if got.FlaggedRate != wantRate {
		t.Fatalf("-json flagged rate = %v, want %v", got.FlaggedRate, wantRate)
	}
}

func TestRunUsage_missingLogExitZero(t *testing.T) {
	t.Setenv("LOCAL_USAGE_LOG", filepath.Join(t.TempDir(), "nope.jsonl"))

	var out bytes.Buffer
	if code := runUsage(nil, &out); code != 0 {
		t.Fatalf("missing log exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "total invocations: 0") {
		t.Fatalf("missing log output = %q, want zero total", out.String())
	}
}

func TestRepoShortName(t *testing.T) {
	cases := []struct {
		cwd  string
		want string
	}{
		{"/home/op/workbench", "workbench"},
		{`C:\Users\op\flare`, "flare"},
		{"/trailing/slash/", "slash"},
		{"", ""},
	}
	for _, c := range cases {
		if got := repoShortName(c.cwd); got != c.want {
			t.Errorf("repoShortName(%q) = %q, want %q", c.cwd, got, c.want)
		}
	}
}
