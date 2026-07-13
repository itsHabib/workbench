package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// usageReport is the rollup of one usage ledger. Repos keys are full cwd paths
// (JSON consumers); text output aggregates by the last path segment.
type usageReport struct {
	TotalInvocations int            `json:"total_invocations"`
	FirstTS          string         `json:"first_ts,omitempty"`
	LastTS           string         `json:"last_ts,omitempty"`
	SkippedLines     int            `json:"skipped_lines"`
	Repos            map[string]int `json:"repos"`
	PerDay           map[string]int `json:"per_day"`
	SourceLocal      int            `json:"source_local"`
	SourceCloud      int            `json:"source_cloud"`
	FlaggedCount     int            `json:"flagged_count"`
	FlaggedRate      float64        `json:"flagged_rate"`
}

func emptyUsageReport() usageReport {
	return usageReport{
		Repos:  map[string]int{},
		PerDay: map[string]int{},
	}
}

// usageReportFromPath rolls up the ledger at path. A missing log is an answer
// (zero report), but any other open failure is surfaced — a silent zero on a
// permissions error would misreport adoption.
func usageReportFromPath(path string) (usageReport, error) {
	if path == "" {
		return emptyUsageReport(), nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyUsageReport(), nil
	}
	if err != nil {
		return usageReport{}, fmt.Errorf("open usage log: %w", err)
	}
	defer f.Close()
	return parseUsageLedger(f), nil
}

func parseUsageLedger(r io.Reader) usageReport {
	rep := emptyUsageReport()
	scan := bufio.NewScanner(r)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		var rec usageRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			rep.SkippedLines++
			continue
		}
		if rec.TS == "" {
			rep.SkippedLines++
			continue
		}
		rep.TotalInvocations++
		if rep.FirstTS == "" || tsBefore(rec.TS, rep.FirstTS) {
			rep.FirstTS = rec.TS
		}
		if rep.LastTS == "" || tsBefore(rep.LastTS, rec.TS) {
			rep.LastTS = rec.TS
		}
		if rec.CWD != "" {
			rep.Repos[rec.CWD]++
		}
		if day := dayFromTS(rec.TS); day != "" {
			rep.PerDay[day]++
		}
		switch rec.Source {
		case "local":
			rep.SourceLocal++
		case "cloud":
			rep.SourceCloud++
		}
		if rec.Flagged {
			rep.FlaggedCount++
		}
	}
	if rep.TotalInvocations > 0 {
		rep.FlaggedRate = float64(rep.FlaggedCount) / float64(rep.TotalInvocations)
	}
	return rep
}

// tsBefore reports whether a sorts before b as RFC 3339 instants, so spans are
// correct across mixed UTC offsets; it falls back to string order when either
// side fails to parse.
func tsBefore(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA != nil || errB != nil {
		return a < b
	}
	return ta.Before(tb)
}

func dayFromTS(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func runUsage(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit report as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rep, err := usageReportFromPath(usageLogPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *asJSON {
		out, err := json.Marshal(rep)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode report:", err)
			return 1
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	printUsageReport(stdout, rep)
	return 0
}

func printUsageReport(w io.Writer, rep usageReport) {
	fmt.Fprintf(w, "total invocations: %d\n", rep.TotalInvocations)
	if rep.FirstTS != "" {
		fmt.Fprintf(w, "span: %s → %s\n", rep.FirstTS, rep.LastTS)
	}
	if rep.SkippedLines > 0 {
		fmt.Fprintf(w, "skipped lines: %d\n", rep.SkippedLines)
	}
	fmt.Fprintf(w, "source: local=%d cloud=%d\n", rep.SourceLocal, rep.SourceCloud)
	fmt.Fprintf(w, "flagged: %d (%.1f%%)\n", rep.FlaggedCount, rep.FlaggedRate*100)

	if len(rep.Repos) > 0 {
		fmt.Fprintln(w, "repos:")
		for _, name := range sortedRepoShortNames(rep.Repos) {
			fmt.Fprintf(w, "  %s: %d\n", name, repoShortCount(rep.Repos, name))
		}
	}
	if len(rep.PerDay) > 0 {
		fmt.Fprintln(w, "per day:")
		for _, day := range sortedKeys(rep.PerDay) {
			fmt.Fprintf(w, "  %s: %d\n", day, rep.PerDay[day])
		}
	}
}

// repoShortName reduces a ledger cwd to its last path segment. The ledger may
// hold paths written on any OS, so both separators are handled regardless of
// the host (filepath.ToSlash is a no-op for `\` on Linux).
func repoShortName(cwd string) string {
	cwd = strings.ReplaceAll(cwd, `\`, "/")
	cwd = strings.TrimRight(cwd, "/")
	if cwd == "" {
		return ""
	}
	if i := strings.LastIndexByte(cwd, '/'); i >= 0 {
		return cwd[i+1:]
	}
	return cwd
}

func repoShortCount(repos map[string]int, short string) int {
	n := 0
	for cwd, count := range repos {
		if repoShortName(cwd) == short {
			n += count
		}
	}
	return n
}

func sortedRepoShortNames(repos map[string]int) []string {
	seen := map[string]bool{}
	var names []string
	for cwd := range repos {
		short := repoShortName(cwd)
		if short == "" || seen[short] {
			continue
		}
		seen[short] = true
		names = append(names, short)
	}
	sort.Strings(names)
	return names
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
