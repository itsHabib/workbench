// Package e2e drives flare's real producer -> sweep -> sink path end to end:
// it seeds fixture gate/ship artifacts on disk (built from the shared contracts
// vocabulary), points a routes config at an in-process capture sink, and execs
// the actual flare binary's `sweep` verb against them. It imports contracts (the
// shared vocabulary) but no other tool's decision code, and exercises flare only
// through its binary + artifacts — the boundary law the whole family observes.
//
// The harness is test-only. It builds the flare binary once in TestMain and
// skips under -short (build+exec is the slow part).
package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts"
)

// flareBin is the path to the flare binary built once in TestMain; empty under
// -short, where every test skips before touching it.
var flareBin string

func TestMain(m *testing.M) {
	flag.Parse() // testing.Short() reads a flag; parse before touching it.
	if testing.Short() {
		os.Exit(m.Run())
	}
	bin, cleanup, err := buildFlare()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	flareBin = bin
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// buildFlare compiles the flare binary to a temp path. Using the full import
// path keeps the build independent of the test's working directory.
func buildFlare() (string, func(), error) {
	dir, err := os.MkdirTemp("", "flare-e2e-bin")
	if err != nil {
		return "", nil, fmt.Errorf("e2e: temp bin dir: %w", err)
	}
	bin := filepath.Join(dir, "flare")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/itsHabib/workbench/cmd/flare")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("e2e: build flare: %w\n%s", err, out)
	}
	return bin, func() { os.RemoveAll(dir) }, nil
}

// runSweep execs `flare sweep -config <cfg> -state <state>` as a subprocess and
// fails the test on a non-zero exit — a swept-clean lie must surface here.
func runSweep(t *testing.T, cfg, state string) {
	t.Helper()
	cmd := exec.Command(flareBin, "sweep", "-config", cfg, "-state", state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("flare sweep failed: %v\n%s", err, out)
	}
}

// delivery is one payload the capture sink recorded: the channel path it landed
// on and the webhook body flare posted.
type delivery struct {
	Path    string
	Payload map[string]string
}

// sink is an in-process httptest server standing in for Slack/toast: every
// webhook channel in the fixture config points a distinct URL path at it, so a
// recorded (path, payload) proves both that an event was delivered and which
// channel its route selected — capturing what would hit production with zero
// production surface change.
type sink struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []delivery
}

func newSink() *sink {
	s := &sink{}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *sink) handle(w http.ResponseWriter, r *http.Request) {
	var p map[string]string
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.got = append(s.got, delivery{Path: r.URL.Path, Payload: p})
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// deliveries returns a snapshot of what the sink has recorded so far.
func (s *sink) deliveries() []delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]delivery, len(s.got))
	copy(out, s.got)
	return out
}

func (s *sink) close() { s.srv.Close() }

func (s *sink) url(path string) string { return s.srv.URL + path }

// seedGateLog writes a gate log.jsonl with a valid prev/hash chain: an
// escalation and a block verdict (both page-worthy), plus a passing verdict that
// must not page — proving gate-side selectivity through the real parser. Built
// from the contracts envelope/verdict types, never a hand-rolled shape.
func seedGateLog(t *testing.T, dir string) string {
	t.Helper()
	when := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	esc := mustBody(t, escalationBody{Outcome: "awaiting_judgment", Question: "Approve the tier-2 merge?", Code: "park"})
	block := mustBody(t, contracts.Verdict{
		Subject:  contracts.Subject{Repo: "itsHabib/ship", Number: 181},
		Source:   "code",
		Producer: contracts.Producer{Class: contracts.ClassCode, Impl: "secret-scan"},
		Decision: contracts.DecisionBlock,
		Tier:     "2",
		Why:      "a live token appears in the diff",
	})
	pass := mustBody(t, contracts.Verdict{
		Subject:  contracts.Subject{Repo: "itsHabib/ship", Number: 182},
		Source:   "code",
		Producer: contracts.Producer{Class: contracts.ClassCode},
		Decision: contracts.DecisionPass,
		Tier:     "1",
		Why:      "clean",
	})
	envs := []contracts.Envelope{
		{ID: "esc-1", Kind: contracts.KindEscalation, Run: "ship-42", Time: when, Body: esc},
		{ID: "vrd-block-1", Kind: contracts.KindVerdict, Run: "ship-181", Time: when.Add(time.Minute), Body: block},
		{ID: "vrd-pass-1", Kind: contracts.KindVerdict, Run: "ship-182", Time: when.Add(2 * time.Minute), Body: pass},
	}
	path := filepath.Join(dir, "log.jsonl")
	writeLines(t, path, chainLines(t, envs))
	return path
}

// escalationBody mirrors gate's escalation payload (a body kind flare renders,
// not part of the shared verdict contract) so the harness can seed one.
type escalationBody struct {
	Outcome  string `json:"outcome"`
	Question string `json:"question"`
	Code     string `json:"code"`
}

// chainLines seals each envelope into a valid hash chain: Prev links to the
// prior line's Hash, and Hash is the sha256 of (prev, kind, id, body) — a real
// chain, so the fixture is a faithful producer artifact, not a stub.
func chainLines(t *testing.T, envs []contracts.Envelope) []string {
	t.Helper()
	prev := ""
	lines := make([]string, len(envs))
	for i := range envs {
		envs[i].Prev = prev
		envs[i].Hash = seal(prev, envs[i])
		prev = envs[i].Hash
		b, err := json.Marshal(envs[i])
		if err != nil {
			t.Fatalf("marshal envelope %s: %v", envs[i].ID, err)
		}
		lines[i] = string(b)
	}
	return lines
}

func seal(prev string, env contracts.Envelope) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n%s", prev, env.Kind, env.ID, env.Body)
	return hex.EncodeToString(h.Sum(nil))
}

// receiptLine is a ship receipt as flare reads it (see source/receipts.go). It
// lives here, local to the harness, so the e2e stays a black-box exercise of the
// flare binary rather than importing ship's writer.
type receiptLine struct {
	Key        string `json:"key"`
	Source     string `json:"source"`
	Outcome    string `json:"outcome"`
	Repo       string `json:"repo"`
	TaskSlug   string `json:"task_slug"`
	PRNumber   int    `json:"pr_number"`
	TerminalAt string `json:"terminal_at"`
}

// seedReceipts writes a ship receipts.jsonl with a parked receipt (page-worthy —
// the ship-side analogue of a gate escalation) and a merged receipt that is not
// page-worthy, so the harness proves selectivity on the ship side too.
func seedReceipts(t *testing.T, dir string) string {
	t.Helper()
	when := "2026-07-15T09:05:00Z"
	receipts := []receiptLine{
		{Key: "run-7", Source: "ship", Outcome: "parked", Repo: "itsHabib/ship", TaskSlug: "e2e-harness", PRNumber: 181, TerminalAt: when},
		{Key: "run-8", Source: "ship", Outcome: "merged", Repo: "itsHabib/ship", TaskSlug: "docs-tweak", PRNumber: 170, TerminalAt: when},
	}
	lines := make([]string, len(receipts))
	for i, r := range receipts {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal receipt %s: %v", r.Key, err)
		}
		lines[i] = string(b)
	}
	path := filepath.Join(dir, "receipts.jsonl")
	writeLines(t, path, lines)
	return path
}

func mustBody(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return b
}

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
