package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	gateStateBranch  = "gate-state"
	maxStateFile     = 32 * 1024 * 1024
	maxStateResponse = 48 * 1024 * 1024
)

var (
	// ErrState reports a malformed or failed gate-state transport.
	ErrState = errors.New("executor_state_transport_failed")
	// ErrStateCAS reports that gate-state moved before a non-force update.
	ErrStateCAS = errors.New("executor_state_cas_failed")
)

// StateFiles are the only two files the Gate App may update on gate-state.
type StateFiles struct {
	Log    []byte
	Anchor []byte
}

// StateSnapshot is a freshly read durable gate-state commit.
type StateSnapshot struct {
	Tip   string
	Files StateFiles
}

// PullState is the minimum GitHub fact set needed to reconcile an expired
// claim. It carries no command or execution input.
type PullState struct {
	Number      int
	HeadSHA     string
	BaseRef     string
	Merged      bool
	MergeCommit string
}

// ReadPullState observes the claimed pull request without executing it.
func (session *Session) ReadPullState(ctx context.Context, number int) (PullState, error) {
	if number < 1 {
		return PullState{}, ErrState
	}
	var response struct {
		Number int  `json:"number"`
		Merged bool `json:"merged"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	if err := session.api(
		ctx, http.MethodGet, session.repoPath(fmt.Sprintf("/pulls/%d", number)),
		nil, &response,
	); err != nil {
		return PullState{}, err
	}
	if response.Number != number || !validSHA(response.Head.SHA) ||
		response.Base.Ref == "" {
		return PullState{}, ErrState
	}
	if response.Merged && !validSHA(response.MergeCommitSHA) {
		return PullState{}, ErrState
	}
	return PullState{
		Number: number, HeadSHA: response.Head.SHA, BaseRef: response.Base.Ref,
		Merged: response.Merged, MergeCommit: response.MergeCommitSHA,
	}, nil
}

// PublishGateState creates one commit from expectedTip, advances gate-state
// without force, and reads the committed bytes back before returning.
func (session *Session) PublishGateState(ctx context.Context, expectedTip, message string, files StateFiles) (StateSnapshot, error) {
	if !validSHA(expectedTip) || !validMessage(message) || !validStateFiles(files) {
		return StateSnapshot{}, ErrState
	}
	current, err := session.readRef(ctx)
	if err != nil {
		return StateSnapshot{}, err
	}
	if current != expectedTip {
		return StateSnapshot{}, ErrStateCAS
	}
	parent, err := session.readCommit(ctx, expectedTip)
	if err != nil {
		return StateSnapshot{}, err
	}
	logBlob, err := session.createBlob(ctx, files.Log)
	if err != nil {
		return StateSnapshot{}, err
	}
	anchorBlob, err := session.createBlob(ctx, files.Anchor)
	if err != nil {
		return StateSnapshot{}, err
	}
	tree, err := session.createTree(ctx, parent.Tree.SHA, logBlob, anchorBlob)
	if err != nil {
		return StateSnapshot{}, err
	}
	commit, err := session.createCommit(ctx, message, tree, expectedTip)
	if err != nil {
		return StateSnapshot{}, err
	}
	if err := session.updateRef(ctx, commit); err != nil {
		return StateSnapshot{}, err
	}
	return session.FetchGateState(ctx, commit)
}

// FetchGateState reads the current remote tip and exact state bytes. expected
// must still be current; a later writer makes the read refuse.
func (session *Session) FetchGateState(ctx context.Context, expected string) (StateSnapshot, error) {
	if !validSHA(expected) {
		return StateSnapshot{}, ErrState
	}
	current, err := session.readRef(ctx)
	if err != nil {
		return StateSnapshot{}, err
	}
	if current != expected {
		return StateSnapshot{}, ErrStateCAS
	}
	commit, err := session.readCommit(ctx, current)
	if err != nil {
		return StateSnapshot{}, err
	}
	tree, err := session.readTree(ctx, commit.Tree.SHA)
	if err != nil {
		return StateSnapshot{}, err
	}
	logSHA, anchorSHA, err := stateBlobSHAs(tree)
	if err != nil {
		return StateSnapshot{}, err
	}
	logData, err := session.readBlob(ctx, logSHA)
	if err != nil {
		return StateSnapshot{}, err
	}
	anchorData, err := session.readBlob(ctx, anchorSHA)
	if err != nil {
		return StateSnapshot{}, err
	}
	files := StateFiles{Log: logData, Anchor: anchorData}
	if !validStateFiles(files) {
		return StateSnapshot{}, ErrState
	}
	return StateSnapshot{Tip: current, Files: files}, nil
}

type gitCommit struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
}

type gitTree struct {
	Tree []struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"tree"`
	Truncated bool `json:"truncated"`
}

func (session *Session) readRef(ctx context.Context) (string, error) {
	var response struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	path := session.repoPath("/git/ref/heads/" + gateStateBranch)
	if err := session.api(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", err
	}
	if !validSHA(response.Object.SHA) {
		return "", ErrState
	}
	return response.Object.SHA, nil
}

func (session *Session) readCommit(ctx context.Context, sha string) (gitCommit, error) {
	var response gitCommit
	if err := session.api(ctx, http.MethodGet, session.repoPath("/git/commits/"+sha), nil, &response); err != nil {
		return gitCommit{}, err
	}
	if response.SHA != sha || !validSHA(response.Tree.SHA) {
		return gitCommit{}, ErrState
	}
	return response, nil
}

func (session *Session) createBlob(ctx context.Context, data []byte) (string, error) {
	request := map[string]string{
		"content":  base64.StdEncoding.EncodeToString(data),
		"encoding": "base64",
	}
	var response struct {
		SHA string `json:"sha"`
	}
	if err := session.api(ctx, http.MethodPost, session.repoPath("/git/blobs"), request, &response); err != nil {
		return "", err
	}
	if !validSHA(response.SHA) {
		return "", ErrState
	}
	return response.SHA, nil
}

func (session *Session) createTree(ctx context.Context, base, log, anchor string) (string, error) {
	request := map[string]any{
		"base_tree": base,
		"tree": []map[string]string{
			{"path": "state/log.jsonl", "mode": "100644", "type": "blob", "sha": log},
			{"path": "anchor.json", "mode": "100644", "type": "blob", "sha": anchor},
		},
	}
	var response struct {
		SHA string `json:"sha"`
	}
	if err := session.api(ctx, http.MethodPost, session.repoPath("/git/trees"), request, &response); err != nil {
		return "", err
	}
	if !validSHA(response.SHA) {
		return "", ErrState
	}
	return response.SHA, nil
}

func (session *Session) createCommit(ctx context.Context, message, tree, parent string) (string, error) {
	request := map[string]any{
		"message": message,
		"tree":    tree,
		"parents": []string{parent},
	}
	var response gitCommit
	if err := session.api(ctx, http.MethodPost, session.repoPath("/git/commits"), request, &response); err != nil {
		return "", err
	}
	if !validSHA(response.SHA) {
		return "", ErrState
	}
	return response.SHA, nil
}

func (session *Session) updateRef(ctx context.Context, sha string) error {
	request := map[string]any{"sha": sha, "force": false}
	var response struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	path := session.repoPath("/git/refs/heads/" + gateStateBranch)
	if err := session.api(ctx, http.MethodPatch, path, request, &response); err != nil {
		return err
	}
	if response.Object.SHA != sha {
		return ErrStateCAS
	}
	return nil
}

func (session *Session) readTree(ctx context.Context, sha string) (gitTree, error) {
	var response gitTree
	path := session.repoPath("/git/trees/" + sha + "?recursive=1")
	if err := session.api(ctx, http.MethodGet, path, nil, &response); err != nil {
		return gitTree{}, err
	}
	if response.Truncated {
		return gitTree{}, ErrState
	}
	return response, nil
}

func (session *Session) readBlob(ctx context.Context, sha string) ([]byte, error) {
	var response struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
	}
	if err := session.api(ctx, http.MethodGet, session.repoPath("/git/blobs/"+sha), nil, &response); err != nil {
		return nil, err
	}
	if response.SHA != sha || response.Encoding != "base64" {
		return nil, ErrState
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil || len(data) > maxStateFile {
		return nil, ErrState
	}
	return data, nil
}

func (session *Session) api(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("%w: encode request", ErrState)
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, session.apiURL()+path, body)
	if err != nil {
		return fmt.Errorf("%w: build request", ErrState)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+session.token.value)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := session.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: request", ErrState)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusUnprocessableEntity {
			return ErrStateCAS
		}
		return fmt.Errorf("%w: HTTP %d", ErrState, response.StatusCode)
	}
	if output == nil {
		return nil
	}
	reader := io.LimitReader(response.Body, maxStateResponse)
	if err := json.NewDecoder(reader).Decode(output); err != nil {
		return fmt.Errorf("%w: decode response", ErrState)
	}
	return nil
}

func (session *Session) apiURL() string {
	value := strings.TrimRight(session.config.APIURL, "/")
	if value == "" {
		return defaultAPIURL
	}
	return value
}

func (session *Session) repoPath(suffix string) string {
	return "/repos/" + session.config.Repository + suffix
}

func stateBlobSHAs(tree gitTree) (string, string, error) {
	var logSHA, anchorSHA string
	for _, entry := range tree.Tree {
		switch entry.Path {
		case "state/log.jsonl":
			if entry.Mode == "100644" && entry.Type == "blob" {
				logSHA = entry.SHA
			}
		case "anchor.json":
			if entry.Mode == "100644" && entry.Type == "blob" {
				anchorSHA = entry.SHA
			}
		}
	}
	if !validSHA(logSHA) || !validSHA(anchorSHA) {
		return "", "", ErrState
	}
	return logSHA, anchorSHA, nil
}

func validStateFiles(files StateFiles) bool {
	return len(files.Log) > 0 && len(files.Log) <= maxStateFile &&
		len(files.Anchor) > 0 && len(files.Anchor) <= maxStateFile
}

func validMessage(message string) bool {
	return message != "" && len(message) <= 200 && !strings.ContainsAny(message, "\r\n")
}
