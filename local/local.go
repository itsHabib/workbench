// Package local is the one shared primitive for running structured calls against
// a local model (Ollama), with an escalate-on-uncertainty gate.
//
// There is only ever one mechanism: prompt + input + schema in, typed JSON out.
// Every seam and every agent sub-task is a call site supplying its own policy
// (its prompt + schema). Ask runs the request locally and escalates to a
// caller-supplied cloud function when the local answer isn't trustworthy — a
// failed verifier first, low self-reported confidence second. The escalator is
// injected, so this package has no hard dependency on any cloud provider or key;
// with no escalator wired, a low-trust result is flagged, not fetched.
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	ollamaURL    = "http://localhost:11434/api/chat"
	defaultModel = "qwen2.5:7b"
)

var httpClient = &http.Client{Timeout: 3 * time.Minute}

// Req is one structured call. Schema is a JSON Schema that constrains the reply
// (Ollama structured output). Model is optional and defaults to qwen2.5:7b.
type Req struct {
	Prompt string
	Input  string
	Schema json.RawMessage
	Model  string
}

// Result carries the answer and how it was reached.
type Result struct {
	Raw    json.RawMessage // the model's structured output
	Source string          // "local" or "cloud"
	Reason string          // why it escalated (or was flagged), empty if local was trusted
}

// Opts configures the escalate gate. All fields are optional.
type Opts struct {
	// Verify returns true when the local result is trustworthy. This is the
	// strongest signal — a failed verifier beats any self-reported confidence.
	Verify func(json.RawMessage) bool
	// MinConfidence escalates when the result's top-level "confidence" number is
	// below this. Ignored when <= 0. (Self-reported confidence is the weakest
	// signal — it can be confidently wrong — so prefer Verify where you can.)
	MinConfidence float64
	// Escalate is the cloud fallback, called when the gate distrusts the local
	// answer. If nil, the local result is returned as-is with Reason set: the
	// call site is told to escalate but nothing is fetched.
	Escalate func(context.Context, Req) (json.RawMessage, error)
}

// Ask runs the request on the local model, then escalates if the gate says so.
func Ask(ctx context.Context, r Req, o Opts) (Result, error) {
	raw, err := Local(ctx, r)
	if err != nil {
		return Result{}, err
	}

	reason := gate(raw, o)
	if reason == "" {
		return Result{Raw: raw, Source: "local"}, nil
	}
	if o.Escalate == nil {
		return Result{Raw: raw, Source: "local", Reason: reason + " (no escalator wired — flagged, not fetched)"}, nil
	}

	cloud, err := o.Escalate(ctx, r)
	if err != nil {
		return Result{Raw: raw, Source: "local", Reason: "escalate failed: " + err.Error()}, nil
	}
	return Result{Raw: cloud, Source: "cloud", Reason: reason}, nil
}

// gate returns a non-empty reason to escalate, or "" to trust the local result.
// Verifier failure ranks above low confidence.
func gate(raw json.RawMessage, o Opts) string {
	if o.Verify != nil && !o.Verify(raw) {
		return "verifier failed"
	}
	if o.MinConfidence > 0 && confidenceOf(raw) < o.MinConfidence {
		return fmt.Sprintf("confidence below %.2f", o.MinConfidence)
	}
	return ""
}

func confidenceOf(raw json.RawMessage) float64 {
	var probe struct {
		Confidence float64 `json:"confidence"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Confidence
}

// Local makes one structured Ollama call and returns the raw JSON reply.
func Local(ctx context.Context, r Req) (json.RawMessage, error) {
	model := r.Model
	if model == "" {
		model = defaultModel
	}

	body, err := json.Marshal(map[string]any{
		"model":  model,
		"stream": false,
		"format": r.Schema,
		"options": map[string]any{
			"temperature": 0,
		},
		"messages": []map[string]string{
			{"role": "system", "content": r.Prompt},
			{"role": "user", "content": r.Input},
		},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama unreachable (is it running?): %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return json.RawMessage(out.Message.Content), nil
}
