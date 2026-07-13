package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// logUsage appends one JSONL record of this invocation to the usage log so
// offload adoption is answerable from data (which repo, how often, escalation
// rate) instead of grepping transcripts.
//
// Best-effort by design: every failure path returns silently. Usage telemetry
// must never break the tool the operator is actually running — a missing home
// dir or an unwritable log is not the caller's problem. This is the one place a
// swallowed error is correct, not a slip.
func logUsage(prompt, source string, flagged bool) {
	path := usageLogPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	cwd, _ := os.Getwd()
	rec, err := json.Marshal(usageRecord{
		TS:      time.Now().UTC().Format(time.RFC3339),
		CWD:     cwd,
		Prompt:  truncate(prompt, 120),
		Source:  source,
		Flagged: flagged,
	})
	if err != nil {
		return
	}

	// O_APPEND with one small single-Write record per invocation: concurrent
	// `local` calls interleave whole lines, not bytes — good enough for
	// best-effort telemetry, chosen deliberately over locking.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(rec, '\n'))
}

type usageRecord struct {
	TS      string `json:"ts"`      // RFC3339 UTC, when the call ran
	CWD     string `json:"cwd"`     // working dir — the repo the offload came from
	Prompt  string `json:"prompt"`  // the task instruction, truncated (never the stdin input)
	Source  string `json:"source"`  // "local" or "cloud"
	Flagged bool   `json:"flagged"` // true when the local result was flagged low-confidence
}

// usageLogPath resolves the log location, or "" to disable logging entirely.
// LOCAL_USAGE_LOG overrides; otherwise it lands under the XDG state dir.
func usageLogPath() string {
	if p := os.Getenv("LOCAL_USAGE_LOG"); p != "" {
		return p
	}
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return filepath.Join(p, "local", "usage.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "local", "usage.jsonl")
}

// truncate caps s at n bytes so a long prompt can't bloat the log line,
// backing up to the nearest rune boundary so a multi-byte character is
// dropped whole rather than split into invalid UTF-8.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
