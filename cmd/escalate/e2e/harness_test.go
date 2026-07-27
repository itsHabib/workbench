// Package e2e drives the real `escalate serve` binary end to end over HTTP: it
// builds a signed Slack interactive-action callback — the exact shape a tapped
// Approve/Block button POSTs through the ngrok tunnel — and fires it at a running
// serve process, asserting the signature gate, the callback→decision map, the
// who-from-verified-identity path, the grant-from-parked-escalation lookup, and
// replay safety, all through the binary.
//
// It composes the way the whole family does: it imports `contracts/escalation`
// (the shared button-action vocabulary flare renders and serve parses) and no
// other tool's code, and it exercises serve only through its binary + a stub gate
// (cmd/escalate/e2e/stubgate) standing in for the real gate's CLI seam. Loopback
// stands in for the tunnel; the stub gate stands in for the real gate, whose
// full resolve→stamp→audit thread Phase 1's manual EVIDENCE captured against the
// real binary. What this adds is the automated, deterministic proof of the
// ngrok-carried INPUT path and the Phase-2 contract composition (the callback
// built from the shared vocabulary resolves the right park).
//
// The real phone-tap over a live tunnel against the real gate is operator infra;
// its runbook is docs/features/escalation-plane/EVIDENCE-escalate-e2e-phase3.md.
package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The signing secret every callback in the suite is signed with; serve is booted
// with the same value in SLACK_SIGNING_SECRET. A forged-secret case signs with a
// different one to prove the mismatch is rejected.
const signingSecret = "8f742231b10c8537228d4e5a1a1a2d3f"

// Built once in TestMain and shared read-only across tests.
var (
	escalateBin string
	stubGateBin string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "escalate-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mkdtemp:", err)
		os.Exit(1)
	}
	escalateBin, err = build(dir, "escalate", "github.com/itsHabib/workbench/cmd/escalate")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build escalate:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	stubGateBin, err = build(dir, "stubgate", "github.com/itsHabib/workbench/cmd/escalate/e2e/stubgate")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build stubgate:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func build(dir, name, pkg string) (string, error) {
	out := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, pkg)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, b)
	}
	return out, nil
}

// server is one running `escalate serve` process against a freshly seeded park.
type server struct {
	t        *testing.T
	baseURL  string
	stateDir string
	secret   string
	esc      string
	grant    string
	cmd      *exec.Cmd
}

// seedPark writes the park the stub gate serves and returns a booted serve
// process pointed at it. The escalation id follows gate's esc_+hex shape so the
// ingest id validator accepts it.
func startServer(t *testing.T) *server {
	t.Helper()
	stateDir := t.TempDir()
	esc := "esc_" + strings.Repeat("a", 16)
	grant := "grt_" + strings.Repeat("b", 16)
	writeSeed(t, stateDir, esc, grant)

	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command(escalateBin, "serve", "-addr", addr, "-gate", stubGateBin, "-state", stateDir)
	// serve requires BOTH a signing secret (authn) and an allowlist (authz) or it
	// refuses to start; the suite authorizes only `operator`, so an unlisted-user
	// tap can prove the 403 authz gate.
	cmd.Env = append(os.Environ(),
		"SLACK_SIGNING_SECRET="+signingSecret,
		"ESCALATE_ALLOWED_SLACK_USERS="+operator.ID,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	s := &server{
		t: t, baseURL: "http://" + addr, stateDir: stateDir,
		secret: signingSecret, esc: esc, grant: grant, cmd: cmd,
	}
	t.Cleanup(s.stop)
	s.waitReady(port)
	return s
}

func (s *server) stop() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
}

// waitReady blocks until the listener accepts, so a test never races the boot.
func (s *server) waitReady(port int) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	s.t.Fatalf("serve did not become ready on port %d", port)
}

// post fires one Slack interactive-action callback at serve with the given
// headers and returns the status + trimmed body. It is the transport a real tap
// takes: an application/x-www-form-urlencoded POST carrying the payload field.
func (s *server) post(sig, ts, body string) (int, string) {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/", strings.NewReader(body))
	if err != nil {
		s.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(out))
}

// tap builds a validly-signed callback for one button action on this server's
// seeded escalation and posts it. actionID is the shared vocabulary
// (escalation.ActionApprove / ActionBlock); user is the verified Slack identity.
func (s *server) tap(actionID string, user slackUser) (int, string) {
	body := callbackBody(actionID, s.esc, user)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	return s.post(sign(s.secret, ts, body), ts, body)
}

// invocations reads back every resolve the stub gate journaled, so a test can
// assert exactly what serve drove gate with.
func (s *server) invocations() []map[string]string {
	s.t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.stateDir, "invocations.jsonl"))
	if err != nil {
		return nil
	}
	var out []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			s.t.Fatalf("decode invocation %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func writeSeed(t *testing.T, stateDir, esc, grant string) {
	t.Helper()
	seed := map[string]any{
		"escalation": esc, "grant": grant,
		"repo": "itsHabib/workbench", "number": 140, "head": "cafe1234",
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "seed.json"), raw, 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
}

// slackUser is the verified identity Slack puts in a signed callback — the id is
// immutable, the username a mutable handle.
type slackUser struct {
	ID       string
	Username string
}

// callbackBody builds the application/x-www-form-urlencoded body Slack POSTs for
// a tapped button: a single `payload` field holding URL-encoded block_actions
// JSON. The action_id selects the verdict; the button value carries the
// escalation id serve joins on. This is the fixture that models the tunnel-
// carried input; extra is a raw JSON fragment a test can smuggle in (e.g. a
// forged top-level who) to prove serve ignores it.
func callbackBody(actionID, escID string, user slackUser) string {
	return callbackBodyWith(actionID, escID, user, "")
}

func callbackBodyWith(actionID, escID string, user slackUser, extra string) string {
	payload := fmt.Sprintf(
		`{%s"type":"block_actions","user":{"id":%q,"username":%q},"actions":[{"action_id":%q,"value":%q}]}`,
		extra, user.ID, user.Username, actionID, escID,
	)
	return "payload=" + url.QueryEscape(payload)
}

// sign computes Slack's v0 signature over the raw body: hex HMAC-SHA256 of
// "v0:{ts}:{body}", matching exactly what serve verifies.
func sign(secret, ts, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "v0:%s:%s", ts, body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// freePort grabs a port the OS just confirmed free, then releases it for serve
// to bind. A tiny TOCTOU window, standard for spawning a real server in a test.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
