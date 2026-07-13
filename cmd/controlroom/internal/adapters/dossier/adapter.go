// Package dossier adapts Dossier's long-lived stdio MCP owner interface.
package dossier

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const (
	protocolVersion   = "2024-11-05"
	collectionTimeout = 20 * time.Second
	cleanupTimeout    = time.Second
	breakerThreshold  = 3
	breakerPause      = 5 * time.Minute
	maxProjects       = 50
	maxTasks          = 500
)

// Result is Dossier's source-local collection result.
type Result struct {
	Tasks   []model.Task
	Receipt model.SourceReceipt
}

type process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() error
	Kill() error
}

type processFactory interface {
	Start(string, ...string) (process, error)
}

type execFactory struct{}

func (execFactory) Start(executable string, args ...string) (process, error) {
	cmd := exec.Command(executable, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *execProcess) Wait() error           { return p.cmd.Wait() }
func (p *execProcess) Kill() error           { return p.cmd.Process.Kill() }

type probeCall struct {
	done   chan struct{}
	result Result
}

// Adapter supervises one handshaken child and exact breaker state.
type Adapter struct {
	executable string
	corpus     string
	factory    processFactory
	now        func() time.Time
	waitFor    time.Duration

	cycleMu   sync.Mutex
	stateMu   sync.Mutex
	session   *session
	failures  int
	openUntil time.Time
	probe     *probeCall
}

// New constructs a long-lived Dossier adapter.
func New(executable, corpus string) *Adapter {
	return &Adapter{executable: executable, corpus: corpus, factory: execFactory{}, now: time.Now, waitFor: cleanupTimeout}
}

// Collect performs an automatic refresh. An open breaker suppresses process creation.
func (a *Adapter) Collect(ctx context.Context) Result { return a.collect(ctx, false) }

// CollectManual performs a user-requested refresh. While the breaker is open,
// concurrent callers join one half-open probe and receive the same result.
func (a *Adapter) CollectManual(ctx context.Context) Result { return a.collect(ctx, true) }

// Close reaps the retained child.
func (a *Adapter) Close() error {
	a.cycleMu.Lock()
	defer a.cycleMu.Unlock()
	return a.invalidateSession()
}

func (a *Adapter) collect(ctx context.Context, manual bool) Result {
	for {
		now := a.now()
		a.stateMu.Lock()
		open := now.Before(a.openUntil)
		if open && !manual {
			a.stateMu.Unlock()
			return unavailable(now, "breaker_open", "Dossier automatic probes are temporarily paused")
		}
		if open && manual {
			if a.probe != nil {
				probe := a.probe
				a.stateMu.Unlock()
				select {
				case <-ctx.Done():
					return unavailable(a.now(), "cancelled", "Dossier manual refresh was cancelled")
				case <-probe.done:
					return probe.result
				}
			}
			probe := &probeCall{done: make(chan struct{})}
			a.probe = probe
			a.stateMu.Unlock()
			a.cycleMu.Lock()
			result, breakerFailure := a.collectCycle(ctx)
			a.cycleMu.Unlock()
			a.stateMu.Lock()
			a.record(result, breakerFailure)
			probe.result = result
			close(probe.done)
			a.probe = nil
			a.stateMu.Unlock()
			return result
		}
		a.stateMu.Unlock()

		a.cycleMu.Lock()
		a.stateMu.Lock()
		if a.now().Before(a.openUntil) {
			a.stateMu.Unlock()
			a.cycleMu.Unlock()
			continue
		}
		a.stateMu.Unlock()
		result, breakerFailure := a.collectCycle(ctx)
		a.stateMu.Lock()
		a.record(result, breakerFailure)
		a.stateMu.Unlock()
		a.cycleMu.Unlock()
		return result
	}
}

func (a *Adapter) record(result Result, breakerFailure bool) {
	if result.Receipt.State == model.SourceOK {
		a.failures = 0
		a.openUntil = time.Time{}
		return
	}
	if !breakerFailure {
		return
	}
	a.failures++
	if a.failures >= breakerThreshold {
		a.openUntil = a.now().Add(breakerPause)
	}
}

func (a *Adapter) collectCycle(ctx context.Context) (Result, bool) {
	started := a.now()
	if a.executable == "" || a.corpus == "" {
		return unavailable(started, "not_configured", "Dossier is not configured"), true
	}
	cycleCtx, cancel := context.WithTimeout(ctx, collectionTimeout)
	defer cancel()
	if a.session == nil {
		child, err := a.factory.Start(a.executable, "serve", "--corpus", a.corpus)
		if err != nil {
			return unavailable(started, "start_failed", "Dossier child could not start"), true
		}
		a.session = newSession(child)
		if err := a.session.initialize(cycleCtx); err != nil {
			_ = a.invalidateSession()
			return unavailable(started, protocolCode(cycleCtx, err, "handshake_failed"), "Dossier handshake failed"), true
		}
	}

	projects, err := callValue[projectList](cycleCtx, a.session, "project.list", map[string]any{"include_terminal": true, "limit": maxProjects})
	if err != nil {
		_ = a.invalidateSession()
		return unavailable(started, protocolCode(cycleCtx, err, "first_call_failed"), "Dossier project inventory failed"), true
	}
	if len(projects.Projects) > maxProjects {
		projects.Projects = projects.Projects[:maxProjects]
	}

	allTasks := make([]taskWire, 0)
	artifacts := make([]artifactWire, 0)
	for _, project := range projects.Projects {
		if project.Slug == "" {
			continue
		}
		var discard json.RawMessage
		if err := a.session.call(cycleCtx, "project.overview", map[string]any{"slug": project.Slug}, &discard); err != nil {
			_ = a.invalidateSession()
			return unavailable(started, protocolCode(cycleCtx, err, "call_failed"), "Dossier project overview failed"), false
		}
		if _, err := callValue[phaseList](cycleCtx, a.session, "phase.list", map[string]any{"project": project.Slug, "include_terminal": true, "bodies": false, "limit": maxTasks}); err != nil {
			_ = a.invalidateSession()
			return unavailable(started, protocolCode(cycleCtx, err, "call_failed"), "Dossier phase inventory failed"), false
		}
		tasks, err := callValue[taskList](cycleCtx, a.session, "task.list", map[string]any{"project": project.Slug, "include_terminal": true, "bodies": false, "limit": maxTasks})
		if err != nil {
			_ = a.invalidateSession()
			return unavailable(started, protocolCode(cycleCtx, err, "call_failed"), "Dossier task inventory failed"), false
		}
		for _, summary := range tasks.Tasks {
			if len(allTasks) >= maxTasks {
				break
			}
			detail, detailErr := callValue[taskWire](cycleCtx, a.session, "task.get", map[string]any{"id": summary.ID})
			if detailErr != nil {
				_ = a.invalidateSession()
				return unavailable(started, protocolCode(cycleCtx, detailErr, "call_failed"), "Dossier task detail failed"), false
			}
			allTasks = append(allTasks, detail)
		}
		linked, err := callValue[artifactList](cycleCtx, a.session, "artifact.list", map[string]any{"project": project.Slug, "task": "", "kind": ""})
		if err != nil {
			_ = a.invalidateSession()
			return unavailable(started, protocolCode(cycleCtx, err, "call_failed"), "Dossier artifact inventory failed"), false
		}
		artifacts = append(artifacts, linked.Artifacts...)
	}

	rows := normalizeTasks(allTasks, artifacts)
	receipt := model.SourceReceipt{Source: "dossier", State: model.SourceOK, ObservedAt: started, DurationMS: max(0, a.now().Sub(started).Milliseconds())}
	return Result{Tasks: rows, Receipt: receipt}, false
}

func (a *Adapter) invalidateSession() error {
	if a.session == nil {
		return nil
	}
	s := a.session
	a.session = nil
	return s.close(a.waitFor)
}

type session struct {
	child  process
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Reader
	nextID int
}

func newSession(child process) *session {
	stdout := child.Stdout()
	return &session{child: child, stdin: child.Stdin(), stdout: stdout, reader: bufio.NewReader(stdout)}
}

func (s *session) initialize(ctx context.Context) error {
	s.nextID++
	request := rpcRequest{JSONRPC: "2.0", ID: s.nextID, Method: "initialize", Params: map[string]any{
		"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "workbench-control-room", "version": "1"},
	}}
	if err := s.exchange(ctx, request, nil); err != nil {
		return err
	}
	return s.write(rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
}

func (s *session) call(ctx context.Context, name string, arguments map[string]any, target any) error {
	s.nextID++
	request := rpcRequest{JSONRPC: "2.0", ID: s.nextID, Method: "tools/call", Params: map[string]any{"name": name, "arguments": arguments}}
	return s.exchange(ctx, request, target)
}

func (s *session) exchange(ctx context.Context, request rpcRequest, target any) error {
	if err := s.write(request); err != nil {
		return err
	}
	type readResult struct {
		line []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		line, err := s.reader.ReadBytes('\n')
		read <- readResult{line: line, err: err}
	}()
	var result readResult
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result = <-read:
	}
	if result.err != nil {
		return result.err
	}
	var response rpcResponse
	if err := json.Unmarshal(result.line, &response); err != nil {
		return fmt.Errorf("malformed json-rpc response")
	}
	if strings.TrimSpace(string(response.ID)) != strconv.Itoa(request.ID) {
		return fmt.Errorf("mismatched json-rpc response id")
	}
	if response.Error != nil {
		return fmt.Errorf("json-rpc call failed")
	}
	if target == nil {
		return nil
	}
	if response.Result.IsError {
		return fmt.Errorf("mcp tool returned an error")
	}
	payload := response.Result.StructuredContent
	if len(payload) == 0 || string(payload) == "null" {
		for _, content := range response.Result.Content {
			if content.Type == "text" && content.Text != "" {
				payload = []byte(content.Text)
				break
			}
		}
	}
	if len(payload) == 0 {
		return fmt.Errorf("mcp tool result had no structured content")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("malformed mcp tool content")
	}
	return nil
}

func (s *session) write(request rpcRequest) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = s.stdin.Write(data)
	return err
}

func (s *session) close(timeout time.Duration) error {
	_ = s.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- s.child.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		_ = s.stdout.Close()
		return err
	case <-timer.C:
		_ = s.child.Kill()
		err := <-done
		_ = s.stdout.Close()
		return err
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
}

func callValue[T any](ctx context.Context, session *session, name string, args map[string]any) (T, error) {
	var value T
	err := session.call(ctx, name, args, &value)
	return value, err
}

type projectList struct {
	Projects []struct {
		Slug string `json:"slug"`
	} `json:"projects"`
}

type phaseList struct {
	Phases []json.RawMessage `json:"phases"`
}

type taskList struct {
	Tasks []taskWire `json:"tasks"`
}

type taskWire struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	ProjectSlug  string    `json:"project_slug"`
	Phase        string    `json:"phase"`
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	Assignee     string    `json:"assignee"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Dependencies []string  `json:"depends_on"`
}

type artifactList struct {
	Artifacts []artifactWire `json:"artifacts"`
}

type artifactWire struct {
	Task      string `json:"task"`
	Kind      string `json:"kind"`
	Reference string `json:"ref"`
	Label     string `json:"label"`
}

func normalizeTasks(tasks []taskWire, artifacts []artifactWire) []model.Task {
	rows := make([]model.Task, 0, len(tasks))
	index := make(map[string]int, len(tasks))
	for _, task := range tasks {
		if task.ID == "" || task.Slug == "" || task.Title == "" || task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() {
			continue
		}
		project := task.ProjectSlug
		if project == "" {
			project = task.Project
		}
		row := model.Task{ID: task.ID, Slug: task.Slug, Title: task.Title, Project: project, Phase: task.Phase, Status: task.Status, Assignee: task.Assignee, Dependencies: append([]string(nil), task.Dependencies...), Blockers: []string{}, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, Artifacts: []model.SafeLink{}, Liveness: model.LivenessUnknown}
		index[row.ID] = len(rows)
		rows = append(rows, row)
	}
	for i := range rows {
		for _, dependency := range rows[i].Dependencies {
			if dependencyIndex, ok := index[dependency]; ok {
				rows[dependencyIndex].Blockers = append(rows[dependencyIndex].Blockers, rows[i].ID)
			}
		}
	}
	for _, artifact := range artifacts {
		taskIndex, ok := index[artifact.Task]
		if !ok {
			continue
		}
		if link, ok := safeArtifact(artifact); ok {
			rows[taskIndex].Artifacts = append(rows[taskIndex].Artifacts, link)
		}
	}
	for i := range rows {
		sort.Strings(rows[i].Dependencies)
		sort.Strings(rows[i].Blockers)
		sort.Slice(rows[i].Artifacts, func(a, b int) bool { return rows[i].Artifacts[a].Label < rows[i].Artifacts[b].Label })
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

func safeArtifact(artifact artifactWire) (model.SafeLink, bool) {
	label := strings.TrimSpace(artifact.Label)
	if label == "" {
		label = strings.TrimSpace(artifact.Kind)
	}
	var link model.SafeLink
	if artifact.Kind == "url" || artifact.Kind == "pr" {
		link = model.SafeLink{Label: label, URL: artifact.Reference}
	} else if artifact.Kind == "file" || artifact.Kind == "doc" {
		link = model.SafeLink{Label: label, Path: artifact.Reference}
	} else {
		return model.SafeLink{}, false
	}
	return link, link.Validate() == nil
}

func unavailable(observed time.Time, code, message string) Result {
	return Result{Receipt: model.SourceReceipt{Source: "dossier", State: model.SourceUnavailable, ObservedAt: observed, ErrorCode: code, Message: message}}
}

func protocolCode(ctx context.Context, err error, fallback string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, io.EOF) {
		return "child_eof"
	}
	return fallback
}
