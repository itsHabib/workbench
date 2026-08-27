package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/grantrequest"
	"github.com/itsHabib/workbench/slackauth"
)

const slackGrantPollInterval = 500 * time.Millisecond

type grantCallbackResult struct {
	Outcome   string `json:"outcome"`
	Request   string `json:"request"`
	Grant     string `json:"grant,omitempty"`
	Repo      string `json:"repo"`
	PR        int    `json:"pr"`
	HeadSHA   string `json:"head_sha"`
	Who       string `json:"who"`
	Reason    string `json:"reason,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

// cmdGrantCallback is the only Slack authority ingress. It accepts the
// original raw Slack body on stdin plus the signature headers Escalate saw,
// then independently authenticates and authorizes before touching Gate state.
func cmdGrantCallback(args []string) error {
	fs := flag.NewFlagSet("grant-callback", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	signature := fs.String("signature", "", "original X-Slack-Signature header")
	timestamp := fs.String("timestamp", "", "original X-Slack-Request-Timestamp header")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	secret := os.Getenv("SLACK_SIGNING_SECRET")
	if secret == "" {
		return errors.New("grant-callback: SLACK_SIGNING_SECRET is required")
	}
	allowed := splitCommaList(os.Getenv("ESCALATE_ALLOWED_SLACK_USERS"))
	if len(allowed) == 0 {
		return errors.New("grant-callback: ESCALATE_ALLOWED_SLACK_USERS is required")
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, slackauth.MaxBody+1))
	if err != nil {
		return fmt.Errorf("grant-callback: read body: %w", err)
	}
	interaction, err := verifyGrantInteraction(
		[]byte(secret), allowed, *signature, *timestamp, body, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	result, code, err := applyGrantCallback(e, interaction, evidence.HeadSHA)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	if code != codeMerge {
		os.Exit(code)
	}
	return nil
}

func verifyGrantInteraction(secret []byte, allowed []string, signature, timestamp string, body []byte, now time.Time) (slackauth.Interaction, error) {
	interaction, err := slackauth.Authenticate(secret, signature, timestamp, body, now)
	if err != nil {
		return slackauth.Interaction{}, fmt.Errorf("grant-callback: %w", err)
	}
	if !slackauth.AllowUsers(allowed, interaction.UserID) {
		return slackauth.Interaction{}, fmt.Errorf("grant-callback: Slack user %s is not authorized", interaction.UserID)
	}
	return interaction, nil
}

func splitCommaList(value string) []string {
	var values []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

// applyGrantCallback maps one already-authenticated interaction to the narrow
// Gate policy. All authority-bearing fields come from the request artifact;
// the interaction contributes only action, request id, and verified identity.
func applyGrantCallback(e env, interaction slackauth.Interaction, headSHA func(evidence.PRRef) (string, error)) (grantCallbackResult, int, error) {
	requestArt, request, err := loadGrantRequest(e.st, interaction.Value)
	if err != nil {
		return grantCallbackResult{}, codeError, err
	}
	if terminal, ok, err := grantRequestTerminal(e.st, requestArt); err != nil {
		return grantCallbackResult{}, codeError, err
	} else if ok {
		return terminalResult(requestArt, request, terminal, interaction.Actor()), codeRefused, nil
	}
	now := e.now().UTC()
	if !now.Before(request.Request.ExpiresAt) {
		return denyGrantRequest(e, requestArt, request, grantrequest.DecisionExpired, interaction.Actor(), "request expired before approval", now)
	}
	if interaction.ActionID == grantrequest.ActionDeny {
		return denyGrantRequest(e, requestArt, request, grantrequest.DecisionDenied, interaction.Actor(), "denied in Slack", now)
	}
	if interaction.ActionID != grantrequest.ActionApprove {
		return grantCallbackResult{}, codeError, fmt.Errorf("grant-callback: unsupported action_id %q", interaction.ActionID)
	}
	liveHead, err := headSHA(evidence.PRRef{Repo: request.Request.Subject.Repo, Number: request.Request.Subject.Number})
	if err != nil {
		return grantCallbackResult{}, codeError, fmt.Errorf("grant-callback: read live head: %w", err)
	}
	if liveHead != request.Request.Subject.HeadSHA {
		return denyGrantRequest(e, requestArt, request, grantrequest.DecisionStale, interaction.Actor(), fmt.Sprintf("head moved from %s to %s", request.Request.Subject.HeadSHA, liveHead), now)
	}
	ttl := request.Request.ExpiresAt.Sub(now)
	grant, err := capability.MintBoundOnce(
		e.st, e.keyPath,
		request.Request.Subject.Repo, request.Request.Action,
		request.Request.MaxTier, request.Request.MaxCycles,
		interaction.Actor(), ttl,
		request.Request.Subject.HeadSHA, request.Request.Subject.Number,
		request.AuthorizationID, requestArt.Run, requestArt.ID,
		func() time.Time { return now },
	)
	if errors.Is(err, state.ErrAlreadyExists) {
		terminal, ok, readErr := grantRequestTerminal(e.st, requestArt)
		if readErr != nil {
			return grantCallbackResult{}, codeError, readErr
		}
		if !ok {
			return grantCallbackResult{}, codeError, errors.New("grant-callback: terminal race won but no terminal artifact is readable")
		}
		return terminalResult(requestArt, request, terminal, interaction.Actor()), codeRefused, nil
	}
	if err != nil {
		return grantCallbackResult{}, codeError, err
	}
	return grantResult("granted", requestArt, request, interaction.Actor(), grant.ID, ""), codeMerge, nil
}

func loadGrantRequest(st *state.Store, id string) (state.Artifact, grantrequest.RequestArtifact, error) {
	artifact, err := st.Get(id)
	if err != nil {
		return state.Artifact{}, grantrequest.RequestArtifact{}, fmt.Errorf("grant-callback: request %s: %w", id, err)
	}
	if artifact.Kind != state.KindGrantRequest {
		return state.Artifact{}, grantrequest.RequestArtifact{}, fmt.Errorf("grant-callback: artifact %s is %s, want %s", id, artifact.Kind, state.KindGrantRequest)
	}
	var request grantrequest.RequestArtifact
	if err := json.Unmarshal(artifact.Body, &request); err != nil {
		return state.Artifact{}, grantrequest.RequestArtifact{}, fmt.Errorf("grant-callback: decode request: %w", err)
	}
	if err := grantrequest.Validate(request); err != nil {
		return state.Artifact{}, grantrequest.RequestArtifact{}, err
	}
	return artifact, request, nil
}

func denyGrantRequest(e env, requestArt state.Artifact, request grantrequest.RequestArtifact, decision, who, reason string, at time.Time) (grantCallbackResult, int, error) {
	denial := grantrequest.Denial{
		SchemaVersion: grantrequest.SchemaVersion, Request: request,
		Decision: decision, Who: who, At: at.UTC(), Reason: reason,
	}
	if err := grantrequest.ValidateDenial(denial); err != nil {
		return grantCallbackResult{}, codeError, err
	}
	_, err := e.st.AppendIfAbsentParentKinds(
		state.KindGrantDenied,
		[]string{state.KindGrant, state.KindGrantDenied},
		requestArt.Run, requestArt.ID, []string{requestArt.ID}, denial,
	)
	if errors.Is(err, state.ErrAlreadyExists) {
		terminal, ok, readErr := grantRequestTerminal(e.st, requestArt)
		if readErr != nil {
			return grantCallbackResult{}, codeError, readErr
		}
		if !ok {
			return grantCallbackResult{}, codeError, errors.New("grant-callback: terminal race won but no terminal artifact is readable")
		}
		return terminalResult(requestArt, request, terminal, who), codeRefused, nil
	}
	if err != nil {
		return grantCallbackResult{}, codeError, err
	}
	code := codeBlocked
	if decision != grantrequest.DecisionDenied {
		code = codeRefused
	}
	return grantResult(decision, requestArt, request, who, "", reason), code, nil
}

func grantRequestTerminal(st *state.Store, request state.Artifact) (state.Artifact, bool, error) {
	artifacts, err := st.Run(request.Run)
	if err != nil {
		return state.Artifact{}, false, err
	}
	for _, artifact := range artifacts {
		if artifact.Kind != state.KindGrant && artifact.Kind != state.KindGrantDenied {
			continue
		}
		if stateArtifactHasParent(artifact, request.ID) {
			return artifact, true, nil
		}
	}
	return state.Artifact{}, false, nil
}

func terminalResult(requestArt state.Artifact, request grantrequest.RequestArtifact, terminal state.Artifact, who string) grantCallbackResult {
	if terminal.Kind == state.KindGrant {
		return grantResult("already_granted", requestArt, request, who, terminal.ID, "request already resolved")
	}
	var denial grantrequest.Denial
	if err := json.Unmarshal(terminal.Body, &denial); err == nil {
		return grantResult("already_"+denial.Decision, requestArt, request, who, "", denial.Reason)
	}
	return grantResult("already_denied", requestArt, request, who, "", "request already resolved")
}

func grantResult(outcome string, requestArt state.Artifact, request grantrequest.RequestArtifact, who, grantID, reason string) grantCallbackResult {
	return grantCallbackResult{
		Outcome: outcome, Request: requestArt.ID, Grant: grantID,
		Repo: request.Request.Subject.Repo, PR: request.Request.Subject.Number,
		HeadSHA: request.Request.Subject.HeadSHA, Who: who, Reason: reason,
		ExpiresAt: request.Request.ExpiresAt.Format(time.RFC3339),
	}
}

// requestSlackGrant appends the inert request and waits for its one terminal
// fact. Expiry is itself recorded atomically so an abandoned request does not
// remain visually actionable forever.
func requestSlackGrant(e env, repo string, pr int, headSHA func(evidence.PRRef) (string, error), poll time.Duration) (state.Artifact, grantrequest.RequestArtifact, error) {
	head, err := headSHA(evidence.PRRef{Repo: repo, Number: pr})
	if err != nil {
		return state.Artifact{}, grantrequest.RequestArtifact{}, err
	}
	request, err := grantrequest.New(
		grantrequest.Subject{Repo: repo, Number: pr, HeadSHA: head},
		e.now().UTC(),
	)
	if err != nil {
		return state.Artifact{}, grantrequest.RequestArtifact{}, err
	}
	run := state.NewRunID()
	requestArt, err := e.st.Append(state.KindGrantRequest, run, nil, request)
	if err != nil {
		return state.Artifact{}, grantrequest.RequestArtifact{}, err
	}
	if poll <= 0 {
		poll = slackGrantPollInterval
	}
	fmt.Fprintf(os.Stderr, "gate: waiting for Slack T0 approval of %s#%d at %s (request %s, expires %s)\n",
		repo, pr, head, requestArt.ID, request.Request.ExpiresAt.Format(time.RFC3339))
	for {
		terminal, ok, err := grantRequestTerminal(e.st, requestArt)
		if err != nil {
			return state.Artifact{}, request, err
		}
		if ok && terminal.Kind == state.KindGrant {
			return terminal, request, nil
		}
		if ok {
			var denial grantrequest.Denial
			if err := json.Unmarshal(terminal.Body, &denial); err != nil {
				return state.Artifact{}, request, err
			}
			return state.Artifact{}, request, fmt.Errorf("slack grant %s: %s", denial.Decision, denial.Reason)
		}
		now := e.now().UTC()
		if !now.Before(request.Request.ExpiresAt) {
			_, _, err := denyGrantRequest(e, requestArt, request, grantrequest.DecisionExpired, "gate", "request expired while waiting for Slack", now)
			if err != nil {
				return state.Artifact{}, request, err
			}
			continue
		}
		time.Sleep(poll)
	}
}
