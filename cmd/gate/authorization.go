package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/authorization"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

func cmdAuthorization(args []string) error {
	if len(args) == 0 {
		return errors.New("authorization: export or promote required")
	}
	switch args[0] {
	case "export":
		return cmdAuthorizationExport(args[1:])
	case "promote":
		return cmdAuthorizationPromote(args[1:])
	default:
		return fmt.Errorf("authorization: unknown verb %q", args[0])
	}
}

func cmdAuthorizationExport(args []string) error {
	fs := flag.NewFlagSet("authorization export", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	run := fs.String("run", "", "newest terminal would_merge run")
	out := fs.String("out", "", "authorization JSON path ('-' for stdout)")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *run == "" || *out == "" {
		return errors.New("authorization export: -run and -out required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	artifact, err := exportAuthorization(e, *run)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *out == "-" {
		_, err = os.Stdout.Write(data)
		return err
	}
	if err := writeAuthorization(*out, data); err != nil {
		return err
	}
	fmt.Println(*out)
	return nil
}

func exportAuthorization(e env, run string) (gateauthorization.Artifact, error) {
	audit, err := e.st.Audit()
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	artifact, err := authorization.Export(audit, run)
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	if !time.Now().Before(artifact.ExpiresAt) {
		return gateauthorization.Artifact{}, errors.New("authorization_expired: Gate action is older than 30 minutes; re-run Gate on the exact head")
	}
	live, err := readLiveGitHub(artifact.Subject.Repo, artifact.Subject.Number, artifact.Subject.HeadSHA)
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	if live.repository != artifact.Subject.Repo {
		return gateauthorization.Artifact{}, errors.New("authorization_repo_mismatch")
	}
	if live.pull.State != "open" {
		return gateauthorization.Artifact{}, errors.New("authorization_pr_not_open")
	}
	if live.pull.HeadSHA != artifact.Subject.HeadSHA {
		return gateauthorization.Artifact{}, errors.New("authorization_head_mismatch")
	}
	if len(live.openPRs) != 1 || live.openPRs[0] != artifact.Subject.Number {
		return gateauthorization.Artifact{}, errors.New("authorization_head_ambiguous")
	}
	confirm, err := e.st.Audit()
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	confirmed, err := authorization.Export(confirm, run)
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	if confirmed.AuthorizationID != artifact.AuthorizationID {
		return gateauthorization.Artifact{}, errors.New("authorization_superseded: Gate state changed during export")
	}
	return artifact, nil
}

func cmdAuthorizationPromote(args []string) error {
	fs := flag.NewFlagSet("authorization promote", flag.ContinueOnError)
	path := fs.String("artifact", "", "GateAuthorizationV1 path")
	runURL := fs.String("run-url", "", "trusted workflow run URL")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *path == "" || *runURL == "" {
		return errors.New("authorization promote: -artifact and -run-url required")
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return fmt.Errorf("authorization promote: read: %w", err)
	}
	artifact, err := gateauthorization.Decode(data)
	if err != nil {
		return err
	}
	live, err := readLiveGitHub(artifact.Subject.Repo, artifact.Subject.Number, artifact.Subject.HeadSHA)
	if err != nil {
		return err
	}
	environment, err := readEnvironment(artifact.Subject.Repo, artifact.TrustRoot.Environment)
	if err != nil {
		return err
	}
	statuses, err := readStatuses(artifact.Subject.Repo, artifact.Subject.HeadSHA)
	if err != nil {
		return err
	}
	facts := authorization.PromotionFacts{
		Now: time.Now(), Repository: os.Getenv("GITHUB_REPOSITORY"),
		DefaultBranch: live.defaultBranch, WorkflowRef: os.Getenv("GITHUB_REF"),
		WorkflowEvent: os.Getenv("GITHUB_EVENT_NAME"),
		GitHubActions: os.Getenv("GITHUB_ACTIONS") == "true",
		Environment:   environment, PullRequest: live.pull,
		OpenPRsForHead: live.openPRs, Statuses: statuses,
	}
	promotion, err := authorization.CheckPromotion(artifact, facts)
	if err != nil {
		return err
	}
	if promotion.Duplicate {
		return errors.New("authorization_duplicate: artifact was already consumed")
	}
	if err := postGateStatus(artifact, promotion.Description, *runURL); err != nil {
		return err
	}
	statuses, err = readStatuses(artifact.Subject.Repo, artifact.Subject.HeadSHA)
	if err != nil {
		return err
	}
	if !statusPostedByActions(statuses, promotion.Description) {
		return errors.New("authorization_status_identity_mismatch: required status was not created by github-actions[bot]")
	}
	printJSON(map[string]any{
		"authorization_id": artifact.AuthorizationID,
		"repo":             artifact.Subject.Repo, "pr": artifact.Subject.Number,
		"head_sha": artifact.Subject.HeadSHA, "status": "success",
	})
	return nil
}

type liveGitHub struct {
	repository    string
	defaultBranch string
	pull          authorization.PullRequest
	openPRs       []int
}

func readLiveGitHub(repo string, number int, headSHA string) (liveGitHub, error) {
	var repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := ghDecode(&repository, "api", "repos/"+repo); err != nil {
		return liveGitHub{}, err
	}
	var pull struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := ghDecode(&pull, "api", fmt.Sprintf("repos/%s/pulls/%d", repo, number)); err != nil {
		return liveGitHub{}, err
	}
	var associated []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := ghDecodeStream(&associated, "api", "--paginate",
		fmt.Sprintf("repos/%s/commits/%s/pulls", repo, headSHA), "--jq", ".[]"); err != nil {
		return liveGitHub{}, err
	}
	open := make([]int, 0, len(associated))
	for _, candidate := range associated {
		if candidate.State == "open" && candidate.Head.SHA == headSHA {
			open = append(open, candidate.Number)
		}
	}
	return liveGitHub{
		repository: repository.FullName, defaultBranch: repository.DefaultBranch,
		pull: authorization.PullRequest{
			Number: pull.Number, State: pull.State, HeadSHA: pull.Head.SHA,
		},
		openPRs: open,
	}, nil
}

func readEnvironment(repo, name string) (authorization.Environment, error) {
	var response struct {
		Name                   string `json:"name"`
		CanAdminsBypass        *bool  `json:"can_admins_bypass"`
		DeploymentBranchPolicy *struct {
			ProtectedBranches bool `json:"protected_branches"`
		} `json:"deployment_branch_policy"`
		ProtectionRules []struct {
			Type              string            `json:"type"`
			PreventSelfReview bool              `json:"prevent_self_review"`
			Reviewers         []json.RawMessage `json:"reviewers"`
		} `json:"protection_rules"`
	}
	if err := ghDecode(&response, "api", fmt.Sprintf("repos/%s/environments/%s", repo, name)); err != nil {
		return authorization.Environment{}, err
	}
	environment := authorization.Environment{
		Name: response.Name, AdminBypassKnown: response.CanAdminsBypass != nil,
		ProtectedBranchesOnly: response.DeploymentBranchPolicy != nil &&
			response.DeploymentBranchPolicy.ProtectedBranches,
	}
	if response.CanAdminsBypass != nil {
		environment.CanAdminsBypass = *response.CanAdminsBypass
	}
	for _, rule := range response.ProtectionRules {
		if rule.Type != "required_reviewers" {
			continue
		}
		environment.RequiredReviewers += len(rule.Reviewers)
		environment.PreventSelfReview = rule.PreventSelfReview
	}
	return environment, nil
}

func readStatuses(repo, headSHA string) ([]authorization.Status, error) {
	var response []struct {
		Context     string `json:"context"`
		State       string `json:"state"`
		Description string `json:"description"`
		Creator     struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"creator"`
	}
	if err := ghDecodeStream(&response, "api", "--paginate",
		fmt.Sprintf("repos/%s/commits/%s/statuses", repo, headSHA), "--jq", ".[]"); err != nil {
		return nil, err
	}
	out := make([]authorization.Status, 0, len(response))
	for _, status := range response {
		out = append(out, authorization.Status{
			Context: status.Context, State: status.State, Body: status.Description,
			Creator: authorization.Actor{Login: status.Creator.Login, Type: status.Creator.Type},
		})
	}
	return out, nil
}

func postGateStatus(artifact gateauthorization.Artifact, description, runURL string) error {
	return ghRun("api", "-X", "POST",
		fmt.Sprintf("repos/%s/statuses/%s", artifact.Subject.Repo, artifact.Subject.HeadSHA),
		"-f", "state=success", "-f", "context=gate",
		"-f", "description="+description, "-f", "target_url="+runURL)
}

func statusPostedByActions(statuses []authorization.Status, description string) bool {
	for _, status := range statuses {
		if status.Context == "gate" && status.State == "success" && status.Body == description &&
			status.Creator.Login == "github-actions[bot]" && status.Creator.Type == "Bot" {
			return true
		}
	}
	return false
}

func ghDecode(value any, args ...string) error {
	data, err := ghOutput(args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("authorization github decode: %w", err)
	}
	return nil
}

func ghDecodeStream(target any, args ...string) error {
	data, err := ghOutput(args...)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var raws []json.RawMessage
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		raws = append(raws, raw)
	}
	joined, err := json.Marshal(raws)
	if err != nil {
		return err
	}
	return json.Unmarshal(joined, target)
}

func ghOutput(args ...string) ([]byte, error) {
	command := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("authorization github read: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func ghRun(args ...string) error {
	command := exec.Command("gh", args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("authorization github write: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func writeAuthorization(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return errors.New("authorization output exists with different content")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("authorization output check: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gate-authorization-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
