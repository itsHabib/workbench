package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

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
