package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// runFlakyGH is the retry tests' stand-in for the CLI. It records every
// invocation and fails the first GH_FAIL_TIMES of them with GH_FAIL_MESSAGE on
// stderr, the way gh reports a transport fault. Counting attempts on disk is
// what lets a test assert the retry actually happened rather than infer it
// from the result. Dispatched by TestMain in panel_test.go.
func runFlakyGH() int {
	f, err := os.OpenFile(os.Getenv("GH_ATTEMPTS_FILE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	if _, err := f.WriteString("x"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	info, err := f.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fails, _ := strconv.Atoi(os.Getenv("GH_FAIL_TIMES"))
	if int(info.Size()) <= fails {
		fmt.Fprintln(os.Stderr, os.Getenv("GH_FAIL_MESSAGE"))
		return 1
	}
	fmt.Println(`{"ok":true}`)
	return 0
}

// resetPeer is the message api.github.com actually returned on 2026-08-23,
// killing a gate run mid-evidence. Pinning the real text keeps the classifier
// honest about the failure it was written for.
const resetPeer = `error connecting to api.github.com: Post "https://api.github.com/graphql": read tcp 10.0.0.2:53000->140.82.113.5:443: read: connection reset by peer`

// TestGHRetriesTransientRead pins the prevention: a read that fails on the
// transport and then succeeds returns the payload instead of aborting the run.
// The backoff grows, so a second attempt does not simply re-hit a server that
// is still recovering.
func TestGHRetriesTransientRead(t *testing.T) {
	attempts := flakyGH(t, 2, resetPeer)
	slept := stubSleep(t)

	out, err := gh("pr", "diff", "7")
	if err != nil {
		t.Fatalf("a transient read must survive the retry: %v", err)
	}
	if string(out) != "{\"ok\":true}\n" {
		t.Fatalf("gh returned %q", out)
	}
	if n := attempts(); n != 3 {
		t.Fatalf("attempts = %d, want 3 (two failures, then the success)", n)
	}
	if len(*slept) != 2 || (*slept)[0] != ghBackoff || (*slept)[1] != 2*ghBackoff {
		t.Fatalf("backoff must grow between attempts, got %v", *slept)
	}
}

// TestGHFailsClosedAtTheBound pins the other half: retrying is bounded, and a
// failure that outlives the bound is returned unchanged. Gate must abort on a
// GitHub outage — never invent the evidence it could not read — so this test
// is the guard against a retry loop quietly becoming a way to proceed without
// an answer.
func TestGHFailsClosedAtTheBound(t *testing.T) {
	attempts := flakyGH(t, ghAttempts, resetPeer)
	stubSleep(t)

	out, err := gh("pr", "diff", "7")
	if err == nil {
		t.Fatalf("a failure that outlives the bound must not become a success: %q", out)
	}
	if n := attempts(); n != ghAttempts {
		t.Fatalf("attempts = %d, want the bound %d", n, ghAttempts)
	}
	if !transientGH(err.Error()) {
		t.Fatalf("the returned error must still carry the cause the operator needs: %v", err)
	}
}

// TestGHDoesNotRetryAPermanentFailure pins the allowlist's payoff: a wrong
// credential, a repo the token cannot see, a malformed query — none of these
// change on a second call, so they must fail on the first one. Retrying them
// would spend the backoff bound on a guaranteed-identical answer and delay the
// error an operator needs immediately.
func TestGHDoesNotRetryAPermanentFailure(t *testing.T) {
	attempts := flakyGH(t, ghAttempts, "gh: Not Found (HTTP 404)")
	stubSleep(t)

	if _, err := gh("pr", "diff", "7"); err == nil {
		t.Fatal("a 404 must still fail")
	}
	if n := attempts(); n != 1 {
		t.Fatalf("attempts = %d, want 1 — a permanent failure is not retried", n)
	}
}

// TestTransientGHIsAnAllowlist pins the classifier's shape as much as its
// entries: recognized transport and retryable-status text retries, and
// EVERYTHING ELSE is permanent by default. The default matters more than any
// single row — an unrecognized failure must not sleep through the bound, and a
// new signature must be added deliberately rather than fall in by accident.
func TestTransientGHIsAnAllowlist(t *testing.T) {
	transient := []string{
		resetPeer,
		`evidence: gh [pr diff]: Get "https://api.github.com": dial tcp: connection refused`,
		`Get "https://api.github.com/repos": net/http: TLS handshake timeout`,
		`Post "https://api.github.com/graphql": read tcp: i/o timeout`,
		`dial tcp: lookup api.github.com: no such host`,
		"gh: HTTP 502 (https://api.github.com/graphql)",
		"gh: HTTP 503",
		"You have exceeded a secondary rate limit",
	}
	permanent := []string{
		`exec: "gh": executable file not found in $PATH`,
		"gh: Not Found (HTTP 404)",
		"gh auth login required (HTTP 401)",
		"gh: Resource not accessible by integration (HTTP 403)",
		"gh: Validation Failed (HTTP 422)",
		"evidence: gh [pr diff]: unexpected argument",
		// An oversized diff routes to the local-diff fallback, not to a retry:
		// the answer is deterministic, and retrying would only delay it.
		"evidence: gh [pr diff]: HTTP 406: the diff exceeded the maximum number of lines",
		"",
	}
	for _, msg := range transient {
		if !transientGH(msg) {
			t.Errorf("must retry: %q", msg)
		}
	}
	for _, msg := range permanent {
		if transientGH(msg) {
			t.Errorf("must not retry: %q", msg)
		}
	}
}

// flakyGH installs the helper-process CLI on PATH and returns a reader for the
// number of invocations it has seen.
func flakyGH(t *testing.T, fails int, message string) func() int {
	t.Helper()
	dir := t.TempDir()
	attemptsPath := filepath.Join(dir, "attempts")
	installFakeGH(t, dir)
	t.Setenv("GO_WANT_GH_RETRY_HELPER_PROCESS", "1")
	t.Setenv("GH_ATTEMPTS_FILE", attemptsPath)
	t.Setenv("GH_FAIL_TIMES", strconv.Itoa(fails))
	t.Setenv("GH_FAIL_MESSAGE", message)
	t.Setenv("PATH", dir)
	return func() int {
		raw, err := os.ReadFile(attemptsPath)
		if err != nil {
			t.Fatalf("read attempts: %v", err)
		}
		return len(raw)
	}
}

// stubSleep swaps the backoff for a recorder, so a test asserts the retry
// policy without paying its wall-clock.
func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	previous := ghSleep
	ghSleep = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { ghSleep = previous })
	return &slept
}
