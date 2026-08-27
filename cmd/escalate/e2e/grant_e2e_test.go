package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts"
	"github.com/itsHabib/workbench/contracts/grantrequest"
)

// TestSlackTapMintsBoundGrantAndReleasesWaitingGate is the isolated whole
// thread: a real `gate gate -slack` appends and waits; a real `escalate serve`
// receives one signed Slack tap; Gate independently verifies it, mints the
// exact T0 grant, and the waiting Gate process leaves the wait and begins its
// evaluation. A fake read-only gh serves the stable head and then deliberately
// refuses the evidence sweep, proving continuation without touching GitHub or
// any live Gate state.
func TestSlackTapMintsBoundGrantAndReleasesWaitingGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated e2e uses a POSIX gh fixture")
	}
	stateDir, keyDir, head, env := grantE2EFixture(t)

	var gateOut, gateErr bytes.Buffer
	gateCmd := exec.Command(realGateBin, "gate", "-state", stateDir, "-key", keyDir, "-repo", "o/r", "-pr", "7", "-slack", "-stamp=false")
	gateCmd.Env = env
	gateCmd.Stdout = &gateOut
	gateCmd.Stderr = &gateErr
	if err := gateCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if gateCmd.Process != nil {
			_ = gateCmd.Process.Kill()
			_, _ = gateCmd.Process.Wait()
		}
	})
	request := waitForGrantRequest(t, stateDir)

	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	serveCmd := exec.Command(escalateBin, "serve", "-addr", addr, "-gate", realGateBin, "-state", stateDir)
	serveCmd.Env = env
	serveCmd.Stderr = os.Stderr
	if err := serveCmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, baseURL: "http://" + addr, stateDir: stateDir, secret: signingSecret, cmd: serveCmd}
	t.Cleanup(s.stop)
	s.waitReady(port)
	body := callbackBody(grantrequest.ActionApprove, request.ID, operator)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	status, ack := s.post(sign(signingSecret, ts, body), ts, body)
	if status != http.StatusOK || !strings.Contains(ack, "exact T0") {
		t.Fatalf("Slack tap status=%d ack=%q", status, ack)
	}
	assertBoundGrant(t, waitForGrantTerminal(t, stateDir, request.ID), head)
	waitForGateContinuation(t, gateCmd, &gateErr)
}

func grantE2EFixture(t *testing.T) (string, string, string, []string) {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	keyDir := filepath.Join(root, "keys")
	head := strings.Repeat("a", 40)
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ghPath := filepath.Join(binDir, "gh")
	ghScript := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "api" ] && [ "$2" = "repos/o/r/pulls/7" ]; then
  echo '{"base":{"sha":"%s"},"head":{"sha":"%s"}}'
  exit 0
fi
echo 'fixture stops after Slack grant wait' >&2
exit 9
`, strings.Repeat("c", 40), head)
	if err := os.WriteFile(ghPath, []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GATE_KEY="+keyDir,
		"SLACK_SIGNING_SECRET="+signingSecret,
		"ESCALATE_ALLOWED_SLACK_USERS="+operator.ID,
	)
	seed := exec.Command(realGateBin, "grant", "-init", "-state", stateDir, "-key", keyDir, "-repo", "fixture/seed", "-max-tier", "T0", "-ttl", "1m")
	seed.Env = env
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed state: %v: %s", err, out)
	}
	return stateDir, keyDir, head, env
}

func assertBoundGrant(t *testing.T, grant contracts.Envelope, head string) {
	t.Helper()
	var bodyGrant struct {
		Repo            string `json:"repo"`
		MaxTier         string `json:"max_tier"`
		MaxCycles       int    `json:"max_cycles"`
		BoundHead       string `json:"bound_head"`
		BoundPR         int    `json:"bound_pr"`
		AuthorizationID string `json:"authorization_id"`
		MintedBy        string `json:"minted_by"`
	}
	if err := json.Unmarshal(grant.Body, &bodyGrant); err != nil {
		t.Fatal(err)
	}
	if bodyGrant.Repo != "o/r" || bodyGrant.MaxTier != "T0" || bodyGrant.MaxCycles != 3 || bodyGrant.BoundHead != head || bodyGrant.BoundPR != 7 || bodyGrant.MintedBy != wantWho || !strings.HasPrefix(bodyGrant.AuthorizationID, "gau_") {
		t.Fatalf("bound grant widened or lost provenance: %+v", bodyGrant)
	}
}

func waitForGateContinuation(t *testing.T, gateCmd *exec.Cmd, gateErr *bytes.Buffer) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- gateCmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("fixture expected the post-grant evidence read to stop Gate")
		}
		if !strings.Contains(gateErr.String(), "fixture stops after Slack grant wait") {
			t.Fatalf("Gate did not continue into evidence after the grant; stderr:\n%s", gateErr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("waiting Gate did not resume after grant; stderr:\n%s", gateErr.String())
	}
}

func waitForGrantRequest(t *testing.T, stateDir string) contracts.Envelope {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, artifact := range readGateLog(t, stateDir) {
			if artifact.Kind == contracts.KindGrantRequest {
				return artifact
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for grant request")
	return contracts.Envelope{}
}

func waitForGrantTerminal(t *testing.T, stateDir, requestID string) contracts.Envelope {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, artifact := range readGateLog(t, stateDir) {
			if artifact.Kind != contracts.KindGrant || !containsString(artifact.Parents, requestID) {
				continue
			}
			return artifact
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for bound grant")
	return contracts.Envelope{}
}

func readGateLog(t *testing.T, stateDir string) []contracts.Envelope {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stateDir, "log.jsonl"))
	if err != nil {
		return nil
	}
	var artifacts []contracts.Envelope
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var artifact contracts.Envelope
		if err := json.Unmarshal([]byte(line), &artifact); err != nil {
			t.Fatalf("decode Gate log: %v", err)
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
