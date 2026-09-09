package verbs

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func requestFixture(t *testing.T) (string, string) {
	t.Helper()
	oldState, oldOrg, oldOut := fleet.State, fleet.OrgState, Out
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	fleet.State, fleet.OrgState, Out = filepath.Join(root, "state"), filepath.Join(root, "org"), io.Discard
	t.Setenv("FLEET_GITHUB", "off")
	t.Setenv("FLEET_WATCH", "off")
	t.Cleanup(func() { fleet.State, fleet.OrgState, Out = oldState, oldOrg, oldOut; _ = os.Chdir(oldCwd) })
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "task")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	sid := "11111111-1111-1111-1111-111111111111"
	if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), fleet.Rec{
		"session": sid, "cwd": repo, "repo": fleet.RepoID(repo), "branch": "task", "last_event_at": fleet.Now(), "pid_kind": "harness", "pid": os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	return repo, sid
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

func requestFile(repo string) string {
	return dispatchFile(fleet.RepoID(repo), "task", "implementation")
}
func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestRequestReplayPreservesOriginalAfterHeadChanges(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "fix-1", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, requestFile(repo))
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "new head")
	if err := CmdRequest("task", "fix-1", sid[:8], "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readBytes(t, requestFile(repo))) {
		t.Fatal("replay rewrote head, timestamp or assignment")
	}
	if err := CmdRequest("task", "fix-1", sid, "lead", "different task"); err == nil {
		t.Fatal("changed replay accepted")
	}
	if !bytes.Equal(before, readBytes(t, requestFile(repo))) {
		t.Fatal("conflict changed record")
	}
	if err := CmdRequest("task", "fix-2", sid, "lead", "fix it"); err == nil {
		t.Fatal("second request replaced occupied assignment")
	}
}

func TestConcurrentRequestOnlyOneWins(t *testing.T) {
	repo, sid := requestFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); <-start; results <- CmdRequest("task", id, sid, "lead", "fix it") }(id)
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
		t.Fatalf("%d assignments won", successes)
	}
	if fleet.ReadJSON(requestFile(repo)) == nil {
		t.Fatal("no surviving assignment")
	}
}

func TestRequestProtectsLegacyMutations(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, requestFile(repo))
	for _, fn := range []func() error{
		func() error { return CmdDispatch("task", "implementation", "lead", "", "", "changed", "", "", true) },
		func() error { return CmdReassign("task", "other") },
		func() error { return cmdUndispatch("task", "") },
	} {
		if err := fn(); err == nil {
			t.Fatal("uncorrelated mutation accepted")
		}
	}
	if !bytes.Equal(before, readBytes(t, requestFile(repo))) {
		t.Fatal("immutable request changed")
	}
}

func TestRequestRejectsForeignLeaseAndMalformedStore(t *testing.T) {
	for _, mode := range []string{"foreign", "malformed", "collision", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			repo, sid := requestFixture(t)
			rid := fleet.RepoID(repo)
			switch mode {
			case "foreign":
				_ = fleet.WriteJSON(fleet.KeyFile("leases", "repo:"+rid+":task"), fleet.Rec{"session": "other", "key": "repo:" + rid + ":task"})
			case "malformed":
				_ = os.MkdirAll(dispatchDir(), 0700)
				_ = os.WriteFile(filepath.Join(dispatchDir(), "broken.json"), []byte("{"), 0600)
			case "collision":
				_ = fleet.WriteJSON(requestFile(repo), fleet.Rec{"repo": "other", "change": "other", "relationship": "implementation"})
			case "legacy":
				_ = fleet.WriteJSON(fleet.Path("stop", "task.json"), fleet.Rec{"repo": rid, "branch": "task", "reason": "legacy"})
			}
			if err := CmdRequest("task", "one", sid, "lead", "fix it"); err == nil {
				t.Fatalf("%s accepted", mode)
			}
			if mode != "collision" {
				if _, err := os.Stat(requestFile(repo)); !os.IsNotExist(err) {
					t.Fatalf("%s wrote assignment", mode)
				}
			}
		})
	}
}

func TestStatusDoesNotInventAcceptanceOrCompletion(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	d := fleet.ReadJSON(requestFile(repo))
	key := "repo:" + fleet.RepoID(repo) + ":task"
	rec := fleet.SessionRecord(sid)
	rec["turn_open"] = true
	for _, c := range []struct {
		name, key string
		at        float64
		want      string
	}{
		{"no activity", "", 0, "Queued"},
		{"old activity", key, fleet.F(d, "at") - 1, "Queued"},
		{"other branch", key + "-other", fleet.Now(), "Queued"},
		{"this branch", key, fleet.Now(), "Activity observed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec["last_write"] = fleet.Rec{"key": c.key, "at": c.at}
			_ = fleet.WriteJSON(fleet.Path("sessions", sid+".json"), rec)
			row := requestStatus(d, fleet.Now())
			if row["status"] != c.want {
				t.Fatalf("%s: %v", c.name, row)
			}
		})
	}
	_ = fleet.WriteJSON(fleet.KeyFile("stop", key), fleet.Rec{"key": key, "reason": "stop"})
	if row := requestStatus(d, fleet.Now()); row["status"] == "Stopped" {
		t.Fatal("stop flag reported termination")
	}
	// A dead session does not prove clean stop either.
	fleet.Unlink(fleet.KeyFile("stop", key))
	rec["ended"] = true
	_ = fleet.WriteJSON(fleet.Path("sessions", sid+".json"), rec)
	if row := requestStatus(d, fleet.Now()); row["status"] != "Status needs checking" {
		t.Fatal(row)
	}
}

func TestStatusReadOnlyAndGapReporting(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, fleet.State)
	var b bytes.Buffer
	Out = &b
	if err := Dispatch([]string{"status", "--json"}); err != nil {
		t.Fatal(err)
	}
	var packet map[string]any
	if err := json.Unmarshal(b.Bytes(), &packet); err != nil {
		t.Fatal(err)
	}
	if packet["schema"] != "fleet-task-status-v1" || packet["complete"] != true {
		t.Fatal(packet)
	}
	after := snapshotFiles(t, fleet.State)
	if !sameFiles(before, after) {
		t.Fatal("status mutated state")
	}
	_ = os.WriteFile(requestFile(repo), []byte("{"), 0600)
	b.Reset()
	if err := Dispatch([]string{"status", "--json"}); err == nil {
		t.Fatal("damaged assignment hidden")
	}
	if err := json.Unmarshal(b.Bytes(), &packet); err != nil {
		t.Fatal(err)
	}
	if packet["complete"] != false {
		t.Fatal(packet)
	}
}

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		m[path] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestRequestCLIRejectsIncompleteAndUnsafeIDs(t *testing.T) {
	_, sid := requestFixture(t)
	for _, args := range [][]string{
		{"request", "task", "--id", "../escape", "--worker", sid, "--for", "lead", "--brief", "fix"},
		{"request", "task", "--id", "one", "--worker", sid, "--brief", "fix"},
		{"request", "task", "--id", "one", "--worker", sid, "--for", "lead", "--brief", "fix", "--take"},
	} {
		if err := Dispatch(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

// Replays also cross the process boundary: each contender has independent globals
// and file descriptors, as two supervisor harnesses would.
func TestRequestConcurrentProcesses(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "different IDs", true: "same ID"}[same], func(t *testing.T) {
			repo, sid := requestFixture(t)
			ids := []string{"one", "two"}
			if same {
				ids[1] = "one"
			}
			var cmds []*exec.Cmd
			for _, id := range ids {
				cmd := exec.Command(os.Args[0], "-test.run=^TestRequestHelperProcess$")
				cmd.Dir = repo
				cmd.Env = append(os.Environ(), "FLEET_REQUEST_HELPER=1", "FLEET_STATE="+fleet.State, "ORG_STATE="+fleet.OrgState, "FLEET_REQUEST_ID="+id, "FLEET_REQUEST_WORKER="+sid)
				cmds = append(cmds, cmd)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
			}
			successes := 0
			for _, cmd := range cmds {
				if cmd.Wait() == nil {
					successes++
				}
			}
			want := 1
			if same {
				want = 2
			}
			if successes != want {
				t.Fatalf("%d successful processes, want %d", successes, want)
			}
			rows, err := strictDispatchRows()
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows %v, err %v", rows, err)
			}
		})
	}
}

func TestRequestHelperProcess(_ *testing.T) {
	if os.Getenv("FLEET_REQUEST_HELPER") != "1" {
		return
	}
	Out = io.Discard
	if err := CmdRequest("task", os.Getenv("FLEET_REQUEST_ID"), os.Getenv("FLEET_REQUEST_WORKER"), "lead", "fix it"); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRequestReplayAfterBranchAndSessionRemoval(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "replay", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, requestFile(repo))
	runGit(t, repo, "checkout", "--detach")
	runGit(t, repo, "branch", "-D", "task")
	fleet.Unlink(fleet.Path("sessions", sid+".json"))
	if err := CmdRequest("task", "replay", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readBytes(t, requestFile(repo))) {
		t.Fatal("replay changed record")
	}
	if err := CmdRequest("task", "replay", sid, "lead", "changed"); err == nil {
		t.Fatal("conflicting replay accepted")
	}
}

func TestLegacyDispatchIgnoresUnrelatedDamage(t *testing.T) {
	repo, _ := requestFixture(t)
	if err := os.MkdirAll(dispatchDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dispatchDir(), "unrelated.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	rid := fleet.RepoID(repo)
	if _, err := replaceableDispatch(rid, "task", "verify"); err != nil {
		t.Fatal(err)
	}
	path := dispatchFile(rid, "task", "verify")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := replaceableDispatch(rid, "task", "verify"); err == nil {
		t.Fatal("target damage ignored")
	}
}

func TestUndispatchLegacySiblingPreservesRequest(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, requestFile(repo))
	path := dispatchFile(fleet.RepoID(repo), "task", "verify")
	if err := fleet.WriteJSON(path, fleet.Rec{"repo": fleet.RepoID(repo), "change": "task", "relationship": "verify"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdUndispatch("task", "verify"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("sibling not removed")
	}
	if !bytes.Equal(before, readBytes(t, requestFile(repo))) {
		t.Fatal("request changed")
	}
}

func TestStatusExemptsRevokeRecipient(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	key := "repo:" + fleet.RepoID(repo) + ":task"
	if err := fleet.WriteJSON(fleet.KeyFile("stop", key), fleet.Rec{"key": key, "except": sid}); err != nil {
		t.Fatal(err)
	}
	if row := requestStatus(fleet.ReadJSON(requestFile(repo)), fleet.Now()); row["status"] != "Queued" {
		t.Fatalf("recipient falsely stopped: %v", row)
	}
}

func TestLegacyMaintenanceIgnoresUnrelatedDamage(t *testing.T) {
	repo, _ := requestFixture(t)
	rid := fleet.RepoID(repo)
	path := dispatchFile(rid, "task", "verify")
	if err := fleet.WriteJSON(path, fleet.Rec{"repo": rid, "change": "task", "relationship": "verify"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dispatchDir(), "unrelated.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CmdReassign("task", "new-lead"); err != nil {
		t.Fatal(err)
	}
	if fleet.S(fleet.ReadJSON(path), "for") != "new-lead" {
		t.Fatal("not reassigned")
	}
	if err := cmdUndispatch("task", "verify"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CmdReassign("task", "new-lead"); err == nil {
		t.Fatal("damaged target accepted")
	}
	if err := cmdUndispatch("task", "verify"); err == nil {
		t.Fatal("damaged target removed")
	}
}

func TestActivitySurvivesWritesOnAnotherBranch(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	ev := fleet.Event{"session_id": sid, "cwd": repo, "hook_event_name": "PostToolUse", "tool_name": "Edit", "tool_input": fleet.Rec{"file_path": filepath.Join(repo, "file")}}
	if v := fleet.Run(ev); v.Code != 0 {
		t.Fatal(v)
	}
	runGit(t, repo, "checkout", "-b", "other")
	if v := fleet.Run(ev); v.Code != 0 {
		t.Fatal(v)
	}
	if row := requestStatus(fleet.ReadJSON(requestFile(repo)), fleet.Now()); row["status"] != "Activity observed" {
		t.Fatalf("prior observation lost: %v", row)
	}
}

func TestRequestKeysByGitSpellingNotCallerSpelling(t *testing.T) {
	repo, sid := requestFixture(t)
	runGit(t, repo, "branch", "Nav-Fix")
	if err := CmdRequest("nav-fix", "case-1", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dispatchFile(fleet.RepoID(repo), "Nav-Fix", "implementation")); err != nil {
		t.Fatalf("assignment keyed by the caller's spelling, not git's: %v", err)
	}
	row := fleet.ReadJSON(dispatchFile(fleet.RepoID(repo), "Nav-Fix", "implementation"))
	if fleet.S(row, "change") != "Nav-Fix" {
		t.Fatalf("change recorded as %q, want git's spelling", fleet.S(row, "change"))
	}
	// A second spelling of the same branch is the same assignment, not a second one.
	if err := CmdRequest("NAV-FIX", "case-2", sid, "lead", "fix it"); err == nil {
		t.Fatal("second assignment for one branch under another spelling was accepted")
	}
}

func TestStatusRefusesDamagedRequestEvidence(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	row := fleet.ReadJSON(requestFile(repo))
	for _, damage := range []string{"worker", "at", "for"} {
		broken := fleet.Rec{}
		for k, v := range row {
			broken[k] = v
		}
		delete(broken, damage)
		if err := fleet.WriteJSON(requestFile(repo), broken); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		Out = &b
		err := Dispatch([]string{"status", "--json"})
		var packet map[string]any
		_ = json.Unmarshal(b.Bytes(), &packet)
		if err == nil || packet["complete"] != false {
			t.Fatalf("row missing %q reported complete: err=%v packet=%v", damage, err, packet)
		}
		if err := CmdRequest("task", "two", sid, "lead", "fix it"); err == nil {
			t.Fatalf("request accepted beside damaged evidence (missing %q)", damage)
		}
	}
}

func TestRequestReplaySurvivesBranchDeletionUnderCallerSpelling(t *testing.T) {
	repo, sid := requestFixture(t)
	runGit(t, repo, "branch", "Nav-Fix")
	if err := CmdRequest("nav-fix", "case-3", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "branch", "-D", "Nav-Fix")
	if err := CmdRequest("nav-fix", "case-3", sid, "lead", "fix it"); err != nil {
		t.Fatalf("identical retry after branch deletion refused: %v", err)
	}
}

func TestStatusRefusesNonStringRequestID(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := CmdRequest("task", "one", sid, "lead", "fix it"); err != nil {
		t.Fatal(err)
	}
	row := fleet.ReadJSON(requestFile(repo))
	row["request_id"] = 7.0
	if err := fleet.WriteJSON(requestFile(repo), row); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	Out = &b
	err := Dispatch([]string{"status", "--json"})
	var packet map[string]any
	_ = json.Unmarshal(b.Bytes(), &packet)
	if err == nil || packet["complete"] != false {
		t.Fatalf("non-string request_id read as complete: err=%v packet=%v", err, packet)
	}
}
