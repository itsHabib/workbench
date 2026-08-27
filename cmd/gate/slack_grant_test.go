package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/grantrequest"
	"github.com/itsHabib/workbench/slackauth"
)

func TestGateIndependentlyVerifiesSlackCallbackAndUser(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	payload := `{"type":"block_actions","user":{"id":"U123","username":"operator"},"actions":[{"action_id":"gate_t0_approve","value":"gqr_abcd"}]}`
	body := []byte(url.Values{"payload": {payload}}.Encode())
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := signSlackTestCallback("secret", timestamp, body)
	interaction, err := verifyGrantInteraction([]byte("secret"), []string{"U123"}, signature, timestamp, body, now)
	if err != nil || interaction.UserID != "U123" {
		t.Fatalf("valid callback = %+v, %v", interaction, err)
	}
	if _, err := verifyGrantInteraction([]byte("secret"), []string{"U123"}, "v0=forged", timestamp, body, now); err == nil {
		t.Fatal("forged signature reached Gate authority path")
	}
	if _, err := verifyGrantInteraction([]byte("secret"), []string{"U999"}, signature, timestamp, body, now); err == nil {
		t.Fatal("unallowlisted Slack user reached Gate authority path")
	}
	stale := strconv.FormatInt(now.Add(-slackauth.MaxSkew-time.Second).Unix(), 10)
	if _, err := verifyGrantInteraction([]byte("secret"), []string{"U123"}, signSlackTestCallback("secret", stale, body), stale, body, now); err == nil {
		t.Fatal("stale signed callback reached Gate authority path")
	}
}

func signSlackTestCallback(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "v0:%s:%s", timestamp, body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGrantCallbackMintsOneExactT0Grant(t *testing.T) {
	e, requestArt, request := slackRequestFixture(t)
	interaction := grantInteraction(grantrequest.ActionApprove, requestArt.ID)
	result, code, err := applyGrantCallback(e, interaction, fixedHead(request.Request.Subject.HeadSHA))
	if err != nil {
		t.Fatal(err)
	}
	if code != codeMerge || result.Outcome != "granted" || result.Grant == "" {
		t.Fatalf("result=%+v code=%d", result, code)
	}
	grant, err := capability.CheckSubject(
		e.st, e.keyPath, result.Grant, request.Request.Subject.Repo, "merge",
		request.Request.Subject.HeadSHA, request.Request.Subject.Number, e.now,
	)
	if err != nil {
		t.Fatalf("exact grant refused: %v", err)
	}
	if grant.MaxTier != "T0" || grant.MaxCycles != 3 || grant.AuthorizationID != request.AuthorizationID || grant.MintedBy != "@operator (U123)" {
		t.Fatalf("grant widened or lost provenance: %+v", grant)
	}
	if _, err := capability.Check(e.st, e.keyPath, result.Grant, request.Request.Subject.Repo, "merge", e.now); !errors.Is(err, capability.ErrHeadMismatch) {
		t.Fatalf("unbound use = %v, want exact-subject refusal", err)
	}
	if _, err := capability.CheckSubject(e.st, e.keyPath, result.Grant, request.Request.Subject.Repo, "merge", strings.Repeat("b", 40), request.Request.Subject.Number, e.now); !errors.Is(err, capability.ErrHeadMismatch) {
		t.Fatalf("moved subject = %v, want head mismatch", err)
	}
}

func TestGrantCallbackDeniesExpiresAndRefusesMovedHead(t *testing.T) {
	tests := []struct {
		name     string
		action   string
		advance  time.Duration
		liveHead string
		outcome  string
		code     int
	}{
		{name: "deny", action: grantrequest.ActionDeny, outcome: grantrequest.DecisionDenied, code: codeBlocked},
		{name: "expired", action: grantrequest.ActionApprove, advance: grantrequest.MaxValidity, outcome: grantrequest.DecisionExpired, code: codeRefused},
		{name: "moved head", action: grantrequest.ActionApprove, liveHead: strings.Repeat("b", 40), outcome: grantrequest.DecisionStale, code: codeRefused},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e, requestArt, request := slackRequestFixture(t)
			at := e.now().Add(test.advance)
			e.now = func() time.Time { return at }
			head := test.liveHead
			if head == "" {
				head = request.Request.Subject.HeadSHA
			}
			result, code, err := applyGrantCallback(e, grantInteraction(test.action, requestArt.ID), fixedHead(head))
			if err != nil {
				t.Fatal(err)
			}
			if code != test.code || result.Outcome != test.outcome {
				t.Fatalf("result=%+v code=%d", result, code)
			}
			terminals := requestTerminals(t, e, requestArt)
			if len(terminals) != 1 || terminals[0].Kind != state.KindGrantDenied {
				t.Fatalf("terminals = %+v", terminals)
			}
		})
	}
}

func TestGrantCallbackIsSingleUseAcrossConcurrentApproveAndDeny(t *testing.T) {
	e, requestArt, request := slackRequestFixture(t)
	// Create the signing key before the race, matching a live Gate state and
	// keeping this test about terminal atomicity rather than key bootstrap.
	if _, err := capability.Mint(e.st, e.keyPath, "other/repo", "merge", "T0", 1, "fixture", time.Minute, e.now); err != nil {
		t.Fatal(err)
	}
	actions := []string{grantrequest.ActionApprove, grantrequest.ActionDeny}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, action := range actions {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			<-start
			_, _, _ = applyGrantCallback(e, grantInteraction(action, requestArt.ID), fixedHead(request.Request.Subject.HeadSHA))
		}(action)
	}
	close(start)
	wg.Wait()
	terminals := requestTerminals(t, e, requestArt)
	if len(terminals) != 1 {
		t.Fatalf("approve/deny race recorded %d terminals: %+v", len(terminals), terminals)
	}
	if terminals[0].Kind != state.KindGrant && terminals[0].Kind != state.KindGrantDenied {
		t.Fatalf("unexpected terminal kind %s", terminals[0].Kind)
	}
	result, code, err := applyGrantCallback(e, grantInteraction(grantrequest.ActionApprove, requestArt.ID), fixedHead(request.Request.Subject.HeadSHA))
	if err != nil {
		t.Fatal(err)
	}
	if code != codeRefused || !strings.HasPrefix(result.Outcome, "already_") {
		t.Fatalf("replay result=%+v code=%d", result, code)
	}
}

func TestGrantCallbackRejectsUnknownActionWithoutClosingRequest(t *testing.T) {
	e, requestArt, request := slackRequestFixture(t)
	_, code, err := applyGrantCallback(e, grantInteraction("widen_to_t3", requestArt.ID), fixedHead(request.Request.Subject.HeadSHA))
	if err == nil || code != codeError {
		t.Fatalf("unknown action err=%v code=%d", err, code)
	}
	if terminals := requestTerminals(t, e, requestArt); len(terminals) != 0 {
		t.Fatalf("unknown action closed request: %+v", terminals)
	}
}

func slackRequestFixture(t *testing.T) (env, state.Artifact, grantrequest.RequestArtifact) {
	t.Helper()
	e := testEnv(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	request, err := grantrequest.New(grantrequest.Subject{
		Repo: "itsHabib/workbench", Number: 245, HeadSHA: strings.Repeat("a", 40),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := e.st.Append(state.KindGrantRequest, "run_0123456789abcdef", nil, request)
	if err != nil {
		t.Fatal(err)
	}
	return e, artifact, request
}

func grantInteraction(action, request string) slackauth.Interaction {
	return slackauth.Interaction{
		UserID: "U123", Username: "operator", ActionID: action, Value: request,
	}
}

func fixedHead(head string) func(evidence.PRRef) (string, error) {
	return func(evidence.PRRef) (string, error) { return head, nil }
}

func requestTerminals(t *testing.T, e env, request state.Artifact) []state.Artifact {
	t.Helper()
	artifacts, err := e.st.Run(request.Run)
	if err != nil {
		t.Fatal(err)
	}
	var terminals []state.Artifact
	for _, artifact := range artifacts {
		if artifact.Kind != state.KindGrant && artifact.Kind != state.KindGrantDenied {
			continue
		}
		for _, parent := range artifact.Parents {
			if parent == request.ID {
				terminals = append(terminals, artifact)
			}
		}
	}
	return terminals
}

func TestGrantDenialBodyConforms(t *testing.T) {
	e, requestArt, request := slackRequestFixture(t)
	_, _, err := denyGrantRequest(e, requestArt, request, grantrequest.DecisionDenied, "@operator (U123)", "denied in Slack", e.now())
	if err != nil {
		t.Fatal(err)
	}
	terminal := requestTerminals(t, e, requestArt)[0]
	var denial grantrequest.Denial
	if err := json.Unmarshal(terminal.Body, &denial); err != nil {
		t.Fatal(err)
	}
	if err := grantrequest.ValidateDenial(denial); err != nil {
		t.Fatal(err)
	}
}

func TestRequestSlackGrantRecordsExpiryWhileWaiting(t *testing.T) {
	e := testEnv(t)
	issued := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	calls := 0
	e.now = func() time.Time {
		calls++
		if calls == 1 {
			return issued
		}
		return issued.Add(grantrequest.MaxValidity)
	}
	head := strings.Repeat("a", 40)
	_, _, err := requestSlackGrant(e, "itsHabib/workbench", 245, fixedHead(head), time.Nanosecond)
	if err == nil || !strings.Contains(err.Error(), grantrequest.DecisionExpired) {
		t.Fatalf("expiry result = %v, want %s", err, grantrequest.DecisionExpired)
	}
	artifacts, err := e.st.List(func(artifact state.Artifact) bool {
		return artifact.Kind == state.KindGrantRequest || artifact.Kind == state.KindGrantDenied
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || artifacts[0].Kind != state.KindGrantRequest || artifacts[1].Kind != state.KindGrantDenied {
		t.Fatalf("request expiry artifacts = %+v", artifacts)
	}
	if len(artifacts[1].Parents) != 1 || artifacts[1].Parents[0] != artifacts[0].ID {
		t.Fatalf("denial parents = %v, request = %s", artifacts[1].Parents, artifacts[0].ID)
	}
	var denial grantrequest.Denial
	if err := json.Unmarshal(artifacts[1].Body, &denial); err != nil {
		t.Fatal(err)
	}
	if denial.Decision != grantrequest.DecisionExpired || denial.Who != "gate" {
		t.Fatalf("denial = %+v", denial)
	}
}
