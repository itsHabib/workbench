package verbs

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func entryFixture(t *testing.T) (string, string, RequestOptions) {
	t.Helper()
	repo, sid := requestFixture(t)
	head, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return repo, sid, RequestOptions{Relationship: "verify", Head: strings.TrimSpace(head), Requires: "unit"}
}

func inputReceipt(t *testing.T, sid, head, verdict string) {
	t.Helper()
	if err := cmdReceipt(head, "unit", verdict, "go test ./...: "+verdict, sid, "", false); err != nil {
		t.Fatal(err)
	}
}

func entryFile(repo string) string { return dispatchFile(fleet.RepoID(repo), "task", "verify") }

func TestEntryQueuesWithActualReceiptAndRetainsItsSnapshot(t *testing.T) {
	repo, sid, opt := entryFixture(t)
	// An ordinary earlier relationship remains present; admission does not erase it.
	if err := CmdRequest("task", "build-1", sid, "lead", "build it"); err != nil {
		t.Fatal(err)
	}
	prior := readBytes(t, requestFile(repo))
	inputReceipt(t, sid, opt.Head, "fail")
	inputReceipt(t, sid, opt.Head[:8], "pass")
	if err := CmdRequest("task", "verify-1", sid, "lead", "check it", opt); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, entryFile(repo))
	row := fleet.ReadJSON(entryFile(repo))
	if err := validateRecordedEntry(row); err != nil {
		t.Fatal(err)
	}
	entry := fleet.M(row, "entry")
	if len(fleet.L(entry, "receipts")) != 1 || fleet.S(entry, "head") != opt.Head {
		t.Fatalf("lost entry evidence: %v", row)
	}
	if !bytes.Equal(prior, readBytes(t, requestFile(repo))) {
		t.Fatal("admission overwrote earlier work")
	}
	inputReceipt(t, sid, opt.Head, "fail")
	if err := CmdRequest("task", "verify-1", sid, "lead", "check it", opt); err != nil {
		t.Fatal("identical replay should return historical admission", err)
	}
	if !bytes.Equal(before, readBytes(t, entryFile(repo))) {
		t.Fatal("new evidence changed an admitted packet on replay")
	}
	if err := CmdRequest("task", "verify-2", sid, "lead", "check it", opt); err == nil {
		t.Fatal("second request overwrote an occupied receiving relationship")
	}
}

func TestEntryRefusesUnusableEvidenceBeforeAssignment(t *testing.T) {
	for _, mode := range []string{"missing", "fail", "stale", "other-repo", "other-head", "dirty", "no-observable", "no-session", "torn", "malformed", "unpublished", "conflicting-time", "bad-index"} {
		t.Run(mode, func(t *testing.T) {
			repo, sid, opt := entryFixture(t)
			inputReceipt(t, sid, opt.Head, "pass")
			latest, history := receiptPaths(opt.Head, opt.Head, "unit")
			r := fleet.ReadJSON(latest)
			breakEntryEvidence(t, mode, repo, sid, opt, r, latest, history)
			if err := CmdRequest("task", "verify-1", sid, "lead", "check it", opt); err == nil {
				t.Fatalf("%s admitted", mode)
			}
			if _, err := os.Stat(entryFile(repo)); !os.IsNotExist(err) {
				t.Fatal("refusal published an assignment", err)
			}
		})
	}
}

func breakEntryEvidence(t *testing.T, mode, repo, sid string, opt RequestOptions, r fleet.Rec, latest, history string) {
	t.Helper()
	switch mode {
	case "missing":
		mustRemove(t, history)
	case "fail":
		inputReceipt(t, sid, opt.Head, "fail")
	case "stale":
		runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "changed")
	case "torn":
		mustWrite(t, history, append(readBytes(t, history), '{'))
	case "malformed":
		mustWrite(t, history, []byte("{\n"))
	case "bad-index":
		mustWrite(t, latest, []byte("{"))
	case "unpublished":
		r["verdict"], r["at"] = "fail", fleet.F(r, "at")+1
		mustAppend(t, history, r)
	case "conflicting-time":
		r["observable"] = "a different result at the same time"
		mustAppend(t, history, r)
		mustJSON(t, latest, r)
	default:
		mutations := map[string]fleet.Rec{
			"other-repo": {"repo": "other"}, "other-head": {"head": strings.Repeat("a", 40)},
			"dirty": {"dirty": true}, "no-observable": {"observable": ""}, "no-session": {"session": ""},
		}
		for k, value := range mutations[mode] {
			r[k] = value
		}
		mustWrite(t, history, append(fleet.DumpJSON(r), '\n'))
		mustJSON(t, latest, r)
	}
}

func TestEntryPublicationFailureCanRecoverWithoutDuplicateAssignment(t *testing.T) {
	repo, sid, opt := entryFixture(t)
	inputReceipt(t, sid, opt.Head, "pass")
	// A failure between history append and latest publication cannot be consumed.
	latest, _ := receiptPaths(opt.Head, opt.Head, "unit")
	mustRemove(t, latest)
	if err := os.Mkdir(latest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := cmdReceipt(opt.Head, "unit", "pass", "rerun", sid, "", false); err == nil {
		t.Fatal("index publication should fail")
	}
	if err := CmdRequest("task", "retry", sid, "lead", "check it", opt); err == nil {
		t.Fatal("unpublished evidence admitted")
	}
	mustRemove(t, latest)
	inputReceipt(t, sid, opt.Head, "pass")
	// A stale process temporary file is not a published assignment.
	tmp := entryFile(repo) + ".123.tmp"
	mustWrite(t, tmp, []byte("{"))
	if err := CmdRequest("task", "retry", sid, "lead", "check it", opt); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, entryFile(repo))
	// A separate process represents restart after a response was lost.
	cmd := exec.Command(os.Args[0], "-test.run=^TestEntryHelperProcess$")
	cmd.Env = append(os.Environ(), "FLEET_ENTRY_HELPER=1", "FLEET_STATE="+fleet.State, "ORG_STATE="+fleet.OrgState, "FLEET_ENTRY_HEAD="+opt.Head, "FLEET_ENTRY_WORKER="+sid)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restart replay: %s: %v", out, err)
	}
	if !bytes.Equal(before, readBytes(t, entryFile(repo))) {
		t.Fatal("restart changed the original publication")
	}
}

func TestEntryHelperProcess(_ *testing.T) {
	if os.Getenv("FLEET_ENTRY_HELPER") != "1" {
		return
	}
	Out = io.Discard
	opt := RequestOptions{Relationship: "verify", Head: os.Getenv("FLEET_ENTRY_HEAD"), Requires: "unit"}
	if err := CmdRequest("task", "retry", os.Getenv("FLEET_ENTRY_WORKER"), "lead", "check it", opt); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestEntryReplayPinsTheEntireContractAndReportsDrift(t *testing.T) {
	repo, sid, opt := entryFixture(t)
	inputReceipt(t, sid, opt.Head, "pass")
	if err := CmdRequest("task", "one", sid, "lead", "check it", opt); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, fleet.State)
	for _, changed := range []RequestOptions{
		{}, {Relationship: "verify"}, {Relationship: "verify", Head: opt.Head, Requires: "integration"},
		{Relationship: "verify", Head: strings.Repeat("b", 40), Requires: "unit"},
	} {
		if err := CmdRequest("task", "one", sid, "lead", "check it", changed); err == nil {
			t.Fatalf("changed contract accepted: %+v", changed)
		}
	}
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "drift")
	if err := CmdRequest("task", "one", sid, "lead", "check it", opt); err != nil {
		t.Fatal("historical replay after drift", err)
	}
	status := requestStatus(fleet.ReadJSON(entryFile(repo)), fleet.Now())
	if fleet.S(status, "status") != "Status needs checking" || !strings.Contains(fleet.S(status, "needs"), "revision") || fleet.M(status, "entry") == nil {
		t.Fatalf("stale admission hidden: %v", status)
	}
	if err := CmdStatus([]string{"--json"}); err != nil {
		t.Fatal(err)
	}
	if !sameFiles(before, snapshotFiles(t, fleet.State)) {
		t.Fatal("conflicting replay or status mutated the evidence")
	}
}

func TestEntryDoesNotDropRequiredKindsAndRejectsDamagedSnapshot(t *testing.T) {
	repo, sid, opt := entryFixture(t)
	inputReceipt(t, sid, opt.Head, "pass")
	opt.Requires = "unit,integration"
	if err := CmdRequest("task", "one", sid, "lead", "check it", opt); err == nil {
		t.Fatal("missing second kind ignored")
	}
	if err := cmdReceipt(opt.Head, "integration", "pass", "integration checks passed", sid, "", false); err != nil {
		t.Fatal(err)
	}
	if err := CmdRequest("task", "one", sid, "lead", "check it", opt); err != nil {
		t.Fatal(err)
	}
	opt.Requires = "integration,unit"
	if err := CmdRequest("task", "one", sid, "lead", "check it", opt); err != nil {
		t.Fatal("set ordering changed replay", err)
	}
	r := fleet.ReadJSON(entryFile(repo))
	fleet.M(r, "entry")["requires"] = []any{"integration", 7, "unit"}
	mustJSON(t, entryFile(repo), r)
	if _, err := strictDispatchRows(); err == nil {
		t.Fatal("malformed required kinds silently dropped")
	}
	if err := CmdRequest("task", "one", sid, "lead", "check it", opt); err == nil {
		t.Fatal("damaged snapshot reported as safe replay")
	}
}

func TestEntryConcurrentRequestsPublishOnce(t *testing.T) {
	repo, sid, opt := entryFixture(t)
	inputReceipt(t, sid, opt.Head, "pass")
	start, results := make(chan struct{}), make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			results <- CmdRequest("task", id, sid, "lead", "check it", opt)
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("%d requests published", successes)
	}
	if err := validateRecordedEntry(fleet.ReadJSON(entryFile(repo))); err != nil {
		t.Fatal(err)
	}
}

func TestEntryWaitsForCompleteReceiptPublication(t *testing.T) {
	repo, sid, opt := entryFixture(t)
	inputReceipt(t, sid, opt.Head, "pass")
	latest, history := receiptPaths(opt.Head, opt.Head, "unit")
	r := fleet.ReadJSON(latest)
	r["verdict"], r["at"] = "fail", fleet.F(r, "at")+1
	staged, release := make(chan struct{}), make(chan struct{})
	writer := make(chan error, 1)
	go func() {
		writer <- fleet.KeyLock("receipts", func() error {
			err := fleet.AppendJSONL(history, r)
			close(staged)
			<-release
			if err != nil {
				return err
			}
			return fleet.WriteJSON(latest, r)
		})
	}()
	<-staged
	result := make(chan error, 1)
	go func() { result <- CmdRequest("task", "one", sid, "lead", "check it", opt) }()
	early := false
	select {
	case <-result:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	if early {
		t.Fatal("admission read an in-progress receipt publication")
	}
	if err := <-result; err == nil || !strings.Contains(err.Error(), "latest receipt is fail") {
		t.Fatal("admission did not consume the completed publication", err)
	}
	if _, err := os.Stat(entryFile(repo)); !os.IsNotExist(err) {
		t.Fatal("a superseded pass admitted work", err)
	}
}

func TestEntryOptionsCannotSilentlyDisableTheBoundary(t *testing.T) {
	_, sid, opt := entryFixture(t)
	base := []string{"request", "task", "--id", "one", "--worker", sid, "--for", "lead", "--brief", "check"}
	for _, args := range [][]string{
		{"--head", opt.Head}, {"--requires", "unit"}, {"--head", ""}, {"--requires", ""},
		{"--head", opt.Head[:8], "--requires", "unit"}, {"--head", opt.Head, "--requires", "unit,unit"},
		{"--head", opt.Head, "--requires", "unit,"}, {"--as", "../escape"},
		{"--head", opt.Head, "--head", strings.Repeat("c", 40), "--requires", "unit"},
	} {
		if err := Dispatch(append(append([]string{}, base...), args...)); err == nil {
			t.Fatalf("invalid options accepted: %v", args)
		}
	}
}

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, path string, r fleet.Rec) {
	t.Helper()
	if err := fleet.WriteJSON(path, r); err != nil {
		t.Fatal(err)
	}
}

func mustAppend(t *testing.T, path string, r fleet.Rec) {
	t.Helper()
	if err := fleet.AppendJSONL(path, r); err != nil {
		t.Fatal(err)
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
