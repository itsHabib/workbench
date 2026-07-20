package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/driverstate"
)

// fixedRun is the deterministic run id every CLI golden test drives, so ids and
// times in the output are stable.
const fixedRun = "dsr_fixed"

// eventLine builds a record event JSON with a caller-fixed id and time, so the
// CLI's sealed output (and its hash chain) is deterministic.
func eventLine(id string, kind dsc.Kind, stream, ts string, body any) string {
	raw, _ := json.Marshal(body)
	e := map[string]any{
		"id":    id,
		"kind":  string(kind),
		"actor": "human:mh",
		"time":  ts,
		"body":  json.RawMessage(raw),
	}
	if stream != "" {
		e["stream"] = stream
	}
	line, _ := json.Marshal(e)
	return string(line)
}

// runCLI invokes the CLI against a temp state root (via WORKBENCH_STATE_DIR) and
// returns stdout.
func runCLI(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	t.Setenv(driverstate.StateDirEnv, dir)
	var out, errb bytes.Buffer
	if err := run(args, strings.NewReader(stdin), &out, &errb); err != nil {
		t.Fatalf("cli %v: %v (stderr %s)", args, err, errb.String())
	}
	return out.String()
}

// seedLifecycle records a full import → dispatch → attempt → pr → merge on
// fixedRun and returns the state dir.
func seedLifecycle(t *testing.T, dir string) {
	t.Helper()
	imp := eventLine("evt_imp", dsc.KindRunImported, "", "2026-07-16T00:00:00Z", dsc.RunImportedBody{
		Repo:     "itsHabib/workbench",
		Source:   "driver.md",
		Manifest: json.RawMessage(`{}`),
		Streams:  []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/x.md"}},
	})
	runCLI(t, dir, imp, "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_disp", dsc.KindStreamDispatched, "dss_a", "2026-07-16T00:01:00Z", struct{}{}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_att", dsc.KindStreamAttempt, "dss_a", "2026-07-16T00:02:00Z",
		dsc.StreamAttemptBody{Seq: 1, DocPath: "docs/x.md", Terminal: true}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_pr", dsc.KindStreamPROpened, "dss_a", "2026-07-16T00:03:00Z",
		dsc.StreamPROpenedBody{PR: 12, URL: "http://pr/12", HeadSHA: "abc"}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_mrg", dsc.KindStreamMerged, "dss_a", "2026-07-16T00:04:00Z",
		dsc.StreamMergedBody{PR: 12, MergeCommit: "def456", MergedAt: "2026-07-16T00:04:00Z"}), "record", "--run", fixedRun)
}

func TestCLIStateJSONGolden(t *testing.T) {
	dir := t.TempDir()
	seedLifecycle(t, dir)
	got := runCLI(t, dir, "", "state", "--run", fixedRun, "--json")

	want := `{
  "run": {
    "repo": "itsHabib/workbench",
    "source": "driver.md",
    "status": "open",
    "imported_at": "2026-07-16T00:00:00Z"
  },
  "streams": {
    "dss_a": {
      "status": "merged",
      "attempts": [
        {
          "seq": 1,
          "terminal": true
        }
      ],
      "pr": 12,
      "url": "http://pr/12",
      "merge_commit": "def456"
    }
  }
}
`
	if got != want {
		t.Fatalf("state --json mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestCLIRunsJSONGolden(t *testing.T) {
	dir := t.TempDir()
	seedLifecycle(t, dir)
	got := runCLI(t, dir, "", "runs", "--json")

	want := `[
  {
    "run": "dsr_fixed",
    "status": "open",
    "repo": "itsHabib/workbench",
    "source": "driver.md",
    "imported_at": "2026-07-16T00:00:00Z"
  }
]
`
	if got != want {
		t.Fatalf("runs --json mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestCLIVerifyJSONGolden(t *testing.T) {
	dir := t.TempDir()
	seedLifecycle(t, dir)
	got := runCLI(t, dir, "", "verify", "--run", fixedRun, "--json")

	want := "{\n  \"ok\": true,\n  \"run\": \"dsr_fixed\"\n}\n"
	if got != want {
		t.Fatalf("verify --json mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestCLIRecordJSONSealsEvent(t *testing.T) {
	dir := t.TempDir()
	imp := eventLine("evt_imp", dsc.KindRunImported, "", "2026-07-16T00:00:00Z", dsc.RunImportedBody{
		Repo:     "itsHabib/workbench",
		Source:   "driver.md",
		Manifest: json.RawMessage(`{}`),
		Streams:  []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/x.md"}},
	})
	got := runCLI(t, dir, imp, "record", "--run", fixedRun, "--json")

	var sealed driverstate.Event
	if err := json.Unmarshal([]byte(got), &sealed); err != nil {
		t.Fatalf("decode sealed event: %v", err)
	}
	if sealed.Run != fixedRun || sealed.ID != "evt_imp" {
		t.Fatalf("identity wrong: %+v", sealed)
	}
	// The first event is sealed (non-empty hash) and links to no prior. (The hash
	// is not recomputed from this pretty-printed output: --json re-indents the
	// raw body, so its bytes differ from the canonical bytes the hash sealed —
	// chain integrity is covered end-to-end by the verify tests.)
	if sealed.Prev != "" || sealed.Hash == "" {
		t.Fatalf("chain seal wrong: prev=%q hash=%q", sealed.Prev, sealed.Hash)
	}
}

func TestCLIRecordMintsRunWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	got := runCLI(t, dir, keyedImportLine("evt_imp2", "2026-07-16T00:00:00Z"), "record", "--json")
	var sealed driverstate.Event
	_ = json.Unmarshal([]byte(got), &sealed)
	if !strings.HasPrefix(sealed.Run, "dsr_") {
		t.Fatalf("run not minted: %q", sealed.Run)
	}
}

// keyedImportLine is an omitted-run run_imported carrying a (repo, source,
// generated_at) key, so minting is retry-safe.
func keyedImportLine(id, generatedAt string) string {
	return eventLine(id, dsc.KindRunImported, "", "2026-07-16T00:00:00Z", dsc.RunImportedBody{
		Repo:        "itsHabib/workbench",
		Source:      "driver.md",
		GeneratedAt: generatedAt,
		Manifest:    json.RawMessage(`{}`),
		Streams:     []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/x.md"}},
	})
}

// A CLI run_imported with no --run and no generated_at is refused — a cron retry
// after a lost response must not mint a duplicate run (parity with the server).
func TestCLIRecordRejectsKeylessMintedImport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(driverstate.StateDirEnv, dir)
	keyless := eventLine("evt_nokey", dsc.KindRunImported, "", "2026-07-16T00:00:00Z", dsc.RunImportedBody{
		Repo:     "itsHabib/workbench",
		Source:   "driver.md",
		Manifest: json.RawMessage(`{}`),
		Streams:  []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/x.md"}},
	})
	var out, errb bytes.Buffer
	err := run([]string{"record"}, strings.NewReader(keyless), &out, &errb)
	if err == nil {
		t.Fatal("record should refuse a keyless omitted-run import")
	}
	if !strings.Contains(err.Error(), "generated_at") {
		t.Fatalf("error = %v, want it to name the missing key", err)
	}
}

// A keyed import retried with no --run resolves to the original run via Append's
// dedupe; the speculatively minted run dir must be cleaned up, leaving exactly
// one run dir on disk.
func TestCLIImportRetryDedupesNoOrphan(t *testing.T) {
	dir := t.TempDir()
	imp := keyedImportLine("evt_dup", "2026-07-16T00:00:00Z")

	first := runCLI(t, dir, imp, "record", "--json")
	var e1 driverstate.Event
	_ = json.Unmarshal([]byte(first), &e1)

	second := runCLI(t, dir, imp, "record", "--json") // the lost-response retry
	var e2 driverstate.Event
	_ = json.Unmarshal([]byte(second), &e2)

	if e2.Run != e1.Run || e2.Hash != e1.Hash {
		t.Fatalf("retry should resolve to the original run/event: e1=%s/%s e2=%s/%s", e1.Run, e1.Hash, e2.Run, e2.Hash)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	runDirs := 0
	for _, en := range entries {
		if en.IsDir() {
			runDirs++
		}
	}
	if runDirs != 1 {
		t.Fatalf("want exactly one run dir (orphan cleaned), got %d", runDirs)
	}
}

func TestCLIPrintsResolvedStateRoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(driverstate.StateDirEnv, dir)
	var out, errb bytes.Buffer
	if err := run([]string{"runs", "--json"}, strings.NewReader(""), &out, &errb); err != nil {
		t.Fatalf("runs: %v", err)
	}
	// The resolved root is printed to stderr (never stdout, so --json stays clean).
	if !strings.Contains(errb.String(), dir) {
		t.Fatalf("stderr did not print the resolved state root %q: %s", dir, errb.String())
	}
	if strings.Contains(out.String(), dir) {
		t.Fatalf("state root leaked onto stdout: %s", out.String())
	}
}

func TestCLIUnknownCommandErrors(t *testing.T) {
	t.Setenv(driverstate.StateDirEnv, t.TempDir())
	var out, errb bytes.Buffer
	if err := run([]string{"frobnicate"}, strings.NewReader(""), &out, &errb); err == nil {
		t.Fatal("expected an error for an unknown command")
	}
}

func TestCLIStreamEventWithoutRunErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(driverstate.StateDirEnv, dir)
	line := eventLine("evt_x", dsc.KindStreamDispatched, "dss_a", "2026-07-16T00:00:00Z", struct{}{})
	var out, errb bytes.Buffer
	// A stream event with no --run has no run to append to (mirrors the server).
	if err := run([]string{"record"}, strings.NewReader(line), &out, &errb); err == nil {
		t.Fatal("expected an error recording a stream event with no run")
	}
}

// seedRenderLifecycle records import → dispatch → failed attempt → re-dispatch →
// PR → merge for render tests.
func seedRenderLifecycle(t *testing.T, dir string) {
	t.Helper()
	imp := eventLine("evt_imp", dsc.KindRunImported, "", "2026-07-16T00:00:00Z", dsc.RunImportedBody{
		Repo:     "itsHabib/workbench",
		Source:   "driver.md",
		Manifest: json.RawMessage(`{}`),
		Streams:  []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/x.md"}},
	})
	runCLI(t, dir, imp, "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_disp1", dsc.KindStreamDispatched, "dss_a", "2026-07-16T00:01:00Z", struct{}{}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_att1", dsc.KindStreamAttempt, "dss_a", "2026-07-16T00:02:00Z",
		dsc.StreamAttemptBody{Seq: 1, DocPath: "docs/x.md", Terminal: true, FailureCategory: "build"}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_disp2", dsc.KindStreamDispatched, "dss_a", "2026-07-16T00:03:00Z", struct{}{}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_att2", dsc.KindStreamAttempt, "dss_a", "2026-07-16T00:04:00Z",
		dsc.StreamAttemptBody{Seq: 2, DocPath: "docs/x.md", Terminal: true}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_pr", dsc.KindStreamPROpened, "dss_a", "2026-07-16T00:05:00Z",
		dsc.StreamPROpenedBody{PR: 12, URL: "http://pr/12", HeadSHA: "abc"}), "record", "--run", fixedRun)
	runCLI(t, dir, eventLine("evt_mrg", dsc.KindStreamMerged, "dss_a", "2026-07-16T00:06:00Z",
		dsc.StreamMergedBody{PR: 12, MergeCommit: "def4567890", MergedAt: "2026-07-16T00:06:00Z"}), "record", "--run", fixedRun)
}

func dirFingerprint(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		path := filepath.Join(dir, en.Name())
		if en.IsDir() {
			sub, err := os.ReadDir(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, sub := range sub {
				data, err := os.ReadFile(filepath.Join(path, sub.Name()))
				if err != nil {
					t.Fatal(err)
				}
				b.WriteString(sub.Name())
				b.Write(data)
			}
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(en.Name())
		b.Write(data)
	}
	return b.String()
}

func TestCLIRenderLifecycle(t *testing.T) {
	dir := t.TempDir()
	seedRenderLifecycle(t, dir)
	got := runCLI(t, dir, "", "render", "--run", fixedRun)

	want := `run dsr_fixed: open (repo itsHabib/workbench, 1 streams)

timeline:
  2026-07-16T00:00:00Z  run_imported  repo=itsHabib/workbench
  2026-07-16T00:01:00Z  stream_dispatched  stream=dss_a
  2026-07-16T00:02:00Z  stream_attempt  stream=dss_a  seq=1 terminal failure=build
  2026-07-16T00:03:00Z  stream_dispatched  stream=dss_a
  2026-07-16T00:04:00Z  stream_attempt  stream=dss_a  seq=2 terminal
  2026-07-16T00:05:00Z  stream_pr_opened  stream=dss_a  pr=12 url=http://pr/12
  2026-07-16T00:06:00Z  stream_merged  stream=dss_a  pr=12 commit=def4567

streams:
  stream dss_a: merged
    attempt 1: terminal, failure=build
    attempt 2: terminal
    pr 12: http://pr/12
    merge def4567
`
	if got != want {
		t.Fatalf("render mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestCLIRenderDeterministic(t *testing.T) {
	dir := t.TempDir()
	seedRenderLifecycle(t, dir)
	first := runCLI(t, dir, "", "render", "--run", fixedRun)
	second := runCLI(t, dir, "", "render", "--run", fixedRun)
	if first != second {
		t.Fatalf("render not deterministic:\n first: %q\nsecond: %q", first, second)
	}
}

func TestCLIRenderReadOnly(t *testing.T) {
	dir := t.TempDir()
	seedRenderLifecycle(t, dir)
	before := dirFingerprint(t, dir)
	runCLI(t, dir, "", "render", "--run", fixedRun)
	after := dirFingerprint(t, dir)
	if before != after {
		t.Fatal("render modified state root")
	}
}

func TestCLIRenderUnknownKindTolerant(t *testing.T) {
	dir := t.TempDir()
	seedRenderLifecycle(t, dir)
	appendUnknownKindEvent(t, dir, fixedRun, "evt_unknown", "dss_a", "2026-07-16T00:07:00Z", "future_kind_v99")
	got := runCLI(t, dir, "", "render", "--run", fixedRun)
	if !strings.Contains(got, "future_kind_v99") {
		t.Fatalf("render did not include unknown kind:\n%s", got)
	}
	wantLine := "  2026-07-16T00:07:00Z  future_kind_v99  stream=dss_a"
	if !strings.Contains(got, wantLine) {
		t.Fatalf("unknown kind line wrong, want %q in:\n%s", wantLine, got)
	}
}

func appendUnknownKindEvent(t *testing.T, dir, run, id, stream, ts, kind string) {
	t.Helper()
	ledger := filepath.Join(dir, run, "events.jsonl")
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	prev := lastEventHash(t, data)
	when, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatal(err)
	}
	e := driverstate.Event{
		ID:     id,
		Run:    run,
		V:      dsc.Version,
		Kind:   dsc.Kind(kind),
		Stream: stream,
		Time:   when,
		Actor:  "human:mh",
		Body:   json.RawMessage(`{}`),
		Prev:   prev,
	}
	e.Hash = dsc.ComputeHash(e)
	line := append(dsc.EncodeEvent(e), '\n')
	f, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func lastEventHash(t *testing.T, data []byte) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	last := lines[len(lines)-1]
	var e struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(last), &e); err != nil {
		t.Fatal(err)
	}
	return e.Hash
}

func TestCLIRenderMissingRunErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(driverstate.StateDirEnv, dir)
	var out, errb bytes.Buffer
	err := run([]string{"render"}, strings.NewReader(""), &out, &errb)
	if err == nil {
		t.Fatal("expected error when --run is missing")
	}
	if !strings.Contains(err.Error(), "render: --run is required") {
		t.Fatalf("error = %v, want missing --run shape", err)
	}
}

func TestCLIRenderUnknownRunPropagatesError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(driverstate.StateDirEnv, dir)
	var out, errb bytes.Buffer
	err := run([]string{"render", "--run", "dsr_missing"}, strings.NewReader(""), &out, &errb)
	if err == nil {
		t.Fatal("expected error for unknown run")
	}
	if !strings.Contains(err.Error(), `run "dsr_missing" not found`) {
		t.Fatalf("error = %v, want read-path not-found error", err)
	}
}
