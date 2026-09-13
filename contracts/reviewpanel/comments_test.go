package reviewpanel

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const recordedHead = "d9ca988176bd47beb6afc33a70b31092b4302e0e"

func recordedBodies(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/workbench-334-comments.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw []struct{ Body string }
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, value := range raw {
		bodies = append(bodies, value.Body)
	}
	return bodies
}

func TestDecodeCodexComment(t *testing.T) {
	body := recordedBodies(t)[1]
	short := recordedHead[:10]
	tests := []struct {
		name, body, token string
		clean             bool
	}{
		{"recorded no-findings framing", body, short, true},
		{"findings framing", strings.Replace(body, "Didn't find any major issues.", "Found a P1 issue.", 1), short, false},
		{"full commit", strings.Replace(body, short, recordedHead, 1), recordedHead, true},
		{"CRLF", strings.ReplaceAll(body, "\n", "\r\n"), short, true},
		{"arbitrary prose", "Approved. Reviewed commit " + recordedHead, "", false},
		{"quoted review", "Example:\n" + body, "", false},
		{"short footer", strings.Replace(body, short, recordedHead[:8], 1), "", false},
		{"other abbreviation", strings.Replace(body, short, recordedHead[:11], 1), "", false},
		{"uppercase footer", strings.Replace(body, short, strings.ToUpper(short), 1), "", false},
		{"conflicting footers", body + "\n**Reviewed commit:** `aaaaaaaaaa`\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DecodeCodexComment(tt.body)
			if ok != (tt.token != "") {
				t.Fatalf("parsed=%v, want token %q: %+v", ok, tt.token, got)
			}
			if ok && (got.ReviewedCommit != tt.token || got.NoFindingsFraming != tt.clean) {
				t.Fatalf("wrong parsed fields: %+v", got)
			}
		})
	}
}

func TestDecodeWorkflowAttestation(t *testing.T) {
	body := recordedBodies(t)[0]
	tests := []struct {
		name, body string
		valid      bool
	}{
		{"recorded attestation", body, true},
		{"CRLF and trailing whitespace", strings.ReplaceAll(body, "\n", "\r\n") + "\t \r\n", true},
		{"quoted marker", "Example:\n" + body, false},
		{"trailing prose", body + "Looks good!", false},
		{"split fields", strings.Replace(body, "\n**Reviewed", "\n\n**Reviewed", 1), false},
		{"short commit", strings.Replace(body, recordedHead, recordedHead[:10], 1), false},
		{"uppercase commit", strings.Replace(body, recordedHead, strings.ToUpper(recordedHead), 1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DecodeWorkflowAttestation(tt.body)
			if ok != tt.valid {
				t.Fatalf("parsed=%v, want %v: %+v", ok, tt.valid, got)
			}
			if ok && (got.HeadSHA != recordedHead || got.Reviewer != "claude") {
				t.Fatalf("wrong parsed fields: %+v", got)
			}
		})
	}
}
