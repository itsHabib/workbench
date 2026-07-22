package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// logSchemaVersion pins the artifact schema. Bump it only when the field set
// changes shape, so an offline reader can branch on the version (spec §5).
const logSchemaVersion = 1

// Verdict values are the closed set of outcomes a request records (spec §5).
const (
	verdictPass          = "pass"
	verdictRefused       = "refused"
	verdictDenied        = "denied"
	verdictUpstreamError = "upstream_error"
)

// logRecord is one JSONL artifact line: enough to explain — and, for the scalar
// query rules v0 permits, replay — a verdict offline (FR3, spec §5). Every field
// is derived from metadata; by construction it never carries a request/response
// body, a header value, or a secret byte. omitempty keeps a refusal line that
// never reached a rule from carrying empty rule fields.
type logRecord struct {
	SchemaVersion   int                 `json:"schema_version"`
	Timestamp       string              `json:"ts"`
	RequestID       string              `json:"request_id"`
	Key             string              `json:"key,omitempty"`
	GrantID         string              `json:"grant_id,omitempty"`
	GrantDigest     string              `json:"grant_digest,omitempty"`
	ManifestDigest  string              `json:"manifest_digest,omitempty"`
	RuleFired       string              `json:"rule_fired,omitempty"`
	Verdict         string              `json:"verdict"`
	Method          string              `json:"method"`
	CanonicalTarget string              `json:"canonical_target,omitempty"`
	RawTargetHash   string              `json:"raw_target_hash,omitempty"`
	QueryKeys       []string            `json:"query_keys,omitempty"`
	MatchedQuery    map[string][]string `json:"matched_query,omitempty"`
	UpstreamStatus  int                 `json:"upstream_status,omitempty"`
	LatencyMs       int64               `json:"latency_ms"`
}

// Logger appends request artifact lines to a single sink behind a mutex. It is a
// mechanism: dumb, single-writer, no policy. One Logger serializes every request
// goroutine's line so the JSONL never interleaves (spec §8: one writer).
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLogger returns a Logger writing newline-terminated JSON to w.
func NewLogger(w io.Writer) *Logger { return &Logger{w: w} }

// write marshals rec and appends it as one line. A marshal error is surfaced
// rather than swallowed, but never blocks the response the caller already sent.
func (l *Logger) write(rec logRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("serve: marshal log line: %w", err)
	}
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.w.Write(data); err != nil {
		return fmt.Errorf("serve: write log line: %w", err)
	}
	return nil
}
