package verbs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func admissionFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repo, sid := requestFixture(t)
	head, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head = strings.TrimSpace(head)
	if err := cmdReceipt(head, "implementation", "pass", "go test: regression fails before the fix and passes after", sid, "", false); err != nil {
		t.Fatal(err)
	}
	return repo, sid, head
}

func guardedDispatch(head string) error {
	return Dispatch([]string{"dispatch", "task", "--as", "verification", "--for", "lead",
		"--requires", "implementation", "--head", head, "--brief", "Independently reproduce the regression"})
}

func admissionJournal(t *testing.T) []fleet.Rec {
	t.Helper()
	b := readBytes(t, fleet.Path("actions.jsonl"))
	var rows []fleet.Rec
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var event fleet.Rec
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if fleet.S(event, "action") == "dispatch_admission" {
			rows = append(rows, fleet.M(event, "row"))
		}
	}
	return rows
}

func TestDispatchAdmissionRecordsWhatTheReceiverConsumed(t *testing.T) {
	repo, _, head := admissionFixture(t)
	// The current ownership mirror cannot carry the packet. A guarded dispatch
	// must not publish a weaker remote row, even when GitHub is enabled.
	bin := t.TempDir()
	called := filepath.Join(bin, "called")
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fmt.Sprintf("#!/bin/sh\nprintf called > %q\nexit 1\n", called)), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FLEET_GITHUB", "")
	if err := guardedDispatch(head); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatal("published a remote ownership row without its admission packet")
	}
	row := fleet.ReadJSON(dispatchFile(fleet.RepoID(repo), "task", "verification"))
	a := fleet.M(row, "admission")
	if fleet.S(row, "head_at_dispatch") != head || fleet.S(a, "result") != "satisfied" || fleet.S(a, "expected_head") != head {
		t.Fatalf("missing revision-bound admission: %v", row)
	}
	receipts, ok := a["receipts"].([]any)
	if !ok || len(receipts) != 1 || fleet.S(receipts[0].(map[string]any), "observable") == "" {
		t.Fatalf("lost consumed evidence: %v", a)
	}
	journal := admissionJournal(t)
	if len(journal) != 1 || string(fleet.DumpJSON(journal[0])) != string(fleet.DumpJSON(a)) {
		t.Fatalf("journal and published decision differ: %v / %v", journal, a)
	}
}

func TestDispatchAdmissionRefusesUnusableEvidenceBeforePublishing(t *testing.T) {
	cases := []struct {
		name string
		edit func(*testing.T, string, string)
	}{
		{"missing", func(t *testing.T, _, path string) { mustRemove(t, path) }},
		{"empty", func(t *testing.T, _, path string) { writeAdmissionBytes(t, path, nil) }},
		{"malformed", func(t *testing.T, _, path string) { writeAdmissionBytes(t, path, []byte("{\n")) }},
		{"torn tail", func(t *testing.T, _, path string) { writeAdmissionBytes(t, path, []byte("{")) }},
		{"unreadable", func(t *testing.T, _, path string) {
			mustRemove(t, path)
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"head advanced", func(t *testing.T, repo, _ string) {
			runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "changed")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _, head := admissionFixture(t)
			_, path := receiptPaths(head, head, "implementation")
			tc.edit(t, repo, path)
			assertAdmissionRefused(t, repo, head)
		})
	}
}

func TestDispatchAdmissionRequiresProvenanceForThisRepositoryAndRevision(t *testing.T) {
	for field, value := range map[string]any{
		"repo": "another-repository", "head": strings.Repeat("a", 40), "sha": "deadbee",
		"kind": "different-kind", "dirty": true, "session": "", "observable": "", "cwd": nil,
		"worktree": "", "at": nil, "verdict": "unknown",
	} {
		t.Run(field, func(t *testing.T) {
			repo, _, head := admissionFixture(t)
			latest, history := receiptPaths(head, head, "implementation")
			r := fleet.ReadJSON(latest)
			r[field] = value
			writeAdmissionBytes(t, history, append(fleet.DumpJSON(r), '\n'))
			assertAdmissionRefused(t, repo, head)
		})
	}
}

func TestDispatchAdmissionUsesAppendOrderAcrossSHASpellingsAndPreservesThePacket(t *testing.T) {
	repo, sid, head := admissionFixture(t)
	if err := guardedDispatch(head); err != nil {
		t.Fatal(err)
	}
	rowPath := dispatchFile(fleet.RepoID(repo), "task", "verification")
	original := readBytes(t, rowPath)
	// A later observation can have an older timestamp. It still supersedes the
	// earlier pass, including when the producer used a different SHA spelling.
	r := fleet.ReadJSON(fleet.Path("receipts", head+".implementation.json"))
	r["sha"], r["at"], r["verdict"], r["observable"] = head[:7], float64(1), "fail", "regression still fails"
	if err := recordReceipt(head[:7], "implementation", r); err != nil {
		t.Fatal(err)
	}
	if err := guardedDispatch(head); err == nil || !strings.Contains(err.Error(), "latest implementation receipt failed") {
		t.Fatalf("earlier pass hid a later failure: %v", err)
	}
	if string(original) != string(readBytes(t, rowPath)) {
		t.Fatal("refused replacement changed the existing work")
	}
	if err := cmdReceipt(head, "implementation", "pass", "regression now passes after repair", sid, "", false); err != nil {
		t.Fatal(err)
	}
	if err := guardedDispatch(head); err != nil {
		t.Fatal(err)
	}
	journal := admissionJournal(t)
	if len(journal) != 3 || fleet.S(journal[0], "result") != "satisfied" || fleet.S(journal[1], "result") != "refused" || fleet.S(journal[2], "result") != "satisfied" {
		t.Fatalf("lost the repair history: %v", journal)
	}
}

func TestDispatchAdmissionNeedsEveryRequestedKindAndAnAuditWrite(t *testing.T) {
	repo, sid, head := admissionFixture(t)
	args := []string{"dispatch", "task", "--as", "verification", "--requires", "implementation,unit", "--head", head}
	if err := Dispatch(args); err == nil {
		t.Fatal("one kind satisfied two requirements")
	}
	if err := cmdReceipt(head, "unit", "pass", "unit suite passed", sid, "", false); err != nil {
		t.Fatal(err)
	}
	mustRemove(t, fleet.Path("actions.jsonl"))
	if err := os.Mkdir(fleet.Path("actions.jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch(args); err == nil || !strings.Contains(err.Error(), "record dispatch admission") {
		t.Fatalf("declared work despite audit failure: %v", err)
	}
	if _, err := os.Stat(dispatchFile(fleet.RepoID(repo), "task", "verification")); !os.IsNotExist(err) {
		t.Fatal("audit failure published work")
	}
	mustRemove(t, fleet.Path("actions.jsonl"))
	if err := Dispatch(args); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchAdmissionWaitsForAnInFlightReceiptWriter(t *testing.T) {
	_, _, head := admissionFixture(t)
	r := fleet.ReadJSON(fleet.Path("receipts", head+".implementation.json"))
	r["verdict"], r["observable"] = "fail", "new observation while the consumer is waiting"
	finished := make(chan error, 1)
	err := fleet.KeyLock("receipts", func() error {
		go func() { finished <- guardedDispatch(head) }()
		select {
		case err := <-finished:
			t.Fatalf("dispatch read past the receipt writer: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		return recordReceiptLocked(head, "implementation", r)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil || !strings.Contains(err.Error(), "latest implementation receipt failed") {
			t.Fatalf("did not consume the completed write: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch did not resume after receipt publication")
	}
}

func TestDispatchAdmissionRejectsIncompleteDemandAndSlotPlacement(t *testing.T) {
	repo, _, head := admissionFixture(t)
	for _, args := range [][]string{
		{"--requires", ""}, {"--head", ""},
		{"--requires", "implementation"}, {"--head", head},
		{"--requires", "implementation", "--head", head[:7]},
		{"--requires", "implementation,", "--head", head},
		{"--requires", "implementation,implementation", "--head", head},
		{"--requires", "implementation", "--head", head, "--slot", "worker"},
	} {
		if err := Dispatch(append([]string{"dispatch", "task", "--as", "verification"}, args...)); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := os.Stat(dispatchFile(fleet.RepoID(repo), "task", "verification")); !os.IsNotExist(err) {
		t.Fatal("refused demand published work")
	}
	if _, err := os.Stat(fleet.Path("assign")); !os.IsNotExist(err) {
		t.Fatal("refused admission placed work")
	}
	// Existing dispatch without entry requirements still works.
	if err := Dispatch([]string{"dispatch", "task", "--as", "implementation"}); err != nil {
		t.Fatal(err)
	}
}

func assertAdmissionRefused(t *testing.T, repo, head string) {
	t.Helper()
	if err := guardedDispatch(head); err == nil {
		t.Fatal("unusable evidence admitted verification")
	}
	if _, err := os.Stat(dispatchFile(fleet.RepoID(repo), "task", "verification")); !os.IsNotExist(err) {
		t.Fatal("refusal published work")
	}
	journal := admissionJournal(t)
	if len(journal) != 1 || fleet.S(journal[0], "result") != "refused" || fleet.S(journal[0], "reason") == "" {
		t.Fatalf("refusal lacks a recorded reason: %v", journal)
	}
}

func writeAdmissionBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(filepath.Clean(path)); err != nil {
		t.Fatal(err)
	}
}
