// Package hook adapts native Codex lifecycle envelopes to codexguard policy.
//
// It owns lifecycle translation and audit ordering, never policy.
package hook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/codexguard/internal/policy"
	"github.com/itsHabib/workbench/contracts/automode"
)

const (
	eventPreToolUse        = "PreToolUse"
	eventPermissionRequest = "PermissionRequest"
	eventPostToolUse       = "PostToolUse"
)

var errUnsupportedEvent = errors.New("unsupported hook event")

// Policy is the one deterministic decision owner the adapter calls.
type Policy interface {
	Evaluate(context.Context, policy.Request) (automode.Decision, error)
}

// Audit persists lifecycle evidence.
type Audit interface {
	Append(automode.AuditEvent) error
	Decision(string) (automode.Decision, bool, error)
}

// Adapter translates native hook envelopes and preserves persist-before-return.
type Adapter struct {
	policy    Policy
	audit     Audit
	gateState string
	shell     string
	now       func() time.Time
}

// New returns an adapter. gateState is trusted process configuration, never
// hook input. shell must describe the host command interpreter.
func New(evaluator Policy, audit Audit, gateState, shell string) Adapter {
	if shell == "" {
		shell = hostShell()
	}
	return Adapter{
		policy:    evaluator,
		audit:     audit,
		gateState: gateState,
		shell:     shell,
		now:       time.Now,
	}
}

// Handle consumes one native Codex hook envelope. A nil response means a
// successful hook with no stdout.
func (a Adapter) Handle(ctx context.Context, data []byte) (*Response, error) {
	input, err := decodeInput(data)
	if err != nil {
		return nil, err
	}
	switch input.HookEventName {
	case eventPreToolUse:
		return a.preToolUse(ctx, input)
	case eventPermissionRequest:
		return a.permissionRequest(ctx, input)
	case eventPostToolUse:
		return a.postToolUse(input)
	}
	return nil, errUnsupportedEvent
}

func (a Adapter) preToolUse(ctx context.Context, input Input) (*Response, error) {
	if input.ToolUseID == "" {
		return nil, errors.New("hook: PreToolUse requires tool_use_id")
	}
	invocationID := toolInvocationID(input)
	decision, err := a.evaluate(ctx, input, false)
	if err != nil {
		return nil, err
	}
	if err := a.appendDecision(invocationID, decision); err != nil {
		return nil, err
	}
	if decision.Outcome == automode.OutcomePass {
		return nil, nil
	}
	return preToolDeny(decision), nil
}

func (a Adapter) permissionRequest(ctx context.Context, input Input) (*Response, error) {
	decision, err := a.evaluate(ctx, input, true)
	if err != nil {
		return nil, err
	}
	invocationID := permissionInvocationID(input, decision.ActionDigest)
	if err := a.appendDecision(invocationID, decision); err != nil {
		return nil, err
	}
	switch decision.Outcome {
	case automode.OutcomePass:
		// The native payload does not identify the capability that caused the
		// approval request. A policy pass for the command is therefore not
		// sufficient evidence to suppress Codex's normal prompt.
		return nil, nil
	case automode.OutcomeBlock, automode.OutcomeRefuse:
		return permissionDeny(decision), nil
	case automode.OutcomePark:
		return nil, nil
	}
	return nil, errors.New("hook: validated policy returned an unknown outcome")
}

func (a Adapter) postToolUse(input Input) (*Response, error) {
	if input.ToolUseID == "" {
		return nil, errors.New("hook: PostToolUse requires tool_use_id")
	}
	invocationID := toolInvocationID(input)
	decision, ok, err := a.audit.Decision(invocationID)
	if err != nil {
		return nil, fmt.Errorf("hook: read prior decision: %w", err)
	}
	if !ok || decision.Outcome != automode.OutcomePass {
		return nil, nil
	}
	completion, ok := completionFrom(input.ToolResponse)
	if !ok {
		return nil, nil
	}
	recordedAt := a.now().UTC()
	event := automode.AuditEvent{
		SchemaVersion: automode.SchemaVersion,
		EventID:       eventID(invocationID, automode.EventCompletion, decision.ActionDigest, recordedAt),
		InvocationID:  invocationID,
		RecordedAt:    recordedAt,
		Kind:          automode.EventCompletion,
		Decision:      decision,
		Completion:    &completion,
	}
	if err := a.audit.Append(event); err != nil {
		return nil, fmt.Errorf("hook: append completion: %w", err)
	}
	return nil, nil
}

func (a Adapter) evaluate(ctx context.Context, input Input, permission bool) (automode.Decision, error) {
	envelope, err := policyEnvelope(input, a.shell, permission)
	if err != nil {
		return automode.Decision{}, err
	}
	decision, err := a.policy.Evaluate(ctx, policy.Request{
		GateState: a.gateState,
		Envelope:  envelope,
	})
	if err != nil {
		return automode.Decision{}, fmt.Errorf("hook: evaluate policy: %w", err)
	}
	if err := automode.ValidateDecision(decision); err != nil {
		return automode.Decision{}, fmt.Errorf("hook: invalid policy decision: %w", err)
	}
	return decision, nil
}

func (a Adapter) appendDecision(invocationID string, decision automode.Decision) error {
	recordedAt := a.now().UTC()
	event := automode.AuditEvent{
		SchemaVersion: automode.SchemaVersion,
		EventID:       eventID(invocationID, automode.EventDecision, decision.ActionDigest, recordedAt),
		InvocationID:  invocationID,
		RecordedAt:    recordedAt,
		Kind:          automode.EventDecision,
		Decision:      decision,
	}
	if err := a.audit.Append(event); err != nil {
		return fmt.Errorf("hook: append decision: %w", err)
	}
	return nil
}

// Input is the current native Codex wire envelope shared by the three events.
type Input struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath json.RawMessage `json:"transcript_path"`
	CWD            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	Model          string          `json:"model"`
	TurnID         string          `json:"turn_id"`
	PermissionMode string          `json:"permission_mode"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	AgentType      string          `json:"agent_type,omitempty"`
}

func decodeInput(data []byte) (Input, error) {
	var input Input
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return Input{}, fmt.Errorf("hook: decode input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Input{}, errors.New("hook: decode input: expected exactly one JSON object")
	}
	if err := validateInput(input); err != nil {
		return Input{}, err
	}
	return input, nil
}

func validateInput(input Input) error {
	required := map[string]string{
		"session_id":      input.SessionID,
		"cwd":             input.CWD,
		"hook_event_name": input.HookEventName,
		"model":           input.Model,
		"turn_id":         input.TurnID,
		"permission_mode": input.PermissionMode,
		"tool_name":       input.ToolName,
	}
	for name, value := range required {
		if value == "" {
			return fmt.Errorf("hook: required field missing: %s", name)
		}
	}
	if !validPermissionMode(input.PermissionMode) {
		return fmt.Errorf("hook: unsupported permission_mode %q", input.PermissionMode)
	}
	if len(input.TranscriptPath) == 0 || !validNullableString(input.TranscriptPath) {
		return errors.New("hook: transcript_path must be a string or null")
	}
	if len(input.ToolInput) == 0 || !json.Valid(input.ToolInput) {
		return errors.New("hook: tool_input must be valid JSON")
	}
	switch input.HookEventName {
	case eventPreToolUse:
		if input.ToolUseID == "" {
			return errors.New("hook: PreToolUse requires tool_use_id")
		}
	case eventPermissionRequest:
		if len(input.ToolResponse) != 0 {
			return errors.New("hook: PermissionRequest must not carry tool_response")
		}
	case eventPostToolUse:
		if input.ToolUseID == "" {
			return errors.New("hook: PostToolUse requires tool_use_id")
		}
		if len(input.ToolResponse) == 0 || !json.Valid(input.ToolResponse) {
			return errors.New("hook: PostToolUse requires valid tool_response JSON")
		}
	default:
		return fmt.Errorf("hook: %w %q", errUnsupportedEvent, input.HookEventName)
	}
	return nil
}

func validPermissionMode(mode string) bool {
	switch mode {
	case "default", "acceptEdits", "plan", "dontAsk", "bypassPermissions":
		return true
	}
	return false
}

func validNullableString(data []byte) bool {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return true
	}
	var value string
	return json.Unmarshal(data, &value) == nil
}

func policyEnvelope(input Input, shell string, permission bool) (policy.Envelope, error) {
	if input.ToolName == "Bash" {
		arguments, err := shellArguments(input.ToolInput, shell, permission)
		if err != nil {
			return policy.Envelope{}, err
		}
		return policy.Envelope{Kind: "local", Tool: "shell_command", Arguments: arguments}, nil
	}
	if strings.HasPrefix(input.ToolName, "mcp__") {
		return policy.Envelope{Kind: "mcp", Tool: input.ToolName, Arguments: input.ToolInput}, nil
	}
	return policy.Envelope{Kind: "local", Tool: input.ToolName, Arguments: input.ToolInput}, nil
}

func shellArguments(data []byte, shell string, permission bool) ([]byte, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, errors.New("hook: Bash tool_input must be an object")
	}
	if permission {
		delete(values, "description")
	}
	shellJSON, err := json.Marshal(shell)
	if err != nil {
		return nil, err
	}
	values["shell"] = shellJSON
	if _, ok := values["workdir"]; !ok {
		values["workdir_unknown"] = json.RawMessage("true")
	}
	out, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("hook: encode Bash policy input: %w", err)
	}
	return out, nil
}

// Response is a native Codex hook response.
type Response struct {
	HookSpecificOutput SpecificOutput `json:"hookSpecificOutput"`
}

// SpecificOutput carries one supported event-specific decision.
type SpecificOutput struct {
	HookEventName            string              `json:"hookEventName"`
	PermissionDecision       string              `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string              `json:"permissionDecisionReason,omitempty"`
	Decision                 *PermissionDecision `json:"decision,omitempty"`
}

// PermissionDecision is the native PermissionRequest decision.
type PermissionDecision struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

func preToolDeny(decision automode.Decision) *Response {
	return &Response{HookSpecificOutput: SpecificOutput{
		HookEventName:            eventPreToolUse,
		PermissionDecision:       "deny",
		PermissionDecisionReason: decisionMessage(decision),
	}}
}

func permissionDeny(decision automode.Decision) *Response {
	return &Response{HookSpecificOutput: SpecificOutput{
		HookEventName: eventPermissionRequest,
		Decision: &PermissionDecision{
			Behavior: "deny",
			Message:  decisionMessage(decision),
		},
	}}
}

func decisionMessage(decision automode.Decision) string {
	return fmt.Sprintf("codexguard %s (%s): %s", decision.Outcome, decision.RuleFired, decision.Remedy)
}

func toolInvocationID(input Input) string {
	inputSum := sha256.Sum256(input.ToolInput)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		input.SessionID,
		input.ToolUseID,
		input.ToolName,
		hex.EncodeToString(inputSum[:]),
	}, "\x00")))
	return "inv_tool_" + hex.EncodeToString(sum[:16])
}

func permissionInvocationID(input Input, actionDigest string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		input.SessionID,
		input.TurnID,
		input.ToolName,
		actionDigest,
	}, "\x00")))
	return "inv_permission_" + hex.EncodeToString(sum[:16])
}

func eventID(invocationID, kind, actionDigest string, recordedAt time.Time) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		invocationID,
		kind,
		actionDigest,
		recordedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")))
	return "evt_" + hex.EncodeToString(sum[:16])
}

func completionFrom(data []byte) (automode.Completion, bool) {
	status, signal, ok := completionStatus(data)
	if !ok {
		return automode.Completion{}, false
	}
	sum := sha256.Sum256(data)
	observables := automode.Values{
		"response_digest": {Value: "sha256:" + hex.EncodeToString(sum[:])},
		"result_signal":   {Value: signal},
	}
	return automode.Completion{Status: status, Observables: observables}, true
}

func completionStatus(data []byte) (string, string, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return "", "", false
	}
	return objectCompletionStatus(object)
}

func objectCompletionStatus(object map[string]json.RawMessage) (string, string, bool) {
	signals, ok := completionSignals(object)
	if !ok || len(signals) == 0 {
		return "", "", false
	}
	failed := signals[0].failed
	labels := make([]string, 0, len(signals))
	for _, signal := range signals {
		if signal.failed != failed {
			return "", "", false
		}
		labels = append(labels, signal.label)
	}
	return statusFromFailure(failed), strings.Join(labels, ","), true
}

type completionSignal struct {
	label  string
	failed bool
}

func completionSignals(object map[string]json.RawMessage) ([]completionSignal, bool) {
	var signals []completionSignal
	for _, name := range []string{"is_error", "isError"} {
		raw, exists := object[name]
		if !exists {
			continue
		}
		var failed bool
		if json.Unmarshal(raw, &failed) != nil {
			return nil, false
		}
		signals = append(signals, completionSignal{label: name, failed: failed})
	}
	if raw, ok := object["success"]; ok {
		var success bool
		if json.Unmarshal(raw, &success) != nil {
			return nil, false
		}
		signals = append(signals, completionSignal{label: "success", failed: !success})
	}
	for _, name := range []string{"exit_code", "exitCode"} {
		raw, exists := object[name]
		if !exists {
			continue
		}
		var code int
		if json.Unmarshal(raw, &code) != nil {
			return nil, false
		}
		signals = append(signals, completionSignal{
			label: name + ":" + strconv.Itoa(code), failed: code != 0,
		})
	}
	return signals, true
}

func statusFromFailure(failed bool) string {
	if failed {
		return automode.StatusFailed
	}
	return automode.StatusSucceeded
}

func hostShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "bash"
}
