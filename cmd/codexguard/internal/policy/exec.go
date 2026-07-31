package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// ExecGateReader reads only gate next -json through the fixed gate executable.
type ExecGateReader struct{}

// Ready invokes the fixed gate binary's read-only inbox projection.
func (ExecGateReader) Ready(ctx context.Context, state string) ([]ReadyMerge, error) {
	if state == "" {
		return nil, errors.New("gate state is required")
	}
	cmd := exec.CommandContext(ctx, "gate", "next", "-state", state, "-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gate next: %w: %s", err, stderr.String())
	}
	var inbox struct {
		Ready []ReadyMerge `json:"ready_to_merge"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &inbox); err != nil {
		return nil, fmt.Errorf("gate next: decode JSON: %w", err)
	}
	return inbox.Ready, nil
}

// ExecPullRequestReader reads one PR through the fixed gh executable.
type ExecPullRequestReader struct{}

// Get invokes the fixed gh binary's read-only PR projection.
func (ExecPullRequestReader) Get(ctx context.Context, repo string, number int) (PullRequest, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", fmt.Sprint(number), "-R", repo, "--json", "state,headRefOid")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return PullRequest{}, fmt.Errorf("gh pr view: %w: %s", err, stderr.String())
	}
	var raw struct {
		State      string `json:"state"`
		HeadRefOID string `json:"headRefOid"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return PullRequest{}, fmt.Errorf("gh pr view: decode JSON: %w", err)
	}
	if raw.State == "" || raw.HeadRefOID == "" {
		return PullRequest{}, errors.New("gh pr view: ambiguous response")
	}
	return PullRequest{State: raw.State, HeadSHA: raw.HeadRefOID}, nil
}
