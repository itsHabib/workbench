package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// Exercise the real gate and authenticated callback commands, using only a
// throwaway ledger/key and fixture gh. No Slack transport or live key is used.
func TestSlackHeadMoveAfterApprovalIsCapabilityRefusal(t *testing.T) {
	t.Setenv("GATE_ANCHOR_RECORD", "")
	e := discoveryFixtureTools(t)
	t.Setenv("GO_DISCOVERY_FAILURE", "moved before view")
	wantState, wantKey, wantFloor := e.stateDir, filepath.Dir(e.keyPath), e.floorBin
	root := filepath.Dir(e.stateDir)
	t.Chdir(root)
	e.stateDir, e.keyPath = "state", filepath.Join("keys", "grant.key")
	var err error
	e.floorBin, err = filepath.Rel(root, e.floorBin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Append(state.KindEvidence, state.NewRunID(), nil, "fixture initialization"); err != nil {
		t.Fatal(err)
	}
	out, code := runSlackGateWithFixtureApproval(t, e)
	if code != codeRefused {
		t.Fatalf("approved Slack head moved: exit %d, want refusal %d: %s", code, codeRefused, out)
	}
	var res gateResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if res.Outcome != "capability_refused" || res.Code != capability.ErrHeadMismatch.Error() {
		t.Fatalf("head binding failure lost its capability classification: %s", out)
	}
	for _, field := range []string{`"error"`, `"grant_discovery"`, `"action"`, `"hash"`, `"stamp"`} {
		if bytes.Contains(out, []byte(field)) {
			t.Fatalf("Slack refusal exposed an error, discovery or authority: %s", out)
		}
	}
	if res.Escape == nil || !strings.Contains(res.Escape.Why, "Slack") || res.RetryHelps == nil || *res.RetryHelps {
		t.Fatalf("head movement needs fresh approval, not a blind retry: %s", out)
	}
	for _, argument := range []string{"gate gate", "-slack=true", "-repo=o/r", "-pr=7", "-stamp=false", "-model-backend=fixture-invalid", wantState, wantKey, wantFloor} {
		if !strings.Contains(res.Escape.Next, argument) {
			t.Fatalf("fresh Slack request lost %q: %s", argument, res.Escape.Next)
		}
	}
	if strings.Contains(res.Escape.Next, "discover-grant") || strings.Contains(res.Escape.Next, "-grant=") {
		t.Fatalf("recovery reused stale authority: %s", res.Escape.Next)
	}
	assertRunAborted(t, e, res.Run, verify.Subject{Repo: "o/r", Number: 7, HeadSHA: discoveryHead}, "PR head changed after selection")
	if got := mustCycleCount(t, e, verify.Subject{Repo: "o/r", Number: 7, HeadSHA: discoveryHead}); got != 0 {
		t.Fatalf("head refusal consumed %d cycles", got)
	}
}

func TestSlackHeadRefusalDoesNotHideRunErrors(t *testing.T) {
	moved := matchBoundView(discoveryHead, json.RawMessage(`{"headRefOid":"cccccccccccccccccccccccccccccccccccccccc"}`))
	writeFailure := errors.New("record run abort: fixture state unavailable")
	for _, cause := range []error{writeFailure, errors.Join(moved, writeFailure)} {
		res, code, err := slackBoundResult(env{}, nil, gateResult{}, codeError, cause)
		if err != cause || code != codeError || res.Outcome != "" || res.Escape != nil {
			t.Fatalf("state failure became a capability refusal: result=%+v code=%d err=%v", res, code, err)
		}
	}
}

func runSlackGateWithFixtureApproval(t *testing.T, e env) ([]byte, int) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"gate", "-repo", "o/r", "-pr", "7", "-slack", "-model-backend", "fixture-invalid", "-stamp=false",
		"-state", e.stateDir, "-key", filepath.Dir(e.keyPath), "-floor", e.floorBin}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = append(os.Environ(), "GO_WANT_DISCOVERY_COMMAND=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if cmd.ProcessState == nil {
			_ = cmd.Wait()
		}
	})
	request := awaitSlackFixtureRequest(ctx, t, e)
	approveSlackFixtureCLI(ctx, t, executable, e, request.ID)
	err = cmd.Wait()
	if ctx.Err() != nil || cmd.ProcessState == nil {
		t.Fatalf("Slack fixture timed out: %v: %s", err, stderr.String())
	}
	return stdout.Bytes(), cmd.ProcessState.ExitCode()
}

func awaitSlackFixtureRequest(ctx context.Context, t *testing.T, e env) state.Artifact {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		requests, err := e.st.List(func(a state.Artifact) bool { return a.Kind == state.KindGrantRequest })
		if err != nil {
			t.Fatal(err)
		}
		if len(requests) == 1 {
			return requests[0]
		}
		select {
		case <-ctx.Done():
			t.Fatal("Slack fixture did not record a request")
		case <-ticker.C:
		}
	}
}

func approveSlackFixtureCLI(ctx context.Context, t *testing.T, executable string, e env, requestID string) {
	t.Helper()
	payload := fmt.Sprintf(`{"type":"block_actions","user":{"id":"U123","username":"operator"},"actions":[{"action_id":"gate_t0_approve","value":%q}]}`, requestID)
	body := []byte(url.Values{"payload": {payload}}.Encode())
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	const secret = "disposable-fixture-secret"
	cmd := exec.CommandContext(ctx, executable, "grant-callback", "-timestamp", timestamp,
		"-signature", signSlackTestCallback(secret, timestamp, body),
		"-state", e.stateDir, "-key", filepath.Dir(e.keyPath), "-floor", e.floorBin)
	cmd.Env = append(os.Environ(), "GO_WANT_DISCOVERY_COMMAND=1", "SLACK_SIGNING_SECRET="+secret, "ESCALATE_ALLOWED_SLACK_USERS=U123")
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("approve disposable request: %v: %s", err, out)
	}
	var res grantCallbackResult
	if err := json.Unmarshal(out, &res); err != nil || res.Outcome != "granted" {
		t.Fatalf("fixture callback did not grant: %v: %s", err, out)
	}
	grant, err := capability.CheckSubject(e.st, e.keyPath, res.Grant, "o/r", "merge", discoveryHead, 7, time.Now)
	if err != nil || grant.MaxTier != "T0" || grant.MaxCycles != 3 || grant.BoundHead != discoveryHead || grant.BoundPR != 7 {
		t.Fatalf("fixture approval widened exact T0 authority: %+v, %v", grant, err)
	}
}
