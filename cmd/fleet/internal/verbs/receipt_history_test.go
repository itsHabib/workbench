package verbs

import (
	"os"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

const historySha = "abc1234"

func historyFixture(t *testing.T) {
	t.Helper()
	old := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = old })
}

func record(t *testing.T, kind, verdict, session, observable string, at float64) fleet.Rec {
	t.Helper()
	r := fleet.Rec{"sha": historySha, "head": historySha + "def", "kind": kind, "verdict": verdict,
		"observable": observable, "session": session, "at": at}
	if err := recordReceipt(historySha, kind, r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestASecondVerdictDoesNotEraseTheFirst(t *testing.T) {
	historyFixture(t)
	record(t, "check", "fail", "sess-one", "the unit line read as a defect", 100)
	latest := record(t, "check", "pass", "sess-two", "the unit line is the acceptance", 200)

	rows := receiptRows(historySha, "", 0, false)
	if len(rows) != 1 || fleet.S(rows[0], "verdict") != "pass" {
		t.Fatalf("the latest verdict is what a reader gets: %v", rows)
	}
	prior := supersededBy(latest)
	if len(prior) != 1 || fleet.S(prior[0], "verdict") != "fail" || fleet.S(prior[0], "session") != "sess-one" {
		t.Fatalf("the first verdict is gone: %v", prior)
	}
}

func TestDoneReportsTheLatestVerdictAndAllShowsWhatItReplaced(t *testing.T) {
	historyFixture(t)
	record(t, "check", "fail", "sess-one", "first look", 100)
	record(t, "check", "pass", "sess-two", "second look", 200)
	v := doneVerdict(historySha, "check")
	if !v.ok || fleet.S(v.kinds["check"], "session") != "sess-two" {
		t.Fatalf("latest wins: ok=%v kinds=%v", v.ok, v.kinds)
	}
	out := capture(t, func() { printDone(v, historySha, "", "sha as given", true) })
	if !strings.Contains(out, "superseded") || !strings.Contains(out, "first look") {
		t.Fatalf("--all does not show the replaced verdict:\n%s", out)
	}
	quiet := capture(t, func() { printDone(v, historySha, "", "sha as given", false) })
	if strings.Contains(quiet, "superseded") {
		t.Fatalf("history leaked into the default view:\n%s", quiet)
	}
}

func TestReceiptsAllRendersHistoryUnderItsVerdict(t *testing.T) {
	historyFixture(t)
	record(t, "check", "fail", "sess-one", "first look", 100)
	record(t, "check", "pass", "sess-two", "second look", 200)
	out := capture(t, func() {
		if err := cmdReceipts(historySha, "", 0, false, false, true); err != nil {
			t.Fatal(err)
		}
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "superseded") || !strings.Contains(lines[1], "superseded") {
		t.Fatalf("the latest verdict leads, its history follows:\n%s", out)
	}
	if !strings.Contains(lines[0], "second look") || !strings.Contains(lines[1], "first look") {
		t.Fatalf("wrong order:\n%s", out)
	}
}

// A store written before history existed keeps its single file and its first verdict:
// the next receipt seeds the history with what was already on disk.
func TestASingleFileStoreStaysReadableAndItsVerdictSurvives(t *testing.T) {
	historyFixture(t)
	latestPath, historyPath := receiptPaths(historySha, "check")
	legacy := fleet.Rec{"sha": historySha, "head": historySha + "def", "kind": "check", "verdict": "fail",
		"observable": "written by an older build", "session": "sess-old", "at": float64(50)}
	if err := fleet.WriteJSON(latestPath, legacy); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatal(err)
	}
	rows := receiptRows(historySha, "", 0, false)
	if len(rows) != 1 || fleet.S(rows[0], "verdict") != "fail" {
		t.Fatalf("a single-file store must read exactly as it did: %v", rows)
	}
	if _, err := os.Stat(historyPath); err == nil {
		t.Fatal("reading a store must not write to it")
	}

	latest := record(t, "check", "pass", "sess-new", "written by this build", 300)
	prior := supersededBy(latest)
	if len(prior) != 1 || fleet.S(prior[0], "session") != "sess-old" {
		t.Fatalf("the pre-existing verdict was not carried into the history: %v", prior)
	}
	after, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "superseded") || !strings.Contains(string(after), "sess-new") {
		t.Fatalf("the latest file changed shape:\n%s\nwas:\n%s", after, before)
	}
}

// The history is beside the latest file, not inside the set of receipts: a reader that
// lists receipts must not see one head's verdict twice.
func TestTheHistoryFileIsNotItselfAReceipt(t *testing.T) {
	historyFixture(t)
	record(t, "check", "fail", "sess-one", "first look", 100)
	record(t, "check", "pass", "sess-two", "second look", 200)
	if rows := receiptRows("", "", 0, false); len(rows) != 1 {
		t.Fatalf("the history was read as a receipt of its own: %v", rows)
	}
}

func capture(t *testing.T, f func()) string {
	t.Helper()
	var buf strings.Builder
	prev := Out
	Out = &buf
	t.Cleanup(func() { Out = prev })
	f()
	Out = prev
	return buf.String()
}

// Seeding a legacy store must be durable before the latest file is overwritten. An
// unwritable history plus an ignored error erases the only copy of the verdict this
// path exists to preserve.
func TestASeedFailureLeavesTheLegacyVerdictIntact(t *testing.T) {
	historyFixture(t)
	latest, history := receiptPaths(historySha, "check")
	legacy := fleet.Rec{"sha": historySha, "kind": "check", "verdict": "fail", "session": "sess-legacy", "at": float64(100)}
	if err := fleet.WriteJSON(latest, legacy); err != nil {
		t.Fatal(err)
	}
	// A directory where the history file belongs: every append to it fails.
	if err := os.MkdirAll(history, 0o700); err != nil {
		t.Fatal(err)
	}
	err := recordReceipt(historySha, "check", fleet.Rec{"sha": historySha, "kind": "check", "verdict": "pass", "session": "sess-new", "at": float64(200)})
	if err == nil {
		t.Fatal("a seed that cannot be appended must fail the record, not proceed")
	}
	if v := fleet.S(fleet.ReadJSON(latest), "verdict"); v != "fail" {
		t.Fatalf("the legacy verdict was overwritten anyway: verdict=%q", v)
	}
}
