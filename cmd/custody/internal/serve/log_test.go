package serve

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// TestLoggerSingleWriterNoInterleave drives many goroutines through one Logger
// and asserts every line is intact JSON — the single-writer mutex must serialize
// concurrent request lines (spec §8).
func TestLoggerSingleWriterNoInterleave(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewLogger(buf)
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.write(logRecord{SchemaVersion: logSchemaVersion, RequestID: "req", Verdict: verdictPass, Method: "GET"})
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	for i, line := range lines {
		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d not intact JSON: %v (%q)", i, err, line)
		}
	}
}
