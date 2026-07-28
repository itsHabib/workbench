package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

var ollamaClient = &http.Client{Timeout: 3 * time.Minute}

const (
	anthropicDirectURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion   = "2023-06-01"
	cloudModelDefault  = "claude-haiku-4-5-20251001"
	structuredToolName = "structured_output"
)

// Model sends system+user prompts with a structured-output schema and returns
// the model's JSON payload as a string so callers keep json.Unmarshal unchanged.
type Model interface {
	chat(ctx context.Context, system, user string, schema json.RawMessage) (content string, err error)
	impl() string
}

type localModel struct {
	url    string
	model  string
	client *http.Client
}

func newLocalModel(url string) Model {
	if url == "" {
		url = ollamaURL
	}
	return &localModel{url: url, model: ollamaModel, client: ollamaClient}
}

func (m *localModel) impl() string { return m.model }

func (m *localModel) chat(ctx context.Context, system, user string, schema json.RawMessage) (string, error) {
	req := map[string]any{
		"model":  m.model,
		"stream": false,
		"format": schema,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"options": map[string]any{"temperature": 0},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ollama: status %d: %s", resp.StatusCode, body)
	}

	var cr struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("ollama decode: %w", err)
	}
	return cr.Message.Content, nil
}

// cloudModel snapshots its key at construction because gate is a short-lived
// CLI. A future daemon must inject a key source instead of reusing this shape.
type cloudModel struct {
	model   string
	apiKey  string
	url     string
	gateway bool
	client  *http.Client
}

func newCloudModel(apiKey, model, base string) (Model, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("cloud model: ANTHROPIC_API_KEY not set")
	}
	if base != "" && model == "" {
		return nil, fmt.Errorf("cloud model: GATE_CLOUD_MODEL not set")
	}
	if model == "" {
		model = cloudModelDefault
	}
	endpoint, err := resolveAnthropicURL(base)
	if err != nil {
		return nil, err
	}
	gateway := base != ""
	return &cloudModel{
		model:   model,
		apiKey:  apiKey,
		url:     endpoint,
		gateway: gateway,
		client:  cloudHTTPClient(gateway),
	}, nil
}

func cloudHTTPClient(gateway bool) *http.Client {
	client := &http.Client{Timeout: 3 * time.Minute}
	if gateway {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client
}

func resolveAnthropicURL(base string) (string, error) {
	if base == "" {
		return anthropicDirectURL, nil
	}
	endpoint, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("cloud model: invalid ANTHROPIC_BASE_URL")
	}
	if !endpoint.IsAbs() || endpoint.Host == "" {
		return "", fmt.Errorf("cloud model: ANTHROPIC_BASE_URL must be absolute")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return "", fmt.Errorf("cloud model: ANTHROPIC_BASE_URL must use http or https")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", fmt.Errorf("cloud model: ANTHROPIC_BASE_URL must not contain userinfo, query, or fragment")
	}
	// Preserve a gateway's provider prefix. Dropping it sends /v1/messages to
	// the gateway root, which commonly fails with an unhelpful 405.
	return endpoint.JoinPath("v1/messages").String(), nil
}

func (m *cloudModel) impl() string { return m.model }

func (m *cloudModel) chat(ctx context.Context, system, user string, schema json.RawMessage) (string, error) {
	var inputSchema any
	if err := json.Unmarshal(schema, &inputSchema); err != nil {
		return "", fmt.Errorf("cloud model: schema: %w", err)
	}
	req := map[string]any{
		"model":       m.model,
		"max_tokens":  1024,
		"temperature": 0,
		"system":      system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
		"tools": []map[string]any{
			{
				"name":         structuredToolName,
				"description":  "Structured JSON output",
				"input_schema": inputSchema,
			},
		},
		"tool_choice": map[string]string{
			"type": "tool",
			"name": structuredToolName,
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", m.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	resp, err := m.do(ctx, httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := m.statusError(resp); err != nil {
		return "", err
	}
	// The success body is capped like the error path: a 200 is model-shaped,
	// but a bounded read still refuses to buffer an unbounded response into
	// memory. 1 MiB is far above any structured-output payload.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("anthropic read: %w", err)
	}

	var envelope struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Error      *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("anthropic decode: %w", err)
	}
	if envelope.Type == "error" && envelope.Error != nil {
		if m.gateway {
			return "", fmt.Errorf("anthropic: gateway response error")
		}
		return "", fmt.Errorf("anthropic: %s: %s", envelope.Error.Type, envelope.Error.Message)
	}
	// A max_tokens stop means the tool input was cut mid-JSON: even if it
	// decodes, it is a partial answer. Fail closed rather than judge on a
	// truncated payload.
	if envelope.StopReason == "max_tokens" {
		return "", fmt.Errorf("anthropic: response truncated (max_tokens)")
	}
	for _, block := range envelope.Content {
		if block.Type != "tool_use" || block.Name != structuredToolName {
			continue
		}
		// An absent or JSON-null tool input marshals to "null", which would
		// slip past as a valid-looking payload. Fail closed instead.
		if len(block.Input) == 0 {
			return "", fmt.Errorf("anthropic: tool_use block has empty input")
		}
		out, err := json.Marshal(block.Input)
		if err != nil {
			return "", fmt.Errorf("anthropic tool input: %w", err)
		}
		return string(out), nil
	}
	return "", fmt.Errorf("anthropic: no tool_use block in response")
}

func (m *cloudModel) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := m.client.Do(req)
	if err == nil {
		return resp, nil
	}
	if !m.gateway {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("anthropic: request failed: %w", ctx.Err())
	}
	return nil, fmt.Errorf("anthropic: request failed")
}

func (m *cloudModel) statusError(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if !m.gateway {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, body)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("anthropic: authentication failed (status %d); re-authenticate and retry", resp.StatusCode)
	}
	return fmt.Errorf("anthropic: gateway status %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
}

// ModelBackend selects a Model implementation for the gate rungs.
func ModelBackend(backend string) (Model, error) {
	switch backend {
	case "", "local":
		return newLocalModel(ollamaURL), nil
	case "cloud":
		base := os.Getenv("ANTHROPIC_BASE_URL")
		model := ""
		if base != "" {
			model = os.Getenv("GATE_CLOUD_MODEL")
		}
		return newCloudModel(os.Getenv("ANTHROPIC_API_KEY"), model, base)
	default:
		return nil, fmt.Errorf("unknown model backend %q", backend)
	}
}

// ModelChat is the exported entry for callers outside verify (e.g. eval harnesses).
func ModelChat(ctx context.Context, m Model, system, user string, schema json.RawMessage) (string, error) {
	if m == nil {
		return "", fmt.Errorf("verify: nil model")
	}
	return m.chat(ctx, system, user, schema)
}
