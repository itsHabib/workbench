package reviewpanel

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const recordedHead = "d9ca988176bd47beb6afc33a70b31092b4302e0e"

func recordedComments(t *testing.T) []Comment {
	t.Helper()
	data, err := os.ReadFile("testdata/workbench-334-comments.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw []struct {
		ID   int64
		Body string
		User struct{ Login, Type string }
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var comments []Comment
	for _, value := range raw {
		comments = append(comments, Comment{
			ID: value.ID, Body: value.Body, Author: value.User.Login,
			IsBot: value.User.Type == "Bot",
		})
	}
	return comments
}

func TestDecodeCodexComment(t *testing.T) {
	base := recordedComments(t)[1]
	tests := []struct {
		name   string
		mutate func(*Comment, *string)
		state  string
	}{
		{name: "recorded clean review", state: "CLEAN"},
		{name: "findings still complete", state: "COMMENTED", mutate: func(c *Comment, _ *string) {
			c.Body = strings.Replace(c.Body, "Didn't find any major issues.", "Found a P1 issue.", 1)
		}},
		{name: "full commit", state: "CLEAN", mutate: func(c *Comment, _ *string) {
			c.Body = strings.Replace(c.Body, recordedHead[:10], recordedHead, 1)
		}},
		{name: "CRLF", state: "CLEAN", mutate: func(c *Comment, _ *string) {
			c.Body = strings.ReplaceAll(c.Body, "\n", "\r\n")
		}},
		{name: "human copy", mutate: func(c *Comment, _ *string) { c.Author = "itsHabib"; c.IsBot = false }},
		{name: "bot metadata required", mutate: func(c *Comment, _ *string) { c.IsBot = false }},
		{name: "alias is not issuer", mutate: func(c *Comment, _ *string) { c.Author = "codex[bot]" }},
		{name: "inline comment", mutate: func(c *Comment, _ *string) { c.Path = "main.go" }},
		{name: "formal comment", mutate: func(c *Comment, _ *string) { c.CommitID = recordedHead }},
		{name: "missing source ID", mutate: func(c *Comment, _ *string) { c.ID = 0 }},
		{name: "stale head", mutate: func(_ *Comment, head *string) { *head = strings.Repeat("a", 40) }},
		{name: "short subject", mutate: func(_ *Comment, head *string) { *head = recordedHead[:10] }},
		{name: "non-hex subject", mutate: func(_ *Comment, head *string) { *head = recordedHead[:39] + "z" }},
		{name: "arbitrary prose", mutate: func(c *Comment, _ *string) {
			c.Body = "Approved. Reviewed commit " + recordedHead
		}},
		{name: "quoted review", mutate: func(c *Comment, _ *string) { c.Body = "Example:\n" + c.Body }},
		{name: "short footer", mutate: func(c *Comment, _ *string) {
			c.Body = strings.Replace(c.Body, recordedHead[:10], recordedHead[:8], 1)
		}},
		{name: "other abbreviation", mutate: func(c *Comment, _ *string) {
			c.Body = strings.Replace(c.Body, recordedHead[:10], recordedHead[:11], 1)
		}},
		{name: "uppercase footer", mutate: func(c *Comment, _ *string) {
			c.Body = strings.Replace(c.Body, recordedHead[:10], strings.ToUpper(recordedHead[:10]), 1)
		}},
		{name: "same prefix stale full footer", mutate: func(c *Comment, _ *string) {
			c.Body = strings.Replace(c.Body, recordedHead[:10], recordedHead[:39]+"a", 1)
		}},
		{name: "conflicting footers", mutate: func(c *Comment, _ *string) {
			c.Body += "\n**Reviewed commit:** `aaaaaaaaaa`\n"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comment, head := base, recordedHead
			if tt.mutate != nil {
				tt.mutate(&comment, &head)
			}
			got, ok := DecodeCodexComment(comment, head)
			if ok != (tt.state != "") {
				t.Fatalf("completion=%v, want state %q: %+v", ok, tt.state, got)
			}
			if ok && (got.State != tt.state || got.HeadSHA != head || got.Name != "codex" ||
				got.Actor != base.Author || got.ReviewID != base.ID) {
				t.Fatalf("wrong decoded evidence: %+v", got)
			}
		})
	}
}

func TestDecodeWorkflowAttestation(t *testing.T) {
	base := recordedComments(t)[0]
	tests := []struct {
		name   string
		mutate func(*Comment)
		valid  bool
	}{
		{name: "recorded attestation", valid: true},
		{name: "CRLF and trailing whitespace", valid: true, mutate: func(c *Comment) {
			c.Body = strings.ReplaceAll(c.Body, "\n", "\r\n") + "\t \r\n"
		}},
		{name: "human copy", mutate: func(c *Comment) { c.Author = "itsHabib"; c.IsBot = false }},
		{name: "bot metadata required", mutate: func(c *Comment) { c.IsBot = false }},
		{name: "provider prose is not attestation", mutate: func(c *Comment) { c.Author = "claude[bot]" }},
		{name: "inline comment", mutate: func(c *Comment) { c.Path = "main.go" }},
		{name: "formal comment", mutate: func(c *Comment) { c.CommitID = recordedHead }},
		{name: "missing source ID", mutate: func(c *Comment) { c.ID = 0 }},
		{name: "quoted marker", mutate: func(c *Comment) { c.Body = "Example:\n" + c.Body }},
		{name: "trailing prose", mutate: func(c *Comment) { c.Body += "Looks good!" }},
		{name: "split fields", mutate: func(c *Comment) {
			c.Body = strings.Replace(c.Body, "\n**Reviewed", "\n\n**Reviewed", 1)
		}},
		{name: "short commit", mutate: func(c *Comment) {
			c.Body = strings.Replace(c.Body, recordedHead, recordedHead[:10], 1)
		}},
		{name: "uppercase commit", mutate: func(c *Comment) {
			c.Body = strings.Replace(c.Body, recordedHead, strings.ToUpper(recordedHead), 1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comment := base
			if tt.mutate != nil {
				tt.mutate(&comment)
			}
			got, ok := DecodeWorkflowAttestation(comment)
			if ok != tt.valid {
				t.Fatalf("completion=%v, want %v: %+v", ok, tt.valid, got)
			}
			if ok && (got.State != "COMMENTED" || got.HeadSHA != recordedHead || got.Name != "claude" ||
				got.Actor != base.Author || got.ReviewID != base.ID) {
				t.Fatalf("wrong decoded evidence: %+v", got)
			}
		})
	}
}
