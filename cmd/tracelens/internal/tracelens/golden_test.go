package tracelens

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

// TestVerdictJSON_Golden pins the emitted verdict JSON byte-for-byte against
// goldens captured before the contracts type swap. The wire surface is the
// contract two CLI consumers gate on; any drift — a field rename, an ordering
// change, a new non-empty field — must fail here, not surface downstream.
func TestVerdictJSON_Golden(t *testing.T) {
	cases := []struct {
		name   string
		events string
		golden string
	}{
		{
			name:   "cursor cap-burn run blocks",
			events: filepath.Join("..", "..", "testdata", "ship", "wf_01KVNKHBS61WJKZ9BVEQG6B5Y6", "events.ndjson"),
			golden: filepath.Join("..", "..", "testdata", "golden", "ship-cursor-capburn.verdict.json"),
		},
		{
			name:   "claude truncated run passes",
			events: filepath.Join("..", "..", "testdata", "corpus", "ship-claude-truncated.ndjson"),
			golden: filepath.Join("..", "..", "testdata", "golden", "ship-claude-truncated.verdict.json"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open(tc.events)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer f.Close()
			tr, err := ParseShipEvents(f)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			got, err := json.MarshalIndent(Analyze(tr, DefaultConfig()).Verdict(), "", "  ")
			if err != nil {
				t.Fatalf("marshal verdict: %v", err)
			}
			got = append(got, '\n') // the CLI emits via json.Encoder, which appends one
			if *update {
				if err := os.WriteFile(tc.golden, got, 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(tc.golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			// A Windows checkout may hand the golden back with CRLF; the pinned
			// bytes are the LF form the encoder emits.
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(got, want) {
				t.Fatalf("verdict JSON drifted from golden %s; if intended, run go test ./cmd/tracelens/internal/tracelens -update\ngot:\n%s\nwant:\n%s", tc.golden, got, want)
			}
		})
	}
}
