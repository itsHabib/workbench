package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func writeRows(t *testing.T, root, name string, rows records) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	var s strings.Builder
	for _, r := range rows {
		s.Write(fleet.DumpJSON(r))
		s.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(s.String()), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestReportScenariosAndReadOnly(t *testing.T) {
	root := t.TempDir()
	event := func(at float64, code float64, fp string) fleet.Rec {
		return fleet.Rec{"at": at, "harness": "claude", "session": "s", "event": "PreToolUse", "code": code, "fingerprint": fp, "ms": at / 1000}
	}
	es := records{event(100, 2, "a"), event(102, 2, "a"), event(103, 0, "b"), event(105, 2, "c")}
	es = append(es, fleet.Rec{"at": 104, "harness": "codex", "session": "s", "event": "Stop", "ms": 1})
	shadow := event(106, 0, "d")
	shadow["shadow"] = true
	es = append(es, shadow)
	writeRows(t, root, "events.jsonl", es)
	row := fleet.Rec{"repo": "r1", "change": "same", "relationship": "build", "for": "hub", "at": 90, "due": 110, "hands": "s", "state": "late"}
	obs := records{{"at": 101, "repo": "r1", "change": "same", "relationship": "build", "from": "working", "to": "late", "row": row}}
	writeRows(t, root, "watch/observed.jsonl", obs)
	acts := records{{"at": 102, "action": "dispatch", "row": fleet.Rec{"repo": "r2", "change": "same", "relationship": "build"}}, {"at": 109, "action": "reassign", "row": row}}
	writeRows(t, root, "actions.jsonl", acts)
	before := snapshot(t, root)
	out := Render(root, 95, 120)
	for _, want := range []string{"retried the same command", "no next event recorded (idle unknown)", "3 recorded refusals", "| reassign | 8.0s |", "r1 / same / build", "10.0s", "claude/PreToolUse | 4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	after := snapshot(t, root)
	if before != after {
		t.Fatal("report mutated store")
	}
	if out != Render(root, 95, 120) {
		t.Fatal("report is not deterministic")
	}
}
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var s strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		s.WriteString(p)
		s.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.String()
}
func TestMissingAndMalformedEvidence(t *testing.T) {
	root := t.TempDir()
	if out := Render(root, 1, 10); !strings.Contains(out, "Coverage: events.jsonl") {
		t.Fatal(out)
	}
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte("{broken}\n{\"at\":20,\"code\":2}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out := Render(root, 1, 10)
	if !strings.Contains(out, "1 malformed records skipped") || !strings.Contains(out, "1 records after report cutoff excluded") || !strings.Contains(out, "0 recorded refusals") {
		t.Fatal(out)
	}
}
func TestNextOutcomeRequiresEvidence(t *testing.T) {
	d := fleet.Rec{"cwd": "a", "fingerprint": "x"}
	cases := []struct {
		n    fleet.Rec
		want string
	}{
		{nil, "no next event recorded"},
		{fleet.Rec{"event": "Stop"}, "went idle"},
		{fleet.Rec{"event": "PreToolUse", "cwd": "b", "code": 0}, "worked elsewhere"},
		{fleet.Rec{"event": "PreToolUse", "cwd": "b", "code": 2}, "destination unknown"},
		{fleet.Rec{"event": "PostToolUse", "fingerprint": "x"}, "work outcome unknown"},
	}
	for _, c := range cases {
		if got := nextOutcome(d, c.n); !strings.Contains(got, c.want) {
			t.Errorf("%v: %s", c.n, got)
		}
	}
}
func TestUndeclaredDailyUniquenessAndEpochs(t *testing.T) {
	root := t.TempDir()
	u := fleet.Rec{"repo": "r", "change": "x", "state": "undeclared"}
	a := fleet.Rec{"repo": "r", "change": "y", "relationship": "check", "at": 1, "for": "hub"}
	b := fleet.Rec{"repo": "r", "change": "y", "relationship": "check", "at": 4, "for": "hub"}
	writeRows(t, root, "watch/observed.jsonl", records{{"at": 2, "row": u}, {"at": 3, "row": u}, {"at": 4, "row": a}, {"at": 5, "row": b}})
	out := Render(root, 1, 10)
	if !strings.Contains(out, "1970-01-01: 1") || strings.Count(out, "| r / y / check |") != 2 {
		t.Fatal(out)
	}
}

func TestNoActionAfterAttentionClearedAndNoGrowingRetiredLateness(t *testing.T) {
	root := t.TempDir()
	r := fleet.Rec{"repo": "r", "change": "x", "relationship": "check", "at": 1, "due": 7, "for": "hub"}
	t1 := fleet.Rec{"at": 2, "repo": "r", "change": "x", "relationship": "check", "to": "dead", "row": r}
	gone := fleet.Rec{"at": 6, "repo": "r", "change": "x", "relationship": "check", "to": "gone", "row": r}
	writeRows(t, root, "watch/observed.jsonl", records{t1, gone})
	writeRows(t, root, "actions.jsonl", records{{"at": 8, "action": "reassign", "row": fleet.Rec{"repo": "r", "change": "x", "relationship": "check"}}})
	out := Render(root, 1, 10)
	if !strings.Contains(out, "none before attention cleared | 4.0s") || strings.Contains(out, "| 3.0s |") {
		t.Fatal(out)
	}
}

func TestOversizedRecordCoverageHasOnePrefix(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte(strings.Repeat("x", 4*1024*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	got := readLog(root, "events.jsonl", 10).problem
	if !strings.HasPrefix(got, "events.jsonl: byte limit reached;") || strings.Count(got, "events.jsonl") != 1 {
		t.Fatal(got)
	}
}

func TestSparseLifetimeLogReadsOnlyTail(t *testing.T) {
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(8*1024*1024*1024, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{\"at\":9,\"code\":2}\n"); err != nil {
		t.Fatal(err)
	}
	data, notes, err := logTail(f)
	if err != nil || len(data) > maxLogBytes || len(notes) != 1 {
		t.Fatalf("bytes=%d notes=%v err=%v", len(data), notes, err)
	}
	got := readLog(root, "events.jsonl", 10)
	if len(got.rows) != 1 || fleet.F(got.rows[0], "at") != 9 || !strings.Contains(got.problem, "report is partial") {
		t.Fatalf("%+v", got)
	}
}

func TestRecordLimitKeepsNewestPhysicalRecords(t *testing.T) {
	root := t.TempDir()
	var rows records
	for i := 1; i <= maxLogRecords+10; i++ {
		rows = append(rows, fleet.Rec{"at": i})
	}
	writeRows(t, root, "events.jsonl", rows)
	got := readLog(root, "events.jsonl", float64(maxLogRecords+10))
	if len(got.rows) != maxLogRecords || fleet.F(got.rows[0], "at") != 11 || !strings.Contains(got.problem, "record limit reached") {
		t.Fatalf("count=%d problem=%s", len(got.rows), got.problem)
	}
}

func TestMissingLogDiagnosticsExcludeAbsolutePath(t *testing.T) {
	root := t.TempDir()
	got := readLog(root, "events.jsonl", 10)
	if strings.Contains(got.problem, root) || !strings.Contains(got.problem, "missing or unreadable") {
		t.Fatal(got.problem)
	}
}
